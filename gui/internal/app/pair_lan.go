package app

import (
	"context"
	"net"
	"sort"
	"time"

	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/discovery"
)

type pendingLANOps struct {
	networks    func() ([]net.IPNet, error)
	probe       func(context.Context, discovery.LANProbeTarget) bool
	interval    time.Duration
	refreshMdns func(context.Context, discovery.LANProbeTarget) bool
}

// App.mu owns the session pointer, results and flags. Cursors belong to its
// single worker. Superseded/canceled sessions can never publish a late device.
type pendingLANSearch struct {
	ctx                            context.Context
	cancel                         context.CancelFunc
	wake                           chan struct{}
	done                           chan struct{}
	running, searching, continuous bool
	found                          map[string]time.Time
	cursors                        map[string]*discovery.LANScanCursor
}

func (a *App) startPendingLANSearch(continuous bool) {
	a.mu.Lock()
	if a.notificationClosing || a.pendingLANOps.networks == nil || a.pendingLANOps.probe == nil {
		a.mu.Unlock()
		return
	}
	if s := a.pendingLAN; s != nil && s.running && s.ctx.Err() == nil {
		if continuous {
			s.continuous = true
		}
		if !s.searching {
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &pendingLANSearch{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{}), running: true, searching: true, continuous: continuous, found: map[string]time.Time{}, cursors: map[string]*discovery.LANScanCursor{}}
	if old := a.pendingLAN; old != nil {
		old.cancel()
		for addr, at := range old.found {
			if time.Since(at) < 45*time.Second {
				s.found[addr] = at
			}
		}
	}
	a.pendingLAN = s
	a.mu.Unlock()
	go a.guard("pending-lan-search", func() { a.runPendingLANSearch(s) })
}

func (a *App) runPendingLANSearch(s *pendingLANSearch) {
	defer close(s.done)
	defer func() { a.mu.Lock(); s.running, s.searching = false, false; a.mu.Unlock() }()
	interval := a.pendingLANOps.interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	for s.ctx.Err() == nil {
		a.mu.Lock()
		current := a.pendingLAN == s
		if current {
			s.searching = true
		}
		a.mu.Unlock()
		if !current {
			return
		}
		a.reapplyDisplay("LAN 5555 search started")
		networks, err := a.pendingLANOps.networks()
		if err == nil && s.ctx.Err() == nil {
			currentNetworks := map[string]bool{}
			for _, n := range networks {
				currentNetworks[n.IP.String()+"/"+n.Mask.String()] = true
			}
			for key := range s.cursors {
				if !currentNetworks[key] {
					delete(s.cursors, key)
				}
			}
			targets := discovery.LANScanTargets(networks, s.cursors, discovery.LANScanLimit)
			// Recheck earlier hits first, including hosts in a wide-subnet page
			// not scheduled this round. They must still be on a current LAN.
			a.mu.RLock()
			var previous []discovery.LANProbeTarget
			for addr := range s.found {
				ip := net.ParseIP(ipOfAddr(addr))
				if source := pendingLANSource(networks, ip); source != "" {
					previous = append(previous, discovery.LANProbeTarget{Addr: addr, Source: source})
				}
			}
			a.mu.RUnlock()
			seen := map[string]bool{}
			var all []discovery.LANProbeTarget
			for _, target := range append(previous, targets...) {
				if len(all) >= discovery.LANScanLimit {
					break
				}
				if !seen[target.Addr] {
					seen[target.Addr] = true
					all = append(all, target)
				}
			}
			ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
			discovery.ScanLAN5555(ctx, all, a.pendingLANOps.probe, func(target discovery.LANProbeTarget, ok bool) {
				a.mu.Lock()
				if a.pendingLAN != s || s.ctx.Err() != nil {
					a.mu.Unlock()
					return
				}
				_, had := s.found[target.Addr]
				if ok {
					s.found[target.Addr] = time.Now()
				} else {
					delete(s.found, target.Addr)
				}
				changed := ok != had
				a.mu.Unlock()
				if changed {
					a.reapplyDisplay("LAN 5555 discovery result")
				}
				if ok && a.pendingLANOps.refreshMdns != nil {
					// Publish the usable 5555 route first. Missing metadata must
					// never delay it; replies enrich the same card via mDNS events.
					if serial, _ := serialFromMdnsServices(a.mdnsSnapshot(), target.Addr); serial == "" {
						a.pendingLANOps.refreshMdns(s.ctx, target)
					}
				}
			})
			cancel()
		} else if err != nil {
			bridge.DebugLog("[app] LAN 5555 网卡枚举失败：%v", err)
		}
		a.mu.Lock()
		current = a.pendingLAN == s && s.ctx.Err() == nil
		if current {
			s.searching = false
			for addr, at := range s.found {
				onLAN := pendingLANSource(networks, net.ParseIP(ipOfAddr(addr))) != ""
				if time.Since(at) > 45*time.Second || err == nil && !onLAN {
					delete(s.found, addr)
				}
			}
		}
		continuous := s.continuous
		a.mu.Unlock()
		if !current {
			return
		}
		a.reapplyDisplay("LAN 5555 search completed")
		if !continuous {
			return
		}
		timer := time.NewTimer(interval)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func pendingLANSource(networks []net.IPNet, ip net.IP) string {
	for _, n := range networks {
		if ip.Equal(n.IP) {
			return ""
		}
	}
	for _, n := range networks {
		if n.Contains(ip) {
			return n.IP.String()
		}
	}
	return ""
}

// Caller holds App.mu, so publication uses the latest LAN results even if a
// probe finished while mDNS's pending list was being built.
func (a *App) mergeLANPendingLocked(out []PendingDevice, services []discovery.MdnsService, online map[string]bool) []PendingDevice {
	s := a.pendingLAN
	if s == nil || s.ctx.Err() != nil {
		return out
	}
	var addrs []string
	for addr, at := range s.found {
		if time.Since(at) < 45*time.Second {
			addrs = append(addrs, addr)
		}
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		ip := ipOfAddr(addr)
		merged := false
		for i := range out {
			if out[i].IP == ip {
				out[i].DirectAddr = addr
				merged = true
			}
		}
		if merged || online[addr] {
			continue
		}
		if _, known := a.profiles.Entry(addr); known {
			continue
		}
		if a.profiles.ResolveClaimableByIP(ip, "") != "" {
			continue
		}
		// A known TLS service on this host was deliberately filtered above;
		// do not recreate it as an anonymous, unpaired TCP/IP card.
		known := false
		for _, service := range services {
			if ipOfAddr(service.Addr) != ip || service.Mode != discovery.MdnsModeTls {
				continue
			}
			if _, ok := a.profiles.Entry(SerialFromServiceName(service.Name)); ok {
				known = true
			}
		}
		if known {
			continue
		}
		out = append(out, PendingDevice{Key: addr, IP: ip, Addr: addr, Name: "TCP/IP 设备", DirectAddr: addr, PairPort: pairingServicePort("", ip, services)})
	}
	return out
}
