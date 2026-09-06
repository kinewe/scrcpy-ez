package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-mdns10：无线规格（及原生规格）从档案现算装饰 ---

func mdns10Seed(a *App, native string, usb, wifi ModeProfile) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Model:      "24117RK2CC",
			Serials:    []string{"601c9f08"},
			Res:        native,
			Addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 1, Mode: ModeTcpip},
				{Addr: "192.168.31.197:45005", State: AddrStateActive, LastOk: 2, Mode: ModeTls},
			},
			Profiles: DeviceProfile{Usb: usb, Wifi: wifi},
		},
	})
}

func mdns10Commit(t *testing.T, a *App, devs []adb.Device) []adb.Device {
	t.Helper()
	a.commitDisplay(devs)
	return a.Snapshot().Devices
}

// 用例 1：档案补齐卡（设备流无）→ 无线规格 = 档案 wifi 默认档
// （1920 → 按原生宽高比 2560x1708 换算为 1920x1281，fps 60）。
func TestGui48Mdns10FallbackCardWifiDefaultSpec(t *testing.T) {
	a, _ := newWirelessApp()
	p := DefaultProfile()
	mdns10Seed(a, "2560x1708", p.Usb, p.Wifi)

	out := mdns10Commit(t, a, nil)
	if len(out) != 1 {
		t.Fatalf("应补齐一张无线卡: %+v", out)
	}
	d := out[0]
	if d.State != "device" || d.ConnType != "wifi" {
		t.Fatalf("补齐卡应为无线状态: %+v", d)
	}
	if d.WirelessRes != "1920x1281" || d.FPS != 60 {
		t.Fatalf("无线规格应为档案 wifi 默认档 1920x1281@60: %+v", d)
	}
	if d.Serial != "192.168.31.197:45005" || d.WirelessIP != "192.168.31.197:45005" {
		t.Fatalf("补齐卡 IP 应为档案 active TLS 优先: %+v", d)
	}
}

// 用例 2：档案 wifi 自定义档（1080/120）→ 无线规格按同宽高比换算。
func TestGui48Mdns10FallbackCardWifiCustomSpec(t *testing.T) {
	a, _ := newWirelessApp()
	p := DefaultProfile()
	wifi := p.Wifi
	wifi.Res = 1080
	wifi.FPS = 120
	wifi.Custom = true
	mdns10Seed(a, "2560x1708", p.Usb, wifi)

	out := mdns10Commit(t, a, nil)
	if len(out) != 1 {
		t.Fatalf("应补齐一张无线卡: %+v", out)
	}
	d := out[0]
	if d.WirelessRes != "1080x720" || d.FPS != 120 {
		t.Fatalf("无线规格应为自定义档 1080x720@120: %+v", d)
	}
}

// 用例 3：有线 USB 卡 → USB 副行 = 档案 usb 默认档（2560→2560x1440、120Hz）
// 且 USB 序列号保留。
func TestGui48Mdns10UsbCardDefaultSpec(t *testing.T) {
	a, _ := newWirelessApp()
	p := DefaultProfile()
	mdns10Seed(a, "2560x1440", p.Usb, p.Wifi)

	out := mdns10Commit(t, a, []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80",
			Res: "2560x1440", FPS: 120},
	})
	if len(out) != 1 {
		t.Fatalf("应一张有线卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "device" {
		t.Fatalf("有线卡形态错误: %+v", d)
	}
	if d.Res != "2560x1440" || d.FPS != 120 {
		t.Fatalf("USB 副行规格应为档案 usb 默认档 2560x1440@120: %+v", d)
	}
}

// 用例 4：档案 usb 自定义档 → 有线副行 = 自定义值（与无线对称）。
func TestGui48Mdns10UsbCardCustomSpec(t *testing.T) {
	a, _ := newWirelessApp()
	p := DefaultProfile()
	usb := p.Usb
	usb.Res = 1080
	usb.FPS = 90
	usb.Custom = true
	mdns10Seed(a, "2560x1440", usb, p.Wifi)

	out := mdns10Commit(t, a, []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80",
			Res: "2560x1440", FPS: 120},
	})
	if len(out) != 1 {
		t.Fatalf("应一张有线卡: %+v", out)
	}
	d := out[0]
	if d.Res != "1080x607" || d.FPS != 90 {
		t.Fatalf("USB 副行规格应为自定义档 1080x607@90: %+v", d)
	}
	if d.Serial != "601c9f08" {
		t.Fatalf("USB 序列号应保留: %+v", d)
	}
}

// 用例 5：设备流在线富化卡 → 档案档权威，覆盖 adb 富化值（不回归且不打架）。
func TestGui48Mdns10OnlineCardProfileSpecWins(t *testing.T) {
	a, _ := newWirelessApp()
	p := DefaultProfile()
	mdns10Seed(a, "2560x1708", p.Usb, p.Wifi)

	out := mdns10Commit(t, a, []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80",
			Res: "2560x1708", FPS: 120, WirelessRes: "1920x1080"},
	})
	if len(out) != 1 {
		t.Fatalf("应一张在线无线卡: %+v", out)
	}
	d := out[0]
	// adb 富化值 1920x1080@120 应被档案默认档 1920x1281@60 覆盖（档案权威）。
	if d.WirelessRes != "1920x1281" || d.FPS != 60 {
		t.Fatalf("在线卡规格应与档案 wifi 档一致: %+v", d)
	}
}

// 用例 6：离线卡 → 无 IP、无规格、无投屏按钮语义（既有），不报错。
func TestGui48Mdns10OfflineCardNoSpecNoError(t *testing.T) {
	a, _ := newWirelessApp()
	p := DefaultProfile()
	mdns10Seed(a, "2560x1708", p.Usb, p.Wifi)
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:5555")
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:45005")

	out := mdns10Commit(t, a, nil)
	if len(out) != 1 {
		t.Fatalf("应一张离线卡: %+v", out)
	}
	d := out[0]
	if d.State != "offline" {
		t.Fatalf("应为离线卡: %+v", d)
	}
	if d.WirelessIP != "" || d.WirelessRes != "" || d.Res != "" || d.FPS != 0 {
		t.Fatalf("离线卡不应携带 IP/规格装饰: %+v", d)
	}
}

// mdns10ProfileRes 纯函数：宽高比换算 + 16:9 兜底。
func TestGui48Mdns10ProfileRes(t *testing.T) {
	if got := mdns10ProfileRes("2560x1708", 1920); got != "1920x1281" {
		t.Fatalf("2560x1708 @1920 = %q", got)
	}
	if got := mdns10ProfileRes("2560x1440", 1080); got != "1080x607" {
		t.Fatalf("2560x1440 @1080 = %q", got)
	}
	if got := mdns10ProfileRes("", 1920); got != "1920x1080" {
		t.Fatalf("无宽高比兜底 16:9 = %q", got)
	}
}
