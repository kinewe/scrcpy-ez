package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-mdns4：显示层档案化（TLS 标/副行 IP 只读档案 active 地址） ---

func profileDisplaySeed(a *App, addrs []AddrEntry) {
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs:      addrs,
		Profiles:   DefaultProfile(),
	})
}

func profileDisplayCard() []adb.Device {
	return []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
}

func TestGui48ProfileDisplayActiveTls(t *testing.T) {
	a, _ := newWirelessApp()
	profileDisplaySeed(a, []AddrEntry{
		{Addr: "192.168.31.99:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
		{Addr: "192.168.31.99:35263", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
	})
	devs := profileDisplayCard()
	a.decorateTls(devs)
	if !devs[0].Tls || devs[0].Serial != "192.168.31.99:35263" || devs[0].WirelessForm != ModeTls {
		t.Fatalf("active TLS 应标亮且副行 IP= TLS: %+v", devs[0])
	}
}

func TestGui48ProfileDisplayActiveTcpipNoTls(t *testing.T) {
	a, _ := newWirelessApp()
	profileDisplaySeed(a, []AddrEntry{
		{Addr: "192.168.31.99:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
	})
	devs := profileDisplayCard()
	a.decorateTls(devs)
	if devs[0].Tls || devs[0].Serial != "192.168.31.99:5555" || devs[0].WirelessForm != ModeTcpip {
		t.Fatalf("active 5555 应无标且副行 IP=5555: %+v", devs[0])
	}
}

func TestGui48ProfileDisplayAllStaleNoTls(t *testing.T) {
	a, _ := newWirelessApp()
	profileDisplaySeed(a, []AddrEntry{
		{Addr: "192.168.31.99:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
		{Addr: "192.168.31.99:35263", State: AddrStateStale, LastOk: 200, Mode: ModeTls, Stale: true},
	})
	a.profiles.MarkAddrStale("Xiaomi Pad 8 Pro", "192.168.31.99:5555")
	devs := profileDisplayCard()
	a.decorateTls(devs)
	if devs[0].Tls || devs[0].WirelessForm != "" {
		t.Fatalf("全部 stale 应不亮且无 active 形态: %+v", devs[0])
	}
}

func TestGui48ProfileDisplayFormsIndependent(t *testing.T) {
	t.Run("TLS gone 不影响 5555", func(t *testing.T) {
		a, _ := newWirelessApp()
		profileDisplaySeed(a, []AddrEntry{
			{Addr: "192.168.31.99:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
			{Addr: "192.168.31.99:35263", State: AddrStateStale, LastOk: 200, Mode: ModeTls, Stale: true},
		})
		devs := profileDisplayCard()
		a.decorateTls(devs)
		if devs[0].Tls || devs[0].Serial != "192.168.31.99:5555" || devs[0].WirelessForm != ModeTcpip {
			t.Fatalf("TLS stale 后应无标且 5555 显示: %+v", devs[0])
		}
	})
	t.Run("5555 gone 不影响 TLS", func(t *testing.T) {
		a, _ := newWirelessApp()
		profileDisplaySeed(a, []AddrEntry{
			{Addr: "192.168.31.99:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip, Stale: true},
			{Addr: "192.168.31.99:35263", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
		})
		devs := profileDisplayCard()
		a.decorateTls(devs)
		if !devs[0].Tls || devs[0].Serial != "192.168.31.99:35263" || devs[0].WirelessForm != ModeTls {
			t.Fatalf("5555 stale 后 TLS 应仍标亮显示: %+v", devs[0])
		}
	})
}
