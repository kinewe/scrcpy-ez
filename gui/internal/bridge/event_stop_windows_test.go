//go:build windows

package bridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/castsupervisor"
	"scrcpy-ez/gui/internal/deviceevents"
)

func TestMain(m *testing.M) {
	if dumpWindowTitleArgsIfRequested() {
		return
	}
	if castsupervisor.RunIfRequested(os.Args[1:]) {
		return
	}
	os.Exit(m.Run())
}

func runnerEventFixture(t *testing.T) (string, *deviceevents.Hub) {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake.exe")
	c := exec.Command("gcc", filepath.Join("..", "castsupervisor", "testdata", "fake_device.c"), "-O2", "-o", fake)
	c.SysProcAttr = &syscallProcAttr{HideWindow: true}
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("fake compile: %v %s", err, b)
	}
	b, err := os.ReadFile(fake)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"adb.exe", "scrcpy.exe"} {
		if err = os.WriteFile(filepath.Join(dir, name), b, 0700); err != nil {
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
	t.Cleanup(cancel)
	hub := deviceevents.NewHub()
	endpoint, token, err := deviceevents.Serve(ctx, hub)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCEZ_EVENT_ENDPOINT", endpoint)
	t.Setenv("SCEZ_EVENT_TOKEN", token)
	t.Setenv("SCEZ_TEST_CAST_LOG", filepath.Join(dir, "casts.log"))
	return bat, hub
}

func TestRunnerEventStopAndParallelIsolation(t *testing.T) {
	bat, hub := runnerEventFixture(t)
	runner := func(params CastParams) (*BatRunner, <-chan struct{}) {
		ready := make(chan struct{}, 4)
		r := NewBatRunner(bat, filepath.Join(filepath.Dir(bat), "adb.exe"), func(line string) {
			if strings.Contains(line, "SCRCPY_EZ_READY") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}, func(int) {})
		if err := r.Start("PHONE_A", params); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Stop() })
		return r, ready
	}
	// A Stop before the helper creates its event must also cancel bootstrap.
	waiting, _ := runner(CastParams{ExpectedSerial: "PHONE_A"})
	if err := waiting.Stop(); err != nil {
		t.Fatal(err)
	}
	if waiting.ExitCode() != 0 {
		t.Fatalf("bootstrap exit %d", waiting.ExitCode())
	}
	if _, err := os.Stat(closedFlagPath(waiting.watchTag)); !os.IsNotExist(err) {
		t.Fatal("close marker leaked")
	}
	hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "192.0.2.1:5555", Kind: "wifi", State: "device"}}})
	params := CastParams{ExpectedSerial: "PHONE_A", Addr: "192.0.2.1:5555"}
	main, mainReady := runner(params)
	params.VdSize = "1280x720"
	params.StartApp = "+test.app"
	virtual, virtualReady := runner(params)
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(15 * time.Second):
			t.Fatal("client not ready")
		}
	}
	wait(mainReady)
	wait(virtualReady)
	if err := main.Stop(); err != nil {
		t.Fatal(err)
	}
	if main.ExitCode() != 0 || virtual.ExitCode() != -1 {
		t.Fatalf("main %d virtual %d", main.ExitCode(), virtual.ExitCode())
	}
	hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "192.0.2.1:5555", Kind: "wifi", State: "device"}, {Serial: "USB_A", Kind: "usb", State: "device"}}})
	wait(virtualReady)
	if main.ExitCode() != 0 {
		t.Fatal("stopped session revived")
	}
	if err := virtual.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerBatchStartAllReady(t *testing.T) {
	bat, hub := runnerEventFixture(t)
	hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{
		{Serial: "USB_A", Kind: "usb", State: "device"},
		{Serial: "USB_B", Kind: "usb", State: "device"},
	}})
	const count = 8
	for round := 0; round < 4; round++ {
		gate := make(chan struct{})
		errors := make(chan error, count)
		var wg sync.WaitGroup
		runners := make([]*BatRunner, count)
		ready := make([]chan struct{}, count)
		exited := make([]chan int, count)
		for i := range runners {
			slot := i
			ready[slot], exited[slot] = make(chan struct{}, 1), make(chan int, 1)
			runners[slot] = NewBatRunner(bat, "", func(line string) {
				if strings.Contains(line, "SCRCPY_EZ_READY") {
					ready[slot] <- struct{}{}
				}
			}, func(code int) { exited[slot] <- code })
			r := runners[slot]
			t.Cleanup(func() { _ = r.Stop() })
			params := CastParams{ExpectedSerial: "PHONE_A", Serial: "USB_A"}
			if slot%2 != 0 {
				params.ExpectedSerial, params.Serial = "PHONE_B", "USB_B"
			}
			if slot > 1 {
				params.VdSize, params.StartApp = "1280x720", "+test.app"
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				errors <- r.Start(params.Serial, params)
			}()
		}
		close(gate)
		wg.Wait()
		for i := 0; i < count; i++ {
			if err := <-errors; err != nil {
				t.Fatal(err)
			}
		}
		for i := range runners {
			select {
			case <-ready[i]:
			case code := <-exited[i]:
				t.Fatalf("batch round %d session %d exited before READY: %d", round, i, code)
			case <-time.After(20 * time.Second):
				t.Fatalf("batch round %d session %d never ready", round, i)
			}
		}
		if err := runners[0].Stop(); err != nil {
			t.Fatal(err)
		}
		for _, r := range runners[1:] {
			if r.ExitCode() != -1 {
				t.Fatalf("batch stop affected another session: %d", r.ExitCode())
			}
		}
		for _, r := range runners[1:] {
			wg.Add(1)
			go func() { defer wg.Done(); _ = r.Stop() }()
		}
		wg.Wait()
	}
}
