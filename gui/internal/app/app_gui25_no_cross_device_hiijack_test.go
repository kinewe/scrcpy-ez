package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui25：runDiscovery 分层泄漏修复（在线设备广播不再截胡探测） ---
//
// 现场（2026-08-26 19:44 实机日志）：双无线设备——K80 无线调试开（TLS 42449
// 常驻广播）+ 平板掉线。runDiscovery 把 MatchMdnsModes 结果（含所有设备的
// 广播地址）按形态倒入 tiers，未限定"仅候选设备的广播地址"；ConnectTiers
// 层内并行、首层任一成功即整体 return → K80 的 TLS 广播 42449 connect 成功
// → 整轮"成功"，平板（183/162）的 tcpip 层从未被尝试（6 轮全部被截胡）。
// gui25 修复：tiers 构建时广播地址只纳入"属于 cands 候选设备"的地址，
// 在线设备（不在 cands）的广播不进层；候选设备的广播（含 MatchMdnsModes
// 刚同步入档的 TLS 新端口）照常优先入层。

// seedGui25TabletArchive 播种平板档案：183/162 两个 tcpip 地址（active fail=0）。
func seedGui25TabletArchive(a *App) {
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "T7000PAD", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:5555", ModeTcpip)
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
}

// seedGui25K80Archive 播种 K80 档案：TLS 42449（在线设备的常驻广播地址）。
func seedGui25K80Archive(a *App) {
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "K80SERXXX", State: "device", ConnType: "usb", Marketname: "Redmi K80"},
	})
	a.profiles.AddrSuccessWithMode("Redmi K80", "192.168.31.197:42449", ModeTls)
}

// gui25TabletCands 手工构建探测候选：仅平板（K80 在线，不在候选内）。
func gui25TabletCands() map[string][]AddrEntry {
	return map[string][]AddrEntry{
		"Xiaomi Pad 8 Pro": {
			{Addr: "192.168.31.183:5555", Mode: ModeTcpip, State: AddrStateActive, Fail: 0, LastOk: 100},
			{Addr: "192.168.31.162:5555", Mode: ModeTcpip, State: AddrStateActive, Fail: 0, LastOk: 200},
		},
	}
}

// gui25HiijackMdns 现场快照：K80 TLS 42449 常驻广播 + 平板 tcpip 183 广播。
func gui25HiijackMdns() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-K80SERXXX-Ab12Cd", Addr: "192.168.31.197:42449", Mode: discovery.MdnsModeTls},
		{Type: "_adb._tcp", Name: "adb-T7000PAD", Addr: "192.168.31.183:5555", Mode: discovery.MdnsModeTcpip},
	}
}

// 截胡现场回归：K80（在线）TLS 广播 42449 + 平板（候选）tcpip 广播 183。
// 修复前 42449 进 tls 层且 connect 成功 → 整轮误判 found(42449)，平板的
// 183/162 从未被 connect；修复后 42449 不进层（非候选设备广播被过滤），
// 轮内尝试序列 = [183（广播优先）, 162（档案兜底）]，成功找回平板 162。
func TestGui25NoCrossDeviceHiijack(t *testing.T) {
	a, _ := newWirelessApp()
	seedGui25TabletArchive(a)
	seedGui25K80Archive(a)

	var mu sync.Mutex
	var calls []string
	// 门闩：162 的兜底成功等 183 的广播 connect 已发起之后（确定性顺序断言，
	// 与 gui23 横跳回归同款）。183 广播已死（10060 等价）→ 162 兜底成功。
	gate := make(chan struct{})
	var once sync.Once
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		if addr == "192.168.31.162:5555" {
			<-gate
		}
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.183:5555" {
			once.Do(func() { close(gate) })
			return context.DeadlineExceeded
		}
		return nil // 42449（K80 在线广播）connect 可成功——修复前会截胡整轮
	}
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return gui25HiijackMdns(), nil
	}

	a.runDiscovery(context.Background(), gui25TabletCands())

	st := waitDiscStatus(t, a, "found")
	if st.Found != "192.168.31.162:5555" {
		t.Fatalf("应找回候选平板 162（修复前会误判 found(42449)）: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	// 调用序列：平板广播 183 先试，档案 162 兜底——42449 从未被 connect
	if len(calls) != 2 || calls[0] != "192.168.31.183:5555" || calls[1] != "192.168.31.162:5555" {
		t.Fatalf("调用序列应为 [183 162]（42449 不参与）: %v", calls)
	}
	if addrIn(calls, "192.168.31.197:42449") {
		t.Fatalf("在线设备 K80 的广播 42449 不应被 connect（截胡）: %v", calls)
	}
	// Tried 不含 42449（非候选广播不进层）
	if len(st.Tried) != 2 || st.Tried[0] != "192.168.31.183:5555" || st.Tried[1] != "192.168.31.162:5555" {
		t.Fatalf("Tried 应为 [183 162] 且不含 42449: %+v", st)
	}
	if addrIn(st.Tried, "192.168.31.197:42449") {
		t.Fatalf("Tried 不应含在线设备广播 42449: %+v", st)
	}
	// K80 档案不被污染（42449 未参与本轮：fail 不增、状态不动）
	k80, ok := a.profiles.Entry("Redmi K80")
	if !ok {
		t.Fatal("K80 档案应存在")
	}
	a42449 := gui24FindAddr(k80, "192.168.31.197:42449")
	if a42449 == nil || a42449.Fail != 0 || a42449.State != AddrStateActive {
		t.Fatalf("K80 42449 档案不应被动（未参与探测）: %+v", k80.Addrs)
	}
	// 平板：162 兜底成功 → active+fail=0（成功路径不回填失败）
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	a162 := gui24FindAddr(e, "192.168.31.162:5555")
	if a162 == nil || a162.Fail != 0 || a162.State != AddrStateActive {
		t.Fatalf("162 兜底成功应保持 active+fail=0: %+v", e.Addrs)
	}
}

// 归属过滤不误伤候选设备自身：平板（候选）广播 TLS 新端口 37201（不在
// cands 列表内，MatchMdnsModes 刚同步入档）→ 照常优先入 tls 层并成功；
// 在线 K80 的 42449 仍被过滤（同一快照内两类广播的归属区分）。
func TestGui25CandidateOwnBroadcastStillFirst(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:5555", ModeTcpip)
	seedGui25K80Archive(a)

	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.183:37201" {
			return nil
		}
		return context.DeadlineExceeded
	}
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return []discovery.MdnsService{
			{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-On9v2R", Addr: "192.168.31.183:37201", Mode: discovery.MdnsModeTls},
			{Type: "_adb-tls-connect._tcp", Name: "adb-K80SERXXX-Ab12Cd", Addr: "192.168.31.197:42449", Mode: discovery.MdnsModeTls},
		}, nil
	}

	a.runDiscovery(context.Background(), map[string][]AddrEntry{
		"Xiaomi Pad 8 Pro": {
			{Addr: "192.168.31.183:5555", Mode: ModeTcpip, State: AddrStateActive, Fail: 0, LastOk: 100},
		},
	})

	st := waitDiscStatus(t, a, "found")
	if st.Found != "192.168.31.183:37201" {
		t.Fatalf("候选设备的 TLS 广播应照常优先并成功: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	// tls 层成功即收工：只 connect 平板自己的 TLS 广播 37201
	if len(calls) != 1 || calls[0] != "192.168.31.183:37201" {
		t.Fatalf("应只尝试候选设备自己的 TLS 广播: %v", calls)
	}
	if addrIn(calls, "192.168.31.197:42449") {
		t.Fatalf("在线设备 K80 的 42449 不应被 connect: %v", calls)
	}
	if addrIn(st.Tried, "192.168.31.197:42449") {
		t.Fatalf("Tried 不应含 K80 广播 42449: %+v", st)
	}
	// MatchMdnsModes 入档副作用保持：37201 mode=tls 已归并进平板档案
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	a37201 := gui24FindAddr(e, "192.168.31.183:37201")
	if a37201 == nil || a37201.Mode != ModeTls {
		t.Fatalf("候选设备 TLS 新端口应保持同步入档（mode=tls）: %+v", e.Addrs)
	}
}
