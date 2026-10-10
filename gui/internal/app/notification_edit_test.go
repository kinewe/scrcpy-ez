package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNotificationEditCommitsTogetherAndPreservesOtherDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := NewSettingsStore(path)
	if err := s.SetNotificationModes([]string{"phone"}, "otp"); err != nil {
		t.Fatal(err)
	}
	preview, minutes := false, 60
	edit := NotificationEdit{Selection: &NotificationSelection{Packages: []string{"com.example.mail", "com.example.mail"}, Other: true}, Preview: &preview, OpenEnabled: &preview, CopyMinutes: &minutes}
	if err := s.ApplyNotificationEdit("tablet", edit, false); err != nil {
		t.Fatal(err)
	}
	loaded := NewSettingsStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	got := loaded.Get()
	p := got.NotificationPolicy("tablet")
	if p.Mode != "whitelist" || !p.Other || p.Preview == nil || *p.Preview || len(p.Packages) != 1 || p.DetailEnabled() || got.NotificationCopyMinutes != 60 || got.NotificationPolicy("phone").Mode != "otp" || !got.NotificationPolicy("phone").DetailEnabled() {
		t.Fatal("transaction lost fields or changed another device")
	}
	if err := loaded.SetNotificationModes([]string{"tablet"}, "otp"); err != nil {
		t.Fatal(err)
	}
	preview = true
	if err := loaded.ApplyNotificationEdit("tablet", NotificationEdit{Preview: &preview}, false); err != nil {
		t.Fatal(err)
	}
	if p = loaded.Get().NotificationPolicy("tablet"); p.Mode != "otp" || len(p.Packages) != 1 || p.DetailEnabled() {
		t.Fatal("preview-only edit overwrote current selection")
	}
}

func TestNotificationEditFailureRollsBackEveryField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	before := s.Get()
	preview, minutes := false, 15
	edit := NotificationEdit{Selection: &NotificationSelection{Packages: []string{"com.example.mail"}}, Preview: &preview, OpenEnabled: &preview, CopyMinutes: &minutes}
	if err := s.ApplyNotificationEdit("tablet", edit, false); err == nil {
		t.Fatal("unwritable target accepted")
	}
	if !reflect.DeepEqual(s.Get(), before) {
		t.Fatal("failed transaction changed selection, preview or expiry")
	}
	for _, invalid := range []NotificationEdit{{Selection: &NotificationSelection{Packages: []string{"../escape"}}, CopyMinutes: &minutes}, {CopyMinutes: new(int)}} {
		if err := s.ApplyNotificationEdit("tablet", invalid, false); err == nil {
			t.Fatal("invalid edit accepted")
		}
		if !reflect.DeepEqual(s.Get(), before) {
			t.Fatal("invalid edit changed settings")
		}
	}
}

func TestAppNotificationEditValidatesDeviceAndAllComplement(t *testing.T) {
	a := New(Config{Version: "test"})
	if err := a.profiles.Save("TABLET", DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	if err := a.profiles.SetApps("device:TABLET", []AppListItem{{Pkg: "com.example.mail"}, {Pkg: "com.example.chat"}}); err != nil {
		t.Fatal(err)
	}
	edit := NotificationEdit{Selection: &NotificationSelection{Packages: []string{"com.example.mail", "com.example.chat"}, Other: true}}
	if err := a.ApplyNotificationEdit("missing", edit); err == nil {
		t.Fatal("unknown device accepted")
	}
	if len(a.Settings().NotificationPolicies) != 0 {
		t.Fatal("invalid device wrote policy")
	}
	if err := a.ApplyNotificationEdit("TABLET", edit); err != nil {
		t.Fatal(err)
	}
	if a.Settings().NotificationPolicy("device:TABLET").Mode != "all" {
		t.Fatal("full complement not normalized to all")
	}
	edit.Selection.Other = false
	if err := a.ApplyNotificationEdit("TABLET", edit); err != nil {
		t.Fatal(err)
	}
	if a.Settings().NotificationPolicy("device:TABLET").Mode != "whitelist" {
		t.Fatal("apps without complement incorrectly include all sources")
	}
}

func TestEmptyNotificationEditAndLegacyWhitelistBecomeOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"notificationPolicies":{"tablet":{"mode":"whitelist","packages":[],"other":false},"dark":{"mode":"whitelist","other":true},"otp":{"mode":"otp"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.Get().NotificationsEnabled("tablet") || s.Get().NotificationPolicy("tablet").Mode != "off" {
		t.Fatal("legacy empty whitelist remained enabled")
	}
	if !s.Get().NotificationsEnabled("dark") || s.Get().NotificationPolicy("otp").Mode != "otp" {
		t.Fatal("empty normalization disabled complementary sources or OTP")
	}
	for _, other := range []bool{true, false} {
		preview := false
		if err := s.ApplyNotificationEdit("tablet", NotificationEdit{Selection: &NotificationSelection{Other: other}, Preview: &preview}, false); err != nil {
			t.Fatal(err)
		}
		loaded := NewSettingsStore(path)
		if err := loaded.Load(); err != nil {
			t.Fatal(err)
		}
		p := loaded.Get().NotificationPolicy("tablet")
		if p.Enabled() != other || p.Other != other || p.Preview == nil || *p.Preview {
			t.Fatal("empty/complement transaction lost mode or preview after restart")
		}
		if !other && p.Mode != "off" {
			t.Fatal("zero selected sources must persist as off")
		}
	}
}
