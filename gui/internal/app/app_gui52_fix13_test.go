package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// gui52-fix13：commitDisplay 最前先清孤儿——applyProfileNames 基于干净档案，
// 不会给 transport 卡套上孤儿键的 IP 名（36475 一闪根因）。
func TestGui52Fix13CommitClearsOrphanBeforeNames(t *testing.T) {
	a, _ := newWirelessApp()
	// 主档案：Pad（有显示名）+ 孤儿 36475 键（同 IP）
	a.profiles.mu.Lock()
	a.profiles.data.Devices["192.168.31.162:36475"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.168.31.162:36475", State: AddrStateActive, Mode: ModeTls}},
		Profiles: DefaultProfile(),
	}
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-On9v2R",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
			{Addr: "192.168.31.162:42747", State: AddrStateActive, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	}
	a.profiles.data.DeviceOrder = []string{"192.168.31.162:36475", "Xiaomi Pad 8 Pro"}
	a.profiles.mu.Unlock()

	// 设备流：36475 transport（device）+ 主 5555 transport（模拟）
	// commitDisplay 应先在 applyProfileNames 前清孤儿 → 卡名应为 Xiaomi Pad 8 Pro。
	a.commitDisplay([]adb.Device{
		{Serial: "192.168.31.162:36475", State: "device", ConnType: "wifi"},
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi"},
	})

	out := a.Snapshot().Devices
	if len(out) != 1 {
		t.Fatalf("应单卡（孤儿归并）: %+v", out)
	}
	if out[0].Name != "Xiaomi Pad 8 Pro" {
		t.Fatalf("卡名应为 Pad 而非孤儿 IP 名: %+v", out[0])
	}
	if _, ok := a.profiles.Entries()["192.168.31.162:36475"]; ok {
		t.Fatal("孤儿键应在显示提交前清除")
	}
}
