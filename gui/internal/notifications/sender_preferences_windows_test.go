//go:build windows && cgo

package notifications

import (
	"fmt"
	"golang.org/x/sys/windows/registry"
	"os"
	"testing"
	"time"
)

func TestSenderPreferenceMigrationKeepsDisabledAndNewChoices(t *testing.T) {
	const root = `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\`
	base := fmt.Sprintf("ScrcpyEZ.NotificationTest.Preferences.%d.%d", os.Getpid(), time.Now().UnixNano())
	oldID, newID := base+".Old", base+".New"
	old, _, err := registry.CreateKey(registry.CURRENT_USER, root+oldID, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		old.Close()
		registry.DeleteKey(registry.CURRENT_USER, root+oldID)
		registry.DeleteKey(registry.CURRENT_USER, root+newID)
	})
	for name, value := range map[string]uint32{"Enabled": 0, "AllowSound": 0, "LastNotificationAddedTime": 42} {
		if err := old.SetDWordValue(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateSenderPreferences(oldID, newID); err != nil {
		t.Fatal(err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, root+newID, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if value, _, err := key.GetIntegerValue("Enabled"); err != nil || value != 0 {
		t.Fatal("disabled preference lost")
	}
	if _, _, err := key.GetIntegerValue("LastNotificationAddedTime"); err != registry.ErrNotExist {
		t.Fatal("metadata copied")
	}
	if err := key.SetDWordValue("Enabled", 1); err != nil {
		t.Fatal(err)
	}
	if err := migrateSenderPreferences(oldID, newID); err != nil {
		t.Fatal(err)
	}
	if value, _, err := key.GetIntegerValue("Enabled"); err != nil || value != 1 {
		t.Fatal("new choice overwritten")
	}
}
