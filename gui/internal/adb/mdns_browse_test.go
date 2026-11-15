package adb

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fastMdnsBrowseOptions() mdnsBrowseOptions {
	opts := defaultMdnsBrowseOptions()
	opts.queryInterval, opts.rebuildInterval = time.Hour, time.Hour
	opts.interfaceInterval, opts.maintenanceInterval = 10*time.Millisecond, time.Millisecond
	opts.retryMin, opts.retryMax = 5*time.Millisecond, 20*time.Millisecond
	return opts
}

func startTestMdnsBrowse(t *testing.T, opts mdnsBrowseOptions, onEvents func(MdnsTrackEvents)) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- browseMdns(ctx, onEvents, nil, opts) }()
	t.Cleanup(cancel)
	return cancel, done
}

func waitMdnsBrowseStopped(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected browse exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("browse did not release readers and stop on cancellation")
	}
}

func TestBrowseMdnsRecoversMainAndQUWithoutLosingState(t *testing.T) {
	opts := fastMdnsBrowseOptions()
	target := mdnsTestTarget("192.0.2.10", 10)
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{target}, nil }
	var mu sync.Mutex
	var mains []*fakeMdnsReadConn
	mainAttempts := 0
	opts.listenMain = func() (mdnsReadConn, []net.Interface, error) {
		mu.Lock()
		defer mu.Unlock()
		mainAttempts++
		if mainAttempts == 1 {
			return nil, nil, errors.New("5353 temporarily unavailable")
		}
		c := newFakeMdnsReadConn()
		mains = append(mains, c)
		return c, nil, nil
	}
	var queryConns []*mdnsTestPacketConn
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		c := newMdnsTestPacketConn(target.bind)
		mu.Lock()
		queryConns = append(queryConns, c)
		mu.Unlock()
		return c, nil
	}
	events := make(chan MdnsTrackEvents, 20)
	cancel, done := startTestMdnsBrowse(t, opts, func(ev MdnsTrackEvents) { events <- ev })
	mdnsEventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(mains) >= 1 && len(queryConns) >= 1
	})
	mu.Lock()
	main1, query1 := mains[0], queryConns[0]
	mu.Unlock()
	main1.enqueueMsg(t, mdnsTestAdvertise("adb-original", "_adb._tcp"))
	firstCount := 0
	waitSnapshot := func(want int) {
		t.Helper()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case ev := <-events:
				if ev.First {
					firstCount++
				}
				if len(ev.Snapshot) == want {
					return
				}
			case <-deadline.C:
				t.Fatalf("missing snapshot of %d services", want)
			}
		}
	}
	waitSnapshot(1)
	main1.setReadError(errors.New("injected main reader failure"))
	query1.failRead(errors.New("injected QU reader failure"))
	mdnsEventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(mains) < 2 {
			return false
		}
		for _, c := range queryConns[1:] {
			if c.LocalAddr().String() == target.bind {
				_, queries, closed := c.stats()
				if !closed && len(queries) >= 3 {
					return true
				}
			}
		}
		return false
	})
	mu.Lock()
	main2 := mains[1]
	mu.Unlock()
	main2.enqueueMsg(t, mdnsTestAdvertise("adb-after-repair", "_adb._tcp"))
	waitSnapshot(2)
	if firstCount != 1 {
		t.Fatalf("socket repairs restarted the event baseline: First count=%d", firstCount)
	}
	cancel()
	waitMdnsBrowseStopped(t, done)
	mu.Lock()
	defer mu.Unlock()
	for _, c := range mains {
		if !c.isClosed() {
			t.Fatal("main socket leaked")
		}
	}
	for _, c := range queryConns {
		_, _, closed := c.stats()
		if !closed {
			t.Fatal("query socket leaked")
		}
	}
}

func TestBrowseMdnsInterfaceChangeQueriesBeforePeriodicTick(t *testing.T) {
	opts := fastMdnsBrowseOptions()
	oldTarget, newTarget := mdnsTestTarget("192.0.2.10", 10), mdnsTestTarget("192.0.2.20", 10)
	var changed atomic.Bool
	opts.queryTargets = func() ([]mdnsQueryTarget, error) {
		if changed.Load() {
			return []mdnsQueryTarget{newTarget}, nil
		}
		return []mdnsQueryTarget{oldTarget}, nil
	}
	opts.listenMain = func() (mdnsReadConn, []net.Interface, error) { return newFakeMdnsReadConn(), nil, nil }
	var mu sync.Mutex
	conns := map[string]*mdnsTestPacketConn{}
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		c := newMdnsTestPacketConn(target.bind)
		mu.Lock()
		conns[target.bind] = c
		mu.Unlock()
		return c, nil
	}
	cancel, done := startTestMdnsBrowse(t, opts, nil)
	mdnsEventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return conns[oldTarget.bind] != nil })
	changed.Store(true)
	mdnsEventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		c := conns[newTarget.bind]
		if c == nil {
			return false
		}
		_, queries, _ := c.stats()
		return len(queries) == 3
	})
	mu.Lock()
	_, _, closed := conns[oldTarget.bind].stats()
	mu.Unlock()
	if !closed {
		t.Fatal("old IP socket remained open after interface refresh")
	}
	cancel()
	waitMdnsBrowseStopped(t, done)
}

func TestBrowseMdnsCancelDuringPersistentFailures(t *testing.T) {
	opts := fastMdnsBrowseOptions()
	var attempts atomic.Int32
	opts.listenMain = func() (mdnsReadConn, []net.Interface, error) {
		attempts.Add(1)
		return nil, nil, errors.New("5353 unavailable")
	}
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return nil, errors.New("enumeration unavailable") }
	opts.listenQuery = func(context.Context, mdnsQueryTarget) (net.PacketConn, error) {
		attempts.Add(1)
		return nil, errors.New("bind unavailable")
	}
	cancel, done := startTestMdnsBrowse(t, opts, nil)
	mdnsEventually(t, func() bool { return attempts.Load() >= 4 })
	cancel()
	waitMdnsBrowseStopped(t, done)
	count := attempts.Load()
	time.Sleep(25 * time.Millisecond)
	if attempts.Load() != count {
		t.Fatal("socket retries continued after cancellation")
	}
}

func TestMdnsMainFailureSurvivesFullNotificationChannel(t *testing.T) {
	conn := newFakeMdnsReadConn()
	owner := newMdnsLoopOwner(conn)
	state := newMdnsListenState(nil, nil, nil)
	defer state.stop()
	errCh := make(chan error, 1)
	errCh <- errors.New("queued retired reader notification")
	wantErr := errors.New("current reader failure")
	conn.setReadError(wantErr)
	startMdnsReadLoop(owner, conn, owner.currentGen(), state, errCh)
	mdnsEventually(t, func() bool { return errors.Is(owner.failure(), wantErr) })
	done := make(chan struct{})
	go func() { owner.readers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("full notification channel blocked reader shutdown")
	}
	owner.closeCurrent()
}

func TestMdnsIdleTimerRejectsOldGenerationAndRecreatedEntry(t *testing.T) {
	var probes atomic.Int32
	state := newMdnsListenState(nil, func(string) { probes.Add(1) }, nil)
	defer state.stop()
	svc := MdnsService{Type: "_adb._tcp", Name: "adb-idle", Addr: "192.0.2.10:5555"}
	key := mdnsRecordKey(svc)
	state.upsert(svc, time.Now(), false)
	state.mu.Lock()
	old, oldGeneration := state.entries[key], state.entries[key].generation
	state.mu.Unlock()
	state.upsert(svc, time.Now(), false)
	state.idleProbe(key, old, oldGeneration)
	state.gone(svc)
	state.upsert(svc, time.Now(), false)
	state.idleProbe(key, old, oldGeneration)
	if probes.Load() != 0 {
		t.Fatal("a queued obsolete timer probed a fresh service")
	}
}

func TestMdnsStateStopDrainsCallbackAndRejectsFurtherEvents(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var events atomic.Int32
	state := newMdnsListenState(func(MdnsTrackEvents) { events.Add(1) }, func(string) { close(started); <-release }, nil)
	svc := MdnsService{Type: "_adb._tcp", Name: "adb-stop", Addr: "192.0.2.10:5555"}
	key := mdnsRecordKey(svc)
	state.upsert(svc, time.Now(), false)
	state.mu.Lock()
	entry, generation := state.entries[key], state.entries[key].generation
	state.mu.Unlock()
	go state.idleProbe(key, entry, generation)
	<-started
	stopped := make(chan struct{})
	go func() { state.stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned with an idle callback still running")
	case <-time.After(5 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop failed to drain callback")
	}
	count := events.Load()
	state.upsert(svc, time.Now(), true)
	state.initial()
	state.gone(svc)
	state.idleProbe(key, entry, generation)
	if events.Load() != count {
		t.Fatal("stopped state emitted an event")
	}
}
