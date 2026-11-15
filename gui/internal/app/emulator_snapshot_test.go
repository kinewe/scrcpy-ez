package app

import (
	"context"
	"reflect"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
)

func TestUnavailableEmulatorFilterPreservesPhysicalAndServiceTransports(t *testing.T) {
	keep := []adb.Device{
		{Serial: "PHONE", State: "offline", ConnType: "usb"},
		{Serial: "PAD", State: "unauthorized", ConnType: "usb"},
		{Serial: "192.0.2.1:5555", State: "offline", ConnType: "wifi"},
		{Serial: "emulator-5554", State: "device", ConnType: "other"},
		{Serial: "adb-PHONE-AbCd12._adb-tls-connect._tcp", State: "offline", ConnType: "other"},
		{Serial: "emulator-phone", State: "offline", ConnType: "other"},
		{Serial: "emulator-5582-extra", State: "offline", ConnType: "other"},
		{Serial: "emulator-", State: "offline", ConnType: "other"},
		{Serial: "emulator-5582", State: "unauthorized", ConnType: "usb"},
	}
	input := append([]adb.Device(nil), keep...)
	for _, state := range []string{"offline", "host", "unauthorized", "bootloader", ""} {
		input = append(input, adb.Device{Serial: "emulator-5582", State: state, ConnType: "other"})
	}
	original := append([]adb.Device(nil), input...)
	if got := filterUnavailableEmulators(input); !reflect.DeepEqual(got, keep) {
		t.Fatalf("filter changed physical devices, service tokens or online emulators: %+v", got)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("filter mutated a shared snapshot")
	}
}

// Exercise all production snapshot entry modes through the final UI state,
// including a ghost-only snapshot and raw observer state kept for diagnostics.
func TestUnavailableEmulatorsNeverReachCardsCountOrProfiles(t *testing.T) {
	for _, source := range []string{"track", "refresh", "direct"} {
		for _, state := range []string{"offline", "host", "unauthorized"} {
			t.Run(source+"/"+state, func(t *testing.T) {
				a, _ := newWirelessApp()
				ghost := adb.Device{Serial: "emulator-5582", State: state, ConnType: "other"}
				var raw *deviceevents.Snapshot
				if source != "direct" {
					snapshot := snapshotFor(a, deviceevents.Transport{Serial: ghost.Serial, State: state, Kind: "other"})
					raw = &snapshot
				}
				a.applyDeviceSnapshot(context.Background(), []adb.Device{ghost}, raw, source)
				if got := a.Snapshot().Devices; len(got) != 0 {
					t.Fatalf("ghost counted as a UI card: %+v", got)
				}
				if key := a.profiles.ResolveKey(ghost.Serial); key != "" {
					t.Fatalf("ghost created an archive: %s", key)
				}
				if raw != nil && len(a.adb.EventHub().Current().Transports) != 1 {
					t.Fatal("presentation filtering changed raw ADB observer state")
				}
			})
		}
	}
}

func TestGhostAlongsidePhysicalDeviceKeepsOnlyPhysicalCard(t *testing.T) {
	a := newDisplayTestApp()
	phone := adb.Device{Serial: displayTestTLS, State: "device", ConnType: "wifi", StableSerial: "CARDTEST"}
	ghost := adb.Device{Serial: "emulator-5582", State: "offline", ConnType: "other"}
	raw := snapshotFor(a,
		deviceevents.Transport{Serial: phone.Serial, State: "device", Kind: "wifi"},
		deviceevents.Transport{Serial: ghost.Serial, State: "offline", Kind: "other"})
	a.applyDeviceSnapshot(context.Background(), []adb.Device{phone, ghost}, &raw, "track")
	d := displayTestCard(t, a)
	if d.Serial != phone.Serial || d.State != "device" {
		t.Fatalf("ghost changed the physical device card: %+v", d)
	}
}
