package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/notifications"
)

func TestNotificationBodyDefaultAndExplicitPrivacySurviveRestart(t *testing.T) {
	for _, sample := range []struct {
		name, data string
		preview    bool
	}{{"missing key", `{}`, true}, {"explicit hidden", `{"notificationPreview":false}`, false}, {"explicit body", `{"notificationPreview":true}`, true}} {
		t.Run(sample.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(sample.data), 0600); err != nil {
				t.Fatal(err)
			}
			store := NewSettingsStore(path)
			if err := store.Load(); err != nil || store.Get().NotificationPreview != sample.preview {
				t.Fatal("default or explicit privacy preference changed")
			}
			if err := store.Set(false, true); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			var saved map[string]any
			if err := json.Unmarshal(data, &saved); err != nil || saved["notificationPreview"] != sample.preview {
				t.Fatal("unrelated save omitted the explicit preview preference")
			}
			reloaded := NewSettingsStore(path)
			if err := reloaded.Load(); err != nil || reloaded.Get().NotificationPreview != sample.preview {
				t.Fatal("preview preference changed after restart")
			}
		})
	}
}

func TestNotificationPoliciesMigratePersistAndRememberWhitelist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"notificationDefault":true,"notificationPreview":false,"notificationDevices":{"tablet":false},"notificationCopyMinutes":60}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.Get().NotificationsEnabled("tablet") || !s.Get().NotificationsEnabled("phone") || s.Get().NotificationPreview {
		t.Fatal("rc.9 settings migration changed previous behavior")
	}
	if err := s.SetNotificationWhitelist("tablet", []string{"com.example.mail", "com.example.mail"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotificationPreview("tablet", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotificationModes([]string{"tablet"}, notifications.ModeOTP); err != nil {
		t.Fatal(err)
	}
	loaded := NewSettingsStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	p := loaded.Get().NotificationPolicy("tablet")
	if p.Mode != notifications.ModeOTP || len(p.Packages) != 1 || p.Preview == nil || !*p.Preview {
		t.Fatal("device rule or saved whitelist did not persist")
	}
	p.Packages[0], *p.Preview = "com.example.other", false
	if loaded.Get().NotificationPolicy("tablet").Packages[0] != "com.example.mail" || !*loaded.Get().NotificationPolicy("tablet").Preview {
		t.Fatal("mutable settings snapshot")
	}
	if err := loaded.SetNotificationWhitelist("tablet", nil); err != nil {
		t.Fatal(err)
	}
	if loaded.Get().NotificationsEnabled("tablet") {
		t.Fatal("empty whitelist starts a listener")
	}
	if err := loaded.SetNotificationDevice("tablet", "inherit"); err != nil {
		t.Fatal(err)
	}
	if !loaded.Get().NotificationsEnabled("tablet") {
		t.Fatal("legacy removal left a new policy behind")
	}
}

func TestNotificationPolicyInvalidBatchAndFailedSaveAreAtomic(t *testing.T) {
	a := New(Config{Version: "test"})
	if err := a.profiles.Save("TABLET", DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetNotificationModes([]string{"TABLET", "missing"}, notifications.ModeOTP); err == nil {
		t.Fatal("missing device accepted")
	}
	if len(a.settings.Get().NotificationPolicies) != 0 {
		t.Fatal("partial batch saved before identity validation")
	}
	if err := a.SetNotificationModes([]string{"TABLET"}, "unknown"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if err := a.SetNotificationWhitelist("TABLET", []string{"../escape"}); err == nil {
		t.Fatal("invalid package accepted")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	s.data.NotificationDefault = false
	if err := s.SetNotificationModes([]string{"tablet", "phone"}, notifications.ModeAll); err == nil {
		t.Fatal("unwritable destination accepted")
	}
	if s.Get().NotificationsEnabled("tablet") || s.Get().NotificationsEnabled("phone") {
		t.Fatal("failed write left a partially active policy")
	}
}
