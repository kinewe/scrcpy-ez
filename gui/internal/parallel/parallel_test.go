package parallel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelBoundsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 20)
	var running, peak, calls atomic.Int32
	done := make(chan struct{})
	go func() {
		For(ctx, 20, 3, func(int) {
			calls.Add(1)
			n := running.Add(1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			entered <- struct{}{}
			<-ctx.Done()
			running.Add(-1)
		})
		close(done)
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not overlap")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not join")
	}
	if peak.Load() != 3 || calls.Load() != 3 || running.Load() != 0 {
		t.Fatalf("peak=%d calls=%d running=%d", peak.Load(), calls.Load(), running.Load())
	}
}
