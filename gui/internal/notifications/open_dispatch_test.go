package notifications

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOpenClicksQueueBehindSlowOrFailedLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan int, 2)
	release := make(chan struct{})
	failures := make(chan error, 1)
	d := newOpenDispatcher(ctx, func(_ openEntry, err error) { failures <- err })
	if !d.Enqueue(openEntry{open: func(context.Context) error { started <- 1; <-release; return errors.New("first failed") }}) {
		t.Fatal("first click rejected")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first launch missing")
	}
	if !d.Enqueue(openEntry{open: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 19*time.Second {
			return errors.New("queued click lost its deadline")
		}
		started <- 2
		return nil
	}}) {
		t.Fatal("second click swallowed while first busy")
	}
	select {
	case <-started:
		t.Fatal("launches raced")
	default:
	}
	close(release)
	select {
	case <-failures:
	case <-time.After(time.Second):
		t.Fatal("failed click lacked feedback")
	}
	select {
	case id := <-started:
		if id != 2 {
			t.Fatal("wrong queued click")
		}
	case <-time.After(time.Second):
		t.Fatal("second click never ran")
	}
}

func TestOpenQueueStopsWhenSinkCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	stopped := make(chan struct{})
	d := newOpenDispatcher(ctx, func(openEntry, error) { t.Error("feedback after close") })
	d.Enqueue(openEntry{open: func(ctx context.Context) error { close(started); <-ctx.Done(); close(stopped); return ctx.Err() }})
	<-started
	d.Enqueue(openEntry{open: func(context.Context) error { t.Error("queued action after close"); return nil }})
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("in-flight action not canceled")
	}
	if d.Enqueue(openEntry{open: func(context.Context) error { return nil }}) {
		t.Fatal("closed sink accepted action")
	}
}
