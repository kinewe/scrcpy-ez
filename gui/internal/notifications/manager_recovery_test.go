package notifications

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type unavailableSource struct {
	attempts atomic.Int32
	err      error
}

func (s *unavailableSource) Run(context.Context, Target, func(Frame) error) error {
	s.attempts.Add(1)
	return s.err
}

func TestManagerPermanentFailureDoesNotUseTransportCooldown(t *testing.T) {
	for _, failure := range []error{ErrUnavailable, ErrProtocol} {
		source := &unavailableSource{err: failure}
		m := NewManager(context.Background(), source, func() (Sink, error) { return &testSink{}, nil })
		m.retryCooldown = time.Millisecond
		target := Target{Identity: "synthetic", Serial: "usb"}
		m.Reconcile([]Target{target}, Options{})
		await(t, func() bool {
			return source.attempts.Load() == 1 && len(m.Status()) == 1 && m.Status()[0].State == "unavailable"
		})
		for i := 0; i < 10; i++ {
			m.Reconcile([]Target{target}, Options{})
		}
		time.Sleep(50 * time.Millisecond)
		m.Close()
		if source.attempts.Load() != 1 || !errors.Is(source.err, failure) {
			t.Fatal("unsupported capability or malformed protocol kept retrying")
		}
	}
}

type failingTransportSource struct{ attempts atomic.Int32 }

func (s *failingTransportSource) Run(ctx context.Context, _ Target, emit func(Frame) error) error {
	s.attempts.Add(1)
	if err := emit(Frame{Sequence: 1, Type: "hello", Cutoff: 1}); err != nil {
		return err
	}
	if err := emit(Frame{Sequence: 2, Type: "ready"}); err != nil {
		return err
	}
	return ErrTransport
}

func TestManagerBoundsTransportRetryAndNewEpochCanRecover(t *testing.T) {
	source := &failingTransportSource{}
	sink := &testSink{}
	m := NewManager(context.Background(), source, func() (Sink, error) { return sink, nil })
	defer m.Close()
	target := Target{Identity: "test", Serial: "usb", Epoch: 1}
	m.Reconcile([]Target{target}, Options{Preview: true, CopyFallback: 7 * 24 * time.Hour})
	deadline := time.Now().Add(6 * time.Second)
	for source.attempts.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if source.attempts.Load() != 3 {
		t.Fatal("transport retry budget not reached")
	}
	for i := 0; i < 20; i++ {
		m.Reconcile([]Target{target}, Options{Preview: true})
	}
	time.Sleep(100 * time.Millisecond)
	if source.attempts.Load() != 3 {
		t.Fatal("same connection restarted an exhausted worker")
	}
	target.Epoch++
	m.Reconcile([]Target{target}, Options{})
	await(t, func() bool { return source.attempts.Load() == 4 })
	sink.mu.Lock()
	cleared := sink.cleared
	sink.mu.Unlock()
	if cleared < 3 {
		t.Fatal("interrupted generations retained cards/actions")
	}
}

type recoveringTransportSource struct {
	attempts atomic.Int32
	active   atomic.Int32
	maximum  atomic.Int32
}

func (s *recoveringTransportSource) Run(ctx context.Context, _ Target, emit func(Frame) error) error {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for old := s.maximum.Load(); active > old && !s.maximum.CompareAndSwap(old, active); old = s.maximum.Load() {
	}
	if s.attempts.Add(1) <= 3 {
		return ErrTransport
	}
	// Existing phone notifications are a quiet baseline after reconnecting.
	for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "snapshot", Record: Record{Key: "old", PostTime: 90, Body: "验证码850329"}}, {Sequence: 3, Type: "ready"}, {Sequence: 4, Type: "post", Record: Record{Key: "new", PostTime: 110, Body: "验证码039571"}}} {
		if err := emit(frame); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestManagerTransportCooldownRecoversWithoutEpochChange(t *testing.T) {
	source := &recoveringTransportSource{}
	sink := &testSink{}
	m := NewManager(context.Background(), source, func() (Sink, error) { return sink, nil })
	defer m.Close()
	// Assigned before Reconcile hands the target to the manager worker.
	m.retryCooldown = 200 * time.Millisecond
	target := Target{Identity: "test", Serial: "usb", Epoch: 1}
	m.Reconcile([]Target{target}, Options{Preview: true})
	deadline := time.Now().Add(6 * time.Second)
	for source.attempts.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if source.attempts.Load() != 3 {
		t.Fatal("short retry burst missing")
	}
	time.Sleep(50 * time.Millisecond)
	if source.attempts.Load() != 3 {
		t.Fatal("cooldown was skipped")
	}
	await(t, func() bool {
		return source.attempts.Load() == 4 && len(m.Status()) == 1 && m.Status()[0].State == "active"
	})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if source.maximum.Load() != 1 || len(sink.shown) != 1 || sink.shown[0].CopyCode != "039571" || sink.shown[0].Silent {
		t.Fatal("recovery overlapped listeners, replayed old OTP or suppressed new OTP")
	}
}
