package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestNewDevicePopupNameFollowsEnrichmentAndRename(t *testing.T) {
	for _, conn := range []string{"usb", "wifi"} {
		t.Run(conn, func(t *testing.T) {
			a, _ := multiTestApp()
			t.Cleanup(a.Close)
			serial := "POPUP_PHONE"
			if conn == "wifi" {
				serial = "192.0.2.172:5555"
			}
			d := adb.Device{Serial: serial, State: "device", ConnType: conn, Name: serial}
			a.applyTrackUpdate([]adb.Device{d})
			initial := a.Snapshot().NewDevice
			if initial == nil || initial.Serial != serial {
				t.Fatalf("missing popup for initial topology: %+v", initial)
			}

			d.StableSerial = "POPUP_PHONE"
			d.Marketname = "Xiaomi Pad 8 Pro"
			d.Name = d.Marketname
			a.applyTrackUpdate([]adb.Device{d})
			market := a.Snapshot().NewDevice
			if market == nil || market.Name != d.Marketname || market.Serial != serial || market.ConnType != conn {
				t.Fatalf("popup must refresh after enrichment without changing its action target: %+v", market)
			}
			if initial.Name != serial {
				t.Fatalf("an earlier snapshot was mutated: %+v", initial)
			}

			key := a.profiles.DeviceKey(&d)
			if key == "" {
				t.Fatal("enriched device was not archived")
			}
			if err := a.RenameDevices(map[string]string{key: "我的平板"}); err != nil {
				t.Fatal(err)
			}
			if named := a.Snapshot().NewDevice; named == nil || named.Name != "我的平板" {
				t.Fatalf("custom name must take priority while popup stays open: %+v", named)
			}
			if market.Name != d.Marketname {
				t.Fatalf("rename mutated a previously returned snapshot: %+v", market)
			}
			if err := a.RenameDevices(map[string]string{key: ""}); err != nil {
				t.Fatal(err)
			}
			if restored := a.Snapshot().NewDevice; restored == nil || restored.Name != d.Marketname {
				t.Fatalf("clearing custom name must restore the market name: %+v", restored)
			}
		})
	}
}

func TestPopupLiveMarketNamePrecedesArchivedModel(t *testing.T) {
	a, _ := multiTestApp()
	t.Cleanup(a.Close)
	d := adb.Device{Serial: "MODEL_ONLY_PHONE", State: "device", ConnType: "usb", Model: "2410TEST"}
	a.profiles.SyncDevices([]adb.Device{d})
	d.Marketname = "REDMI K80"
	if name := a.displayNameOf(&d); name != d.Marketname {
		t.Fatalf("archived model must not hide a newly read market name: %q", name)
	}
}
