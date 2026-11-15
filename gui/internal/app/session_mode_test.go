package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestSessionModeAvailableBeforeFirstSpec(t *testing.T) {
	for _, mode := range []string{"usb", "wifi"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := multiTestApp()
			t.Cleanup(a.Close)
			serial := "MODE_PHONE"
			if mode == "wifi" {
				serial = "192.0.2.159:5555"
			}
			setDevices(a, []adb.Device{{Serial: serial, State: "device", ConnType: mode}})
			if err := a.StartCast(serial); err != nil {
				t.Fatal(err)
			}
			if got := a.Snapshot().Sessions[0]; got.Mode != mode || got.Cast.Mode != mode {
				t.Fatalf("startup mode missing before first output: %+v", got)
			}
			a.NotifyLine(serial, "[自动切换] 切换至 wifi：192.0.2.159:5555")
			if got := a.Snapshot().Sessions[0].Mode; got != "wifi" {
				t.Fatalf("wireless route must update before spec: %q", got)
			}
			a.NotifyLine(serial, "[自动切换] 启动 usb：MODE_PHONE")
			a.NotifyLine(serial, "[学习] 保存无线地址 192.0.2.159:5555")
			if got := a.Snapshot().Sessions[0].Mode; got != "usb" {
				t.Fatalf("learning must not overwrite the actual USB route: %q", got)
			}
		})
	}
}
