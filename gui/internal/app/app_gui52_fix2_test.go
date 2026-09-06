package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui52fix2：IP:port transport 身份判定 IP 级回退 ---

// fix2SeedPad 播种 Pad 档案：serials + 同 IP 的 5555/TLS 两条 active 地址。
func fix2SeedPad(a *App) {
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Model:      "25091RP04C",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
			{Addr: "192.168.31.162:46051", State: AddrStateActive, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
}

// TestGui52Fix2DeviceEntryIPFallback：端口不在档案 addrs、IP 相同 → 命中已知档案。
func TestGui52Fix2DeviceEntryIPFallback(t *testing.T) {
	a, _ := multiTestApp()
	fix2SeedPad(a)

	d := adb.Device{Serial: "192.168.31.162:33793", State: "device", ConnType: "wifi"}
	e, ok := a.deviceEntry(&d)
	if !ok {
		t.Fatalf("同 IP 瞬时端口 transport 应经 IP 回退命中档案: %+v", d)
	}
	if e.Marketname != "Xiaomi Pad 8 Pro" {
		t.Fatalf("命中的应是 Pad 主档案: %+v", e)
	}
	if got := a.identityOf(&d); got != "Xiaomi Pad 8 Pro" {
		t.Fatalf("identityOf 应回退到档案 identity，got %q", got)
	}
	if got := a.displayNameOf(&d); got != "Xiaomi Pad 8 Pro" {
		t.Fatalf("displayNameOf 应回退到档案名，got %q", got)
	}
}

// TestGui52Fix2ExactMatchBeforeIPFallback：精确匹配严格优先——同 IP 存在两个档案时，
// 端口精确命中（即使该条目 stale）必须压过另一档案的 active IP 回退。
func TestGui52Fix2ExactMatchBeforeIPFallback(t *testing.T) {
	a, _ := multiTestApp()
	gui15Seed(a.profiles, "ExactDevice", &DeviceEntry{
		Marketname: "ExactDevice",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:33793", State: AddrStateStale, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	gui15Seed(a.profiles, "SameIPDevice", &DeviceEntry{
		Marketname: "SameIPDevice",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})

	d := adb.Device{Serial: "192.168.31.162:33793", State: "device", ConnType: "wifi"}
	e, ok := a.deviceEntry(&d)
	if !ok || e.Marketname != "ExactDevice" {
		t.Fatalf("精确 addr 匹配应优先于 IP 回退，got %+v ok=%v", e, ok)
	}
	if got := a.mdns9ProfileKey(&d); got != "ExactDevice" {
		t.Fatalf("mdns9ProfileKey 精确匹配应优先，got %q", got)
	}
}

// TestGui52Fix2UnknownPortTransportNoNewDevicePopup：adb 自动连接的瞬时旧端口
// transport 不再判新设备（不弹窗）。
func TestGui52Fix2UnknownPortTransportNoNewDevicePopup(t *testing.T) {
	a, _ := multiTestApp()
	fix2SeedPad(a)

	// 无 marketname/man/model/identity：修复前会退化为 serial 身份 → 判新设备。
	d := adb.Device{Serial: "192.168.31.162:33793", State: "device", ConnType: "wifi"}
	a.applyTrackUpdate([]adb.Device{d})
	if np := a.Snapshot().NewDevice; np != nil {
		t.Fatalf("同 IP 已知档案的瞬时端口 transport 不得弹新设备窗: %+v", np)
	}
	a.applyTrackUpdate([]adb.Device{d})
	if np := a.Snapshot().NewDevice; np != nil {
		t.Fatalf("持续在线也不得弹新设备窗: %+v", np)
	}
}

// TestGui52Fix2UnknownPortTransportMergesIntoMainCard：显示层 unify 同 IP 归并，
// 瞬时端口 transport 与主 TLS 卡合为一张卡（主卡 TLS active 显示）。
func TestGui52Fix2UnknownPortTransportMergesIntoMainCard(t *testing.T) {
	a, _ := multiTestApp()
	fix2SeedPad(a)

	devs := []adb.Device{
		{Serial: "192.168.31.162:33793", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro", Identity: ""},
		{Serial: "192.168.31.162:46051", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	out := a.unifyProfileCards(devs)
	if len(out) != 1 {
		t.Fatalf("同 IP transport 应归并成单卡: %+v", out)
	}
	if out[0].Serial != "192.168.31.162:46051" || !out[0].Tls {
		t.Fatalf("主卡应显示 active TLS 46051: %+v", out[0])
	}
}

// TestGui52Fix2DifferentIPStillNewDevice：不同 IP 且无档案 → 仍判新设备（行为不变）。
func TestGui52Fix2DifferentIPStillNewDevice(t *testing.T) {
	a, _ := multiTestApp()
	fix2SeedPad(a)

	d := adb.Device{Serial: "10.0.0.99:33793", State: "device", ConnType: "wifi"}
	a.applyTrackUpdate([]adb.Device{d})
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "10.0.0.99:33793" {
		t.Fatalf("不同 IP 无档案仍应判新设备: %+v", np)
	}
}
