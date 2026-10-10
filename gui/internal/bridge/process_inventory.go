package bridge

import (
	"context"
	"sync"
	"time"
)

// processInventory allows only one outstanding native query, including after a
// caller times out. An unresponsive provider must not accumulate COM threads
// on repeated clicks. Results belong to the flight and are immutable once done.
type processInventory struct {
	mu      sync.Mutex
	flight  *processInventoryFlight
	timeout time.Duration
	query   func(context.Context) ([]scrcpyProc, error)
}

type processInventoryFlight struct {
	done  chan struct{}
	procs []scrcpyProc
	err   error
}

func (p *processInventory) load(ctx context.Context) ([]scrcpyProc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	f := p.flight
	if f == nil {
		f = &processInventoryFlight{done: make(chan struct{})}
		p.flight = f
		go func() {
			queryCtx, cancel := context.WithTimeout(context.Background(), p.timeout)
			defer cancel()
			procs, err := p.query(queryCtx)
			if queryCtx.Err() != nil {
				procs, err = nil, queryCtx.Err()
			}
			if err != nil {
				procs = nil // A partial inventory must never select a window.
			}
			p.mu.Lock()
			f.procs, f.err = procs, err
			p.flight = nil
			close(f.done)
			p.mu.Unlock()
		}()
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return append([]scrcpyProc(nil), f.procs...), f.err
	}
}
