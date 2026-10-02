//go:build windows

package rootrepair

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRootExistingMutexSerializesAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	exec := func(ctx context.Context, _ []string, _ string) (string, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	o := Options{Serial: "USB_A", Identity: t.Name(), LogDir: t.TempDir(), Execute: exec}
	first := make(chan error, 1)
	go func() { _, e := PrepareLocked(ctx, o); first <- e }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first attempt did not own mutex")
	}
	secondCtx, secondCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer secondCancel()
	_, e := PrepareLocked(secondCtx, o)
	if e != context.DeadlineExceeded || calls.Load() != 1 {
		t.Fatalf("existing mutex not held: err=%v calls=%d", e, calls.Load())
	}
	cancel()
	select {
	case e = <-first:
		if e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not release mutex")
	}
}
