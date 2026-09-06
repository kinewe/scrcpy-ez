package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49-fix9：mdns10DecorateSpecs 不给 connecting 卡填规格 ---

func TestGui49Fix9ConnectingCardGetsNoSpecs(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok {
		t.Fatal("档案缺失")
	}
	e.Res = "2560x1708"

	// 插线遮罩卡形态：State=device + ConnType=usb + Connecting=true。
	d := adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Connecting: true}
	mdns10DecorateSpecs(&d, e)
	if d.Res != "" || d.FPS != 0 || d.WirelessRes != "" {
		t.Fatalf("connecting 卡不得被填规格: %+v", d)
	}

	// 真实有线卡（Connecting=false）规格现算不回归。
	real := adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb"}
	mdns10DecorateSpecs(&real, e)
	if real.Res != "2560x1708" || real.FPS != 120 {
		t.Fatalf("真实有线卡应照常现算规格: %+v", real)
	}
}
