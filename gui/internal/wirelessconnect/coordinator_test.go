package wirelessconnect

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func target(addr string) Target {
	return Target{Identity: "phone", DeviceSerial: "PHONE", Addresses: []string{addr}, ServerEpoch: 1}
}
func receive[T any](t *testing.T, c <-chan T) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("event did not arrive")
		var zero T
		return zero
	}
}
func policy() Policy {
	return Policy{Settle: 30 * time.Millisecond, Timeout: time.Second, Backoff: []time.Duration{100 * time.Millisecond}, Parallelism: 2}
}

func TestDuplicateEventsPreserveSettleAndSuccess(t *testing.T) {
	calls := make(chan Target, 8)
	c := New(context.Background(), func(_ context.Context, t Target) error { calls <- t; return nil }, policy())
	defer c.Close()
	wanted := []Target{target("192.0.2.1:5555")}
	for i := 0; i < 15; i++ {
		c.Reconcile(wanted)
		time.Sleep(5 * time.Millisecond)
	}
	if receive(t, calls).Addresses[0] != wanted[0].Addresses[0] {
		t.Fatal("wrong target")
	}
	select {
	case <-calls:
		t.Fatal("duplicate events repeated a completed connect")
	case <-time.After(50 * time.Millisecond):
	}
	c.Reconcile(nil)
	c.Reconcile(wanted)
	_ = receive(t, calls) // a genuine remove/re-add permits another attempt
}

func TestAddressFlappingCancelsBeforeStable(t *testing.T) {
	calls := make(chan Target, 8)
	p := policy()
	p.Settle = 80 * time.Millisecond
	c := New(context.Background(), func(_ context.Context, t Target) error { calls <- t; return nil }, p)
	defer c.Close()
	c.Reconcile([]Target{target("192.0.2.1:5555")})
	c.Reconcile([]Target{target("192.0.2.2:5555")})
	c.Reconcile(nil)
	select {
	case <-calls:
		t.Fatal("unstable/withdrawn address connected")
	case <-time.After(120 * time.Millisecond):
	}
}

func TestReplacementJoinsCanceledCommandAndSkipsIntermediateIP(t *testing.T) {
	calls := make(chan string, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	canceled := make(chan struct{})
	c := New(context.Background(), func(ctx context.Context, v Target) error {
		addr := v.Addresses[0]
		calls <- addr
		if addr == "old" {
			<-ctx.Done()
			close(canceled)
			<-release
		}
		return nil
	}, policy())
	defer func() { unblock(); c.Close() }()
	c.Reconcile([]Target{target("old")})
	_ = receive(t, calls)
	c.Reconcile([]Target{target("intermediate")})
	_ = receive(t, canceled)
	c.Reconcile([]Target{target("new")})
	select {
	case <-calls:
		t.Fatal("new connect overlapped old command cleanup")
	case <-time.After(80 * time.Millisecond):
	}
	unblock()
	if receive(t, calls) != "new" {
		t.Fatal("obsolete intermediate IP connected")
	}
}

func TestFailureBackoffCannotBeResetByBroadcasts(t *testing.T) {
	calls := make(chan Target, 8)
	c := New(context.Background(), func(_ context.Context, v Target) error { calls <- v; return errors.New("not ready") }, policy())
	defer c.Close()
	w := []Target{target("address")}
	c.Reconcile(w)
	_ = receive(t, calls)
	for i := 0; i < 5; i++ {
		c.Reconcile(w)
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-calls:
		t.Fatal("broadcast bypassed backoff")
	default:
	}
	_ = receive(t, calls)
	c.Reconcile(nil)
	select {
	case <-calls:
		t.Fatal("withdrawn broadcast continued retries")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestIdentityFailureWaitsForNewEpoch(t *testing.T) {
	calls := make(chan Target, 8)
	c := New(context.Background(), func(_ context.Context, v Target) error { calls <- v; return ErrIdentity }, policy())
	defer c.Close()
	w := target("address")
	c.Reconcile([]Target{w})
	_ = receive(t, calls)
	c.Reconcile([]Target{w})
	select {
	case <-calls:
		t.Fatal("identity mismatch retried without new evidence")
	case <-time.After(150 * time.Millisecond):
	}
	w.ServerEpoch++
	c.Reconcile([]Target{w})
	if receive(t, calls).ServerEpoch != 2 {
		t.Fatal("new server epoch not applied")
	}
}

func TestParallelBudgetAndExitJoin(t *testing.T) {
	started := make(chan string, 8)
	release := make(chan struct{})
	var active, maxActive atomic.Int32
	c := New(context.Background(), func(ctx context.Context, v Target) error {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		started <- v.Identity
		<-ctx.Done()
		<-release
		return ctx.Err()
	}, policy())
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() { unblock(); c.Close() }()
	var targets []Target
	for _, id := range []string{"a", "b", "c"} {
		v := target("address")
		v.Identity = id
		targets = append(targets, v)
	}
	c.Reconcile(targets)
	_ = receive(t, started)
	_ = receive(t, started)
	select {
	case <-started:
		t.Fatal("more than two background commands")
	case <-time.After(50 * time.Millisecond):
	}
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("exit did not join command cleanup")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	_ = receive(t, closed)
	c.Reconcile(targets)
	if maxActive.Load() != 2 || active.Load() != 0 {
		t.Fatal("parallel budget or cleanup failed")
	}
}
