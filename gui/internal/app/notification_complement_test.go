package app

import (
	"os"
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
)

func TestNotificationComplementSelectionAndDefaults(t *testing.T) {
	a := New(Config{Version: "test"})
	if err := a.profiles.Save("TABLET", DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	if err := a.profiles.SetApps("device:TABLET", []AppListItem{{Pkg: "com.example.mail"}, {Pkg: "com.example.chat"}}); err != nil {
		t.Fatal(err)
	}
	if a.Settings().NotificationPolicy("device:TABLET").Mode != "all" {
		t.Fatal("new devices must default to all notifications")
	}
	for _, sample := range []struct {
		packages []string
		other    bool
		mode     string
	}{{nil, true, "whitelist"}, {[]string{"com.example.mail", "com.example.chat"}, false, "whitelist"}, {[]string{"com.example.mail", "com.example.chat"}, true, "all"}, {nil, false, "off"}} {
		if err := a.SetNotificationSelection("TABLET", sample.packages, sample.other); err != nil {
			t.Fatal(err)
		}
		p := a.Settings().NotificationPolicy("device:TABLET")
		if p.Mode != sample.mode || p.Other != sample.other {
			t.Fatal("all apps plus complementary sources must equal all notifications")
		}
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"notificationDefault":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.Get().NotificationsEnabled("tablet") {
		t.Fatal("explicit existing off preference overwritten")
	}
	if err := s.Set(false, true); err != nil {
		t.Fatal(err)
	}
	afterUnrelatedSave := NewSettingsStore(path)
	if err := afterUnrelatedSave.Load(); err != nil {
		t.Fatal(err)
	}
	if afterUnrelatedSave.Get().NotificationsEnabled("tablet") {
		t.Fatal("explicit off preference was omitted while saving unrelated settings")
	}
	if err := s.SetNotificationSelection("tablet", []string{"com.example.mail"}, true, false); err != nil {
		t.Fatal(err)
	}
	reloaded := NewSettingsStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	persisted := reloaded.Get().NotificationPolicy("tablet")
	if !persisted.Other || len(persisted.Packages) != 1 || persisted.Mode != "whitelist" {
		t.Fatal("complement selection lost after restart")
	}
}

func TestNotificationTargetRejectsAddressAsPhysicalIdentity(t *testing.T) {
	d := adb.Device{Identity: "device:TABLET", Serial: "192.0.2.1:5555", StableSerial: "192.0.2.1:5555", ConnType: "wifi"}
	raw := deviceevents.Snapshot{Available: true, Transports: []deviceevents.Transport{{Serial: d.Serial, State: "device", Kind: "wifi"}}}
	targets := notificationTargets([]adb.Device{d}, map[string]DeviceEntry{d.Identity: {Serials: []string{"TABLET"}}}, raw, DefaultSettings())
	if len(targets) != 1 || targets[0].DeviceSerial != "TABLET" {
		t.Fatal("wireless address reached the notification identity handshake")
	}
}
