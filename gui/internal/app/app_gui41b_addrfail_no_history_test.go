package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui41b 安全补丁（gui52 语义更新）：addrFailLocked 写 state=stale ---

// TestGui41bAddrFailKeepsActive：唯一条目连续失败 → 落盘状态必须是 stale；
// fail/lastFail 只是内存态统计（不落盘、不删条目）。
func TestGui41bAddrFailKeepsActive(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:39419", State: AddrStateActive, Fail: 0, LastOk: 100, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})

	for i := 0; i < 4; i++ {
		s.AddrFail("REDMI K80", "192.168.31.197:39419")
	}
	e, _ := s.Entry("REDMI K80")
	a := gui24FindAddr(e, "192.168.31.197:39419")
	if a == nil || a.State != AddrStateStale {
		t.Fatalf("gui52 失败必须写 state=stale（不转 history 也不保持 active）: %+v", a)
	}
	if a.Fail != 4 || a.LastFail == 0 {
		t.Fatalf("内存态失败统计/节流应保留: %+v", a)
	}
}

// TestGui41bAddrFailThenNormalizeKeeps：K80 场景组合回归——多次失败后
// normalizeLocked 不得把唯一（stale）无线记忆删掉。
func TestGui41bAddrFailThenNormalizeKeeps(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 200, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})

	for i := 0; i < 3; i++ {
		s.AddrFail("REDMI K80", "192.168.31.197:5555")
	}
	s.normalizeLocked()

	e, _ := s.Entry("REDMI K80")
	a := gui24FindAddr(e, "192.168.31.197:5555")
	if a == nil || a.State != AddrStateStale {
		t.Fatalf("normalizeLocked 后唯一 stale 记忆不得被删除: %+v", e.Addrs)
	}
}

// TestGui41bAddrFailUnknownAppends：未知地址失败仍 append（state=stale + 内存统计）。
func TestGui41bAddrFailUnknownAppends(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "X", &DeviceEntry{Marketname: "X", Profiles: DefaultProfile()})
	s.AddrFail("X", "10.0.0.1:5555")
	e, _ := s.Entry("X")
	a := gui24FindAddr(e, "10.0.0.1:5555")
	if a == nil || a.State != AddrStateStale || a.Fail != 1 || a.LastFail == 0 {
		t.Fatalf("未知地址失败应 append state=stale 并记内存统计: %+v", e.Addrs)
	}
}

// TestGui41bHasOnlineWirelessTcpipOnly：5555-only 无线不算 mDNS 广播者；
// TLS 端口无线才算；USB 不算。
func TestGui41bHasOnlineWirelessTcpipOnly(t *testing.T) {
	a, _ := newTestApp()

	set := func(devs []adb.Device) bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.devices = devs
		return a.hasOnlineWirelessLocked()
	}

	if set([]adb.Device{{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi"}}) {
		t.Fatal("5555 tcpip 设备不应算 mDNS 广播者")
	}
	if !set([]adb.Device{{Serial: "192.168.31.197:39419", State: "device", ConnType: "wifi"}}) {
		t.Fatal("TLS 端口无线设备应算 mDNS 广播者")
	}
	if set([]adb.Device{{Serial: "601c9f08", State: "device", ConnType: "usb"}}) {
		t.Fatal("USB 设备不应算无线广播者")
	}
	if set([]adb.Device{{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi"}}) {
		t.Fatal("offline 设备不应算广播者")
	}
}
