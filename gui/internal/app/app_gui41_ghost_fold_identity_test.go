package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui41 Fix B：foldGhostWireless 归并身份判据修复 ---
//
// 根因：历史 IP 集合（keyIPs）被用于跨设备归并，IP 复用会把 K80 关机幽灵
// （197:5555 offline）并进平板 Wireless 副行 → K80 离线卡消失 +
// OfflineCandidateAddrs 误判 K80 在线 → 探测/补卡永不执行。
// 修复：归并只用 ownKeys（ResolveKey(d.Serial) 直解身份）；keyIPs/tlsIPKeys
// 仅保留给“无身份不建卡”过滤兜底。

// TestGui41GhostFoldNoIpMerge（核心回归）：制造平板含 K80 IP 历史 TLS 条目的
// 污染，K80 幽灵不得并进平板；K80 独立成离线卡，OfflineCandidateAddrs 返回 K80。
func TestGui41GhostFoldNoIpMerge(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Serials:    []string{"601c9f08"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
			},
			Profiles: DefaultProfile(),
		},
		"Xiaomi Pad 8 Pro": {
			Marketname: "Xiaomi Pad 8 Pro",
			Serials:    []string{"a743e1df"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.162:5555", State: AddrStateActive, LastOk: 200, Mode: ModeTcpip},
				// 故意制造 IP 复用污染：历史 TLS 条目曾在 K80 的 IP 上
				{Addr: "192.168.31.197:36155", State: AddrStateHistory, LastOk: 150, Mode: ModeTls},
				{Addr: "192.168.31.197:42379", State: AddrStateHistory, LastOk: 140, Mode: ModeTls},
			},
			Profiles: DefaultProfile(),
		},
	})

	devs := []adb.Device{
		{Serial: "HUAWEI123", State: "device", ConnType: "usb", Name: "HUAWEI"},
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Name: "REDMI K80",
			Identity: "REDMI K80"},
	}
	folded := a.foldGhostWireless(devs)

	// ① 平板不得被 K80 IP 误导写入 Wireless 副行
	tablet := gui34Find(folded, "192.168.31.162:5555")
	if tablet == nil || tablet.Wireless != "" {
		t.Fatalf("平板 Wireless 不应被 K80 幽灵污染: %+v", folded)
	}
	// ② K80 独立保留为离线卡
	k80 := gui34Find(folded, "192.168.31.197:5555")
	if k80 == nil || k80.State != "offline" || k80.ConnType != "wifi" || k80.Identity != "REDMI K80" {
		t.Fatalf("K80 幽灵应独立成离线卡（Identity=REDMI K80）: %+v", folded)
	}
	// ③ gui52：K80 档案 5555 仍是 active（本测试未过 SyncDevices 的离线观察），
	// active=在线证据 → OfflineCandidateAddrs 不得给离线候选；真实链路里
	// offline 幽灵会先经 SyncDevices 把 state 翻 stale，再进入候选。
	offline := a.profiles.OfflineCandidateAddrs(folded)
	if _, ok := offline["REDMI K80"]; ok {
		t.Fatalf("active 地址=在线证据，不应有离线候选: %+v", offline)
	}
	// 翻 stale 后恢复离线候选（闭环验证）。
	if !a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:5555") {
		t.Fatal("MarkAddrStale 应有改动")
	}
	if _, ok := a.profiles.OfflineCandidateAddrs(folded)["REDMI K80"]; !ok {
		t.Fatal("全 stale 后 OfflineCandidateAddrs 应包含 REDMI K80")
	}
	if len(folded) != 3 {
		t.Fatalf("应保留 3 张卡（华为+平板+K80 离线）: %+v", folded)
	}
}

// TestGui41GhostFoldSameDeviceMerge（原功能保留）：同身份幽灵仍并入同设备
// 主卡 Wireless 副行，不单独成卡。
func TestGui41GhostFoldSameDeviceMerge(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)

	devs := []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"},
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"},
	}
	folded := a.foldGhostWireless(devs)
	if len(folded) != 1 {
		t.Fatalf("同身份幽灵应归并为主卡副行: %+v", folded)
	}
	if folded[0].Serial != "601c9f08" || folded[0].Wireless != "192.168.31.197:5555" {
		t.Fatalf("K80 USB 卡应带 Wireless 副行=197:5555: %+v", folded[0])
	}
}

// TestGui41GhostFoldNoIdentityDropped（原功能保留）：无任何档案身份/线索的
// 未知无线幽灵仍被过滤，不建卡。
func TestGui41GhostFoldNoIdentityDropped(t *testing.T) {
	a, _ := newWirelessApp()
	devs := []adb.Device{
		{Serial: "10.0.0.99:5555", State: "offline", ConnType: "wifi"},
	}
	folded := a.foldGhostWireless(devs)
	if len(folded) != 0 {
		t.Fatalf("无身份幽灵应过滤: %+v", folded)
	}
}

// TestGui41GhostFoldTlsTokenUnchanged：mDNS 令牌（FQN）路径保持原语义——
// 同身份主卡在场 → 隐去令牌卡并把档案 ip:port 补进主卡 Wireless。
func TestGui41GhostFoldTlsTokenUnchanged(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)

	token := "adb-601c9f08-Ab12Cd._adb-tls-connect._tcp"
	devs := []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"},
		{Serial: token, State: "offline", ConnType: "other"},
	}
	folded := a.foldGhostWireless(devs)
	if len(folded) != 1 {
		t.Fatalf("令牌应隐去、主卡保留: %+v", folded)
	}
	if folded[0].Serial != "601c9f08" || folded[0].Wireless != "192.168.31.197:5555" {
		t.Fatalf("令牌命中同身份时应把档案 ip:port 补进主卡 Wireless: %+v", folded[0])
	}
	if gui34Find(folded, token) != nil {
		t.Fatalf("令牌卡不应保留为独立卡: %+v", folded)
	}
}
