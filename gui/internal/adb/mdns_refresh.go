package adb

import (
	"context"
	"net"
	"sort"
	"sync"
)

const mdnsRefreshHostLimit = 1024
const mdnsRefreshHostBatch = 8

// MdnsRefreshTarget is an already discovered host and the physical LAN source
// address used to discover it. The socket owner rechecks the current subnet.
type MdnsRefreshTarget struct{ IP, Source string }

type mdnsHostRefresh struct {
	target MdnsRefreshTarget
	ctx    context.Context
}

func normalizeMdnsRefreshTarget(target MdnsRefreshTarget) (MdnsRefreshTarget, bool) {
	ip, source := net.ParseIP(target.IP).To4(), net.ParseIP(target.Source).To4()
	if ip == nil || source == nil || !usableMdnsIPv4(ip) || !usableMdnsIPv4(source) || ip.IsMulticast() || source.IsMulticast() || ip.Equal(source) {
		return MdnsRefreshTarget{}, false
	}
	return MdnsRefreshTarget{IP: ip.String(), Source: source.String()}, true
}

// Requests wake the existing socket owner; callers never write to its sockets.
// Keep the latest caller context so closing an old pairing window cannot cancel
// a refresh requested by a newly opened window.
type mdnsRefreshRequests struct {
	mu     sync.Mutex
	owner  context.Context
	caller context.Context
	wake   chan struct{}
	closed bool
	hosts  map[MdnsRefreshTarget]context.Context
}

func (r *mdnsRefreshRequests) request(ctx context.Context, targets ...MdnsRefreshTarget) bool {
	if len(targets) > mdnsRefreshHostLimit {
		return false
	}
	normalized := make(map[MdnsRefreshTarget]bool, len(targets))
	for _, target := range targets {
		target, ok := normalizeMdnsRefreshTarget(target)
		if !ok {
			return false
		}
		normalized[target] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.owner.Err() != nil || ctx == nil || ctx.Err() != nil {
		return false
	}
	if r.hosts == nil {
		r.hosts = map[MdnsRefreshTarget]context.Context{}
	}
	for target, caller := range r.hosts {
		if caller.Err() != nil {
			delete(r.hosts, target)
		}
	}
	additional := 0
	for target := range normalized {
		if _, exists := r.hosts[target]; !exists {
			additional++
		}
	}
	if len(r.hosts)+additional > mdnsRefreshHostLimit {
		return false
	}
	for target := range normalized {
		r.hosts[target] = ctx
	}
	r.caller = ctx
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return true
}

func (r *mdnsRefreshRequests) take() (bool, []mdnsHostRefresh) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx := r.caller
	r.caller = nil
	if r.closed || r.owner.Err() != nil {
		r.hosts = nil
		return false, nil
	}
	var keys []MdnsRefreshTarget
	for target, caller := range r.hosts {
		if caller.Err() != nil {
			delete(r.hosts, target)
		} else {
			keys = append(keys, target)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Source != keys[j].Source {
			return keys[i].Source < keys[j].Source
		}
		return keys[i].IP < keys[j].IP
	})
	var hosts []mdnsHostRefresh
	for _, target := range keys {
		if len(hosts) == mdnsRefreshHostBatch {
			break
		}
		hosts = append(hosts, mdnsHostRefresh{target: target, ctx: r.hosts[target]})
		delete(r.hosts, target)
	}
	return ctx != nil && ctx.Err() == nil || len(hosts) != 0, hosts
}

func (r *mdnsRefreshRequests) pending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.owner.Err() != nil {
		return false
	}
	if r.caller != nil && r.caller.Err() == nil {
		return true
	}
	for _, ctx := range r.hosts {
		if ctx.Err() == nil {
			return true
		}
	}
	return false
}

// RequestMdnsRefresh queues a best-effort, coalesced classic/TLS query without
// connecting, authenticating or waiting for replies. False means no live browse
// session accepted it. Optional verified targets also receive directed queries
// from their current physical LAN address. Replies use the usual event stream.
func (m *Manager) RequestMdnsRefresh(ctx context.Context, targets ...MdnsRefreshTarget) bool {
	m.mdnsRefreshMu.Lock()
	r := m.mdnsRefresh
	m.mdnsRefreshMu.Unlock()
	return r != nil && r.request(ctx, targets...)
}

func (m *Manager) browseMdns(ctx context.Context, onEvents func(MdnsTrackEvents), onIdleProbe func(string), opts mdnsBrowseOptions) error {
	r := &mdnsRefreshRequests{owner: ctx, wake: make(chan struct{}, 1)}
	m.mdnsRefreshMu.Lock()
	m.mdnsRefresh = r
	m.mdnsRefreshMu.Unlock()
	defer func() {
		r.mu.Lock()
		r.closed, r.caller = true, nil
		r.hosts = nil
		r.mu.Unlock()
		m.mdnsRefreshMu.Lock()
		if m.mdnsRefresh == r {
			m.mdnsRefresh = nil
		}
		m.mdnsRefreshMu.Unlock()
	}()
	opts.refreshRequests = r
	return browseMdns(ctx, onEvents, onIdleProbe, opts)
}
