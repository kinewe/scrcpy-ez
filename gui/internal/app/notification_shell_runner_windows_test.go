//go:build windows

package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/notifications"
)

// Exercises the real application handler, batch Runner and supervisor, not a
// direct scrcpy spawn. Only this test's generated shell notifications are read.
func TestDeviceNotificationThroughShellRunner(t *testing.T) {
	runtime, fixture := os.Getenv("SCEZ_DETAIL_RUNTIME"), os.Getenv("SCEZ_DETAIL_FIXTURE")
	serial, expected := os.Getenv("SCEZ_DETAIL_SERIAL"), os.Getenv("SCEZ_DETAIL_DEVICE_ID")
	if runtime == "" || fixture == "" || serial == "" || expected == "" {
		t.Skip("opt-in authorized physical-device integration")
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
		out, err := command(args...).Output()
		if err != nil {
			t.Fatalf("owned fixture ADB failed (%s): %v", args[0], err)
		}
		return strings.TrimSpace(string(out))
	}
	if run("shell", "getprop", "ro.serialno") != expected {
		t.Fatal("foreign device")
	}
	id := "device:" + expected
	a := New(Config{BatPath: filepath.Join(runtime, "投屏支持.bat"), AdbPath: filepath.Join(runtime, "adb.exe"), ProfilesPath: filepath.Join(t.TempDir(), "profiles.json"), SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Version: "test"})
	setDevices(a, []adb.Device{{Serial: serial, State: "device", ConnType: "wifi", Identity: id, StableSerial: expected, Name: "合成详情验证", Res: "2560x1708"}})
	_ = a.profiles.Save(id, DeviceProfile{})
	a.profiles.mu.Lock()
	a.profiles.data.Devices[id].Serials = []string{expected}
	a.profiles.data.Devices[id].Addrs = []AddrEntry{{Addr: serial, State: AddrStateActive}}
	a.profiles.mu.Unlock()
	a.physCache[id] = devPhys{longSide: 2560, dpi: 320, at: time.Now()}
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
	remote := "/data/local/tmp/" + tag
	run("push", fixture, remote+".jar")
	run("push", filepath.Join(runtime, "scrcpy-server"), remote+"-server.jar")
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		cmd := exec.CommandContext(cleanup, filepath.Join(runtime, "adb.exe"), "-s", serial, "shell", "rm", "-f", remote+".jar", remote+"-server.jar")
		adb.HideConsole(cmd)
		_ = cmd.Run()
	}()
	source := &notifications.ADBSource{ADB: a.cfg.AdbPath, Server: filepath.Join(runtime, "scrcpy-server"), TestTag: tag}
	ready := make(chan struct{}, 1)
	posts := make(chan notifications.Frame, 4)
	sourceCtx, stopSource := context.WithCancel(ctx)
	sourceDone := make(chan error, 1)
	go func() {
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
	helper := command("shell", "-T", "CLASSPATH="+remote+".jar:"+remote+"-server.jar", "app_process", "/", "lab.DetailNotificationFixture", tag, "settings-pages")
	output, _ := helper.StdoutPipe()
	helper.Stderr = io.Discard
	input, _ := helper.StdinPipe()
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	helperReady := make(chan struct{}, 1)
	go func() {
		s := bufio.NewScanner(output)
		for s.Scan() {
			if s.Text() == "FIXTURE_READY" {
				helperReady <- struct{}{}
			}
		}
	}()
	defer func() {
		_ = input.Close()
		done := make(chan error, 1)
		go func() { done <- helper.Wait() }()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			_ = helper.Process.Kill()
		}
	}()
	select {
	case <-helperReady:
	case <-ctx.Done():
		t.Fatal("fixture timeout")
	}
	var first *appWinState
	for n := 1; n <= 2; n++ {
		fmt.Fprintln(input, n)
		var frame notifications.Frame
		select {
		case frame = <-posts:
		case <-ctx.Done():
			t.Fatal("notification missing")
		}
		r := frame.Record
		req := notifications.OpenRequest{Identity: id, Session: frame.Session, Key: r.Key, Token: r.OpenToken, Package: r.OpenPackage, OwnerPackage: r.Package, DisplayPackage: r.DisplayPackage, App: "合成通知详情"}
		if err := a.OpenNotification(ctx, req, source); err != nil {
			for _, window := range a.Snapshot().AppWins {
				for _, line := range window.Log {
					t.Log(line)
				}
			}
			t.Fatal(err)
		}
		a.mu.RLock()
		window := a.appWins[appWinKey(id, req.Package)]
		a.mu.RUnlock()
		if window == nil || !window.displayHasVideo {
			t.Fatal("no real decoded frame")
		}
		if n == 1 {
			first = window
		} else if first != window || starts != 1 {
			t.Fatal("repeated detail restarted the cast")
		}
	}
	t.Logf("actual App → Runner → supervisor → original detail; first frame, same window/PID %d, display %d", first.clientPID, first.displayID)
}
