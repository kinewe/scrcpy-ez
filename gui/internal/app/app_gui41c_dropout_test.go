package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui41c（mdns5 保留部分）：失败入档 + 档案驱动显示 + 掉线首拍 ---
// ForceDiscover 连接探测已退役，相关用例删除。

func gui41cK80Profiles(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Serials:    []string{"601c9f08"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
			},
			Profiles: DefaultProfile(),
		},
	})
}

func TestGui41cAddrFailMarksStale(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 100, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})

	s.AddrFail("REDMI K80", "192.168.31.197:5555")
	e, _ := s.Entry("REDMI K80")
	a := gui24FindAddr(e, "192.168.31.197:5555")
	if a == nil || a.State != AddrStateStale || a.Fail != 1 || a.LastFail == 0 {
		t.Fatalf("失败应写 state=stale 并累计内存统计: %+v", a)
	}

	s.AddrSuccessWithMode("REDMI K80", "192.168.31.197:5555", ModeTcpip)
	e, _ = s.Entry("REDMI K80")
	if a := gui24FindAddr(e, "192.168.31.197:5555"); a == nil || a.State != AddrStateActive {
		t.Fatalf("成功应翻回 state=active（闭环）: %+v", a)
	}
}

func TestGui41cAppendOfflineCardStaleOnly(t *testing.T) {
	t.Run("全 stale 补离线卡", func(t *testing.T) {
		a, _ := newWirelessApp()
		seedProfiles(a, map[string]*DeviceEntry{
			"REDMI K80": {
				Marketname: "REDMI K80",
				Serials:    []string{"601c9f08"},
				Addrs: []AddrEntry{
					{Addr: "192.168.31.197:5555", State: AddrStateStale, LastOk: 100, Mode: ModeTcpip, Stale: true},
				},
				Profiles: DefaultProfile(),
			},
		})
		devs := a.appendProfileOfflineCards(nil)
		if len(devs) != 1 || devs[0].State != "offline" {
			t.Fatalf("全 stale 应补离线卡: %+v", devs)
		}
	})
	t.Run("有 active 补在线合成卡", func(t *testing.T) {
		a, _ := newWirelessApp()
		seedProfiles(a, map[string]*DeviceEntry{
			"REDMI K80": {
				Marketname: "REDMI K80",
				Serials:    []string{"601c9f08"},
				Addrs: []AddrEntry{
					{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
					{Addr: "192.168.31.197:33895", State: AddrStateStale, LastOk: 200, Mode: ModeTls, Stale: true},
				},
				Profiles: DefaultProfile(),
			},
		})
		devs := a.appendProfileOfflineCards(nil)
		if len(devs) != 1 || devs[0].State != "device" || devs[0].ConnType != "wifi" ||
			devs[0].Serial != "192.168.31.197:5555" {
			t.Fatalf("档案有 active 应补在线合成卡（副行 active tcpip）: %+v", devs)
		}
	})
	t.Run("已有卡不重复", func(t *testing.T) {
		a, _ := newWirelessApp()
		gui41cK80Profiles(a)
		existing := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Identity: "REDMI K80"}
		devs := a.appendProfileOfflineCards([]adb.Device{existing})
		if len(devs) != 1 || devs[0] != existing {
			t.Fatalf("已有卡不应重复补: %+v", devs)
		}
	})
}

func TestGui41cJustDropped(t *testing.T) {
	a, _ := newWirelessApp()
	gui41cK80Profiles(a)
	online := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	offline := adb.Device{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}

	if !a.justDropped([]adb.Device{online}, nil) {
		t.Fatal("上轮在线→本轮无：应为掉线首拍")
	}
	if a.justDropped(nil, nil) {
		t.Fatal("上轮无→本轮无：不应判掉线")
	}
	if a.justDropped([]adb.Device{offline}, nil) {
		t.Fatal("上轮仅是离线卡→本轮无：不应判新掉线")
	}
	if a.justDropped([]adb.Device{online}, []adb.Device{online}) {
		t.Fatal("上轮在线→本轮同在线：不应判掉线")
	}
}
