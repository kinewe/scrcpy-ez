package app

import (
	"context"
	"sync"
	"time"

	"scrcpy-ez/gui/internal/parallel"
)

type wirelessDiscoveryPlan struct {
	identity string
	tiers    [][]string
	addrs    []string
}

func (p *wirelessDiscoveryPlan) mode(addr string) string {
	for _, candidate := range p.tiers[0] {
		if candidate == addr {
			return ModeTls
		}
	}
	return ModeTcpip
}

func (a *App) runDiscoveryPlans(ctx context.Context, plans []*wirelessDiscoveryPlan, tried []string) {
	if a.disc == nil {
		a.mu.Lock()
		a.discStatus = DiscoveryStatus{Status: "notfound", Tried: tried, LastTry: time.Now().Unix()}
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	if a.discoveryDevices == nil {
		a.discoveryDevices = map[string]bool{}
	}
	for _, p := range plans {
		a.discoveryDevices[p.identity] = true
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		for _, p := range plans {
			delete(a.discoveryDevices, p.identity)
		}
		a.mu.Unlock()
		a.reconcileWirelessConnect()
	}()

	var mu sync.Mutex
	found := make([]string, len(plans))
	completed := 0
	stamp := time.Now().Unix()
	parallel.For(ctx, len(plans), parallel.Devices, func(i int) {
		p := plans[i]
		defer func() {
			a.mu.Lock()
			delete(a.discoveryDevices, p.identity)
			a.mu.Unlock()
			a.reconcileWirelessConnect()
		}()
		qctx, finish, ok := a.beginChannelCheck(ctx, p.identity+"|discovery")
		if !ok {
			return
		}
		defer finish()
		observations := map[string]channelObservation{}
		for _, mode := range []string{ModeTls, ModeTcpip} {
			observations[mode] = a.profiles.observeChannel(p.identity, mode)
		}
		addr, connected := a.disc.ConnectTiers(qctx, p.tiers, connectTimeout, discoveryWindow)
		active, serial := false, ""
		if connected {
			active, serial = a.probeOwnerOK(qctx, p.identity, addr)
		}
		if ctx.Err() != nil || qctx.Err() == context.Canceled {
			return
		}
		changed, success := false, false
		if connected {
			mode := p.mode(addr)
			accepted, updated := a.profiles.finishChannelCheckResult(p.identity, addr, mode, serial, "", observations[mode], active && qctx.Err() == nil)
			changed, success = updated, accepted && active && qctx.Err() == nil
		} else {
			for _, candidate := range p.addrs {
				mode := p.mode(candidate)
				changed = a.profiles.finishChannelCheck(p.identity, candidate, mode, "", "", observations[mode], false) || changed
			}
		}
		if changed {
			a.rebuildCurrentDisplay("单设备无线恢复完成")
		}
		mu.Lock()
		defer mu.Unlock()
		completed++
		if success {
			found[i] = addr
		}
		st := DiscoveryStatus{Status: "searching", Tried: tried, LastTry: stamp}
		for _, candidate := range found {
			if candidate != "" {
				st.Status, st.Found = "found", candidate
				break
			}
		}
		if completed == len(plans) && st.Found == "" {
			st.Status = "notfound"
		}
		a.mu.Lock()
		a.discStatus = st
		a.mu.Unlock()
	})
	if ctx.Err() == nil {
		mu.Lock()
		st := DiscoveryStatus{Status: "notfound", Tried: tried, LastTry: stamp}
		for _, addr := range found {
			if addr != "" {
				st.Status, st.Found = "found", addr
				break
			}
		}
		a.mu.Lock()
		a.discStatus = st
		a.mu.Unlock()
		mu.Unlock()
	}
}
