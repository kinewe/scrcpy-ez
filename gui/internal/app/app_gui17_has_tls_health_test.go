package app

// --- gui17：卡级 TLS 标识健康化（"死 TLS 地址不再标 TLS"） ---
// HasTlsAddr 语义收紧为"健康可用"：mode=tls 且 fail<2 才计入；fail>=2 死地址
// （含 history）不再标 TLS——与 gui15 候选过滤同口径（统一按 Fail，不按 State）。
// decorateTls 集成回归 K80 实测场景：档案 33895（tls,fail=2,已死）+ 在线 5555
// → 卡不再标 [TLS]，副行「已入档」形态照常透传；在播 mDNS tls 服务（实时能力
// 判定）仍标。
// gui26 语义更新：TLS 标全面实时化——档案健康 tls 地址（fail<2）若没有在播
// 广播/当前连接形态佐证，也不再点亮 TLS 标（档案记录不再驱动"现在有没有
// 无线调试"的状态标；TestDecorateTlsHealthyArchiveNoTagWithoutLiveSignal
// 原「健康归档仍标」断言反转）。

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// TestHasTlsAddrHealth：HasTlsAddr 健康化判定矩阵
// （tls+fail0/1 → true；tls+fail2/3(history) → false；无 tls/mode 空 → false；
// 死 tls + 健康 tls 并存 → true（新端口入档 fail=0 自动恢复标识））。
func TestHasTlsAddrHealth(t *testing.T) {
	seed := func(addrs []AddrEntry) *ProfileStore {
		s := NewProfileStore("")
		gui15Seed(s, "Xiaomi Pad 8 Pro", &DeviceEntry{
			Marketname: "Xiaomi Pad 8 Pro",
			Serials:    []string{"a743e1df"},
			Addrs:      addrs,
			Profiles:   DefaultProfile(),
		})
		return s
	}
	cases := []struct {
		name  string
		addrs []AddrEntry
		want  bool
	}{
		{
			name: "tls+fail0 健康",
			addrs: []AddrEntry{
				{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: 0, LastOk: 100, Mode: ModeTls},
			},
			want: true,
		},
		{
			name: "tls+fail1 瞬态保留",
			addrs: []AddrEntry{
				{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: 1, LastOk: 100, Mode: ModeTls},
			},
			want: true,
		},
		{
			name: "tls+fail2 但 state=active 仍标（fail 不入判据）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: 2, LastOk: 100, Mode: ModeTls},
			},
			want: true,
		},
		{
			name: "tls+state=stale 不标",
			addrs: []AddrEntry{
				{Addr: "192.168.31.99:33895", State: AddrStateStale, Fail: 3, LastOk: 100, Mode: ModeTls},
			},
			want: false,
		},
		{
			name: "无 tls 地址",
			addrs: []AddrEntry{
				{Addr: "192.168.31.99:5555", State: AddrStateActive, Fail: 0, LastOk: 100},
			},
			want: false,
		},
		{
			name: "mode 空 tcpip 5555",
			addrs: []AddrEntry{
				{Addr: "192.168.31.99:5555", State: AddrStateActive, Fail: 0, LastOk: 100, Mode: ""},
			},
			want: false,
		},
		{
			name: "死 tls + 健康 tls 并存（新端口入档恢复）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: 2, LastOk: 100, Mode: ModeTls},
				{Addr: "192.168.31.99:41234", State: AddrStateActive, Fail: 0, LastOk: 200, Mode: ModeTls},
			},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := seed(c.addrs)
			if got := s.HasTlsAddr("Xiaomi Pad 8 Pro"); got != c.want {
				t.Fatalf("HasTlsAddr(identity) = %v, want %v", got, c.want)
			}
			if got := s.HasTlsAddr("a743e1df"); got != c.want {
				t.Fatalf("HasTlsAddr(serial) = %v, want %v", got, c.want)
			}
		})
	}
	// 未知 key 防御
	if s := seed([]AddrEntry{
		{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: 0, LastOk: 100, Mode: ModeTls},
	}); s.HasTlsAddr("unknown") {
		t.Fatal("未知 key 应 false")
	}
}

// gui17SeedK80 种入 K80 实测场景档案：在线 5555（tcpip）+ 33895（tls,fail 可调），
// wireless=tls（配对入档形态——副行「已入档」标注来源，与 d.Tls 徽标解耦）。
func gui17SeedK80(a *App, tlsFail int) {
	a.profiles.mu.Lock()
	defer a.profiles.mu.Unlock()
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Wireless:   ModeTls,
		Addrs: []AddrEntry{
			{Addr: "192.168.31.99:5555", State: AddrStateActive, Fail: 0, LastOk: 200, Mode: ModeTcpip},
			{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: tlsFail, LastOk: 100, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	}
}

// TestDecorateTlsStaleTlsNoTag（gui48-mdns4 档案化）：TLS 地址 stale → 不标 TLS；
// 副行形态由档案 active 地址决定（此处只有 5555 active → tcpip）。
func TestDecorateTlsDeadAddrNoTlsTag(t *testing.T) {
	a, _ := newWirelessApp()
	gui17SeedK80(a, 2)
	a.profiles.mu.Lock()
	for i := range a.profiles.data.Devices["Xiaomi Pad 8 Pro"].Addrs {
		if addrEntryClass(a.profiles.data.Devices["Xiaomi Pad 8 Pro"].Addrs[i]) == ModeTls {
			a.profiles.data.Devices["Xiaomi Pad 8 Pro"].Addrs[i].State = AddrStateStale
			a.profiles.data.Devices["Xiaomi Pad 8 Pro"].Addrs[i].Stale = true
		}
	}
	a.profiles.mu.Unlock()

	devs := []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.decorateTls(devs)
	if devs[0].Tls {
		t.Fatalf("TLS 地址 stale 不应标 TLS: %+v", devs[0])
	}
	if devs[0].WirelessForm != ModeTcpip {
		t.Fatalf("副行形态应由 active 地址决定（5555 active → tcpip）: %+v", devs[0])
	}
}

// TestDecorateTlsHealthyArchiveNoTagWithoutLiveSignal（gui48-mdns4 断言反转）：
// 档案 active TLS 地址即事实源 → 标亮（不再依赖广播快照）。
func TestDecorateTlsHealthyArchiveNoTagWithoutLiveSignal(t *testing.T) {
	a, _ := newWirelessApp()
	gui17SeedK80(a, 0)

	devs := []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.decorateTls(devs)
	if !devs[0].Tls {
		t.Fatalf("档案 active TLS 地址应点亮 TLS 标: %+v", devs[0])
	}
}

// TestDecorateTlsLiveMdnsStillTags（gui48-mdns4）：档案 active TLS（即使 fail=2）
// 即事实源 → 标亮；mDNS 快照不再参与显示判定。
func TestDecorateTlsLiveMdnsStillTags(t *testing.T) {
	a, _ := newWirelessApp()
	gui17SeedK80(a, 2)

	devs := []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.decorateTls(devs)
	if !devs[0].Tls {
		t.Fatalf("档案 active TLS 地址应标 TLS: %+v", devs[0])
	}
}
