package app

import (
	"context"
	"errors"
	"sort"
	"time"

	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/discovery"
	"scrcpy-ez/gui/internal/wirelessconnect"
)

func (a *App) startWirelessConnect(ctx context.Context) {
	c := wirelessconnect.New(ctx, a.connectDiscoveredWireless, wirelessconnect.Policy{})
	a.mu.Lock()
	a.wirelessConnector = c
	a.wirelessStartupDone = make(chan struct{})
	a.mu.Unlock()
	go func() {
		for range a.adb.EventHub().Subscribe(ctx) {
			a.reconcileWirelessConnect()
		}
	}()
}

// Only current connect services with strong archived serial/GUID evidence may
// trigger a background connect. Old IPs, unknown devices and pairing ports cannot.
func wirelessConnectTargets(entries map[string]DeviceEntry, services []discovery.MdnsService, raw deviceevents.Snapshot, busy map[string]bool) []wirelessconnect.Target {
	if !raw.Available {
		return nil
	}
	matched := map[string][]discovery.MdnsService{}
	for _, s := range services {
		if !IsIPPort(s.Addr) || s.Mode != ModeTls && s.Mode != ModeTcpip {
			continue
		}
		serial, owner := SerialFromServiceName(s.Name), ""
		for id, e := range entries {
			if len(e.Serials) == 0 || !contains(e.Serials, serial) && (e.TlsGuid == "" || e.TlsGuid != s.Name || !serialCompatible(&e, serial)) {
				continue
			}
			if owner != "" {
				owner = ""
				break
			}
			owner = id
		}
		if owner != "" {
			matched[owner] = append(matched[owner], s)
		}
	}
	var out []wirelessconnect.Target
	for id, svcs := range matched {
		entry := entries[id]
		blocked := busy[id]
		for _, serial := range raw.Learning {
			blocked = blocked || contains(entry.Serials, serial)
		}
		if blocked {
			continue
		}
		var ordered []discovery.MdnsService
		for _, mode := range []string{ModeTls, ModeTcpip} {
			// Archive order preserves the original current-address preference.
			// A failed probe can mark it stale; an ongoing broadcast, rather than
			// an old archive-only address, justifies bounded retry.
			var candidates []discovery.MdnsService
			for _, ae := range entry.Addrs {
				for _, s := range svcs {
					if s.Mode == mode && s.Addr == ae.Addr {
						candidates = append(candidates, s)
					}
				}
			}
			if len(candidates) == 0 {
				for _, s := range svcs {
					if s.Mode == mode {
						candidates = append(candidates, s)
					}
				}
			}
			if len(candidates) > 0 {
				ordered = append(ordered, candidates[0])
			}
		}
		if len(ordered) == 0 {
			continue
		}
		for _, t := range raw.Transports {
			for _, s := range ordered {
				alias := s.Name + "._adb-tls-connect._tcp"
				if t.Serial == s.Addr || s.Mode == ModeTls && t.Serial == alias {
					blocked = blocked || t.State == "device" || t.State == "unauthorized"
				}
			}
		}
		if blocked {
			continue
		}
		physical := entry.Serials[0]
		if candidate := SerialFromServiceName(ordered[0].Name); contains(entry.Serials, candidate) {
			physical = candidate
		}
		target := wirelessconnect.Target{Identity: id, DeviceSerial: physical, ServerEpoch: raw.Epoch}
		for _, s := range ordered {
			target.Addresses = append(target.Addresses, s.Addr)
		}
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity < out[j].Identity })
	return out
}

func (a *App) currentWirelessConnectTargets() []wirelessconnect.Target {
	a.mu.RLock()
	closing, discovering := a.notificationClosing, a.discBusy
	busy := map[string]bool{}
	a.mu.RUnlock()
	if closing || discovering {
		return nil
	}
	a.teachMu.Lock()
	for id := range a.plugging {
		busy[id] = true
	}
	for id := range a.pairing {
		busy[id] = true
	}
	a.teachMu.Unlock()
	return wirelessConnectTargets(a.profiles.Entries(), a.mdnsSnapshot(), a.adb.EventHub().Current(), busy)
}

func (a *App) reconcileWirelessConnect() {
	// Read facts after serializing triggers: an older request cannot win.
	a.wirelessConnectMu.Lock()
	defer a.wirelessConnectMu.Unlock()
	a.mu.RLock()
	c := a.wirelessConnector
	a.mu.RUnlock()
	if c != nil {
		c.Reconcile(a.currentWirelessConnectTargets())
	}
}

func (a *App) wirelessConnectCurrent(target wirelessconnect.Target) bool {
	for _, current := range a.currentWirelessConnectTargets() {
		if wirelessconnect.Equal(target, current) {
			return true
		}
	}
	return false
}

func (a *App) connectDiscoveredWireless(ctx context.Context, target wirelessconnect.Target) error {
	a.waitSrvReady(ctx)
	a.mu.RLock()
	startup := a.wirelessStartupDone
	a.mu.RUnlock()
	if startup != nil {
		select {
		case <-startup:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var last error
	for _, addr := range target.Addresses {
		if !a.wirelessConnectCurrent(target) {
			return wirelessconnect.ErrObsolete
		}
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := a.disc.ConnectOut(qctx, addr)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			last = err
			continue
		}
		serial := a.readWirelessDeviceSerial(ctx, addr)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if serial == "" {
			last = errors.New("wireless identity temporarily unavailable")
			continue
		}
		if serial != target.DeviceSerial {
			bridge.DebugLog("[app] 无线静默连接身份不符：%s/%s，停止本轮恢复", target.Identity, addr)
			return wirelessconnect.ErrIdentity
		}
		// Real track-devices events enrich cards and elect listeners. This worker
		// cannot write an old address or commit a stale display after interruption.
		bridge.DebugLog("[app] 无线静默连接完成：%s/%s", target.Identity, addr)
		return nil
	}
	if last == nil {
		last = errors.New("no current wireless connection address")
	}
	return last
}
