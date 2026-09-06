package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-mdns9：显示层统一（一设备一卡，双源合一） ---

func mdns9SeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555", "192.168.31.197:45005"}),
	})
}

func mdns9Commit(t *testing.T, a *App, devs []adb.Device) []adb.Device {
	t.Helper()
	a.commitDisplay(devs)
	return a.Snapshot().Devices
}

func mdns9FindAddr(devs []adb.Device, addr string) *adb.Device {
	for i := range devs {
		if devs[i].Serial == addr || devs[i].Wireless == addr || devs[i].WirelessIP == addr {
			return &devs[i]
		}
	}
	return nil
}

// 用例 1：设备流卡 + 档案 active 并存 → 只有一张卡（不再双卡）。
func TestGui48Mdns9DeviceStreamPlusProfileActiveOneCard(t *testing.T) {
	a, _ := newWirelessApp()
	mdns9SeedK80(a)
	out := mdns9Commit(t, a, []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(out) != 1 {
		t.Fatalf("设备流卡 + 档案 active 应只有一张卡: %+v", out)
	}
	if out[0].Identity != "REDMI K80" || out[0].State != "device" || out[0].ConnType != "wifi" {
		t.Fatalf("唯一卡状态应为无线在线: %+v", out)
	}
}

// 用例 2（现象 A）：设备流卡 Serial 被档案同形态单条规则淘汰（ResolveKey 失败），
// 但卡自身 Identity 命中档案 → 卡片仍正确显示 IP（TLS 优先）+ TLS 标。
func TestGui48Mdns9EliminatedSerialStillShowsIpAndTls(t *testing.T) {
	a, _ := newWirelessApp()
	mdns9SeedK80(a)
	// 183:5555 不在档案 addrs/serials 中：ResolveKey(Serial) 失败，只能走 Identity 直解。
	out := mdns9Commit(t, a, []adb.Device{
		{Serial: "192.168.31.183:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(out) != 1 {
		t.Fatalf("应归并成一张卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "192.168.31.197:45005" {
		t.Fatalf("显示 IP 应按档案 active 排序取 TLS 优先（45005）: %+v", d)
	}
	if d.WirelessIP != "192.168.31.197:45005" {
		t.Fatalf("WirelessIP 应与显示 IP 同源: %+v", d)
	}
	if !d.Tls || d.WirelessForm != ModeTls {
		t.Fatalf("TLS 标/形态应与 IP 同一判据点亮: %+v", d)
	}
}

// 用例 3（现象 B）：设备不在设备流、档案有 active → 一张「无线状态」卡
// 且带 IP + TLS 标（骨架+装饰合一，不再有未被装饰的合成卡）。
func TestGui48Mdns9AbsentDeviceProfileActiveGetsDecoratedCard(t *testing.T) {
	a, _ := newWirelessApp()
	mdns9SeedK80(a)
	out := mdns9Commit(t, a, nil)
	if len(out) != 1 {
		t.Fatalf("设备流无 + 档案 active 应补一张唯一无线卡: %+v", out)
	}
	d := out[0]
	if d.State != "device" || d.ConnType != "wifi" || d.Identity != "REDMI K80" {
		t.Fatalf("补齐卡应为无线状态且绑定档案 identity: %+v", d)
	}
	if d.Serial != "192.168.31.197:45005" || d.WirelessIP != "192.168.31.197:45005" {
		t.Fatalf("补齐卡必须带档案 active IP（TLS 优先）: %+v", d)
	}
	if !d.Tls || d.WirelessForm != ModeTls {
		t.Fatalf("补齐卡必须带 TLS 标（与 IP 同源现算）: %+v", d)
	}
}

// 用例 4：全 stale + 设备流无 → 一张「离线」卡（无 IP、无投屏按钮语义）。
func TestGui48Mdns9AllStaleNoDevicesOfflineCard(t *testing.T) {
	a, _ := newWirelessApp()
	mdns9SeedK80(a)
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:5555")
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:45005")
	out := mdns9Commit(t, a, nil)
	if len(out) != 1 {
		t.Fatalf("全 stale + 无设备流应只有一张离线卡: %+v", out)
	}
	d := out[0]
	if d.State != "offline" || d.ConnType != "usb" || d.Serial != "601c9f08" {
		t.Fatalf("离线卡形态错误: %+v", d)
	}
	if d.WirelessIP != "" || d.Tls {
		t.Fatalf("离线卡不得显示 IP / TLS 标: %+v", d)
	}
}

// 用例 5：USB + 无线双 transport → 一张「有线」卡，无线并入副行。
func TestGui48Mdns9UsbWifiDualTransportOneWiredCard(t *testing.T) {
	a, _ := newWirelessApp()
	mdns9SeedK80(a)
	out := mdns9Commit(t, a, []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(out) != 1 {
		t.Fatalf("双 transport 应归并成一张卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "601c9f08" || d.State != "device" || d.ConnType != "usb" {
		t.Fatalf("状态优先级应是有线卡: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线地址应并入副行: %+v", d)
	}
	if mdns9FindAddr(out, "192.168.31.197:5555") != &out[0] {
		t.Fatalf("无线 transport 不应独立成卡: %+v", out)
	}
}

// 用例 6：同一 identity 多来源（设备流/档案补齐/离线残留）逐组合断言
// 绝不产生第二张卡。
func TestGui48Mdns9SameIdentitySourcesNeverDuplicate(t *testing.T) {
	cases := []struct {
		name         string
		devs         []adb.Device
		wantSerial   string
		wantConn     string
		wantWireless string
		wantTls      bool
	}{
		{
			name:       "仅在线无线卡",
			devs:       []adb.Device{{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Identity: "REDMI K80"}},
			wantSerial: "192.168.31.197:45005", wantConn: "wifi", wantTls: true,
		},
		{
			name:       "USB device + 无线 device",
			devs:       []adb.Device{{Serial: "601c9f08", State: "device", ConnType: "usb", Identity: "REDMI K80"}, {Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Identity: "REDMI K80"}},
			wantSerial: "601c9f08", wantConn: "usb", wantWireless: "192.168.31.197:5555", wantTls: true,
		},
		{
			name:       "USB offline + 无线 device",
			devs:       []adb.Device{{Serial: "601c9f08", State: "offline", ConnType: "usb", Identity: "REDMI K80"}, {Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Identity: "REDMI K80"}},
			wantSerial: "601c9f08", wantConn: "usb", wantWireless: "192.168.31.197:5555", wantTls: true,
		},
		{
			name:       "仅无线 offline 残留（档案 active 接管）",
			devs:       []adb.Device{{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Identity: "REDMI K80"}},
			wantSerial: "192.168.31.197:45005", wantConn: "wifi", wantTls: true,
		},
		{
			name:       "设备流完全缺失（档案补齐）",
			devs:       nil,
			wantSerial: "192.168.31.197:45005", wantConn: "wifi", wantTls: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newWirelessApp()
			mdns9SeedK80(a)
			out := mdns9Commit(t, a, c.devs)
			if len(out) != 1 {
				t.Fatalf("同一 identity 多来源必须只有一张卡: %+v", out)
			}
			d := out[0]
			if d.Serial != c.wantSerial || d.ConnType != c.wantConn {
				t.Fatalf("卡片形态不符（want serial=%s conn=%s）: %+v", c.wantSerial, c.wantConn, d)
			}
			if c.wantWireless != "" && d.Wireless != c.wantWireless {
				t.Fatalf("无线副行不符（want %s）: %+v", c.wantWireless, d)
			}
			if d.Tls != c.wantTls {
				t.Fatalf("TLS 标不符（want %v）: %+v", c.wantTls, d)
			}
		})
	}
}
