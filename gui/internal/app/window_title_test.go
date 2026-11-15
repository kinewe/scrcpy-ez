package app

import (
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func TestCastWindowTitlesPreferCustomName(t *testing.T) {
	for _, conn := range []string{"usb", "wifi"} {
		for _, custom := range []bool{false, true} {
			t.Run(conn+map[bool]string{false: "/market", true: "/custom"}[custom], func(t *testing.T) {
				a, rec := multiTestApp()
				t.Cleanup(a.Close)
				serial := "TITLE_PHONE"
				if conn == "wifi" {
					serial = "192.0.2.159:5555"
				}
				d := adb.Device{Serial: serial, StableSerial: "TITLE_PHONE", State: "device", ConnType: conn,
					Marketname: "REDMI K80", Name: "REDMI K80"}
				a.profiles.SyncDevices([]adb.Device{d})
				key := a.profiles.DeviceKey(&d)
				deviceName := d.Marketname
				if custom {
					deviceName = `我的 手机 "A" 100% ! & 😀`
					a.profiles.SetDisplayName(key, deviceName)
				}
				setDevices(a, []adb.Device{d})
				a.physMu.Lock()
				a.physCache[key] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
				a.physMu.Unlock()
				if err := a.StartCast(serial); err != nil {
					t.Fatal(err)
				}
				if title := rec.serial(serial)[0].waitParams(t, 1).WinTitle; title != deviceName+" - 主投屏" {
					t.Fatalf("main title=%q", title)
				}
				if err := a.StartAppWin(serial, "com.android.browser", "浏览器 Beta"); err != nil {
					t.Fatal(err)
				}
				if title := rec.serial(serial)[1].waitParams(t, 1).WinTitle; title != deviceName+" - 浏览器 Beta" {
					t.Fatalf("application title=%q", title)
				}
			})
		}
	}
}

func TestWindowTitleUsesArchivedNameAfterDisconnect(t *testing.T) {
	a, _ := multiTestApp()
	t.Cleanup(a.Close)
	d := adb.Device{Serial: "OFFLINE_TITLE", StableSerial: "OFFLINE_TITLE", State: "device", ConnType: "usb", Marketname: "REDMI K80"}
	a.profiles.SyncDevices([]adb.Device{d})
	key := a.profiles.DeviceKey(&d)
	a.profiles.SetDisplayName(key, "手机")
	if name := a.windowDeviceName(key); name != "手机" {
		t.Fatalf("restart archive lookup lost the custom name: %q", name)
	}
	if got := castWindowTitle(" 手机\x00\n", " 浏览器\r\n"); got != "手机 - 浏览器" {
		t.Fatalf("control characters must not enter the child environment: %q", got)
	}
}
