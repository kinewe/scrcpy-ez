package app

import (
	"context"
	"sync"
	"time"

	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/discovery"
)

const channelCheckTimeout = 5 * time.Second
const channelCheckInterval = 10 * time.Second
const channelRecoveryInterval = 3 * time.Second
const maxChannelChecks = 32

// An observation belongs to one channel only. A 5555 heartbeat cannot invalidate
// a TLS failure; a newer TLS port/heartbeat must invalidate an old TLS result.
type channelObservation struct {
	entry   *DeviceEntry
	peers   []AddrEntry
	serials []string
}

func channelPeers(e *DeviceEntry, mode string) []AddrEntry {
	var out []AddrEntry
	for _, addr := range e.Addrs {
		if addrEntryClass(addr) == mode {
			out = append(out, addr)
		}
	}
	return out
}

func (s *ProfileStore) observeChannel(key, mode string) channelObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return channelObservation{}
	}
	return channelObservation{entry: e, peers: channelPeers(e, mode), serials: append([]string(nil), e.Serials...)}
}

func (s *ProfileStore) finishChannelCheck(key, addr, mode, serial, guid string, observation channelObservation, active bool) bool {
	_, changed := s.finishChannelCheckResult(key, addr, mode, serial, guid, observation, active)
	return changed
}

func (s *ProfileStore) finishChannelCheckResult(key, addr, mode, serial, guid string, observation channelObservation, active bool) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok || e != observation.entry {
		return false, false
	}
	peers := channelPeers(e, mode)
	previous := map[string]AddrEntry{}
	for _, peer := range observation.peers {
		previous[peer.Addr] = peer
	}
	for _, peer := range peers {
		old, existed := previous[peer.Addr]
		if peer.Addr == addr || peer.State == AddrStateActive {
			if !existed || old.State != peer.State || old.evidence != peer.evidence {
				return false, false
			}
		}
		delete(previous, peer.Addr)
	}
	// Removal of a previously known endpoint is also newer channel evidence.
	if len(previous) != 0 {
		return false, false
	}
	changed := false
	if active {
		if serial == "" || !serialCompatible(e, serial) {
			return false, false
		}
		if !contains(e.Serials, serial) {
			e.Serials = append(e.Serials, serial)
			changed = true
		}
		changed = s.addrSuccessModeLocked(e, addr, mode, time.Now()) || changed
		changed = s.applyWirelessFormLocked(e, mode) || changed
		s.rememberMdnsAddressesLocked([]MdnsAddr{{Addr: addr, Mode: mode}})
		if mode == ModeTls && guid != "" && e.TlsGuid != guid {
			e.TlsGuid, changed = guid, true
		}
	} else {
		// A failed cached/new endpoint cannot retire a different working endpoint.
		for _, peer := range peers {
			if peer.Addr == addr {
				changed = s.addrFailModeLocked(e, addr, mode, time.Now())
				break
			}
		}
	}
	if changed {
		_ = s.persistLocked()
	}
	return true, changed
}

// checkChannel never switches tcpip, restarts ADB, or falls back to a different
// transport. TCP reachability and authenticated identity both belong to addr.
func (a *App) checkChannel(ctx context.Context, key, addr, mode, expectedSerial, guid string) {
	a.checkChannelProbe(ctx, key, addr, mode, expectedSerial, guid, true)
}

func (a *App) checkChannelProbe(ctx context.Context, key, addr, mode, expectedSerial, guid string, probe bool) {
	a.checkChannelRequested(ctx, key, addr, mode, expectedSerial, guid, probe, false)
}

func (a *App) refreshChannel(ctx context.Context, key, addr, mode, expectedSerial, guid string) {
	a.checkChannelRequested(ctx, key, addr, mode, expectedSerial, guid, true, true)
}

func (a *App) checkChannelRequested(ctx context.Context, key, addr, mode, expectedSerial, guid string, probe, fresh bool) {
	if a.disc == nil || !IsIPPort(addr) || (mode != ModeTls && mode != ModeTcpip) || ctx.Err() != nil {
		return
	}
	var retry func()
	if fresh {
		retry = func() { a.refreshChannel(ctx, key, addr, mode, expectedSerial, guid) }
	}
	qctx, finish, ok := a.beginChannelCheckRequest(ctx, key+"|"+addr, retry)
	if !ok {
		return
	}
	defer finish()
	observation := a.profiles.observeChannel(key, mode)
	if observation.entry == nil {
		return
	}
	active, serial := false, ""
	if !probe || a.disc.TcpProbe(qctx, addr) {
		if _, err := a.disc.ConnectOut(qctx, addr); err == nil {
			for _, candidate := range a.readDeviceSerials(qctx, addr) {
				if contains(observation.serials, candidate) || candidate == key ||
					len(observation.serials) == 0 && expectedSerial != "" && candidate == expectedSerial {
					active, serial = true, candidate
					break
				}
			}
		}
	}
	active = active && qctx.Err() == nil
	// Parent/global cancellation is not evidence that a phone went offline.
	if ctx.Err() != nil || qctx.Err() == context.Canceled {
		return
	}
	changed := a.profiles.finishChannelCheck(key, addr, mode, serial, guid, observation, active)
	bridge.DebugLog("[app] 无线通道复核：%s/%s mode=%s active=%v changed=%v mdns=%v", key, addr, mode, active, changed, guid != "")
	if changed {
		// Publish each result independently, without launching tcpip learning.
		a.rebuildCurrentDisplay("无线通道独立复核")
	}
}

func (a *App) beginChannelCheck(ctx context.Context, jobKey string) (context.Context, func(), bool) {
	return a.beginChannelCheckRequest(ctx, jobKey, nil)
}

func (a *App) beginChannelCheckRequest(ctx context.Context, jobKey string, retry func()) (context.Context, func(), bool) {
	a.channelCheckMu.Lock()
	if a.channelChecksClosing || ctx.Err() != nil {
		a.channelCheckMu.Unlock()
		return nil, nil, false
	}
	if a.channelChecks[jobKey] != nil {
		if retry != nil {
			if a.channelRechecks == nil {
				a.channelRechecks = map[string]func(){}
			}
			a.channelRechecks[jobKey] = retry
		}
		a.channelCheckMu.Unlock()
		return nil, nil, false
	}
	if len(a.channelChecks) >= maxChannelChecks {
		a.channelCheckMu.Unlock()
		return nil, nil, false
	}
	if a.channelChecks == nil {
		a.channelChecks = map[string]context.CancelFunc{}
	}
	qctx, cancel := context.WithTimeout(ctx, channelCheckTimeout)
	a.channelChecks[jobKey] = cancel
	a.channelCheckWG.Add(1)
	a.channelCheckMu.Unlock()
	return qctx, func() {
		cancel()
		a.channelCheckMu.Lock()
		delete(a.channelChecks, jobKey)
		recheck := a.channelRechecks[jobKey]
		delete(a.channelRechecks, jobKey)
		if a.channelChecksClosing {
			recheck = nil
		}
		a.channelCheckMu.Unlock()
		a.channelCheckWG.Done()
		if recheck != nil {
			go a.guard("wireless-channel-fresh-recheck", recheck)
		}
	}, true
}

// Reconcile every stored channel even when the other channel/USB is still up.
// mDNS snapshots are address candidates, never successful connection evidence.
func (a *App) checkProfileChannels(ctx context.Context, onlyKey string, services []discovery.MdnsService) {
	a.checkProfileChannelsFiltered(ctx, onlyKey, services, false)
}

func (a *App) checkProfileChannelsFiltered(ctx context.Context, onlyKey string, services []discovery.MdnsService, recovery bool) {
	type target struct{ key, addr, mode, serial, guid string }
	targets := map[string]target{}
	entries := a.profiles.Entries()
	needsCheck := func(key, mode string) bool {
		if mode != ModeTls && mode != ModeTcpip {
			return false
		}
		if recovery {
			for _, peer := range entries[key].Addrs {
				if addrEntryClass(peer) == mode && peer.State == AddrStateActive {
					return false
				}
			}
		}
		return true
	}
	for key, entry := range entries {
		if onlyKey != "" && key != onlyKey {
			continue
		}
		for _, addr := range entry.Addrs {
			if !IsIPPort(addr.Addr) || !needsCheck(key, addrEntryClass(addr)) {
				continue
			}
			targets[key+"|"+addr.Addr] = target{key: key, addr: addr.Addr, mode: addrEntryClass(addr)}
		}
	}
	for _, service := range services {
		if service.Mode != ModeTls && service.Mode != ModeTcpip {
			continue
		}
		key := a.mdnsServiceIdentity(&service)
		if key == "" || onlyKey != "" && key != onlyKey || a.deletedUsbMarked(SerialFromServiceName(service.Name)) || !IsIPPort(service.Addr) || !needsCheck(key, service.Mode) {
			continue
		}
		targets[key+"|"+service.Addr] = target{key, service.Addr, service.Mode, SerialFromServiceName(service.Name), service.Name}
	}
	var wg sync.WaitGroup
	jobs := make(chan target, len(targets))
	for _, item := range targets {
		jobs <- item
	}
	close(jobs)
	for i := 0; i < min(maxChannelChecks, len(targets)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				a.checkChannel(ctx, t.key, t.addr, t.mode, t.serial, t.guid)
			}
		}()
	}
	wg.Wait()
}

func (a *App) closeChannelChecks() {
	a.channelCheckMu.Lock()
	a.channelChecksClosing = true
	for _, cancel := range a.channelChecks {
		cancel()
	}
	a.channelCheckMu.Unlock()
	a.channelCheckWG.Wait()
}
