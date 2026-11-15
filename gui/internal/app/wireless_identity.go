package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

// One record per continuously online transport. Removing/offlining the
// transport invalidates both cached evidence and outstanding query results.
// App.mu protects all fields; queries never hold the display or archive locks.
type wirelessIdentityCheck struct {
	epoch   uint64
	addr    string
	serial  string
	busy    bool
	runs    int
	name    string
	nameSet bool
}

func (a *App) prepareWirelessIdentityChecks(devs []adb.Device) []*wirelessIdentityCheck {
	services := a.mdnsSnapshot()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wirelessIdentityChecks == nil {
		a.wirelessIdentityChecks = map[string]*wirelessIdentityCheck{}
	}
	seen := map[string]bool{}
	var jobs []*wirelessIdentityCheck
	for _, d := range devs {
		if d.ConnType != "wifi" || d.State != "device" || !IsIPPort(d.Serial) {
			continue
		}
		seen[d.Serial] = true
		if a.wirelessIdentityChecks[d.Serial] != nil {
			continue
		}
		a.wirelessIdentityEpoch++
		check := &wirelessIdentityCheck{addr: d.Serial, epoch: a.wirelessIdentityEpoch}
		a.wirelessIdentityChecks[d.Serial] = check
		if serial, _ := serialFromMdnsServices(services, d.Serial); serial == "" {
			check.busy, check.runs = true, 1
			jobs = append(jobs, check)
		}
	}
	for addr := range a.wirelessIdentityChecks {
		if !seen[addr] {
			delete(a.wirelessIdentityChecks, addr)
		}
	}
	return jobs
}

func (a *App) verifiedWirelessSerial(addr string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if check := a.wirelessIdentityChecks[addr]; check != nil {
		return check.serial
	}
	return ""
}

func (a *App) wirelessEpoch(addr string) uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if check := a.wirelessIdentityChecks[addr]; check != nil {
		return check.epoch
	}
	return 0
}

func (a *App) wirelessCheckCurrent(check *wirelessIdentityCheck) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.wirelessIdentityChecks[check.addr] == check
}

func (a *App) learnWirelessIdentity(check *wirelessIdentityCheck) {
	a.guard("wireless-identity", func() {
		a.waitSrvReady(context.Background())
		ctx, cancel := context.WithTimeout(context.Background(), pairLearnBudget)
		defer cancel()
		defer func() {
			a.mu.Lock()
			check.busy = false
			a.mu.Unlock()
		}()
		for attempt := 0; attempt < learnRetryAttempts && ctx.Err() == nil; attempt++ {
			if !a.wirelessCheckCurrent(check) {
				return
			}
			if serial := a.readWirelessDeviceSerial(ctx, check.addr); serial != "" {
				a.commitWirelessIdentity(check, serial)
				return
			}
			if attempt+1 < learnRetryAttempts {
				select {
				case <-ctx.Done():
					return
				case <-time.After(learnRetryInterval):
				}
			}
		}
	})
}

// Wireless adb get-serialno returns the transport address, not a device serial.
// Read the device properties directly, with boot serial as a missing-value fallback.
func (a *App) readWirelessDeviceSerial(ctx context.Context, addr string) string {
	if a.pairOps.getpropFn == nil {
		return ""
	}
	for _, prop := range []string{"ro.serialno", "ro.boot.serialno"} {
		if ctx.Err() != nil {
			break
		}
		qctx, cancel := context.WithTimeout(ctx, serialReadTimeout)
		value, err := a.pairOps.getpropFn(qctx, addr, prop)
		cancel()
		if err == nil {
			if serial := adb.StableSerial(value); serial != "" {
				return serial
			}
		}
	}
	return ""
}

// Called under App.mu on a successful cast signal. One additional bounded
// round is allowed after an unsuccessful online round, never on UI polling.
func (a *App) retryWirelessIdentityLocked(check *wirelessIdentityCheck) {
	if check == nil || a.wirelessIdentityChecks[check.addr] != check || check.serial != "" || check.busy || check.runs >= 2 {
		return
	}
	check.busy = true
	check.runs++
	go a.learnWirelessIdentity(check)
}

// A cast can recreate ADB between startup and connection. Only a fresh success
// signal from that cast may attach its provisional identity to the new online
// epoch; ordinary background discovery cannot move an old session to a new owner.
// Caller holds App.mu and has checked the runner is still current.
func (a *App) bindPendingCastIdentityLocked(identity string, marker **wirelessIdentityCheck, addr string) string {
	if !strings.HasPrefix(identity, "pending:") {
		return identity
	}
	if !IsIPPort(addr) && *marker != nil {
		addr = (*marker).addr
	}
	check := a.wirelessIdentityChecks[addr]
	if check == nil {
		return identity
	}
	*marker = check
	if check.serial != "" {
		if key := a.profiles.ResolveKey(check.serial); key != "" {
			return key
		}
	}
	a.retryWirelessIdentityLocked(check)
	return identity
}

func (a *App) commitWirelessIdentity(check *wirelessIdentityCheck, serial string) {
	a.applyTrackMu.Lock()
	defer a.applyTrackMu.Unlock()
	a.mu.Lock()
	if a.wirelessIdentityChecks[check.addr] != check {
		a.mu.Unlock()
		return
	}
	base := append([]adb.Device(nil), a.lastTrack...)
	found := false
	for i := range base {
		if base[i].Serial == check.addr && base[i].State == "device" {
			base[i].StableSerial = serial
			base[i].Identity = "" // direct serial evidence decides the archive
			found = true
		}
	}
	if !found {
		a.mu.Unlock()
		return
	}
	check.serial = serial
	name, nameSet := check.name, check.nameSet
	a.mu.Unlock()
	// Sync the whole current list so other devices retain their online state.
	a.profiles.SyncDevicesExempt(a.devicesForSync(base), a.plugExemptIDs())
	a.annotateDeviceIdentities(base)
	key := a.profiles.ResolveKey(serial)
	if key == "" {
		return // ambiguous legacy claims must not attach to an arbitrary archive
	}
	if nameSet {
		a.profiles.SetDisplayName(key, name)
	}
	a.mu.Lock()
	a.lastTrack = base
	for _, st := range a.sessions {
		if st.identityCheck == check && strings.HasPrefix(st.identity, "pending:") {
			st.identity = key
			if spec := st.cast.Spec; spec != nil && !spec.Custom {
				a.updateBaseline(key, st.cast.Mode, Baseline{Res: spec.MaxSize, FPS: spec.FPS, Bitrate: spec.Mbps})
			}
		}
	}
	for _, st := range a.appWins {
		if st.identityCheck == check && strings.HasPrefix(st.identity, "pending:") {
			st.identity = key
		}
	}
	a.mu.Unlock()
	bridge.DebugLog("[app] 无线身份补学完成：%s -> %s", check.addr, key)
	a.rebuildCurrentDisplay("无线身份补学")
}

// A rename submitted during learning belongs to that online epoch only. Never
// write a pending/IP archive or carry a queued name to a later address owner.
func (a *App) renamePendingDevice(key, name string) (bool, error) {
	if !strings.HasPrefix(key, "pending:") {
		return false, nil
	}
	a.applyTrackMu.Lock()
	defer a.applyTrackMu.Unlock()
	addr := strings.TrimPrefix(key, "pending:")
	pos := strings.LastIndexByte(addr, '|')
	if pos < 0 {
		return true, errors.New("设备身份正在确认，请稍后重新选择设备改名")
	}
	epoch, err := strconv.ParseUint(addr[pos+1:], 10, 64)
	if err != nil {
		return true, err
	}
	addr = addr[:pos]
	a.mu.Lock()
	check := a.wirelessIdentityChecks[addr]
	if check == nil || check.epoch != epoch {
		a.mu.Unlock()
		return true, errors.New("设备已离线，请重新选择设备后改名")
	}
	serial := check.serial
	check.name, check.nameSet = strings.TrimSpace(name), true
	a.mu.Unlock()
	if serial != "" {
		if resolved := a.profiles.ResolveKey(serial); resolved != "" {
			a.profiles.SetDisplayName(resolved, name)
		}
	}
	return true, nil
}
