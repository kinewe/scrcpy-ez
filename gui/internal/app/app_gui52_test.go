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
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui52：档案状态精简 + 无线接入学习 ---

// TestGui52LegacyMigrationToTwoState 旧 JSON（fail/lastOk/lastFail/stale/history
// 多字段并存）载入后必须折叠成二态：每形态一条；active 优先（lastOk 最新），
// 无 active 时保留 stale（lastOk 最新）；旧统计字段清空且不再落盘。
func TestGui52LegacyMigrationToTwoState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "serials": ["a743e1df"],
      "addrs": [
        {"addr": "192.168.31.99:33895", "state": "active", "fail": 11, "lastOk": 1750000100, "lastFail": 1750000090, "mode": "tls"},
        {"addr": "192.168.31.99:41234", "state": "active", "fail": 2, "lastOk": 1750000200, "stale": true, "mode": "tls"},
        {"addr": "192.168.31.99:45005", "state": "history", "fail": 5, "lastOk": 1750000150, "mode": "tls"},
        {"addr": "192.168.31.183:5555", "state": "history", "fail": 254, "lastOk": 1750000300, "mode": "tcpip"},
        {"addr": "192.168.31.162:5555", "state": "history", "fail": 223, "lastOk": 1750000400, "mode": "tcpip"},
        {"addr": "192.168.31.197:5555", "state": "active", "lastOk": 1750000050, "stale": true, "mode": "tcpip"}
      ],
      "profiles": {"usb": {}, "wifi": {}}
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	e, ok := s.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatalf("档案应加载: %v", s.Entries())
	}
	if len(e.Addrs) != 2 {
		t.Fatalf("迁移折叠后应只剩 TLS 一条 + tcpip 一条: %+v", e.Addrs)
	}
	tls := gui24FindAddr(e, "192.168.31.99:33895")
	if tls == nil || tls.State != AddrStateActive || tls.Mode != ModeTls {
		t.Fatalf("同形态 active 应保留 lastOk 最新的 33895: %+v", e.Addrs)
	}
	tcp := gui24FindAddr(e, "192.168.31.162:5555")
	if tcp == nil || tcp.State != AddrStateStale || tcp.Mode != ModeTcpip {
		t.Fatalf("旧 history/stale 应折叠成 lastOk 最新的 162 stale: %+v", e.Addrs)
	}
	for _, a := range e.Addrs {
		if a.Fail != 0 || a.LastOk != 0 || a.LastFail != 0 {
			t.Fatalf("迁移后旧统计不得入内存: %+v", a)
		}
		if a.Stale != (a.State == AddrStateStale) {
			t.Fatalf("内存态 Stale 冗余标必须与 state 同步: %+v", a)
		}
	}

	got := s.OrderedAddrs("a743e1df")
	if len(got) != 2 || got[0].Addr != "192.168.31.99:33895" || got[0].State != AddrStateActive ||
		got[1].Addr != "192.168.31.162:5555" || got[1].State != AddrStateStale {
		t.Fatalf("二态候选应为 active TLS → stale tcpip: %+v", got)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b)
	if !strings.Contains(raw, `"state": "active"`) || !strings.Contains(raw, `"state": "stale"`) {
		t.Fatalf("落盘应只有二态 state:\n%s", b)
	}
	for _, legacy := range []string{`"fail":`, `"lastOk":`, `"lastFail":`, `"stale":`, `"history":`} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("迁移后不得残留旧字段 %s:\n%s", legacy, b)
		}
	}

	// 幂等：二次加载不再改写
	b1, _ := os.ReadFile(path)
	s2 := NewProfileStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(path)
	if string(b1) != string(b2) {
		t.Fatalf("二次加载应幂等:\n---1---\n%s\n---2---\n%s", b1, b2)
	}
}

// TestGui52TwoStateMutualExclusion 成功/失败写入二态互斥：
// active 与 stale 永不同时成立；成功覆盖对应端口写 active，失败写 stale；
// 两形态（TLS/5555）各自独立。
func TestGui52TwoStateMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.99:5555"},
	})
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)

	// 初始：两条都 active
	e, _ := s.Entry("a743e1df")
	for i := range e.Addrs {
		if e.Addrs[i].State != AddrStateActive || e.Addrs[i].Stale {
			t.Fatalf("成功入档必须 state=active 且无 stale 标: %+v", e.Addrs[i])
		}
	}

	// 失败只打对应端口：TLS 翻 stale，5555 仍 active（形态独立）
	s.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.99:33895")
	e, _ = s.Entry("a743e1df")
	tls := gui24FindAddr(e, "192.168.31.99:33895")
	tcp := gui24FindAddr(e, "192.168.31.99:5555")
	if tls == nil || tls.State != AddrStateStale || tcp == nil || tcp.State != AddrStateActive {
		t.Fatalf("二态必须按端口独立: tls=%+v tcpip=%+v", tls, tcp)
	}

	// 成功覆盖对应端口：TLS 翻回 active，5555 不受影响
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)
	e, _ = s.Entry("a743e1df")
	tls = gui24FindAddr(e, "192.168.31.99:33895")
	tcp = gui24FindAddr(e, "192.168.31.99:5555")
	if tls == nil || tls.State != AddrStateActive || tls.Stale || tcp == nil || tcp.State != AddrStateActive {
		t.Fatalf("成功必须覆盖对应端口为 active: tls=%+v tcpip=%+v", tls, tcp)
	}

	// 落盘干净
	b, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b)
	for _, legacy := range []string{`"fail":`, `"lastOk":`, `"lastFail":`, `"stale":`, `"history":`} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("gui52 落盘不得出现旧字段 %s:\n%s", legacy, b)
		}
	}
}

// TestGui52PairLearningSerialTlsAnd5555 无线接入一次性学习三覆盖：
// 配对成功 → Serials 学习服务名短号；TLS 地址与 5555 地址并行探测后双双 active。
func TestGui52PairLearningSerialTlsAnd5555(t *testing.T) {
	a, _ := newWirelessApp()
	var mu sync.Mutex
	var conns []string
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		conns = append(conns, addr)
		mu.Unlock()
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		switch prop {
		case "ro.product.marketname":
			return "REDMI K80", nil
		case "ro.product.manufacturer":
			return "Xiaomi", nil
		case "ro.product.model":
			return "24117RK2CC", nil
		}
		return "", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return []discovery.MdnsService{
			{Type: "_adb-tls-connect._tcp", Name: "adb-601c9f08-KWqpio", Addr: "192.168.1.2:33895", Mode: discovery.MdnsModeTls},
		}, nil
	}

	if err := a.PairConnect("", "192.168.1.2", "37033", "33895", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)

	waitFor(t, 3*time.Second, func() bool {
		e, ok := a.profiles.Entry("REDMI K80")
		return ok && contains(e.Serials, "601c9f08") &&
			gui50Fix45EntryHasAddr(e, "192.168.1.2:33895", ModeTls)
	}, "短号/ TLS 入档未完成")

	e, ok := a.profiles.Entry("REDMI K80")
	if !ok {
		t.Fatal("配对成功应建档")
	}
	if !contains(e.Serials, "601c9f08") {
		t.Fatalf("服务名 adb-601c9f08-KWqpio 应学习短号到 Serials: %+v", e.Serials)
	}
	// gui52-fix7：无线接入学习（TLS/5555 connect 探测）已移除——5555(5555 地址)
	// 由 mDNS _adb._tcp 广播匹配入档（MatchMdnsModes），不在此处断言 connect 探测。
}

// TestGui52PairLearningProbeFailWritesStale 5555 探测不通 → 仍入档但 state=stale；
// TLS 探测通 → active（二态按探测结果，失败不阻断成功态）。

func TestGui52DisplayNameWinsForOnlineCard(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Marketname: "REDMI K80", Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	})
	a.profiles.SetDisplayName("REDMI K80", "红米k80")

	devs := []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Marketname: "REDMI K80", Manufacturer: "Xiaomi", Model: "24117RK2CC", Identity: "REDMI K80"},
	}
	applyProfileNames(devs, a.profiles)
	if devs[0].Name != "红米k80" {
		t.Fatalf("在线卡 DisplayNameSet=true 应显示自定义名: %+v", devs[0])
	}

	// DisplayNameSet=false → 在线卡保留 adb 富化名（marketname 链）
	a.profiles.SetDisplayName("REDMI K80", "")
	devs = []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"},
	}
	applyProfileNames(devs, a.profiles)
	if devs[0].Name != "REDMI K80" {
		t.Fatalf("未自定义时在线卡应保留原富化名: %+v", devs[0])
	}
}

// TestGui52CandidateDegradeRecoverLoop 降级/恢复闭环：
// active=在线证据（无离线候选）→ 打 stale 后成为离线候选 → 探测成功翻回 active
// → 再次无离线候选。
func TestGui52CandidateDegradeRecoverLoop(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro", Wireless: "192.168.31.99:5555"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)

	// active → 在线证据
	if got := a.profiles.OfflineCandidateAddrs(nil); len(got) != 0 {
		t.Fatalf("active 地址=在线证据，不应有离线候选: %+v", got)
	}
	// 全 stale → 离线候选（TLS 优先）
	if !a.profiles.MarkAllAddrsStale("Xiaomi Pad 8 Pro") {
		t.Fatal("MarkAllAddrsStale 应有改动")
	}
	got := a.profiles.OfflineCandidateAddrs(nil)["Xiaomi Pad 8 Pro"]
	if len(got) != 2 || got[0].Addr != "192.168.31.99:33895" || got[1].Addr != "192.168.31.99:5555" {
		t.Fatalf("全 stale 后应产出 TLS 优先离线候选: %+v", got)
	}
	// 探测成功 → 翻回 active
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	tls := gui24FindAddr(e, "192.168.31.99:33895")
	if tls == nil || tls.State != AddrStateActive {
		t.Fatalf("恢复应翻回 active: %+v", e.Addrs)
	}
	// 5555 仍 stale；但存在 active TLS → 仍视为在线证据
	if got := a.profiles.OfflineCandidateAddrs(nil); len(got) != 0 {
		t.Fatalf("任一 active 地址即在线证据: %+v", got)
	}
}
