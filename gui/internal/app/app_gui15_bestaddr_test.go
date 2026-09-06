package app

import (
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui15：地址选择健康过滤（gui27 起改为"失败节流"语义，3 处旧断言更新） ---
// gui15 原语义：档案中 fail>=2 的地址（已连续两次失败，非瞬态）不再作为
// BestAddr/探测候选，在线健康地址（fail<2）自然胜出。
// gui27 按主人设计拍板（2026-08-26 20:30）抛弃 fail 计数拉黑：fail 计数不再
// 参与任何候选判定（字段保留兼容旧档案），判定改用时间戳语义——lastFail
// 距今 < 60s → 节流跳过（防每轮刷失败）；超过 60s → 无条件恢复参与
// （永不拉黑、永不失忆）。本文件 3 处 fail>=2 旧断言按节流语义更新
// （TestBestAddrSkipsUnhealthyTls / TestOrderedAddrsHealthFilter /
// TestStartCastSkipsUnhealthyTlsAddr）；TestBestAddrKeepsFail1 语义不变仍绿
// （fail=1 与 fail=2 一样不参与判定——tls 层仍优先）。

// gui15Seed 直接种入内存档案条目（复用既有测试直接写 data 的手法）。
func gui15Seed(s *ProfileStore, identity string, e *DeviceEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Devices[identity] = e
}

// TestBestAddrSkipsThrottledTls（gui15 旧断言 1 → 节流语义；gui52 二态）：
// tls(33895) 刚失败（lastFail=now → 60s 节流期内）+ tcpip(5555) →
// BestAddr=5555。OfflineCandidateAddrs：档案存在 active 地址（5555）=
// 在线证据 → 不产出离线候选（降级交给 90s 静默问询/探测失败翻 stale）。
func TestBestAddrSkipsThrottledTls(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "Redmi K80", &DeviceEntry{
		Marketname: "Redmi K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:33895", State: AddrStateActive, Fail: 2, LastOk: 1750000001, LastFail: time.Now().Unix(), Mode: ModeTls},
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1750000002, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	if got := s.BestAddr("601c9f08"); got != "192.168.31.197:5555" {
		t.Fatalf("60s 节流期内的 tls 地址不应胜出，BestAddr 应为 5555: %q", got)
	}
	if list := s.OfflineCandidateAddrs(nil)["Redmi K80"]; len(list) != 0 {
		t.Fatalf("active 地址=在线证据，不应有离线候选: %+v", list)
	}
}

// TestBestAddrKeepsFail1：fail=1（瞬态失败）不降级——gui27 语义下 fail 计数
// 一律不参与判定（lastFail 未记/超时即参与），tls 层仍优先。
func TestBestAddrKeepsFail1(t *testing.T) {
	s := NewProfileStore("")
	gui15Seed(s, "Redmi K80", &DeviceEntry{
		Marketname: "Redmi K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:33895", State: AddrStateActive, Fail: 1, LastOk: 1750000002, Mode: ModeTls},
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1750000001, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	if got := s.BestAddr("601c9f08"); got != "192.168.31.197:33895" {
		t.Fatalf("fail=1 且无 lastFail 的 tls 地址应参与（tls 层优先）: %q", got)
	}
}

// TestOrderedAddrsThrottleFilter（gui15 旧断言 → 节流语义；gui32 再更新为
// active 严格优先）：每类有 active → 只取 active 中 lastOk 最新的一条
// （history 条目即使 lastOk 更新也不抢——死记忆让位）；该条在节流期内 → 本层
// 无候选（不回退旧条目）；lastFail 超 60s → 恢复参与（fail 计数/state 均不阻
// 参与——永不拉黑）。从未成功过（lastOk=0）的旧条目不参与（非本类最新）。
func TestOrderedAddrsThrottleFilter(t *testing.T) {
	now := time.Now()
	s := NewProfileStore("")
	gui15Seed(s, "Redmi K80", &DeviceEntry{
		Marketname: "Redmi K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:33895", State: AddrStateHistory, Fail: 254, LastOk: 1750000004, LastFail: now.Unix(), Mode: ModeTls}, // 最新 history tls 但节流中：无 active 才轮到它
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1750000003, Mode: ModeTcpip},                         // 保留：tcpip 最新
			{Addr: "192.168.31.197:41234", State: AddrStateActive, Fail: 1, LastOk: 1750000002, Mode: ModeTls},                          // 唯一 active tls → 严格优先（history 33895 lastOk 更新也不抢）
			{Addr: "192.168.31.197:44444", State: AddrStateHistory, Fail: 3, LastOk: 1750000001, Mode: ModeTls},                         // 旧 tls：不参与
			{Addr: "192.168.31.197:33333", State: AddrStateHistory, Fail: 1, LastOk: 0},                                                 // 从未成功：不参与
		},
		Profiles: DefaultProfile(),
	})
	got := s.OrderedAddrs("601c9f08")
	// gui32 active 严格优先：tls 层取唯一 active 41234（history 33895 虽 lastOk
	// 更新也不抢——死记忆让位）；tcpip 层 5555。
	if len(got) != 2 || got[0].Addr != "192.168.31.197:41234" || got[1].Addr != "192.168.31.197:5555" {
		t.Fatalf("active 严格优先结果错误（tls 41234 → tcpip 5555）: %+v", got)
	}

	// active 41234 拨进节流期 → 本层无候选（不回退 history 33895——节流是本类
	// 整体跳过，旧条目=纯噪声；与 gui27 节流同口径）。
	s.mu.Lock()
	for i := range s.data.Devices["Redmi K80"].Addrs {
		if s.data.Devices["Redmi K80"].Addrs[i].Addr == "192.168.31.197:41234" {
			s.data.Devices["Redmi K80"].Addrs[i].LastFail = now.Unix()
		}
	}
	s.mu.Unlock()
	got = s.OrderedAddrs("601c9f08")
	if len(got) != 1 || got[0].Addr != "192.168.31.197:5555" {
		t.Fatalf("active 被节流后本类无候选（不回退 history）: %+v", got)
	}
}

// TestStartCastSkipsThrottledTlsAddr（gui15 端到端断言 → 节流语义；gui32 加
// 验证链 fake）：档案 tls(33895) lastFail=now（节流中）+ tcpip(5555)，设备卡
// 在线 5555 → 候选只剩 5555（gui32 验证后选用）→ StartCast 注入 SCEZ_ADDR=5555
// （而非节流中的 33895），bat 不再 5s 循环重试。
func TestStartCastSkipsThrottledTlsAddr(t *testing.T) {
	a, f := newWirelessApp()
	gui15Seed(a.profiles, "Redmi K80", &DeviceEntry{
		Marketname: "Redmi K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:33895", State: AddrStateActive, Fail: 2, LastOk: 1750000001, LastFail: time.Now().Unix(), Mode: ModeTls},
			{Addr: "192.168.31.197:5555", State: AddrStateActive, Fail: 0, LastOk: 1750000002, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "Redmi K80",
			Marketname: "Redmi K80", Identity: "Redmi K80"},
	}
	a.mu.Unlock()
	startVerifyAlwaysOK(a) // gui32 验证链：候选 5555 验证通过（本测试锁定候选语义）

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("SCEZ_ADDR 应注入 5555 而非节流中的 33895: %+v", p)
	}
}
