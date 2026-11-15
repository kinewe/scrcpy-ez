//go:build windows

package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
)

func dumpWindowTitleArgsIfRequested() bool {
	path := os.Getenv("SCEZ_TEST_TITLE_DUMP")
	if path == "" || !strings.EqualFold(filepath.Base(os.Args[0]), "scrcpy.exe") {
		return false
	}
	b, err := json.Marshal(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		os.Exit(3)
	}
	return true
}

func TestRunnerWindowTitleRoundTrip(t *testing.T) {
	bat, hub := runnerEventFixture(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(bat), "scrcpy.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"usb", "wifi"} {
		for _, appWindow := range []bool{false, true} {
			name := kind + map[bool]string{false: "/main", true: "/app"}[appWindow]
			t.Run(name, func(t *testing.T) {
				serial := "USB_A"
				if kind == "wifi" {
					serial = "192.0.2.1:5555"
				}
				hub.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: serial, Kind: kind, State: "device"}}})
				output := filepath.Join(t.TempDir(), "args.json")
				t.Setenv("SCEZ_TEST_TITLE_DUMP", output)
				title := `我的 手机😀 "A" & (平板) | 100% !PATH! ^ <> - 主投屏\`
				if appWindow {
					title = `REDMI K80 - 浏览器 "Beta" \`
				}
				params := CastParams{ExpectedSerial: "PHONE_A", WinTitle: title}
				if kind == "wifi" {
					params.Addr = serial
				} else {
					params.Serial = serial
				}
				if appWindow {
					params.VdSize, params.StartApp = "1280x720", "test.app"
				}
				done := make(chan int, 1)
				r := NewBatRunner(bat, "", func(string) {}, func(code int) { done <- code })
				t.Cleanup(func() { _ = r.Stop() })
				if err := r.Start(serial, params); err != nil {
					t.Fatal(err)
				}
				select {
				case code := <-done:
					if code != 0 {
						t.Fatalf("helper exit=%d", code)
					}
				case <-time.After(15 * time.Second):
					t.Fatal("title helper did not exit")
				}
				b, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				var args []string
				if err := json.Unmarshal(b, &args); err != nil {
					t.Fatal(err)
				}
				var titles []string
				for _, arg := range args {
					if strings.HasPrefix(arg, "--window-title=") {
						titles = append(titles, strings.TrimPrefix(arg, "--window-title="))
					}
				}
				if len(titles) != 1 || titles[0] != title {
					t.Fatalf("title must reach the actual child exactly once and unchanged: titles=%q argv=%q", titles, args)
				}
			})
		}
	}
}
