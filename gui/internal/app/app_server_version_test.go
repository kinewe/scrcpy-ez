package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func TestAppHelperUsesBundledProtocolForCatalogAndIcons(t *testing.T) {
	for _, version := range []string{"4.1", "4.1-ez2.2.2", "4.1-ez2.2.16", "4.2-dev", "5.0.1-ez2.3.1-rc.1"} {
		t.Run(version, func(t *testing.T) {
			got, err := parseAppServerVersion("scrcpy " + version + " <https://github.com/Genymobile/scrcpy>\r\nDependencies:\r\n - SDL: 3.4.12\r\n")
			if err != nil || got != version {
				t.Fatal(got, err)
			}
			helper := appServerHelper{remote: "/data/local/tmp/scrcpy-ez-apps-test", version: got}
			for _, options := range []string{"app_catalog=true", "export_app_icons=com.android.settings export_app_icons_dir=/data/local/tmp/scrcpy/icons-job-test"} {
				command := helper.command(options)
				if !strings.Contains(command, "com.genymobile.scrcpy.Server "+version+" cleanup=false "+options) {
					t.Fatal("helper discarded bundled protocol", command)
				}
			}
		})
	}
	for _, out := range []string{"", "SDL: 4.1", "scrcpy", "scrcpy 4.1;reboot", "scrcpy $(reboot)", "scrcpy " + strings.Repeat("1", 81)} {
		if _, err := parseAppServerVersion(out); err == nil {
			t.Fatalf("invalid protocol accepted: %q", out)
		}
	}
}

func TestAppHelperDoesNotFallBackToAnOldProtocol(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scrcpy-server"), []byte("server"), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{BatPath: filepath.Join(dir, "投屏支持.bat"), Version: "test"})
	if _, err := a.appCatalogServer(); err == nil {
		t.Fatal("missing bundled client silently used a fixed protocol")
	}
}

// Opt-in package/device check: all archives and icons stay in a temporary directory.
// It reads the installed client/server and never starts or stops a cast or ADB server.
func TestConnectedAppCatalogAndIcons(t *testing.T) {
	runtimeDir, serial := os.Getenv("SCEZ_APP_TEST_RUNTIME"), os.Getenv("SCEZ_APP_TEST_SERIAL")
	if runtimeDir == "" || serial == "" {
		t.Skip("bundled runtime/device verification is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	physical, err := exec.CommandContext(ctx, filepath.Join(runtimeDir, "adb.exe"), "-s", serial, "shell", "getprop", "ro.serialno").Output()
	if err != nil || adb.StableSerial(strings.TrimSpace(string(physical))) == "" {
		t.Fatalf("read physical device identity: %v", err)
	}
	raw := adb.ParseDevices(serial + "\tdevice\n")
	if len(raw) != 1 {
		t.Fatal("invalid test transport")
	}
	device := adb.BuildDevice(raw)
	device.StableSerial = strings.TrimSpace(string(physical))
	for _, retained := range []bool{true, false} {
		name := "removed-device"
		if retained {
			name = "retained-profile"
		}
		t.Run(name, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), "profiles.json")
			if source := os.Getenv("SCEZ_APP_TEST_PROFILE"); retained && source != "" {
				b, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(profile, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			a := New(Config{BatPath: filepath.Join(runtimeDir, "投屏支持.bat"), AdbPath: filepath.Join(runtimeDir, "adb.exe"), ProfilesPath: profile, Version: "test"})
			devices := []adb.Device{device}
			a.profiles.SyncDevices(devices)
			a.devices = devices
			identity := a.appListKeyFor(serial)
			items, helper, err := a.listAppCatalogOnce(identity, serial)
			if err != nil || len(items) == 0 {
				t.Fatalf("catalog apps=%d: %v", len(items), err)
			}
			if err = a.profiles.SetApps(identity, items); err != nil {
				t.Fatal(err)
			}
			needed, removed := planAppIcons(a.iconsDirFor(identity), items)
			a.runAppIconDelta(identity, serial, helper, items, needed, removed)
			remaining, _ := planAppIcons(a.iconsDirFor(identity), items)
			if len(remaining) != 0 {
				t.Fatalf("catalog read but %d of %d icons still unavailable", len(remaining), len(items))
			}
			t.Logf("bundled protocol=%s apps=%d icons=%d; warm cache has no pending export", helper.version, len(items), len(needed))
		})
	}
}
