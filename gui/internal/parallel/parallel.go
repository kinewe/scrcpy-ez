// Package parallel bounds independent device work without changing result order.
package parallel

import (
	"context"
	"sync"
	"sync/atomic"
)

const Devices = 8

// Limiter shares the device budget between independently scheduled jobs.
type Limiter struct {
	once  sync.Once
	slots chan struct{}
}

func (l *Limiter) Do(ctx context.Context, work func()) bool {
	l.once.Do(func() { l.slots = make(chan struct{}, Devices) })
	select {
	case l.slots <- struct{}{}:
		defer func() { <-l.slots }()
		if ctx.Err() != nil {
			return false
		}
		work()
		return true
	case <-ctx.Done():
		return false
	}
}

// For joins all started work. The callback must honor ctx for blocking I/O.
func For(ctx context.Context, count, limit int, work func(int)) {
	if count <= 0 || ctx.Err() != nil {
		return
	}
	if limit < 1 {
		limit = 1
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for worker := 0; worker < min(count, limit); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= count {
					return
				}
				if ctx.Err() == nil {
					work(i)
				}
			}
		}()
	}
	wg.Wait()
}
