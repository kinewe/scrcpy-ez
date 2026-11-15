package app

import (
	"context"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/discovery"
)

type wirelessTcpipLearning struct {
	online, busy bool
	cancel       context.CancelFunc
}

// Track the online transition as well as our own connect result: ADB's native
// mDNS connector can establish TLS before the recovery worker gets a turn.
func (a *App) maybeLearnWirelessTcpip(devs []adb.Device) {
	if a.pairOps.tcpipFn == nil || a.pairOps.probeFn == nil {
		return
	}
	seen := map[string]bool{}
	for _, d := range devs {
		if d.State != "device" || d.ConnType != "wifi" {
			continue
		}
		for _, s := range a.mdnsSnapshot() {
			if s.Mode != discovery.MdnsModeTls || !validPairIP(ipOfAddr(s.Addr)) {
				continue
			}
			if d.Serial != s.Addr && d.Serial != s.Name+"._adb-tls-connect._tcp" {
				continue
			}
			serial := SerialFromServiceName(s.Name)
			key := a.profiles.ResolveKey(serial)
			if key == "" {
				continue
			}
			seen[key] = true
			a.startWirelessTcpipLearning(key, s.Addr, serial)
		}
	}
	a.mu.Lock()
	for key, state := range a.wirelessTcpipLearning {
		if !seen[key] {
			state.online = false
			if !state.busy {
				delete(a.wirelessTcpipLearning, key)
			}
		}
	}
	a.mu.Unlock()
}

func (a *App) startWirelessTcpipLearning(key, addr, serial string) {
	if a.pairOps.tcpipFn == nil || a.pairOps.probeFn == nil || key == "" || serial == "" ||
		!validPairIP(ipOfAddr(addr)) || !validPairPort(addrPort(addr)) || addrPort(addr) == "5555" {
		return
	}
	a.mu.Lock()
	// The pairing flow already owns initial TCP/IP setup. Do not restart adbd
	// underneath its verification/archive phase or duplicate its success job.
	if ps := a.pair; ps != nil && (ps.Phase == PairPhasePairing || ps.Phase == PairPhaseConn || ps.Phase == PairPhaseVerify ||
		ps.Phase == PairPhaseSuccess && ipOfAddr(ps.Addr) == ipOfAddr(addr)) {
		a.mu.Unlock()
		return
	}
	if a.wirelessTcpipLearning == nil {
		a.wirelessTcpipLearning = map[string]*wirelessTcpipLearning{}
	}
	state := a.wirelessTcpipLearning[key]
	if state == nil {
		state = &wirelessTcpipLearning{}
		a.wirelessTcpipLearning[key] = state
	}
	already := state.online || state.busy
	state.online = true
	if !already {
		state.busy = true
	}
	a.mu.Unlock()
	if already {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	a.mu.Lock()
	if a.notificationClosing {
		state.busy = false
		a.mu.Unlock()
		cancel()
		return
	}
	state.cancel = cancel
	a.learningWG.Add(1)
	a.mu.Unlock()
	go a.guard("wireless-relearn-tcpip", func() {
		defer a.learningWG.Done()
		defer func() { a.mu.Lock(); state.busy = false; a.mu.Unlock() }()
		defer cancel()
		a.learningWork.Do(ctx, func() {
			// Verify through the actual connected TLS transport before issuing any
			// command that can restart adbd. mDNS/IP claims are discovery hints only.
			verified := false
			for attempt := 0; attempt < 3 && ctx.Err() == nil; attempt++ {
				actual := a.readWirelessDeviceSerial(ctx, addr)
				if actual != "" && actual != serial || a.profiles.ResolveKey(serial) != key {
					return
				}
				if actual == serial {
					verified = true
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(400 * time.Millisecond):
				}
			}
			if !verified {
				return
			}
			ip := ipOfAddr(addr)
			target := ip + ":5555"
			pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
			port, err := a.pairOps.getpropFn(pctx, addr, "service.adb.tcp.port")
			pcancel()
			if err != nil {
				return
			}
			if strings.TrimSpace(port) != "5555" {
				tctx, tcancel := context.WithTimeout(ctx, 2*time.Second)
				err = a.pairOps.tcpipFn(tctx, addr, "5555")
				tcancel()
				if err != nil {
					bridge.DebugLog("[app] 无线恢复学习 tcpip 失败：%s %v", addr, err)
					// adbd may restart before its acknowledgement arrives. The
					// authenticated probe below decides whether setup actually worked.
				}
			}
			for attempt := 0; attempt < 3 && ctx.Err() == nil; attempt++ {
				if a.pairOps.probeFn(ctx, target) {
					cctx, ccancel := context.WithTimeout(ctx, 3*time.Second)
					_, err := a.pairOps.connectFn(cctx, target)
					ccancel()
					if err == nil && a.readWirelessDeviceSerial(ctx, target) == serial && a.profiles.ResolveKey(serial) == key {
						a.profiles.AddrSuccessMode(key, target, ModeTcpip)
						bridge.DebugLog("[app] 无线恢复学习 tcpip 完成：%s %s", key, target)
						return
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(400 * time.Millisecond):
				}
			}
		})
	})
}
