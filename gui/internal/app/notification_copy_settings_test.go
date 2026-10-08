package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotificationCopyLifetimeMigrationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, raw := range []string{`{}`, `{"notificationCopyMinutes":0}`, `{"notificationCopyMinutes":-1}`, `{"notificationCopyMinutes":4321}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		s := NewSettingsStore(path)
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		if s.Get().NotificationCopyMinutes != 1440 {
			t.Fatal("missing/invalid value lost long default")
		}
	}
	s := NewSettingsStore(path)
	if err := s.SetNotificationCopyMinutes(4320); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotificationSettings(true, true); err != nil {
		t.Fatal(err)
	}
	loaded := NewSettingsStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	if loaded.Get().NotificationCopyMinutes != 4320 || !loaded.Get().NotificationPreview {
		t.Fatal("copy setting not persisted independently")
	}
	if loaded.SetNotificationCopyMinutes(4321) == nil || loaded.Get().NotificationCopyMinutes != 4320 {
		t.Fatal("invalid setting mutated current value")
	}
}
