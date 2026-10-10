//go:build windows

package bridge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetFrontInventoryCache() {
	scrcpyProcsCache.mu.Lock()
	scrcpyProcsCache.at = time.Time{}
	scrcpyProcsCache.procs = nil
	scrcpyProcsCache.mu.Unlock()
}

func TestFrontInventoryWaitIsCancelableAndDoesNotHoldCacheLock(t *testing.T) {
	original := nativeScrcpyInventory
	started, release := make(chan struct{}), make(chan struct{})
	var queries atomic.Int32
	var releaseOnce sync.Once
	nativeScrcpyInventory = &processInventory{timeout: time.Second, query: func(context.Context) ([]scrcpyProc, error) {
		if queries.Add(1) == 1 {
			close(started)
			<-release
		}
		return []scrcpyProc{{pid: 7}}, nil
	}}
	resetFrontInventoryCache()
	defer func() {
		releaseOnce.Do(func() { close(release) })
		nativeScrcpyInventory = original
		resetFrontInventoryCache()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := listScrcpyProcsCached(ctx, false); first <- err }()
	<-started
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("old click could not cancel its query wait")
	}
	joined := &inventoryJoinedContext{Context: context.Background(), joined: make(chan struct{})}
	second := make(chan error, 1)
	go func() {
		procs, err := listScrcpyProcsCached(joined, false)
		if err == nil && (len(procs) != 1 || procs[0].pid != 7) {
			err = errors.New("shared result missing")
		}
		second <- err
	}()
	select {
	case <-joined.joined:
	case <-time.After(time.Second):
		t.Fatal("new click queued behind cache lock")
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if queries.Load() != 1 {
		t.Fatal("concurrent clicks did not share the query")
	}
	procs, err := listScrcpyProcsCached(context.Background(), false)
	if err != nil || len(procs) != 1 || queries.Load() != 1 {
		t.Fatal("successful query was not cached", err)
	}
	procs[0].pid = 999
	procs, _ = listScrcpyProcsCached(context.Background(), false)
	if procs[0].pid != 7 {
		t.Fatal("caller changed cached rows")
	}
	if _, err := listScrcpyProcsCached(context.Background(), true); err != nil || queries.Load() != 2 {
		t.Fatal("fresh inventory did not re-query", err)
	}
}

func TestFrontInventoryDoesNotCacheProviderFailure(t *testing.T) {
	original := nativeScrcpyInventory
	var queries atomic.Int32
	nativeScrcpyInventory = &processInventory{timeout: time.Second, query: func(context.Context) ([]scrcpyProc, error) {
		if queries.Add(1) == 1 {
			return nil, errors.New("provider unavailable")
		}
		return []scrcpyProc{{pid: 7}}, nil
	}}
	resetFrontInventoryCache()
	defer func() { nativeScrcpyInventory = original; resetFrontInventoryCache() }()
	if _, err := listScrcpyProcsCached(context.Background(), false); err == nil {
		t.Fatal("provider failure was hidden")
	}
	procs, err := listScrcpyProcsCached(context.Background(), false)
	if err != nil || len(procs) != 1 || queries.Load() != 2 {
		t.Fatal("provider recovery hidden by failed-query cache", err)
	}
}
