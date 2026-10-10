package bridge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessInventorySharesBlockedQueryAndRecovers(t *testing.T) {
	started, expired, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var queries atomic.Int32
	p := processInventory{timeout: 30 * time.Millisecond, query: func(ctx context.Context) ([]scrcpyProc, error) {
		if queries.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(expired)
			<-release // Simulate a provider that ignores cancellation.
		}
		return []scrcpyProc{{pid: 42}}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { _, err := p.load(ctx); firstDone <- err }()
	<-started
	cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if _, err := p.load(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("blocked query: %v", err)
			}
		}()
	}
	wg.Wait()
	if queries.Load() != 1 {
		t.Fatalf("timed out requests started %d queries", queries.Load())
	}
	p.mu.Lock()
	f := p.flight
	p.mu.Unlock()
	<-expired
	close(release)
	<-f.done
	if !errors.Is(f.err, context.DeadlineExceeded) || len(f.procs) != 0 {
		t.Fatalf("expired result was published: %+v %v", f.procs, f.err)
	}
	procs, err := p.load(context.Background())
	if err != nil || len(procs) != 1 || queries.Load() != 2 {
		t.Fatalf("query failed to recover: %+v %v queries=%d", procs, err, queries.Load())
	}
}

func TestProcessInventoryCallerCancellationDoesNotCancelOtherCaller(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p := processInventory{timeout: time.Second, query: func(ctx context.Context) ([]scrcpyProc, error) {
		close(started)
		select {
		case <-release:
			return []scrcpyProc{{pid: 7}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	short, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := p.load(short); first <- err }()
	<-started
	second := make(chan error, 1)
	joined := &inventoryJoinedContext{Context: context.Background(), joined: make(chan struct{})}
	go func() {
		procs, err := p.load(joined)
		if err == nil && (len(procs) != 1 || procs[0].pid != 7) {
			err = errors.New("shared result missing")
		}
		second <- err
	}()
	<-joined.joined
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("other caller lost its query: %v", err)
	}
}

type inventoryJoinedContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *inventoryJoinedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func TestProcessInventoryRejectsPartialFailureAndCanceledCalls(t *testing.T) {
	var queries atomic.Int32
	want := errors.New("provider unavailable")
	p := processInventory{timeout: time.Second, query: func(context.Context) ([]scrcpyProc, error) {
		queries.Add(1)
		return []scrcpyProc{{pid: 42}}, want
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.load(ctx); !errors.Is(err, context.Canceled) || queries.Load() != 0 {
		t.Fatal("canceled caller started a query", err)
	}
	for i := 0; i < 2; i++ {
		procs, err := p.load(context.Background())
		if !errors.Is(err, want) || len(procs) != 0 {
			t.Fatalf("partial failure returned rows: %+v %v", procs, err)
		}
	}
	if queries.Load() != 2 {
		t.Fatal("failed query was cached")
	}
}
