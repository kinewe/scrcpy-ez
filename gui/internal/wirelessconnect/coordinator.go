// Package wirelessconnect schedules bounded connects from existing discovery facts.
// It never discovers, pairs, changes adbd ports, disconnects or writes device archives.
package wirelessconnect

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// Stop retries until new discovery/transport evidence changes the target.
var ErrObsolete = errors.New("wireless target is obsolete")
var ErrIdentity = errors.New("wireless device identity does not match")

type Target struct {
	Identity, DeviceSerial string
	Addresses              []string // TLS before classic TCP; at most one current address per mode
	ServerEpoch            uint64
}

func Equal(a, b Target) bool {
	return a.Identity == b.Identity && a.DeviceSerial == b.DeviceSerial && a.ServerEpoch == b.ServerEpoch && slices.Equal(a.Addresses, b.Addresses)
}

type Policy struct {
	Settle, Timeout time.Duration
	Backoff         []time.Duration
	Parallelism     int
}

type job struct {
	target Target
	cancel context.CancelFunc
	done   chan struct{}
}

type Coordinator struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	jobs    map[string]*job
	tails   map[string]<-chan struct{}
	wg      sync.WaitGroup
	slots   chan struct{}
	policy  Policy
	attempt func(context.Context, Target) error
}

func New(parent context.Context, attempt func(context.Context, Target) error, policy Policy) *Coordinator {
	if policy.Settle <= 0 {
		policy.Settle = 2 * time.Second
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 8 * time.Second
	}
	if len(policy.Backoff) == 0 {
		policy.Backoff = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, time.Minute}
	}
	policy.Backoff = slices.Clone(policy.Backoff)
	if policy.Parallelism <= 0 {
		policy.Parallelism = 2
	}
	ctx, cancel := context.WithCancel(parent)
	return &Coordinator{ctx: ctx, cancel: cancel, jobs: map[string]*job{}, tails: map[string]<-chan struct{}{}, slots: make(chan struct{}, policy.Parallelism), policy: policy, attempt: attempt}
}

// Same evidence preserves its settle timer and retry budget. Replacing an address
// cancels its old command and joins it before starting another for that identity.
func (c *Coordinator) Reconcile(targets []Target) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.ctx.Err() != nil {
		return
	}
	wanted := map[string]Target{}
	for _, t := range targets {
		if t.Identity == "" || t.DeviceSerial == "" || len(t.Addresses) == 0 {
			continue
		}
		t.Addresses = slices.Clone(t.Addresses)
		wanted[t.Identity] = t
	}
	for id, j := range c.jobs {
		if t, exists := wanted[id]; !exists || !Equal(t, j.target) {
			j.cancel()
			delete(c.jobs, id)
		}
	}
	for id, t := range wanted {
		if c.jobs[id] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(c.ctx)
		j := &job{target: t, cancel: cancel, done: make(chan struct{})}
		previous := c.tails[id]
		c.tails[id], c.jobs[id] = j.done, j
		c.wg.Add(1)
		go c.run(ctx, j, previous)
	}
}

func wait(ctx context.Context, delay time.Duration) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return ctx.Err() == nil
	}
}

func (c *Coordinator) run(ctx context.Context, j *job, previous <-chan struct{}) {
	defer c.wg.Done()
	defer func() {
		close(j.done)
		c.mu.Lock()
		if c.tails[j.target.Identity] == j.done {
			delete(c.tails, j.target.Identity)
		}
		c.mu.Unlock()
	}()
	if previous != nil {
		// A canceled intermediate request must retain its predecessor's fence.
		// Closing our done early would let a later IP overlap the old command.
		<-previous
		if ctx.Err() != nil {
			return
		}
	}
	if !wait(ctx, c.policy.Settle) {
		return
	}
	for failures := 0; ctx.Err() == nil; failures++ {
		select {
		case <-ctx.Done():
			return
		case c.slots <- struct{}{}:
		}
		qctx, cancel := context.WithTimeout(ctx, c.policy.Timeout)
		var err error
		if qctx.Err() == nil {
			err = c.attempt(qctx, j.target)
		}
		cancel()
		<-c.slots
		if ctx.Err() != nil || err == nil || errors.Is(err, ErrObsolete) || errors.Is(err, ErrIdentity) {
			return
		}
		if !wait(ctx, c.policy.Backoff[min(failures, len(c.policy.Backoff)-1)]) {
			return
		}
	}
}

// Calling Close also fences concurrent requests before waiting for all commands.
func (c *Coordinator) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.wg.Wait()
}
