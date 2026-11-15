package adb

import (
	"context"
	"net"
	"time"

	"scrcpy-ez/gui/internal/bridge"
)

// Dependency injection is local to one browse session, so tests do not replace
// process-wide socket factories or timers while reader goroutines are running.
type mdnsBrowseOptions struct {
	listenMain          mdnsMainSocketListenFn
	queryTargets        func() ([]mdnsQueryTarget, error)
	listenQuery         func(context.Context, mdnsQueryTarget) (net.PacketConn, error)
	listenDirect        func(context.Context, MdnsRefreshTarget) (net.Conn, error)
	directTimeout       time.Duration
	directRetryInterval time.Duration
	queryInterval       time.Duration
	rebuildInterval     time.Duration
	interfaceInterval   time.Duration
	maintenanceInterval time.Duration
	querySocketMaxAge   time.Duration
	retryMin            time.Duration
	retryMax            time.Duration
	refreshRequests     *mdnsRefreshRequests
	refreshMinInterval  time.Duration
}

func defaultMdnsBrowseOptions() mdnsBrowseOptions {
	return mdnsBrowseOptions{
		listenMain: listenMdnsMainSocketForRebuild, queryTargets: collectMdnsQueryTargets,
		listenQuery: listenMdnsQuerySocket, queryInterval: mdnsQueryInterval,
		rebuildInterval: mdnsRebuildInterval, interfaceInterval: 5 * time.Second,
		maintenanceInterval: time.Second, retryMin: time.Second, retryMax: 30 * time.Second,
		querySocketMaxAge: 5 * time.Minute, refreshMinInterval: 2 * time.Second,
		listenDirect: listenMdnsDirectSocket, directTimeout: 8 * time.Second,
		directRetryInterval: time.Second,
	}
}

// BrowseMdns retains one event state across socket repairs. A failed multicast
// listener does not disable working unicast receivers (and vice versa).
func (m *Manager) BrowseMdns(ctx context.Context, onEvents func(MdnsTrackEvents), onIdleProbe func(string)) error {
	return m.browseMdns(ctx, onEvents, onIdleProbe, defaultMdnsBrowseOptions())
}

func browseMdns(ctx context.Context, onEvents func(MdnsTrackEvents), onIdleProbe func(string), opts mdnsBrowseOptions) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(ctx)
	state := newMdnsListenState(onEvents, onIdleProbe, nil)
	queries := newMdnsQuerySockets(ctx, state, opts)
	state.queryFn = queries.send
	owner := newMdnsLoopOwner(nil)
	defer func() {
		cancel()
		state.stop()
		queries.close()
		owner.closeCurrent()
		owner.readers.Wait()
	}()

	state.initial()
	errCh := make(chan error, 1)
	mainRepair := true
	mainRetry := mdnsRetry{}
	var mainOpened time.Time
	rebuildMain := func(now time.Time) {
		if ctx.Err() != nil || now.Before(mainRetry.next) {
			return
		}
		if _, _, err := mdnsRebuildMainSocket(owner, state, errCh, opts.listenMain); err != nil {
			mainRepair = true
			mainRetry.fail(now, opts.retryMin, opts.retryMax)
			return
		}
		wasRepair := mainRepair
		mainRepair = false
		mainRetry.next = time.Time{}
		mainOpened = now
		if wasRepair {
			for _, serviceType := range mdnsListenServiceTypes {
				queries.send(serviceType)
			}
		}
	}
	noticeMainFailure := func(now time.Time) {
		if !mainRepair && owner.failure() != nil {
			mainRepair = true
			owner.closeCurrent()
			mainRetry.fail(now, opts.retryMin, opts.retryMax)
			bridge.DebugLog("[app] mdns 主 socket 读取失败，稍后重建：retry=%v err=%v", mainRetry.delay, owner.failure())
		}
	}
	rebuildMain(time.Now())
	queries.refreshTargets()
	queries.reconcile(time.Now()) // New sockets immediately query all three types.
	go state.selfCheck(ctx)

	queryTick := time.NewTicker(opts.queryInterval)
	defer queryTick.Stop()
	rebuildTick := time.NewTicker(opts.rebuildInterval)
	defer rebuildTick.Stop()
	interfaceTick := time.NewTicker(opts.interfaceInterval)
	defer interfaceTick.Stop()
	maintenanceTick := time.NewTicker(opts.maintenanceInterval)
	defer maintenanceTick.Stop()
	var refreshWake <-chan struct{}
	if opts.refreshRequests != nil {
		refreshWake = opts.refreshRequests.wake
	}
	var refreshTimer *time.Timer
	var refreshTick <-chan time.Time
	var lastRefresh time.Time
	defer func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
	}()
	scheduleRefresh := func(delay time.Duration) {
		if refreshTick == nil {
			refreshTimer = time.NewTimer(delay)
			refreshTick = refreshTimer.C
		}
	}
	refresh := func() {
		accepted, hosts := opts.refreshRequests.take()
		if !accepted {
			return
		}
		now := time.Now()
		changed := queries.refreshTargets()
		queries.reconcile(now)
		if changed {
			rebuildMain(now)
		}
		if len(hosts) == 0 {
			state.periodicQuery()
		}
		for _, host := range hosts {
			if !queries.sendHost(host) {
				opts.refreshRequests.request(host.ctx, host.target)
			}
		}
		lastRefresh = now
		if opts.refreshRequests.pending() {
			scheduleRefresh(opts.refreshMinInterval)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-errCh:
			// Inspect current ownership instead of trusting a queued old error.
			noticeMainFailure(time.Now())
		case <-queries.wake:
			queries.reconcile(time.Now())
		case <-refreshWake:
			if refreshTick != nil {
				continue // The scheduled batch also includes newly queued hosts.
			}
			if remaining := time.Until(lastRefresh.Add(opts.refreshMinInterval)); remaining > 0 {
				scheduleRefresh(remaining)
			} else {
				refresh()
			}
		case <-refreshTick:
			refreshTick = nil
			refresh()
		case <-maintenanceTick.C:
			now := time.Now()
			state.expireRecords(now)
			state.flushPresence(now)
			queries.reconcile(now)
			noticeMainFailure(now)
			if mainRepair {
				rebuildMain(now)
			} else if now.Sub(mainOpened) >= opts.retryMax {
				mainRetry = mdnsRetry{}
			}
		case <-interfaceTick.C:
			now := time.Now()
			changed := queries.refreshTargets()
			queries.reconcile(now)
			if changed {
				rebuildMain(now) // Rejoin groups on newly available LAN adapters, too.
			}
		case <-queryTick.C:
			queries.reconcile(time.Now())
			state.periodicQuery()
		case <-rebuildTick.C:
			rebuildMain(time.Now())
		}
	}
}
