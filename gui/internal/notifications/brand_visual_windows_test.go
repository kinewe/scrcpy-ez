//go:build windows && cgo

package notifications

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// Explicit visual verification with a new, isolated sender and public artwork.
// Leaves the user's real notification identity and settings untouched.
func TestSenderBrandVisualProbe(t *testing.T) {
	exe := os.Getenv("SCEZ_NOTIFICATION_VISUAL_EXE")
	if exe == "" {
		t.Skip("opt-in synthetic Windows branding screenshot")
	}
	id := fmt.Sprintf("ScrcpyEZ.NotificationTest.Brand.%d.%d", os.Getpid(), time.Now().UnixNano())
	product := os.Getenv("SCEZ_NOTIFICATION_VISUAL_PRODUCT") == "1"
	shortcut, title := "", "ez 图标验证"
	if product {
		// Explicit final verification with the GUI stopped. Keep the production
		// registration; only this synthetic card exists during the probe.
		id, _ = sourceIdentity(Card{})
		shortcut, title = "scrcpy-ez", "rc.14 图标验证"
	}
	sink, err := newWindowsSinkWithExecutable(id, "scrcpy-ez", shortcut, "", exe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sink.Close()
		if product {
			return
		}
		base, _ := os.UserConfigDir()
		_ = os.Remove(filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", id+".lnk"))
		_ = registry.DeleteKey(registry.CURRENT_USER, "Software\\Classes\\AppUserModelId\\"+id)
		_ = registry.DeleteKey(registry.CURRENT_USER, "Software\\Microsoft\\Windows\\CurrentVersion\\Notifications\\Settings\\"+id)
		_ = registry.DeleteKey(registry.CURRENT_USER, "Software\\Classes\\CLSID\\"+activationCLSID(id)+"\\LocalServer32")
		_ = registry.DeleteKey(registry.CURRENT_USER, "Software\\Classes\\CLSID\\"+activationCLSID(id))
	})
	if err := sink.Show(Card{Group: "synthetic-brand", Tag: "synthetic-brand", Title: title, Body: "仅合成测试通知，稍后自动移除", App: "通知测试", Device: "本机验证"}); err != nil {
		t.Fatal(err)
	}
	t.Log("Synthetic branding card is ready")
	time.Sleep(60 * time.Second) // Opt-in visual fixture; not used by ordinary tests.
}
