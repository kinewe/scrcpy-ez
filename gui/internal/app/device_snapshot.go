package app

import (
	"context"
	"regexp"
	"strings"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/deviceevents"
)

// Property enrichment happens outside applyTrackMu. Validate its original raw
// token inside the commit lock so a slow refresh cannot recreate a removed USB.
func (a *App) applyDeviceSnapshot(ctx context.Context, devs []adb.Device, raw *deviceevents.Snapshot, source string) {
	a.applyTrackMu.Lock()
	defer a.applyTrackMu.Unlock()
	var usb []adb.Device
	var replacements map[string]bool
	if raw != nil {
		current := a.adb.EventHub().Current()
		if !deviceevents.SameTransports(*raw, current) {
			bridge.DebugLog("[app] 丢弃过期设备快照：来源=%s epoch=%d seq=%d，当前 epoch=%d seq=%d", source, raw.Epoch, raw.Sequence, current.Epoch, current.Sequence)
			return
		}
		if raw.Available {
			replacements = a.usbTransportReplacements(*raw)
			var summary strings.Builder
			for _, t := range raw.Transports {
				if t.Kind == "usb" {
					usb = append(usb, adb.Device{Serial: t.Serial, State: t.State, ConnType: "usb"})
				}
				summary.WriteString(" [" + t.Serial + "|" + t.State + "|" + t.Kind + "|id=" + t.ID + "]")
			}
			bridge.DebugLog("[app] 原始设备快照：来源=%s epoch=%d seq=%d%s", source, raw.Epoch, raw.Sequence, summary.String())
		} else {
			usb = usbDevices(devs)
		}
	} else {
		usb = usbDevices(devs)
	}
	a.applyTrackUpdateLocked(ctx, filterUnavailableEmulators(devs), usb, raw != nil && raw.Available, replacements)
	if raw != nil && raw.Available {
		// The enriched list may already have folded TLS+5555 into one card.
		// Detect removals from raw endpoint tokens, before that information is lost.
		for key := range a.removedWirelessChannels(*raw) {
			// Refresh snapshots use a short-lived context which is cancelled as
			// soon as the commit returns. Checks own their bounded lifetime and
			// are cancelled by BeginClose instead.
			go a.checkProfileChannels(context.Background(), key, nil)
		}
	}
}

var emulatorSerial = regexp.MustCompile(`^emulator-[0-9]+$`)

// ADB can register arbitrary loopback listeners as offline emulators. EZ's
// device list focuses on physical Android devices, so unavailable emulator
// transports do not become cards or affect its count. Keep online emulators
// and other service tokens: mDNS folding still needs the latter.
func filterUnavailableEmulators(devs []adb.Device) []adb.Device {
	visible := make([]adb.Device, 0, len(devs))
	for _, d := range devs {
		if d.ConnType == "other" && d.State != "device" && emulatorSerial.MatchString(d.Serial) {
			continue
		}
		visible = append(visible, d)
	}
	return visible
}

// Called under applyTrackMu. An unavailable ADB snapshot is not negative
// evidence for every phone; retain the previous tokens until ADB returns.
func (a *App) removedWirelessChannels(raw deviceevents.Snapshot) map[string]bool {
	next := map[string]deviceevents.Transport{}
	for _, transport := range raw.Transports {
		if IsIPPort(transport.Serial) {
			next[transport.Serial] = transport
		}
	}
	removed := map[string]bool{}
	for addr, previous := range a.lastChannelToken {
		current, present := next[addr]
		if previous.State != "device" || present && current.State == "device" {
			continue
		}
		if key := a.profiles.ResolveKey(addr); key != "" {
			removed[key] = true
		}
	}
	a.lastChannelToken = next
	return removed
}

// Coalescing may skip an entire unplug/replug gap. A new transport ID or ADB
// epoch still identifies the new connection even when serial/state are equal.
func (a *App) usbTransportReplacements(raw deviceevents.Snapshot) map[string]bool {
	replaced := map[string]bool{}
	next := map[string]deviceevents.Transport{}
	for _, t := range raw.Transports {
		if t.Kind != "usb" {
			continue
		}
		if old, exists := a.lastUSBToken[t.Serial]; exists && (a.lastUSBEpoch != raw.Epoch || old.ID != "" && t.ID != "" && old.ID != t.ID || (old.ID == "" || t.ID == "") && old.State == t.State && old.Generation != t.Generation) {
			replaced[t.Serial] = true
		}
		next[t.Serial] = t
	}
	a.lastUSBToken, a.lastUSBEpoch = next, raw.Epoch
	return replaced
}

func (a *App) resetReplacedUSBLearning(serial string) bool {
	id := a.plugIDForSerial(serial)
	a.teachMu.Lock()
	if state := a.usbLearning[serial]; state != nil && state.restartIssued.Load() {
		a.teachMu.Unlock()
		return false // the transport replacement belongs to our bounded restart
	}
	delete(a.taughtTcpip, serial)
	a.teachMu.Unlock()
	a.plugClear(id, "USB连接代次更换")
	return true
}

func usbDevices(devs []adb.Device) []adb.Device {
	var usb []adb.Device
	for _, d := range devs {
		if d.ConnType == "usb" {
			usb = append(usb, d)
		}
	}
	return usb
}

// Only a tcpip command issued by this learning cycle can justify hiding USB
// removal during an adbd restart. An ordinary unplug cancels obsolete learning
// immediately and lets the unplug state machine confirm the wireless route.
func (a *App) releaseDetachedUSBProtection(usb []adb.Device) {
	present := make(map[string]bool, len(usb))
	for _, d := range usb {
		present[a.identityOf(&d)] = true
	}
	var clear []string
	a.teachMu.Lock()
	for id := range a.plugging {
		if present[id] {
			continue
		}
		restarting := false
		for _, state := range a.usbLearning {
			if state.id == id && state.restartIssued.Load() {
				restarting = true
				break
			}
		}
		if !restarting {
			clear = append(clear, id)
		}
	}
	a.teachMu.Unlock()
	for _, id := range clear {
		a.plugClear(id, "原始USB移除，无adbd重启")
	}
}
