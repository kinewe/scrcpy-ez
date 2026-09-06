package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui48-mdns8：Goodbye 先问再定 + 无信号保持现状 + 双卡 Identity ---

// mdns8StartLogCapture 把 bridge 调试日志重定向到测试临时文件，供日志断言。
func mdns8StartLogCapture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bridge.log")
	bridge.EnableDebugLog(p)
	t.Cleanup(func() { bridge.DisableDebugLog() })
	return p
}

// mdns8LogContains 等待日志文件出现指定子串（异步写入，轮询最多 3s）。
func mdns8LogContains(t *testing.T, path, sub string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), sub) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("日志缺少 %q，当前内容：%s", sub, string(b))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mdns8ProbeGate 按发起顺序给每个探测单独发结果：交错场景可控制先后完成顺序。
type mdns8ProbeGate struct {
	mu      sync.Mutex
	calls   []string
	results []chan bool
	started chan struct{}
}

func newMdns8ProbeGate() *mdns8ProbeGate {
	return &mdns8ProbeGate{started: make(chan struct{}, 16)}
}

func (g *mdns8ProbeGate) fn() func(context.Context, string) bool {
	return func(_ context.Context, addr string) bool {
		g.mu.Lock()
		g.calls = append(g.calls, addr)
		ch := make(chan bool, 1)
		g.results = append(g.results, ch)
		g.mu.Unlock()
		g.started <- struct{}{}
		return <-ch
	}
}

func (g *mdns8ProbeGate) waitCalls(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		g.mu.Lock()
		got := append([]string(nil), g.calls...)
		g.mu.Unlock()
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %d 次 TCP 探测超时，当前 %v", want, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (g *mdns8ProbeGate) release(t *testing.T, i int, ok bool) {
	t.Helper()
	g.mu.Lock()
	if i >= len(g.results) {
		g.mu.Unlock()
		t.Fatalf("release 索引越界: %d/%d", i, len(g.results))
	}
	ch := g.results[i]
	g.mu.Unlock()
	ch <- ok
}

// Goodbye 探测通 → 翻回 active（硬事实直接定状态）+ 触发日志。
func TestGui48Mdns8GoodbyeProbeSuccessActiveAndLog(t *testing.T) {
	a, _ := newWirelessApp()
	addr := "192.168.31.197:45005"
	fix2Seed(a, addr)
	a.profiles.MarkAddrStale("REDMI K80", addr) // 先 stale：验证翻回
	logPath := mdns8StartLogCapture(t)
	probeCalls := make(chan string, 1)
	a.disc.TcpProbeFn = func(ctx context.Context, got string) bool {
		probeCalls <- got
		return true
	}

	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	a.onMdnsDropped(mdnsServiceDropKey(svc), svc)
	waitForMdns(t, "Goodbye 探测通应翻回 active", func() bool {
		ae := fix2Addr(a, addr)
		return ae != nil && ae.State == AddrStateActive && !ae.Stale
	})
	select {
	case got := <-probeCalls:
		if got != addr {
			t.Fatalf("TCP 探测地址错误: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Goodbye 应发起 TCP 探测")
	}
	mdns8LogContains(t, logPath, "[app] mdns Goodbye（TCP 探测通，active）：REDMI K80/"+addr)
}

// Goodbye 探测不通 → 打 stale（原降级语义保留）。
func TestGui48Mdns8GoodbyeProbeFailStale(t *testing.T) {
	a, _ := newWirelessApp()
	addr := "192.168.31.197:45005"
	fix2Seed(a, addr)
	probeCalls := make(chan string, 1)
	a.disc.TcpProbeFn = func(ctx context.Context, got string) bool {
		probeCalls <- got
		return false
	}

	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	a.onMdnsDropped(mdnsServiceDropKey(svc), svc)
	waitForMdns(t, "Goodbye 探测不通应打 stale", func() bool {
		ae := fix2Addr(a, addr)
		return ae != nil && ae.Stale
	})
	select {
	case got := <-probeCalls:
		if got != addr {
			t.Fatalf("TCP 探测地址错误: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Goodbye 应发起 TCP 探测")
	}
}

// Goodbye 非 IP:port 条目（令牌）→ 不探测直接打 stale（原语义）。
func TestGui48Mdns8GoodbyeNonIPNoProbeDirectStale(t *testing.T) {
	a, _ := newWirelessApp()
	token := "adb-601c9f08-KWqpio"
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs:      []AddrEntry{{Addr: token, State: AddrStateActive, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	var mu sync.Mutex
	probes := 0
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool {
		mu.Lock()
		probes++
		mu.Unlock()
		return false
	}

	svc := discovery.MdnsService{Type: "_adb-tls-connect._tcp", Name: token, Addr: token, Mode: discovery.MdnsModeTls}
	a.onMdnsDropped(mdnsServiceDropKey(svc), svc)
	mu.Lock()
	n := probes
	mu.Unlock()
	if n != 0 {
		t.Fatalf("非 IP:port 条目不应发起 TCP 探测: calls=%d", n)
	}
	if ae := fix2Addr(a, token); ae == nil || !ae.Stale {
		t.Fatalf("非 IP:port Goodbye 应保持原 stale 语义: %+v", ae)
	}
}

// 90s 静默触发后不立即打 stale（档案状态不变）；探测结果回来才定状态。
func TestGui48Mdns8IdleSignalNoImmediateStale(t *testing.T) {
	a, _ := newWirelessApp()
	addr := "192.168.31.197:45005"
	fix2Seed(a, addr)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	a.disc.TcpProbeFn = func(ctx context.Context, got string) bool {
		close(started)
		<-release
		close(finished)
		return false
	}

	a.onMdnsIdleSignal(addr)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("90s 静默应发起 TCP 问询")
	}
	if ae := fix2Addr(a, addr); ae == nil || ae.Stale {
		t.Fatalf("问询未回前不得打 stale（保持现状）: %+v", ae)
	}

	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("探测应已返回")
	}
	waitForMdns(t, "静默问询不通应打 stale", func() bool {
		ae := fix2Addr(a, addr)
		return ae != nil && ae.Stale
	})
}

// 收敛性：静默 → 探测通（翻回 active）→ 再次静默 → 探测不通（stale）。
func TestGui48Mdns8IdleTwoRoundsConverge(t *testing.T) {
	a, _ := newWirelessApp()
	addr := "192.168.31.197:45005"
	fix2Seed(a, addr)
	a.profiles.MarkAddrStale("REDMI K80", addr)
	var mu sync.Mutex
	n := 0
	a.disc.TcpProbeFn = func(ctx context.Context, got string) bool {
		mu.Lock()
		n++
		cur := n
		mu.Unlock()
		return cur == 1 // 第一轮通、第二轮不通
	}

	a.onMdnsIdleSignal(addr)
	waitForMdns(t, "第一轮问询通应翻回 active", func() bool {
		ae := fix2Addr(a, addr)
		return ae != nil && !ae.Stale && ae.State == AddrStateActive
	})
	a.onMdnsIdleSignal(addr)
	waitForMdns(t, "第二轮问询不通应打 stale", func() bool {
		ae := fix2Addr(a, addr)
		return ae != nil && ae.Stale
	})
	mu.Lock()
	got := n
	mu.Unlock()
	if got != 2 {
		t.Fatalf("两轮收敛应恰好探测 2 次: calls=%d", got)
	}
}

// Goodbye 探测与 90s 静默探测交错 → 后发者胜：先发结果过期直接丢弃。
func TestGui48Mdns8InterleavedProbeLastWins(t *testing.T) {
	a, _ := newWirelessApp()
	addr := "192.168.31.197:45005"
	fix2Seed(a, addr)
	gate := newMdns8ProbeGate()
	a.disc.TcpProbeFn = gate.fn()

	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	a.onMdnsDropped(mdnsServiceDropKey(svc), svc) // 探测 0：Goodbye（先发）
	gate.waitCalls(t, 1)
	a.onMdnsIdleSignal(addr) // 探测 1：静默问询（后发，最新）
	gate.waitCalls(t, 2)

	gate.release(t, 1, false) // 后发结果先回：不通 → stale 落库
	waitForMdns(t, "后发探测不通应打 stale", func() bool {
		ae := fix2Addr(a, addr)
		return ae != nil && ae.Stale
	})
	gate.release(t, 0, true) // 先发结果后回：通 → 但已过期，必须丢弃
	time.Sleep(100 * time.Millisecond)
	if ae := fix2Addr(a, addr); ae == nil || !ae.Stale {
		t.Fatalf("过期 Goodbye 结果不得翻回状态（后发者胜）: %+v", ae)
	}
}

// 双卡修复：设备流卡 Serial 解析失败但 Identity 命中档案 → 不补合成在线卡。
func TestGui48Mdns8ProfileOfflineCardIdentityHave(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	devs := a.appendProfileOfflineCards([]adb.Device{
		{Serial: "192.168.31.183:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 1 {
		t.Fatalf("Identity 命中已有卡不应再补合成在线卡: %+v", devs)
	}
	if devs[0].Serial != "192.168.31.183:5555" || devs[0].Identity != "REDMI K80" {
		t.Fatalf("原设备卡应原样保留: %+v", devs)
	}
}
