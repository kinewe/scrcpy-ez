package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui32：投屏地址基准=活着的（mDNS 广播优先 + 档案地址验证后才选，不选死记忆） ---
//
// 实况（2026-08-26 22:56 K80）：无线调试关，广播只有 adb-601c9f08 @ 197:5555
// （tcpip 活）；档案 TLS 类 35263(history, lastOk 更晚, fail19) 与
// 42449(active, fail2)——旧 newestOfClassLocked 比较键只有 lastOk → 选 35263
// 死端口，投屏 fail19。修复：
//   A. newestOfClassLocked：active 严格优先（history=死记忆只作兜底）；
//   B. StartCast 地址链：mDNS 广播命中直接选；无广播 → 档案 OrderedAddrs 逐个
//      adb connect 验证（1s 超时），首个成功 → 选用（回写档案）；全失败 →
//      提示「未找到可用无线地址」（无线卡；USB 卡走 USB serial 不涉及）；
//   C. TLS 标跟随所选地址形态（选 5555 自然不标 TLS）。

// startVerifyAlwaysOK 注入"候选验证总是成功"的 fake connect（旧测试适配 gui32
// 验证链：这些测试锁定的语义是"候选入选"，不含验证失败分支——验证环节由本文件
// 的 TestGui32StartCastVerifyCandidates* 专测）。
func startVerifyAlwaysOK(a *App) {
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return nil }
}

// k80Gui32Archive 复现实测档案（无线调试关现场）：tcpip 5555 active（活）；
// tls 类 42449 active(fail2) + 35263 history(lastOk 更晚, fail19)。
func k80Gui32Archive(a *App) {
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1787745800, Mode: ModeTcpip},
			{Addr: "192.168.31.197:42449", State: AddrStateActive, Fail: 2, LastOk: 1787745225, Mode: ModeTls},
			{Addr: "192.168.31.197:35263", State: AddrStateHistory, Fail: 19, LastOk: 1787745659, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
}

// k80Gui32WifiCard 设备列表放 K80 无线卡（当前在线地址 5555）。
func k80Gui32WifiCard(a *App) {
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	}
	a.mu.Unlock()
}

// TestGui32NewestOfClassActiveFirst：同 class active 严格优先——active(42449,
// fail2) vs history(35263, lastOk 更新) → 选 42449（死记忆不抢）；无 active 的
// 类 → history 中 lastOk 最新兜底。
func TestGui32NewestOfClassActiveFirst(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1787745800, Mode: ModeTcpip},
			{Addr: "192.168.31.197:42449", State: AddrStateActive, Fail: 2, LastOk: 1787745225, Mode: ModeTls},
			{Addr: "192.168.31.197:35263", State: AddrStateHistory, Fail: 19, LastOk: 1787745659, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	got := s.OrderedAddrs("601c9f08")
	if len(got) != 2 || got[0].Addr != "192.168.31.197:42449" || got[1].Addr != "192.168.31.197:5555" {
		t.Fatalf("TLS 类应选 active 42449 而非 history 35263（tls → tcpip 分层）: %+v", got)
	}
	if s.BestAddr("601c9f08") != "192.168.31.197:42449" {
		t.Fatalf("BestAddr 应为 active 42449: %q", s.BestAddr("601c9f08"))
	}

	// gui52 二态：旧 history 归一为 stale=离线候选 → 取档案顺序第一条 stale
	s2 := NewProfileStore("")
	gui15Seed(s2, "X", &DeviceEntry{
		Marketname: "X",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:35263", State: AddrStateHistory, Fail: 19, LastOk: 1787745659, Mode: ModeTls},
			{Addr: "192.168.31.197:44444", State: AddrStateHistory, Fail: 5, LastOk: 1787744000, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	got2 := s2.OrderedAddrs("601c9f08")
	if len(got2) != 1 || got2[0].Addr != "192.168.31.197:35263" || got2[0].State != AddrStateStale {
		t.Fatalf("旧 history 应归一为 stale 离线候选（第一条 35263）: %+v", got2)
	}
}

// TestGui32StartCastBroadcastDirectNoVerify（gui48-mdns4 档案化）：TLS 已 stale、
// tcpip 5555 active → 直接选 5555，不触发档案候选验证（ConnectFn 零调用）；
// 不标 TLS。
func TestGui32StartCastBroadcastDirectNoVerify(t *testing.T) {
	a, f := newWirelessApp()
	k80Gui32Archive(a)
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:42449")
	k80Gui32WifiCard(a)
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return nil
	}

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("广播命中应直接选 5555: %+v", p)
	}
	if s := a.Snapshot(); s.Cast.Tls {
		t.Fatalf("5555 明文连接不应标 TLS: %+v", s.Cast)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("广播命中不得触发档案候选验证: %v", calls)
	}
	// 不写档案：35263 死记忆状态原样（state/lastOk 不被触碰）
	e, ok := a.profiles.Entry("192.168.31.197:5555")
	if !ok {
		t.Fatal("档案应可解析")
	}
	a35263 := gui24FindAddr(e, "192.168.31.197:35263")
	if a35263 == nil || a35263.State != AddrStateHistory || a35263.LastOk != 1787745659 {
		t.Fatalf("广播路径不应触碰档案死记忆: %+v", a35263)
	}
}

// TestGui32StartCastVerifyCandidatesOrderAndPickLive（gui36 更新 + gui41 history
// 退役）：无广播 → 档案候选仅剩 [5555(tcpip active)]（35263 是 history，不再参与
// 候选），直取 OrderedAddrs[0]=5555；不触发 disc.Connect；不写档案。
func TestGui32StartCastVerifyCandidatesOrderAndPickLive(t *testing.T) {
	a, f := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1787745800, Mode: ModeTcpip},
			// TLS 类无 active → history 35263 兜底候选（死）
			{Addr: "192.168.31.197:35263", State: AddrStateHistory, Fail: 19, LastOk: 1787745659, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	k80Gui32WifiCard(a)
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return nil // 即使可连也不应被调用：gui36 不验证
	}

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("history 退役后应直取唯一 active 候选 5555: %+v", p)
	}
	mu.Lock()
	gotCalls := append([]string{}, calls...)
	mu.Unlock()
	if len(gotCalls) != 0 {
		t.Fatalf("gui36 投屏不得触发 disc.Connect 验证: %v", gotCalls)
	}
	// 不写档案：35263/5555 原样（无验证成功回写）
	e, ok := a.profiles.Entry("192.168.31.197:5555")
	if !ok {
		t.Fatal("档案应可解析")
	}
	a35263 := gui24FindAddr(e, "192.168.31.197:35263")
	if a35263 == nil || a35263.State != AddrStateHistory || a35263.Fail != 19 || a35263.LastFail != 0 {
		t.Fatalf("直选路径不应触碰档案死记忆: %+v", a35263)
	}
	a5555 := gui24FindAddr(e, "192.168.31.197:5555")
	if a5555 == nil || a5555.State != AddrStateActive || a5555.LastFail != 0 {
		t.Fatalf("直选路径不应写档案（5555 原样）: %+v", a5555)
	}
}

// TestGui32StartCastAllCandidatesDeadPrompts（gui36 更新）：无广播且有候选
// （即便地址已死）→ 不提示「未找到」，直选 OrderedAddrs[0]=42449 并启动 bat；
// ConnectFn 零调用；启动成功后参数浮窗覆盖照常消耗。
func TestGui32StartCastAllCandidatesDeadPrompts(t *testing.T) {
	a, f := newWirelessApp()
	k80Gui32Archive(a) // 候选 [42449(tls active), 5555(tcpip active)]
	k80Gui32WifiCard(a)
	a.mu.Lock()
	a.nextParams["192.168.31.197:5555"] = bridge.CastParams{Usb: bridge.ModeParams{Res: 2400, Set: true}}
	a.mu.Unlock()
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return errors.New("cannot connect")
	}

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatalf("有候选即直选，不应提示「未找到可用无线地址」: %v", err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:42449" {
		t.Fatalf("应直取 OrderedAddrs[0]=42449（候选全死也选）: %+v", p)
	}
	if f.startsN() != 1 {
		t.Fatalf("有候选应启动 bat: %d", f.startsN())
	}
	a.mu.RLock()
	_, kept := a.nextParams["192.168.31.197:5555"]
	a.mu.RUnlock()
	if kept {
		t.Fatalf("启动成功应消耗参数浮窗覆盖: %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("gui36 投屏不得触发 disc.Connect 验证: %v", calls)
	}
}

// TestGui32StartCastTlsBroadcastStillFirst：广播 TLS 在场（K80 无线调试开）→
// 直接选 TLS 地址（TLS 优先不回归）、不触发候选验证；标 TLS。
func TestGui32StartCastTlsBroadcastStillFirst(t *testing.T) {
	a, f := newWirelessApp()
	k80Gui32Archive(a)
	setMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-601c9f08-Kk80Xx", Addr: "192.168.31.197:42449", Mode: discovery.MdnsModeTls},
		{Type: "_adb._tcp", Name: "adb-601c9f08", Addr: "192.168.31.197:5555", Mode: discovery.MdnsModeTcpip},
	})
	k80Gui32WifiCard(a)
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return nil
	}

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:42449" {
		t.Fatalf("TLS 广播在场应直接选 TLS 地址: %+v", p)
	}
	if s := a.Snapshot(); !s.Cast.Tls {
		t.Fatalf("TLS 地址启动应标 TLS: %+v", s.Cast)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("TLS 广播命中不得触发档案候选验证: %v", calls)
	}
}

// TestGui32StartCastUsbCardNotInvolved：USB 卡走 USB serial——无线地址链不触发
// 验证（ConnectFn 零调用）、候选全死也不提示（照常 USB 投屏）。
func TestGui32StartCastUsbCardNotInvolved(t *testing.T) {
	a, f := newWirelessApp()
	k80Gui32Archive(a) // 候选全死（ConnectFn 恒失败）
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	}
	a.mu.Unlock()
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return errors.New("cannot connect")
	}

	if err := a.StartCast("601c9f08"); err != nil {
		t.Fatalf("USB 卡投屏不应被无线地址验证阻断: %v", err)
	}
	p := f.waitParams(t, 1)
	if p.Serial != "601c9f08" {
		t.Fatalf("USB 卡应注入 SCEZ_SERIAL: %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("USB 卡不应触发无线候选验证: %v", calls)
	}
}
