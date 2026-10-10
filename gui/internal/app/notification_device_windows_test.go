//go:build windows

package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/castsupervisor"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/notifications"
)

func TestMain(m *testing.M) {
	if castsupervisor.RunIfRequested(os.Args[1:]) {
		return
	}
	os.Exit(m.Run())
}

// Real application handler, batch Runner and supervisor. The temporary APK
// has its own UID, immutable actions and a non-exported detail activity.
func TestDeviceNotificationThroughActualAppRunner(t *testing.T) {
	runtime, apk := os.Getenv("SCEZ_DETAIL_RUNTIME"), os.Getenv("SCEZ_DETAIL_APP_APK")
	serial, expected := os.Getenv("SCEZ_DETAIL_SERIAL"), os.Getenv("SCEZ_DETAIL_DEVICE_ID")
	if runtime == "" || apk == "" || serial == "" || expected == "" {
		t.Skip("opt-in authorized temporary app / physical-device integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, filepath.Join(runtime, "adb.exe"), append([]string{"-s", serial}, args...)...)
		adb.HideConsole(cmd)
		return cmd
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := command(args...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned fixture ADB failed (%s): %v; %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if run("shell", "getprop", "ro.serialno") != expected {
		t.Fatal("foreign device")
	}
	const pkg = "org.scrcpyez.notificationlab"
	if run("shell", "pm", "list", "packages", pkg) != "" {
		t.Fatal("do not overwrite an existing lab app")
	}
	result := run("install", "-g", apk)
	if !strings.Contains(result, "Success") {
		t.Fatal("temporary fixture did not install")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		cmd := exec.CommandContext(cleanup, filepath.Join(runtime, "adb.exe"), "-s", serial, "uninstall", pkg)
		adb.HideConsole(cmd)
		if err := cmd.Run(); err != nil {
			t.Error("temporary fixture uninstall failed")
		}
	}()
	run("shell", "pm", "grant", pkg, "android.permission.POST_NOTIFICATIONS")
	// HyperOS may retain a UID-level denial despite the package-level grant.
	// This is the newly installed, test-owned UID; never alter another app.
	run("shell", "cmd", "appops", "set", "--uid", pkg, "POST_NOTIFICATION", "allow")
	run("shell", "am", "start", "-W", "-n", pkg+"/.MainActivity")
	if helper := os.Getenv("SCEZ_DETAIL_FIXTURE"); helper != "" {
		remote := "/data/local/tmp/scez_lab_permission_" + strconv.FormatInt(time.Now().UnixNano(), 16)
		run("push", helper, remote+".jar")
		run("push", filepath.Join(runtime, "scrcpy-server"), remote+"-server.jar")
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			cmd := exec.CommandContext(cleanup, filepath.Join(runtime, "adb.exe"), "-s", serial, "shell", "rm", "-f", remote+".jar", remote+"-server.jar")
			adb.HideConsole(cmd)
			_ = cmd.Run()
		}()
		run("shell", "-T", "CLASSPATH="+remote+".jar:"+remote+"-server.jar", "app_process", "/", "lab.LabNotificationPermissions")
	}
	appPID := run("shell", "pidof", pkg)
	native, phys := authorizedDeviceMetrics(t, command)
	id := "device:" + expected
	a := New(Config{BatPath: filepath.Join(runtime, "投屏支持.bat"), AdbPath: filepath.Join(runtime, "adb.exe"), ProfilesPath: filepath.Join(t.TempDir(), "profiles.json"), SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Version: "test"})
	setDevices(a, []adb.Device{{Serial: serial, State: "device", ConnType: "wifi", Identity: id, StableSerial: expected, Name: "合成详情验证", Res: native}})
	if err := a.profiles.Save(id, DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	a.profiles.mu.Lock()
	a.profiles.data.Devices[id].Addrs = []AddrEntry{{Addr: serial, State: AddrStateActive}}
	a.profiles.mu.Unlock()
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
	starts := 0
	a.SetRunnerFactory(func(_ string, line func(string), exit func(int)) (Runner, error) {
		starts++
		return bridge.NewBatRunner(a.cfg.BatPath, a.cfg.AdbPath, line, exit), nil
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
	tag := "scez_app_detail_" + strconv.FormatInt(time.Now().UnixNano(), 16)
	source := &notifications.ADBSource{ADB: a.cfg.AdbPath, Server: filepath.Join(runtime, "scrcpy-server"), TestTag: tag}
	ready := make(chan struct{}, 1)
	posts := make(chan notifications.Frame, 4)
	sourceCtx, stopSource := context.WithCancel(ctx)
	sourceDone := make(chan error, 1)
	go func() {
		defer close(sourceDone)
		sourceDone <- source.Run(sourceCtx, notifications.Target{Identity: id, Serial: serial, DeviceSerial: expected}, func(frame notifications.Frame) error {
			if frame.Type == "ready" {
				ready <- struct{}{}
			}
			if frame.Type == "post" {
				posts <- frame
			}
			return nil
		})
	}()
	defer func() {
		stopSource()
		select {
		case <-sourceDone:
		case <-time.After(5 * time.Second):
			t.Error("owned listener cleanup timeout")
		}
	}()
	select {
	case <-ready:
	case err := <-sourceDone:
		t.Fatalf("listener: %v", err)
	case <-ctx.Done():
		t.Fatal("listener timeout")
	}
	var first *appWinState
	for n := 1; n <= 2; n++ {
		broadcast := run("shell", "am", "broadcast", "-n", pkg+"/.PostReceiver", "--es", "tag", tag, "--ei", "case", strconv.Itoa(n))
		var frame notifications.Frame
		select {
		case frame = <-posts:
		case <-time.After(8 * time.Second):
			t.Log(broadcast)
			state, _ := command("shell", "run-as", pkg, "cat", "files/post-result.json").CombinedOutput()
			t.Logf("own fixture notification state: %s", state)
			permission, _ := command("shell", "cmd", "appops", "get", pkg, "POST_NOTIFICATION").CombinedOutput()
			t.Logf("own fixture notification permission: %s", permission)
			t.Fatal("owned notification missing")
		case <-ctx.Done():
			t.Fatal("owned notification missing")
		}
		r := frame.Record
		req := notifications.OpenRequest{Identity: id, Session: frame.Session, Key: r.Key, Token: r.OpenToken, Package: r.OpenPackage, OwnerPackage: r.Package, DisplayPackage: r.DisplayPackage, App: "合成通知详情"}
		if req.Package != pkg {
			t.Fatal("action destination package was replaced by its creator")
		}
		if err := a.OpenNotification(ctx, req, source); err != nil {
			for _, window := range a.Snapshot().AppWins {
				for _, line := range window.Log {
					t.Log(line)
				}
			}
			t.Fatal(err)
		}
		a.mu.RLock()
		window := a.appWins[appWinKey(id, pkg)]
		a.mu.RUnlock()
		if window == nil || !window.displayHasVideo {
			t.Fatal("no real decoded frame")
		}
		if n == 1 {
			first = window
		} else if first != window || starts != 1 {
			t.Fatal("second detail restarted cast")
		}
		var detail struct {
			Case    int    `json:"case"`
			Display int    `json:"display"`
			PID     int    `json:"pid"`
			Tag     string `json:"tag"`
		}
		payload := run("shell", "run-as", pkg, "cat", "files/detail-result.json")
		if json.Unmarshal([]byte(payload), &detail) != nil || detail.Case != n || detail.Display != window.displayID || detail.Tag != tag || strconv.Itoa(detail.PID) != appPID {
			t.Fatalf("wrong specific detail or app restarted: case=%d display=%d pid=%d", detail.Case, detail.Display, detail.PID)
		}
	}
	t.Logf("App → real Runner → supervisor → immutable action → exact non-exported details 1/2; first frame, same app process %s and client PID %d, display %d", appPID, first.clientPID, first.displayID)
}
