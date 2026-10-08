package rootrepair

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorConsentSharingAndDenial(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	c := NewCoordinator(filepath.Join(t.TempDir(), "root-repair.json"), func(ctx context.Context, r Request) (Report, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return Report{Status: "repaired"}, nil
		case <-ctx.Done():
			return Report{}, ctx.Err()
		}
	})
	r := Request{Serial: "USB_A", Identity: "PHONE_A", Key: "1/USB_A/1"}
	if o := c.Repair(context.Background(), r); o.Status != "disabled" || calls.Load() != 0 {
		t.Fatal(o, calls.Load())
	}
	if e := c.SetEnabled(r.Identity, true); e != nil {
		t.Fatal(e)
	}
	one := make(chan Outcome, 1)
	two := make(chan Outcome, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { one <- c.Repair(ctx, r) }()
	<-entered
	go func() { two <- c.Repair(context.Background(), r) }()
	until := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		n := c.flights[r.Identity].waiters
		c.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("second waiter missing")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if o := <-one; o.Status != "canceled" {
		t.Fatal(o)
	}
	close(release)
	if o := <-two; !o.Accepted() || calls.Load() != 1 {
		t.Fatal(o, calls.Load())
	}
	// Another route that already failed can share the recent repair result.
	r.Serial = "192.0.2.1:5555"
	r.Key = "1/192.0.2.1:5555/1"
	if o := c.Repair(context.Background(), r); !o.Accepted() || calls.Load() != 1 {
		t.Fatal(o, calls.Load())
	}
	if !NewCoordinator(c.path, c.run).enabled["PHONE_A"] {
		t.Fatal("consent did not persist")
	}
	d := NewCoordinator("", func(context.Context, Request) (Report, error) { calls.Add(1); return Report{}, errors.New("denied") })
	_ = d.SetEnabled("PHONE_A", true)
	if o := d.Repair(context.Background(), r); o.Status != "failed" {
		t.Fatal(o)
	}
	before := calls.Load()
	if o := d.Repair(context.Background(), r); o.Status != "held" || calls.Load() != before {
		t.Fatal(o, calls.Load())
	}
	_ = d.SetEnabled("PHONE_A", true)
	if o := d.Repair(context.Background(), r); o.Status != "failed" || calls.Load() != before+1 {
		t.Fatal(o, calls.Load())
	}
}

func TestCoordinatorAllWaitersCancelAndNoUnsafeConsent(t *testing.T) {
	stopped := make(chan struct{})
	c := NewCoordinator("", func(ctx context.Context, r Request) (Report, error) {
		<-ctx.Done()
		close(stopped)
		return Report{}, ctx.Err()
	})
	if e := c.SetEnabled("PHONE';reboot", true); e == nil {
		t.Fatal("unsafe preference")
	}
	_ = c.SetEnabled("PHONE_A", true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Outcome, 1)
	go func() { done <- c.Repair(ctx, Request{Serial: "USB_A", Identity: "PHONE_A", Key: "1/USB_A/1"}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("orphan repair kept running")
	}
}

func TestServiceRejectsStaleRouteAndUntrustedEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	c := NewCoordinator("", func(context.Context, Request) (Report, error) { calls.Add(1); return Report{Status: "repaired"}, nil })
	_ = c.SetEnabled("PHONE_A", true)
	endpoint, token, e := Serve(ctx, c, func(Request) bool { return false })
	if e != nil {
		t.Fatal(e)
	}
	req := Request{Serial: "USB_A", Identity: "PHONE_A", Key: "stale"}
	call, cc := context.WithTimeout(ctx, time.Second)
	defer cc()
	if o := Call(call, endpoint, token, req); o.Status != "canceled" || calls.Load() != 0 {
		t.Fatal(o, calls.Load())
	}
	if o := Call(call, "192.0.2.1:10", token, req); o.Status != "failed" {
		t.Fatal(o)
	}
}
