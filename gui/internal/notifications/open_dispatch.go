package notifications

import (
	"context"
	"time"
)

// Each accepted click gets its own deadline after reaching the front of the
// queue. A slow first launch must not swallow clicks on another application.
type openDispatcher struct {
	ctx  context.Context
	jobs chan openEntry
}

func newOpenDispatcher(ctx context.Context, failed func(openEntry, error)) *openDispatcher {
	d := &openDispatcher{ctx: ctx, jobs: make(chan openEntry, 16)}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-d.jobs:
				if ctx.Err() != nil {
					return
				}
				call, cancel := context.WithTimeout(ctx, 20*time.Second)
				err := job.open(call)
				cancel()
				if err != nil && ctx.Err() == nil {
					failed(job, err)
				}
			}
		}
	}()
	return d
}

func (d *openDispatcher) Enqueue(job openEntry) bool {
	if job.open == nil || d.ctx.Err() != nil {
		return false
	}
	select {
	case d.jobs <- job:
		return true
	default:
		return false
	}
}
