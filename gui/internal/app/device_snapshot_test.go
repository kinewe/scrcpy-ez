package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
)

func snapshotFor(a *App, transports ...deviceevents.Transport) deviceevents.Snapshot {
	a.adb.EventHub().Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: transports})
	return a.adb.EventHub().Current()
}

func TestLateUSBRefreshCannotReopenLearning(t *testing.T) {
	a, usb := learningTestApp(t)
	wifi := adb.Device{Serial: "192.0.2.20:5555", State: "device", ConnType: "wifi", Identity: usb.Identity}
	a.profiles.AddrSuccess(usb.Identity, wifi.Serial)
	var reads atomic.Int32
	a.teachOps.getpropFn = func(context.Context, string, string) (string, error) {
		reads.Add(1)
		return "", errors.New("USB removed")
	}
	old := snapshotFor(a, deviceevents.Transport{Serial: usb.Serial, State: "device", Kind: "usb", ID: "10"})
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		<-release // old refresh finishes after the unplug event
		a.applyDeviceSnapshot(context.Background(), []adb.Device{usb}, &old, "refresh")
		close(done)
	}()
	<-started
	current := snapshotFor(a, deviceevents.Transport{Serial: wifi.Serial, State: "device", Kind: "wifi", ID: "11"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{wifi}, &current, "track")
	close(release)
	<-done
	devices := a.Snapshot().Devices
	if len(devices) != 1 || devices[0].Serial != wifi.Serial || devices[0].Connecting || reads.Load() != 0 {
		t.Fatalf("late refresh revived USB or learning: devices=%+v USB reads=%d", devices, reads.Load())
	}
}

func TestRawUSBOfflineIsNotAnotherPlugOrReadyDevice(t *testing.T) {
	a, usb := learningTestApp(t)
	wifi := adb.Device{Serial: "192.0.2.20:5555", State: "device", ConnType: "wifi", Identity: usb.Identity}
	a.profiles.AddrSuccess(usb.Identity, wifi.Serial)
	var reads atomic.Int32
	a.teachOps.getpropFn = func(context.Context, string, string) (string, error) {
		reads.Add(1)
		return "", errors.New("not ready")
	}
	rawUSB := deviceevents.Transport{Serial: usb.Serial, State: "offline", Kind: "usb", ID: "10"}
	rawWiFi := deviceevents.Transport{Serial: wifi.Serial, State: "device", Kind: "wifi", ID: "11"}
	first := snapshotFor(a, rawUSB, rawWiFi)
	a.applyDeviceSnapshot(context.Background(), []adb.Device{wifi}, &first, "track")
	a.teachMu.Lock()
	started := a.plugging[usb.Identity]
	a.teachMu.Unlock()
	// The display merge can make a USB card look device-ready by borrowing the
	// online wireless state. Raw USB is still offline and has not been re-added.
	second := snapshotFor(a, rawUSB, rawWiFi)
	a.applyDeviceSnapshot(context.Background(), []adb.Device{usb, wifi}, &second, "refresh")
	a.teachMu.Lock()
	unchanged := a.plugging[usb.Identity].Equal(started)
	a.teachMu.Unlock()
	if !unchanged || reads.Load() != 0 {
		t.Fatalf("card merging started USB learning: unchanged=%v reads=%d", unchanged, reads.Load())
	}
}

func TestUnplugDuringLearningCancelsUSBWithoutRestart(t *testing.T) {
	a, usb := learningTestApp(t)
	a.teachOps.getpropFn = func(context.Context, string, string) (string, error) { return "5555", nil }
	a.teachOps.probeFn = func(context.Context, string) bool { return false }
	initial := snapshotFor(a, deviceevents.Transport{Serial: usb.Serial, State: "device", Kind: "usb", ID: "10"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{usb}, &initial, "track")
	a.teachMu.Lock()
	state := a.usbLearning[usb.Serial]
	a.teachMu.Unlock()
	if state == nil {
		t.Fatal("learning was not started")
	}
	wifi := adb.Device{Serial: "192.0.2.20:5555", State: "device", ConnType: "wifi", Identity: usb.Identity}
	// The ready wireless transport has confirmed ownership. Identity alone is a
	// display hint and cannot add an unverified IP to a permanent archive.
	wifi.StableSerial = usb.Serial
	current := snapshotFor(a, deviceevents.Transport{Serial: wifi.Serial, State: "device", Kind: "wifi", ID: "11"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{wifi}, &current, "track")
	devices := a.Snapshot().Devices
	if state.ctx.Err() == nil || a.plugActiveSerial(usb.Serial) || len(devices) != 1 || devices[0].ConnType != "wifi" || devices[0].Connecting {
		t.Fatalf("removed USB still covered ready wireless: cancelled=%v devices=%+v", state.ctx.Err(), devices)
	}
}

func TestIssuedAdbdRestartPreservesUSBProtection(t *testing.T) {
	a, usb := learningTestApp(t)
	a.teachOps.probeFn = func(context.Context, string) bool { return false }
	initial := snapshotFor(a, deviceevents.Transport{Serial: usb.Serial, State: "device", Kind: "usb", ID: "10"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{usb}, &initial, "track")
	a.teachMu.Lock()
	state := a.usbLearning[usb.Serial]
	deadline, _ := state.ctx.Deadline()
	a.teachMu.Unlock()
	waitFor(t, 2*time.Second, state.restartIssued.Load, "learning must issue tcpip before the simulated restart")
	if !state.restartIssued.Load() {
		t.Fatal("test must issue tcpip before the restart gap")
	}
	gap := snapshotFor(a)
	a.applyDeviceSnapshot(context.Background(), nil, &gap, "track")
	if !a.plugActiveSerial(usb.Serial) || state.ctx.Err() != nil {
		t.Fatal("actual adbd restart lost its protection")
	}
	// USB returns with a new transport ID. This is the same intentional restart,
	// so the learning deadline remains bounded rather than receiving another 10s.
	returned := snapshotFor(a, deviceevents.Transport{Serial: usb.Serial, State: "device", Kind: "usb", ID: "12"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{usb}, &returned, "track")
	a.teachMu.Lock()
	next := a.usbLearning[usb.Serial]
	nextDeadline, _ := next.ctx.Deadline()
	a.teachMu.Unlock()
	if next != state || !deadline.Equal(nextDeadline) {
		t.Fatal("adbd restart replaced or extended the learning cycle")
	}
}

func TestUnplugBeforeUSBReadyDoesNotWaitForLearningTimeout(t *testing.T) {
	a, usb := learningTestApp(t)
	usb.State = "offline"
	a.unplugConfirmFn = func(context.Context, string, string, int) {}
	a.profiles.AddrSuccess(usb.Identity, "192.0.2.20:5555")
	initial := snapshotFor(a, deviceevents.Transport{Serial: usb.Serial, State: "offline", Kind: "usb", ID: "10"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{usb}, &initial, "track")
	removed := snapshotFor(a)
	a.applyDeviceSnapshot(context.Background(), nil, &removed, "track")
	devices := a.Snapshot().Devices
	if a.plugActiveSerial(usb.Serial) || len(devices) != 1 || devices[0].ConnType != "wifi" || !devices[0].Connecting {
		t.Fatalf("offline USB removal kept insertion shield: %+v", devices)
	}
}

func TestCoalescedUSBReplugStartsFreshLearning(t *testing.T) {
	a, usb := learningTestApp(t)
	a.teachOps.getpropFn = func(context.Context, string, string) (string, error) { return "5555", nil }
	a.teachOps.probeFn = func(context.Context, string) bool { return false }
	initial := snapshotFor(a, deviceevents.Transport{Serial: usb.Serial, State: "device", Kind: "usb", ID: "10"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{usb}, &initial, "track")
	a.teachMu.Lock()
	old := a.usbLearning[usb.Serial]
	a.teachMu.Unlock()
	// The GUI does not consume the intermediate empty list.
	snapshotFor(a)
	replugged := snapshotFor(a, deviceevents.Transport{Serial: usb.Serial, State: "device", Kind: "usb", ID: "12"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{usb}, &replugged, "track")
	a.teachMu.Lock()
	current := a.usbLearning[usb.Serial]
	a.teachMu.Unlock()
	if current == nil || current == old || old.ctx.Err() == nil {
		t.Fatal("coalesced physical replug reused the cancelled USB learning cycle")
	}
}

func TestUSBRemovalOnlyCancelsItsOwnDeviceLearning(t *testing.T) {
	a, pad := learningTestApp(t)
	phone := adb.Device{Serial: "PHONE2", State: "device", ConnType: "usb", Identity: "device:PHONE2", Marketname: "Other Phone"}
	a.profiles.SyncDevices([]adb.Device{pad, phone})
	a.appListBusy[phone.Identity] = a.appListBusy[pad.Identity]
	a.teachOps.getpropFn = func(context.Context, string, string) (string, error) { return "5555", nil }
	a.teachOps.probeFn = func(context.Context, string) bool { return false }
	phoneTransport := deviceevents.Transport{Serial: phone.Serial, State: "device", Kind: "usb", ID: "20"}
	initial := snapshotFor(a, deviceevents.Transport{Serial: pad.Serial, State: "device", Kind: "usb", ID: "10"}, phoneTransport)
	a.applyDeviceSnapshot(context.Background(), []adb.Device{pad, phone}, &initial, "track")
	a.teachMu.Lock()
	padState, phoneState := a.usbLearning[pad.Serial], a.usbLearning[phone.Serial]
	a.teachMu.Unlock()
	wifi := adb.Device{Serial: "192.0.2.20:5555", State: "device", ConnType: "wifi", Identity: pad.Identity}
	current := snapshotFor(a, deviceevents.Transport{Serial: wifi.Serial, State: "device", Kind: "wifi", ID: "11"}, phoneTransport)
	a.applyDeviceSnapshot(context.Background(), []adb.Device{wifi, phone}, &current, "track")
	if padState.ctx.Err() == nil || phoneState.ctx.Err() != nil || !a.plugActiveSerial(phone.Serial) {
		t.Fatalf("Pad unplug affected the other USB device: pad=%v phone=%v", padState.ctx.Err(), phoneState.ctx.Err())
	}
}
