package app

import (
	"context"
	"sync"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui37：mDNS 广播缺席打标——档案形态"用不了"预区分（主人设计） ---
//
// 根因：gui36 直选不再验证后，旧 TLS 条目 state=active 且 TLS 层优先 →
// OrderedAddrs[0] 恒为死 TLS；mDNS 只处理"广播在场"的匹配，广播缺席形态
// 无任何处理，死 TLS 永不被纠正。
// 方案：mDNS 每 15s 非空扫描后统一检视——设备自报某形态而另一形态缺席 →
// 缺席形态全部档案条目标记 Stale=true（用不了）；广播匹配/连接成功即复活；
// 空快照/daemon 挂不检视（防瞬态误标）。
// 本文件验证：缺席打标、直选跳过、广播复活、对称打标、空快照保守、
// 档案保留、新端口 retire 回归、连接成功复活。

// gui37SeedTablet 播种平板现场基线：tls(42627) active + tcpip(5555) active。
func gui37SeedTablet(a *App) {
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:42627", State: AddrStateActive, Fail: 2, LastOk: 1787740000, LastFail: 1787700000, Mode: ModeTls},
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Fail: 0, LastOk: 1787741000, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
}

// gui37SetWifiDevice 放一台在线无线卡（StartCast 目标）。
func gui37SetWifiDevice(a *App, serial string) {
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: serial, State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.mu.Unlock()
}

// gui37MdnsTcpipOnly 平板只广播 5555（无线调试关：无 TLS 广播）。
func gui37MdnsTcpipOnly() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb._tcp", Name: "adb-a743e1df", Addr: "192.168.31.162:5555", Mode: discovery.MdnsModeTcpip},
	}
}

// gui37MdnsTlsOnly 平板只广播 TLS 42627（经典 5555 缺席）。
func gui37MdnsTlsOnly() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-Xy9zQ2", Addr: "192.168.31.162:42627", Mode: discovery.MdnsModeTls},
	}
}

// runGui37Scan 直接种入 mdns 快照并按旧口径做 MatchMdnsModes + 广播缺席打标
// （等价于旧 15s 扫描的档案同步部分；track 事件路径已在 gui48 测试覆盖）。
func runGui37Scan(t *testing.T, a *App, svcs []discovery.MdnsService) {
	t.Helper()
	a.mdnsMu.Lock()
	a.mdns = append([]discovery.MdnsService(nil), svcs...)
	a.mdnsFirstDone = true
	a.mdnsMu.Unlock()
	matches := make([]MdnsMatch, 0, len(svcs))
	for _, s := range svcs {
		matches = append(matches, MdnsMatch{Name: s.Name, Addr: s.Addr, Mode: s.Mode})
	}
	_ = a.profiles.MatchMdnsModes(matches)
	a.profiles.MarkMdnsAbsentStale(matches)
}

// TestGui37TlsAbsentMarksStaleAndStartCastPicksTcpip：设备只广播 5555 →
// TLS 形态缺席 state=stale；OrderedAddrs active 5555 优先、stale TLS 作离线
// 候选；清空广播后 StartCast 直选 5555（零 Connect）。
func TestGui37TlsAbsentMarksStaleAndStartCastPicksTcpip(t *testing.T) {
	a, f := newWirelessApp()
	gui37SeedTablet(a)
	gui37SetWifiDevice(a, "192.168.31.162:5555")

	runGui37Scan(t, a, gui37MdnsTcpipOnly())

	e, ok := a.profiles.Entry("a743e1df")
	if !ok {
		t.Fatal("档案应可解析")
	}
	tls := gui24FindAddr(e, "192.168.31.162:42627")
	if tls == nil || !tls.Stale {
		t.Fatalf("TLS 广播缺席应打标 Stale=true: %+v", tls)
	}
	tcpip := gui24FindAddr(e, "192.168.31.162:5555")
	if tcpip == nil || tcpip.Stale {
		t.Fatalf("5555 广播在场不应打标: %+v", tcpip)
	}
	got := a.profiles.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.162:5555" || got[0].State != AddrStateActive ||
		got[1].Addr != "192.168.31.162:42627" || got[1].State != AddrStateStale {
		t.Fatalf("OrderedAddrs 应 active 5555 优先、stale TLS 作离线候选: %+v", got)
	}

	// 清空广播快照，强制走"无广播 → OrderedAddrs[0]"直选链
	setMdns(a, nil)
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return nil
	}
	if err := a.StartCast("192.168.31.162:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.162:5555" {
		t.Fatalf("直选应跳过打标 TLS 选 5555: %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("gui37 直选不得触发 Connect 验证: %v", calls)
	}
}

// TestGui37TlsRebroadcastClearsStaleAndOrderedAddrsIncludesTls：TLS 广播再现
// （同地址）→ MatchMdnsModes 清除 Stale（复活）→ OrderedAddrs 重新含 TLS。
func TestGui37TlsRebroadcastClearsStaleAndOrderedAddrsIncludesTls(t *testing.T) {
	a, _ := newWirelessApp()
	gui37SeedTablet(a)

	// 先构造"TLS 缺席已打标"状态（直接走检视，等价于上一轮只有 5555 广播）
	a.profiles.MarkMdnsAbsentStale([]MdnsMatch{
		{Name: "adb-a743e1df", Addr: "192.168.31.162:5555", Mode: discovery.MdnsModeTcpip},
	})
	e0, _ := a.profiles.Entry("a743e1df")
	if tls0 := gui24FindAddr(e0, "192.168.31.162:42627"); tls0 == nil || !tls0.Stale {
		t.Fatalf("前置：TLS 应已打标: %+v", tls0)
	}

	// 本轮 TLS 广播再现
	runGui37Scan(t, a, gui37MdnsTlsOnly())

	e, _ := a.profiles.Entry("a743e1df")
	tls := gui24FindAddr(e, "192.168.31.162:42627")
	if tls == nil || tls.Stale {
		t.Fatalf("TLS 广播再现应清除 Stale（复活）: %+v", tls)
	}
	got := a.profiles.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.162:42627" || got[0].State != AddrStateActive ||
		got[1].Addr != "192.168.31.162:5555" || got[1].State != AddrStateStale {
		t.Fatalf("复活后 OrderedAddrs 应 active TLS 优先（5555 本轮缺席变 stale 候选）: %+v", got)
	}
}

// TestGui37TcpipAbsentMarksStaleAndDirectPicksTls：对称场景——只广播 TLS →
// 5555 打标；清空广播后直选 TLS（零 Connect）。
func TestGui37TcpipAbsentMarksStaleAndDirectPicksTls(t *testing.T) {
	a, f := newWirelessApp()
	gui37SeedTablet(a)
	gui37SetWifiDevice(a, "192.168.31.162:5555")

	runGui37Scan(t, a, gui37MdnsTlsOnly())

	e, _ := a.profiles.Entry("a743e1df")
	tcpip := gui24FindAddr(e, "192.168.31.162:5555")
	if tcpip == nil || !tcpip.Stale {
		t.Fatalf("5555 广播缺席应打标 Stale=true: %+v", tcpip)
	}
	got := a.profiles.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.162:42627" || got[0].State != AddrStateActive ||
		got[1].Addr != "192.168.31.162:5555" || got[1].State != AddrStateStale {
		t.Fatalf("OrderedAddrs 应 active TLS 优先、stale 5555 作离线候选: %+v", got)
	}

	setMdns(a, nil)
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return nil
	}
	if err := a.StartCast("192.168.31.162:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.162:42627" {
		t.Fatalf("5555 打标后直选应取 TLS 42627: %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("直选不得触发 Connect 验证: %v", calls)
	}
}

// TestGui37EmptySnapshotNoMark：成功扫描但快照为空（daemon 挂/设备哑巴语义）
// → 不检视不误标，条目原样参与。
func TestGui37EmptySnapshotNoMark(t *testing.T) {
	a, _ := newWirelessApp()
	gui37SeedTablet(a)

	runGui37Scan(t, a, nil) // 成功扫描、无任何广播

	e, _ := a.profiles.Entry("a743e1df")
	tls := gui24FindAddr(e, "192.168.31.162:42627")
	tcpip := gui24FindAddr(e, "192.168.31.162:5555")
	if tls == nil || tls.Stale {
		t.Fatalf("空快照不应打标 TLS: %+v", tls)
	}
	if tcpip == nil || tcpip.Stale {
		t.Fatalf("空快照不应打标 tcpip: %+v", tcpip)
	}
	got := a.profiles.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.162:42627" || got[1].Addr != "192.168.31.162:5555" {
		t.Fatalf("空快照应保持两个形态原样参与: %+v", got)
	}
}

// TestGui37StaleEntriesRetainedInArchive：打标=state 翻 stale，不删条目——
// Entry 查询仍可见，mode/fail/lastOk/lastFail 内存统计原样；AllAddrs 仍包含。
func TestGui37StaleEntriesRetainedInArchive(t *testing.T) {
	a, _ := newWirelessApp()
	gui37SeedTablet(a)

	eBefore, _ := a.profiles.Entry("a743e1df")
	tlsBefore := gui24FindAddr(eBefore, "192.168.31.162:42627")
	if tlsBefore == nil {
		t.Fatal("前置 TLS 条目应存在")
	}
	before := *tlsBefore

	runGui37Scan(t, a, gui37MdnsTcpipOnly())

	e, _ := a.profiles.Entry("a743e1df")
	tls := gui24FindAddr(e, "192.168.31.162:42627")
	if tls == nil {
		t.Fatalf("打标不应删除 TLS 条目: %+v", e.Addrs)
	}
	if !tls.Stale || tls.State != AddrStateStale {
		t.Fatalf("TLS 应已打标 state=stale: %+v", tls)
	}
	if tls.State == before.State || tls.Mode != before.Mode || tls.Fail != before.Fail ||
		tls.LastOk != before.LastOk || tls.LastFail != before.LastFail {
		t.Fatalf("打标应只改 state（其余内存字段原样）: before=%+v after=%+v", before, *tls)
	}
	all := a.profiles.AllAddrs("a743e1df")
	found := false
	for _, addr := range all {
		if addr == "192.168.31.162:42627" {
			found = true
		}
	}
	if !found {
		t.Fatalf("AllAddrs 应保留打标条目: %v", all)
	}
}

// TestGui37BroadcastNewTlsPortRetireNotRegress：广播新 TLS 端口入档 →
// 新条目 active、旧同类条目按 gui41 单记忆直接删除（不再转 history），
// 新端口参与候选。
func TestGui37BroadcastNewTlsPortRetireNotRegress(t *testing.T) {
	a, _ := newWirelessApp()
	gui37SeedTablet(a)

	runGui37Scan(t, a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.162:45005", Mode: discovery.MdnsModeTls},
	})

	e, _ := a.profiles.Entry("a743e1df")
	newTls := gui24FindAddr(e, "192.168.31.162:45005")
	if newTls == nil || newTls.State != AddrStateActive || newTls.Stale {
		t.Fatalf("新 TLS 广播应入档 active 且未打标: %+v", newTls)
	}
	if gui24FindAddr(e, "192.168.31.162:42627") != nil {
		t.Fatalf("旧 TLS 应按单记忆删除（不再 history）: %+v", e.Addrs)
	}
	got := a.profiles.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.162:45005" || got[0].State != AddrStateActive ||
		got[1].Addr != "192.168.31.162:5555" || got[1].State != AddrStateStale {
		t.Fatalf("候选应 active 新 TLS 45005 优先（5555 本轮缺席变 stale 候选）: %+v", got)
	}
}

// TestGui37AddrSuccessWithModeClearsStale：连接成功（AddrSuccessWithMode）=
// 活性事实，解除广播缺席打标并恢复 active。
func TestGui37AddrSuccessWithModeClearsStale(t *testing.T) {
	a, _ := newWirelessApp()
	gui37SeedTablet(a)

	// 先打标 TLS（等价于上一轮只有 5555 广播）
	a.profiles.MarkMdnsAbsentStale([]MdnsMatch{
		{Name: "adb-a743e1df", Addr: "192.168.31.162:5555", Mode: discovery.MdnsModeTcpip},
	})
	e0, _ := a.profiles.Entry("a743e1df")
	if tls0 := gui24FindAddr(e0, "192.168.31.162:42627"); tls0 == nil || !tls0.Stale {
		t.Fatalf("前置：TLS 应已打标: %+v", tls0)
	}

	a.profiles.AddrSuccessWithMode("a743e1df", "192.168.31.162:42627", ModeTls)

	e, _ := a.profiles.Entry("a743e1df")
	tls := gui24FindAddr(e, "192.168.31.162:42627")
	if tls == nil || tls.Stale {
		t.Fatalf("连接成功应解除打标: %+v", tls)
	}
	if tls.State != AddrStateActive || tls.LastFail != 0 {
		t.Fatalf("连接成功应恢复 active/清 lastFail: %+v", tls)
	}
	got := a.profiles.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.162:42627" {
		t.Fatalf("解除打标后 TLS 应重新参与候选: %+v", got)
	}
}
