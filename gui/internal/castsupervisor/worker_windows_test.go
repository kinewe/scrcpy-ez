//go:build windows

package castsupervisor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/sessioncontrol"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "--event-cast" {
		os.Exit(run(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

var fakeOnce sync.Once
var fakeBytes []byte
var fakeErr error

type fixture struct {
	t        *testing.T
	hub      *deviceevents.Hub
	dir, tag string
	cmd      *exec.Cmd
	done     chan error
	output   bytes.Buffer
	user     *sessioncontrol.Event
	cancel   context.CancelFunc
}

func newFixture(t *testing.T, hub *deviceevents.Hub, extra ...string) *fixture {
	t.Helper()
	dir := t.TempDir()
	fakeOnce.Do(func() {
		path := filepath.Join(dir, "fake.exe")
		c := exec.Command("gcc", "testdata/fake_device.c", "-O2", "-o", path)
		hide(c)
		if b, err := c.CombinedOutput(); err != nil {
			fakeErr = fmt.Errorf("fake compile %v: %s", err, b)
			return
		}
		fakeBytes, fakeErr = os.ReadFile(path)
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	for _, name := range []string{"adb.exe", "scrcpy.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), fakeBytes, 0700); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "packaging", "投屏启动.bat"))
	if err != nil {
		t.Fatal(err)
	}
	bat := filepath.Join(dir, "cast with spaces.bat")
	if err = os.WriteFile(bat, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	endpoint, token, err := deviceevents.Serve(ctx, hub)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := sessioncontrol.NewTag("EVENT_TEST")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, hub: hub, dir: dir, tag: tag, done: make(chan error, 1), cancel: cancel}
	f.user, err = sessioncontrol.New(`Local\SCEZ_TEST_USER_` + f.tag)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range extra {
		if value == "SCEZ_TEST_ROOT_ENABLE=1" {
			if e := os.WriteFile(filepath.Join(dir, "root-repair.json"), []byte(`{"PHONE_A":true}`), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if production := os.Getenv("SCEZ_TEST_PRODUCTION_HELPER"); production != "" {
		exe = production
	}
	c := exec.Command("cmd.exe", "/d", "/c", bat)
	hide(c)
	c.SysProcAttr.CmdLine = `cmd.exe /d /s /c ""` + bat + `""`
	c.Env = append(os.Environ(), "SCEZ_ROOT_ENDPOINT=", "SCEZ_ROOT_TOKEN=", "SCEZ_EVENT_CHILD=", "SCEZ_EVENT_ROUTE=", "SCEZ_EVENT_HELPER="+exe, "SCEZ_WATCH_TAG="+f.tag, "SCEZ_EXPECT_SERIAL=PHONE_A", "SCEZ_SERIAL=", "SCEZ_ADDR=192.0.2.1:5555", "SCEZ_EVENT_ENDPOINT="+endpoint, "SCEZ_EVENT_TOKEN="+token, fmt.Sprintf("SCEZ_EVENT_PARENT_PID=%d", os.Getpid()), "SCEZ_TEST_ADB_LOG="+filepath.Join(dir, "adb.log"), "SCEZ_TEST_CAST_LOG="+filepath.Join(dir, "cast.log"))
	c.Env = append(c.Env, extra...)
	c.Stdout = &f.output
	c.Stderr = &f.output
	if err = c.Start(); err != nil {
		t.Fatal(err)
	}
	f.cmd = c
	go func() { f.done <- c.Wait() }()
	t.Cleanup(func() {
		_ = sessioncontrol.SignalStop(f.tag)
		select {
		case <-f.done:
		case <-time.After(6 * time.Second):
			kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(c.Process.Pid))
			hide(kill)
			_ = kill.Run()
			_ = c.Process.Kill()
		}
		cancel()
		f.user.Close()
	})
	return f
}

func (f *fixture) log(name string) string {
	b, _ := os.ReadFile(filepath.Join(f.dir, name))
	return string(b)
}
func (f *fixture) starts() int { return strings.Count(f.log("cast.log"), "START ") }
func (f *fixture) waitStarts(n int) {
	f.t.Helper()
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		select {
		case err := <-f.done:
			f.done <- err
			f.t.Fatalf("worker exited before cast: %v: %s", err, f.output.String())
		default:
		}
		if f.starts() >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("starts=%d want %d, log=%s", f.starts(), n, f.log("cast.log"))
}
func (f *fixture) assertStable(n int, duration time.Duration) {
	f.t.Helper()
	time.Sleep(duration)
	if got := f.starts(); got != n {
		f.t.Fatalf("starts=%d want %d: %s", got, n, f.log("cast.log"))
	}
}
func transport(serial, kind, state string) deviceevents.Transport {
	return deviceevents.Transport{Serial: serial, Kind: kind, State: state}
}
func publish(h *deviceevents.Hub, ts ...deviceevents.Transport) {
	h.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: ts})
}

func TestWorkerStartupWaitsForUSBIdentityAndLearning(t *testing.T) {
	h := deviceevents.NewHub()
	w := transport("192.0.2.1:5555", "wifi", "device")
	a := transport("USB_A", "usb", "device")
	h.SetLearning(a.Serial, true)
	publish(h, w, a)
	f := newFixture(t, h, "SCEZ_TEST_ID_DELAY=1")
	f.assertStable(0, 900*time.Millisecond)
	h.SetLearning(a.Serial, false)
	f.waitStarts(1)
	f.assertStable(1, 700*time.Millisecond)
	if log := f.log("cast.log"); !strings.Contains(log, "START USB_A ") || strings.Contains(log, "START "+w.Serial+" ") {
		t.Fatal("initial USB startup briefly launched wireless: " + log)
	}
}

func TestWorkerSwitchRecoveryAndUserClose(t *testing.T) {
	h := deviceevents.NewHub()
	w := transport("192.0.2.1:5555", "wifi", "device")
	a := transport("USB_A", "usb", "device")
	b := transport("USB_B", "usb", "device")
	publish(h, w)
	f := newFixture(t, h)
	f.waitStarts(1)
	unauthorized := a
	unauthorized.State = "unauthorized"
	publish(h, w, unauthorized, b)
	f.assertStable(1, 700*time.Millisecond)
	h.SetLearning("USB_A", true)
	publish(h, w, a, b)
	f.assertStable(1, 1700*time.Millisecond)
	h.SetLearning("USB_A", false)
	f.waitStarts(2)
	if !strings.Contains(f.log("cast.log"), "START USB_A ") {
		t.Fatal(f.log("cast.log"))
	}
	publish(h, w, b)
	f.waitStarts(3)
	// All finite startup recovery has now ended. Healthy silence emits no ADB calls.
	time.Sleep(1700 * time.Millisecond)
	before := f.log("adb.log")
	f.assertStable(3, 2200*time.Millisecond)
	if after := f.log("adb.log"); after != before {
		t.Fatalf("ADB command during healthy silence:\n%s", strings.TrimPrefix(after, before))
	}
	if strings.Contains(before, "devices ") || strings.Contains(before, "get-state ") || strings.Contains(before, "kill-server ") {
		t.Fatal(before)
	}
	f.user.Signal()
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatal(err)
		}
		f.done <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("user close did not finish")
	}
	publish(h, w, a)
	f.assertStable(3, 700*time.Millisecond)
	log := f.log("cast.log")
	alive := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "START ") {
			alive++
		}
		if strings.HasPrefix(line, "EXIT ") {
			alive--
		}
		if alive > 1 {
			t.Fatal("overlapping clients: " + log)
		}
	}
}

func TestWorkerRejectsStaleIdentityAndObserverLoss(t *testing.T) {
	h := deviceevents.NewHub()
	w := transport("192.0.2.1:5555", "wifi", "device")
	a := transport("USB_A", "usb", "device")
	publish(h, w)
	f := newFixture(t, h, "SCEZ_TEST_ID_DELAY=1")
	f.waitStarts(1)
	publish(h, w, a)
	time.Sleep(100 * time.Millisecond)
	publish(h, w)
	f.assertStable(1, 700*time.Millisecond)
	h.Publish(deviceevents.Snapshot{Epoch: 1, Error: "observer EOF"})
	f.assertStable(1, 400*time.Millisecond)
	h.Publish(deviceevents.Snapshot{Epoch: 2, Available: true, Transports: []deviceevents.Transport{w}})
	f.assertStable(1, 700*time.Millisecond)
	publish(h, w, a)
	f.waitStarts(2)
}

func TestWorkerFailureBudgetAndWaitingStop(t *testing.T) {
	h := deviceevents.NewHub()
	publish(h, transport("192.0.2.1:5555", "wifi", "device"))
	f := newFixture(t, h, "SCEZ_TEST_CLIENT_RC=1")
	f.waitStarts(3)
	f.assertStable(3, 3*time.Second)
	// Another device appearing must not clear this route's failure budget.
	publish(h, transport("192.0.2.1:5555", "wifi", "device"), transport("USB_B", "usb", "device"))
	f.assertStable(3, time.Second)
	_ = sessioncontrol.SignalStop(f.tag)
	select {
	case <-f.done:
		f.done <- nil
		if !strings.Contains(f.output.String(), "SCRCPY_EZ_RETRY_WAIT") || !strings.Contains(f.output.String(), "code=1 (0x00000001)") {
			t.Fatalf("missing retry/exit evidence: %s", f.output.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiting worker did not cancel")
	}
}

func TestWorkerOtherDeviceSwitchAndRestartDoesNotStopMain(t *testing.T) {
	h := deviceevents.NewHub()
	w := transport("192.0.2.1:5555", "wifi", "device")
	b := transport("USB_B", "usb", "device")
	bw := transport("192.0.2.99:5555", "wifi", "device")
	publish(h, w, b, bw)
	main := newFixture(t, h)
	pad := newFixture(t, h, "SCEZ_EXPECT_SERIAL=PHONE_B", "SCEZ_SERIAL=USB_B", "SCEZ_ADDR=192.0.2.99:5555")
	main.waitStarts(1)
	pad.waitStarts(1)
	publish(h, w, bw)
	pad.waitStarts(2)
	publish(h, w, b, bw)
	pad.waitStarts(3)
	_ = sessioncontrol.SignalStop(pad.tag)
	select {
	case <-pad.done:
		pad.done <- nil
	case <-time.After(3 * time.Second):
		t.Fatal("pad did not stop")
	}
	pad2 := newFixture(t, h, "SCEZ_EXPECT_SERIAL=PHONE_B", "SCEZ_SERIAL=USB_B", "SCEZ_ADDR=192.0.2.99:5555", "SCEZ_USB_CUSTOM=1", "SCEZ_USB_RES=1280", "SCEZ_USB_FPS=30", "SCEZ_USB_BITRATE=6")
	pad2.waitStarts(1)
	main.assertStable(1, time.Second)
	if strings.Contains(main.log("cast.log"), "EXIT") {
		t.Fatalf("another device interrupted main: %s", main.log("cast.log"))
	}
}

func TestWorkerParallelSessionIsolation(t *testing.T) {
	h := deviceevents.NewHub()
	w := transport("192.0.2.1:5555", "wifi", "device")
	a := transport("USB_A", "usb", "device")
	publish(h, w)
	main := newFixture(t, h)
	virtual := newFixture(t, h, "SCEZ_VD_SIZE=1280x720", "SCEZ_START_APP=+test.app")
	main.waitStarts(1)
	virtual.waitStarts(1)
	_ = sessioncontrol.SignalStop(main.tag)
	select {
	case <-main.done:
		main.done <- nil
	case <-time.After(3 * time.Second):
		t.Fatal("main stop")
	}
	publish(h, w, a)
	virtual.waitStarts(2)
	main.assertStable(1, 500*time.Millisecond)
	if strings.Contains(main.log("cast.log"), "USB_A") {
		t.Fatal("stopped session revived")
	}
}
