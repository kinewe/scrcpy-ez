package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49-fix5：插线瞬态双层遮蔽（显示层连接中 + 档案层豁免） ---

func gui49fix5SeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
}

// USB 条目在列时，无线 offline 是 adbd 连带重启瞬态：不打 stale、不记失败节流。
func TestGui49Fix5UsbInListWirelessOfflineExempt(t *testing.T) {
	a, _ := newWirelessApp()
	gui49fix5SeedK80(a)

	a.profiles.SyncDevices([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Identity: "REDMI K80"},
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Identity: "REDMI K80"},
	})
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
	if ae == nil || ae.Stale || ae.LastFail != 0 || ae.Fail != 0 {
		t.Fatalf("USB 在列时无线 offline 应豁免（不打标/不节流）: %+v", ae)
	}
}

// USB removed 后无线 offline 打标不回归（真离线观察恢复）。
func TestGui49Fix5UsbRemovedWirelessOfflineStillStale(t *testing.T) {
	a, _ := newWirelessApp()
	gui49fix5SeedK80(a)

	a.profiles.SyncDevices([]adb.Device{
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Identity: "REDMI K80"},
	})
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
	if ae == nil || !ae.Stale || ae.LastFail == 0 {
		t.Fatalf("USB removed 后无线 offline 应打 stale/记失败: %+v", ae)
	}
}
