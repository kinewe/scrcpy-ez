package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49-fix4：有线周期形态锁定（added↔removed 之间不切无线形态） ---

func gui49fix4SeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
}

// USB offline 条目仍在列表 → 主卡钉有线形态（State=offline 如实呈现），
// 无线 device 只并入副行，不升级为主卡。
func TestGui49Fix4UsbOfflineLocksWiredForm(t *testing.T) {
	a, _ := newWirelessApp()
	gui49fix4SeedK80(a)
	out := mdns10Commit(t, a, []adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb",
			Name: "REDMI K80", Identity: "REDMI K80"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(out) != 1 {
		t.Fatalf("应单张卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" {
		t.Fatalf("USB offline 在列必须钉有线主卡: %+v", d)
	}
	// gui49-fix5：offline 瞬态标记 Connecting（前端渲染「连接中…」，不显示离线）。
	if d.State != "offline" || !d.Connecting {
		t.Fatalf("USB offline 在列应渲染连接中（非离线）: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线地址应并入副行: %+v", d)
	}
}

// USB unauthorized 同样钉有线主卡（状态如实呈现），不独立出无线卡。
func TestGui49Fix4UsbUnauthorizedLocksWiredForm(t *testing.T) {
	a, _ := newWirelessApp()
	gui49fix4SeedK80(a)
	out := mdns10Commit(t, a, []adb.Device{
		{Serial: "601c9f08", State: "unauthorized", ConnType: "usb",
			Name: "REDMI K80", Identity: "REDMI K80"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(out) != 1 {
		t.Fatalf("应单张有线主卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "unauthorized" {
		t.Fatalf("USB unauthorized 在列必须钉有线主卡: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线地址应并入副行: %+v", d)
	}
}

// USB 条目 removed（不在列表）→ 恢复无线主卡（现有逻辑不回归）。
func TestGui49Fix4UsbRemovedSwitchesToWireless(t *testing.T) {
	a, _ := newWirelessApp()
	gui49fix4SeedK80(a)
	out := mdns10Commit(t, a, []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(out) != 1 {
		t.Fatalf("应单张无线卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "192.168.31.197:5555" || d.ConnType != "wifi" || d.State != "device" {
		t.Fatalf("USB removed 后应切无线形态: %+v", d)
	}
}
