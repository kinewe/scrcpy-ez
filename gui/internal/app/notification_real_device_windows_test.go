//go:build windows

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/notifications"
)

// Explicit opt-in only. Opens one authorized app's original notification,
// never logs its title/body/URI/extras/contact, never starts its home or kills it.
func TestAuthorizedDeviceOriginalNotification(t *testing.T) {
	runtime, pkg := os.Getenv("SCEZ_DETAIL_RUNTIME"), os.Getenv("SCEZ_REAL_NOTIFICATION_PACKAGE")
	serial, expected := os.Getenv("SCEZ_DETAIL_SERIAL"), os.Getenv("SCEZ_DETAIL_DEVICE_ID")
	if runtime == "" || pkg == "" || serial == "" || expected == "" {
		t.Skip("explicitly authorized real notification only")
	}
	if !rePkgName.MatchString(pkg) {
		t.Fatal("invalid package")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, filepath.Join(runtime, "adb.exe"), append([]string{"-s", serial}, args...)...)
		adb.HideConsole(cmd)
		return cmd
	}
	out, err := command("shell", "getprop", "ro.serialno").Output()
	if err != nil || strings.TrimSpace(string(out)) != expected {
		t.Fatal("foreign or unavailable device")
	}
	id := "device:" + expected
	native, phys := authorizedDeviceMetrics(t, command)
	profilePath, settingsPath := filepath.Join(t.TempDir(), "profiles.json"), filepath.Join(t.TempDir(), "settings.json")
	if cfg := os.Getenv("SCEZ_REAL_CONFIG_RUNTIME"); cfg != "" {
		for _, item := range []struct{ name, destination string }{{"profiles.json", profilePath}, {"settings.json", settingsPath}} {
			data, err := os.ReadFile(filepath.Join(cfg, item.name))
			if err != nil || os.WriteFile(item.destination, data, 0600) != nil {
				t.Fatal("could not copy authorized configuration")
			}
		}
	}
	a := New(Config{BatPath: filepath.Join(runtime, "投屏支持.bat"), AdbPath: filepath.Join(runtime, "adb.exe"), ProfilesPath: profilePath, SettingsPath: settingsPath, Version: "test"})
	setDevices(a, []adb.Device{{Serial: serial, State: "device", ConnType: "wifi", Identity: id, StableSerial: expected, Name: "设备通知诊断", Res: native}})
	if _, exists := a.profiles.Entry(id); !exists {
		_ = a.profiles.Save(id, DeviceProfile{})
	}
	a.profiles.mu.Lock()
	a.profiles.data.Devices[id].Serials = []string{expected}
	a.profiles.data.Devices[id].Addrs = []AddrEntry{{Addr: serial, State: AddrStateActive}}
	a.profiles.mu.Unlock()
	enabled := true
	_ = a.settings.ApplyNotificationEdit(id, NotificationEdit{OpenEnabled: &enabled}, false)
	a.physCache[id] = phys
	hub := a.adb.EventHub()
	hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: serial, Kind: "wifi", State: "device"}}})
	endpoint, token, err := deviceevents.Serve(ctx, hub)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCEZ_EVENT_ENDPOINT", endpoint)
	t.Setenv("SCEZ_EVENT_TOKEN", token)
	t.Setenv("SCEZ_NO_ADB_RESET", "1")
	var mu sync.Mutex
	var safeLog []string
	a.SetRunnerFactory(func(_ string, line func(string), exit func(int)) (Runner, error) {
		return bridge.NewBatRunner(a.cfg.BatPath, a.cfg.AdbPath, func(s string) {
			line(s)
			// scrcpy technical readiness lines only; no Android application logs.
			if strings.Contains(s, "New display:") || strings.Contains(s, "SCRCPY_EZ_READY") || strings.Contains(s, "Texture (") || strings.Contains(s, "Video decoding:") {
				mu.Lock()
				safeLog = append(safeLog, s)
				mu.Unlock()
			}
		}, exit), nil
	})
	defer func() {
		for _, window := range a.Snapshot().AppWins {
			_ = a.StopAppWin(window.Serial, window.Pkg)
		}
		deadline := time.Now().Add(15 * time.Second)
		for len(a.Snapshot().AppWins) > 0 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if len(a.Snapshot().AppWins) > 0 {
			t.Error("owned cast cleanup timeout")
		}
	}()
	source := &notifications.ADBSource{ADB: a.cfg.AdbPath, Server: filepath.Join(runtime, "scrcpy-server")}
	ready := make(chan struct{}, 1)
	records := make(chan notifications.Frame, 8)
	sourceCtx, stopSource := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- source.Run(sourceCtx, notifications.Target{Identity: id, Serial: serial, DeviceSerial: expected}, func(frame notifications.Frame) error {
			if frame.Type == "ready" {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
			if (frame.Type == "snapshot" || frame.Type == "post") && frame.Record.Package == pkg && notifications.SupportsDetailAction(frame.Record) {
				select {
				case records <- frame:
				default:
				}
			}
			return nil
		})
	}()
	defer func() {
		stopSource()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("listener cleanup timeout")
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("listener: %v", err)
	case <-ctx.Done():
		t.Fatal("listener timeout")
	}
	var frame notifications.Frame
	select {
	case frame = <-records:
	case <-time.After(3 * time.Second):
		t.Skip("no current eligible ordinary notification for authorized package")
	}
	record := frame.Record
	req := notifications.OpenRequest{Identity: id, Session: frame.Session, Key: record.Key, Token: record.OpenToken, Package: record.OpenPackage, OwnerPackage: record.Package, DisplayPackage: record.DisplayPackage, App: "应用通知诊断"}
	t.Logf("authorized owner=%s destination=%s; OTP exclusion applied", pkg, req.Package)
	err = a.OpenNotification(ctx, req, source)
	mu.Lock()
	for _, line := range safeLog {
		t.Log(line)
	}
	mu.Unlock()
	if err != nil {
		t.Fatalf("original notification action failed: %v", err)
	}
	a.mu.RLock()
	window := a.appWins[appWinKey(id, req.Package)]
	a.mu.RUnlock()
	if window == nil || !window.displayHasVideo {
		t.Fatal("no decoded frame")
	}
	t.Logf("original notification opened with real video: display=%d clientPID=%d", window.displayID, window.clientPID)
}

func authorizedDeviceMetrics(t *testing.T, command func(...string) *exec.Cmd) (string, devPhys) {
	t.Helper()
	size, err := command("shell", "wm", "size").Output()
	if err != nil {
		t.Fatal("could not read authorized device size")
	}
	metrics := regexp.MustCompile(`(?:Physical|Override) size:\s*(\d+)x(\d+)`).FindAllStringSubmatch(string(size), -1)
	if len(metrics) == 0 {
		t.Fatal("missing device size")
	}
	m := metrics[len(metrics)-1]
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	longSide := w
	if h > longSide {
		longSide = h
	}
	density, err := command("shell", "wm", "density").Output()
	if err != nil {
		t.Fatal("could not read authorized device density")
	}
	densities := regexp.MustCompile(`(?:Physical|Override) density:\s*(\d+)`).FindAllStringSubmatch(string(density), -1)
	if len(densities) == 0 {
		t.Fatal("missing device density")
	}
	dpi, _ := strconv.Atoi(densities[len(densities)-1][1])
	if w <= 0 || h <= 0 || dpi <= 0 {
		t.Fatal("invalid authorized device metrics")
	}
	return m[1] + "x" + m[2], devPhys{longSide: longSide, dpi: dpi, at: time.Now()}
}
