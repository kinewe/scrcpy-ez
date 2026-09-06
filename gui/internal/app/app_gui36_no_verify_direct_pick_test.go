package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- gui36：投屏去掉 IP 活性验证——档案地址直选（主人设计：验证交给 15s mDNS） ---
//
// 根因：StartCast 无线地址链②对档案候选逐个 adb connect 活性验证
// （1s × N 候选），阻塞在 StartCast 内、拖慢投屏启动反馈 0.5-2s。
// 方案：mDNS 15s 扫描 + 探测链 15s 节流已在维护档案地址活性；投屏时不再做任何
// IP 验证——广播命中直接选（不变），无广播直接拿档案 OrderedAddrs[0]
// （active 优先、TLS 优先、自动排除 60s 失败节流条目）。
// 本文件验证：直选、零 Connect、无候选走 target.Serial 兜底、广播回归、
// 候选顺序与节流排除。

// TestGui36NoBroadcastActiveCandidatesDirectPickNoConnect：无广播 + 档案有
// active/history 候选（K80 实测档案）→ startAddr=OrderedAddrs[0]=42449
// （tls active），全程零 disc.Connect 调用；不提示「未找到」。
func TestGui36NoBroadcastActiveCandidatesDirectPickNoConnect(t *testing.T) {
	a, f := newWirelessApp()
	k80Gui32Archive(a)
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
		t.Fatalf("无广播应直取 OrderedAddrs[0]=42449: %+v", p)
	}
	mu.Lock()
	gotCalls := append([]string{}, calls...)
	mu.Unlock()
	if len(gotCalls) != 0 {
		t.Fatalf("gui36 投屏不得触发 Connect 验证: %v", gotCalls)
	}
}

// TestGui36NoBroadcastNoCandidatesFallbackToSerial：无广播 + 档案无候选 →
// startAddr=""、不触发「未找到可用无线地址」；注入 target.Serial 兜底并启动 bat。
func TestGui36NoBroadcastNoCandidatesFallbackToSerial(t *testing.T) {
	a, f := newWirelessApp()
	k80Gui32WifiCard(a) // 在线无线卡，但未种档案地址
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return nil
	}

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatalf("无候选不应提示「未找到」，应走 target.Serial 兜底: %v", err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("档案无候选时应注入当前在线 target.Serial: %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("无候选兜底也不得触发 Connect 验证: %v", calls)
	}
}

// TestGui36BroadcastStillDirectSelect（gui48-mdns4 档案化）：TLS stale、tcpip
// 5555 active → 直接选档案 active 5555，不验证、不写档案。
func TestGui36BroadcastStillDirectSelect(t *testing.T) {
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
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("广播命中不得触发 Connect 验证: %v", calls)
	}
}

// TestGui36CandidateOrderTlsBeforeTcpipDirectPick：无广播 + 档案 tls/tcpip 两类
// active 并存 → OrderedAddrs[0]=tls 条目，直选该 tls 地址（TLS 优先不回归）。
func TestGui36CandidateOrderTlsBeforeTcpipDirectPick(t *testing.T) {
	a, f := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1787745800, Mode: ModeTcpip},
			{Addr: "192.168.31.197:33895", State: AddrStateActive, Fail: 0, LastOk: 1787745799, Mode: ModeTls},
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
		return nil
	}

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:33895" {
		t.Fatalf("OrderedAddrs[0] 应为 tls 33895（tls 优先），直选该地址: %+v", p)
	}
	if s := a.Snapshot(); !s.Cast.Tls {
		t.Fatalf("直选 tls 地址应标 TLS: %+v", s.Cast)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("tls 优先直选也不得触发 Connect 验证: %v", calls)
	}
}

// TestGui36ThrottledEntryExcludedFromCandidates（回归）：tls 条目 lastFail 距今
// <60s → OrderedAddrs 不返回该条目（60s 失败节流），只剩 tcpip 候选；
// StartCast 直选 tcpip 5555。
func TestGui36ThrottledEntryExcludedFromCandidates(t *testing.T) {
	a, f := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:33895", State: AddrStateActive, Fail: 2, LastOk: 1750000001, LastFail: time.Now().Unix(), Mode: ModeTls},
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1750000002, Mode: ModeTcpip},
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
		return nil
	}

	got := a.profiles.OrderedAddrs("601c9f08")
	if len(got) != 1 || got[0].Addr != "192.168.31.197:5555" {
		t.Fatalf("60s 节流期内的 tls 条目不应出现在候选里: %+v", got)
	}
	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("节流排除后应直选剩余候选 5555: %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("节流候选直选也不得触发 Connect 验证: %v", calls)
	}
}
