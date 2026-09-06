package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui46：离线无线残留折叠（不让离线卡坏在线卡） ---

func gui46K80WithAddrs(t *testing.T, addrs []AddrEntry) *App {
	t.Helper()
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Serials:    []string{"601c9f08"},
			Addrs:      addrs,
			Profiles:   DefaultProfile(),
		},
	})
	return a
}

// TestGui46ResidualMergedIntoDeviceCard：离线残留 + 同身份 device 卡 → 并入 Wireless，单卡。
func TestGui46ResidualMergedIntoDeviceCard(t *testing.T) {
	a := gui46K80WithAddrs(t, []AddrEntry{
		{Addr: "192.168.31.197:36967", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
		{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
	})
	devs := []adb.Device{
		{Serial: "192.168.31.197:36967", State: "offline", ConnType: "wifi"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"},
	}
	out := a.appendProfileOfflineCards(devs)
	if len(out) != 1 || out[0].Serial != "192.168.31.197:5555" || out[0].Wireless != "192.168.31.197:36967" {
		t.Fatalf("残留应并入 device 卡 Wireless，单卡: %+v", out)
	}
	if gui34Find(out, "192.168.31.197:36967") != nil {
		t.Fatalf("残留不应独立成卡: %+v", out)
	}
}

// TestGui46ResidualNotBlockSynthOnline：只有离线残留 + 档案有 active → 合成在线卡。
func TestGui46ResidualNotBlockSynthOnline(t *testing.T) {
	a := gui46K80WithAddrs(t, []AddrEntry{
		{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
	})
	devs := []adb.Device{
		{Serial: "192.168.31.197:36967", State: "offline", ConnType: "wifi"},
	}
	out := a.appendProfileOfflineCards(devs)
	if len(out) != 1 || out[0].State != "device" || out[0].ConnType != "wifi" || out[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("档案 active 应合成在线卡（不被残留堵住）: %+v", out)
	}
}

// TestGui46ResidualAllStaleFallsBackOffline：只有离线残留 + 档案全 stale → 补离线卡。
func TestGui46ResidualAllStaleFallsBackOffline(t *testing.T) {
	a := gui46K80WithAddrs(t, []AddrEntry{
		{Addr: "192.168.31.197:36967", State: AddrStateStale, LastOk: 200, Mode: ModeTls, Stale: true},
		{Addr: "192.168.31.197:5555", State: AddrStateStale, LastOk: 100, Mode: ModeTcpip, Stale: true},
	})
	devs := []adb.Device{
		{Serial: "192.168.31.197:36967", State: "offline", ConnType: "wifi"},
	}
	out := a.appendProfileOfflineCards(devs)
	if len(out) != 1 || out[0].State != "offline" {
		t.Fatalf("全 stale 应补离线卡: %+v", out)
	}
}

// TestGui46UsbOfflineUnauthorizedUntouched：USB offline/unauthorized 原样保留。
func TestGui46UsbOfflineUnauthorizedUntouched(t *testing.T) {
	a, _ := newWirelessApp()
	devs := []adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb"},
		{Serial: "601c9f08", State: "unauthorized", ConnType: "usb"},
	}
	out := a.appendProfileOfflineCards(devs)
	if len(out) != 2 {
		t.Fatalf("USB offline/unauthorized 应原样保留: %+v", out)
	}
}
