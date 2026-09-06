package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui12：TLS 优先连接策略 + 形态字段（profiles 层） ---

// BestAddr TLS 优先：档案同时有健康 tls 地址与 5555 active 地址时，
// tls 层在前（先 tls 后 5555；层内 active 优先）。
// （gui15：history+fail>=3 的不健康地址被健康过滤排除，另行由
// app_gui15_bestaddr_test.go 覆盖"死地址不再抢占"。）
func TestBestAddrPrefersTls(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()

	s.mu.Lock()
	s.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.99:5555", State: AddrStateActive, LastOk: 200},
			{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: 0, LastOk: 100, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	}
	s.mu.Unlock()

	if got := s.BestAddr("a743e1df"); got != "192.168.31.99:33895" {
		t.Fatalf("TLS 优先：history tls 应在 5555 active 前: %q", got)
	}
	// OrderedAddrs 顺序：tls 层在前
	ordered := s.OrderedAddrs("a743e1df")
	if len(ordered) != 2 || ordered[0].Addr != "192.168.31.99:33895" || ordered[1].Addr != "192.168.31.99:5555" {
		t.Fatalf("OrderedAddrs 应为 tls 优先: %+v", ordered)
	}
	// 层内 active 优先：两个 tls 地址时 active 在前（经 addrSuccess 正常排序路径）
	s.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.77:33999", ModeTls)
	if got := s.BestAddr("a743e1df"); got != "192.168.31.77:33999" {
		t.Fatalf("tls 层内应 active 优先（最近成功）: %q", got)
	}
}

// 旧档案兼容：无 mode 字段 → 按 tcpip 处理（排序/首选不变）。
func TestBestAddrLegacyNoModeIsTcpip(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})
	if got := s.BestAddr("a743e1df"); got != "192.168.31.162:5555" {
		t.Fatalf("旧档案地址应可用: %q", got)
	}
	e, _ := s.Entry("a743e1df")
	if e.Wireless != ModeTcpip {
		t.Fatalf("旧档案同步后 wireless 应回填 tcpip: %+v", e.Wireless)
	}
}

// MatchMdnsModes：tls 服务实例名 adb-<serial>-XXXXXX 剥前后缀命中 serial →
// 新 tls 地址归并入档（mode=tls）+ wireless=tls + tlsGuid 记录。
func TestMatchMdnsModesTlsGuidMerge(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	matched := s.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:33895", Mode: discovery.MdnsModeTls},
	})
	if len(matched) != 1 || matched[0].Addr != "192.168.31.99:33895" || matched[0].Mode != ModeTls {
		t.Fatalf("tls 服务应匹配为 tls 候选: %+v", matched)
	}
	e, _ := s.Entry("a743e1df")
	if e.Wireless != ModeTls {
		t.Fatalf("tls 服务命中后 wireless 应记 tls: %+v", e)
	}
	if e.TlsGuid != "adb-a743e1df-Ab12Cd" {
		t.Fatalf("tlsGuid 应记录实例名: %+v", e)
	}
	var got *AddrEntry
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.99:33895" {
			got = &e.Addrs[i]
		}
	}
	if got == nil || got.Mode != ModeTls || got.State != AddrStateActive {
		t.Fatalf("tls 地址应入档 mode=tls active: %+v", e.Addrs)
	}
	// 旧签名 MatchMdns 兼容：返回地址列表
	if list := s.MatchMdns([]MdnsMatch{
		{Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:33895", Mode: discovery.MdnsModeTls},
	}); len(list) != 1 || list[0] != "192.168.31.99:33895" {
		t.Fatalf("旧 MatchMdns 签名应兼容: %v", list)
	}
	// tlsGuid 已知 → 换端口重播（同 guid 新端口）仍识别为已知设备
	if !s.TlsGuidKnown("adb-a743e1df-Ab12Cd") {
		t.Fatal("TlsGuidKnown 应识别已入档 guid")
	}
	matched2 := s.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:41234", Mode: discovery.MdnsModeTls},
	})
	if len(matched2) != 1 {
		t.Fatalf("同 guid 换端口应仍匹配（端口变化场景）: %+v", matched2)
	}
}

// MatchMdnsModes：经典 _adb._tcp（mode=tcpip）行为回归 + 形态回填。
func TestMatchMdnsModesTcpipRegression(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	matched := s.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-a743e1df", Addr: "192.168.31.183:5555", Mode: discovery.MdnsModeTcpip},
	})
	if len(matched) != 1 || matched[0].Addr != "192.168.31.183:5555" || matched[0].Mode != ModeTcpip {
		t.Fatalf("经典服务应匹配 tcpip 候选: %+v", matched)
	}
	e, _ := s.Entry("a743e1df")
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.183:5555" && e.Addrs[i].Mode != ModeTcpip {
			t.Fatalf("经典地址应记 tcpip: %+v", e.Addrs[i])
		}
	}
	// tcpip 观察不覆盖既有 tls（两形态并存，tls 优先）
	s.mu.Lock()
	s.data.Devices["Xiaomi Pad 8 Pro"].Wireless = ModeTls
	s.mu.Unlock()
	s.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-a743e1df", Addr: "192.168.31.184:5555", Mode: discovery.MdnsModeTcpip},
	})
	e, _ = s.Entry("a743e1df")
	if e.Wireless != ModeTls {
		t.Fatalf("tcpip 观察不应覆盖 tls 形态: %+v", e.Wireless)
	}
}

// tls 服务实例名解析：adb-<serial>-6位后缀 → serial；异常输入防御。
func TestTlsServiceIdentity(t *testing.T) {
	cases := map[string]string{
		"adb-a743e1df-Ab12Cd":    "a743e1df",
		"adb-R58T00WA0YM-0x9zQ2": "R58T00WA0YM",
		"a743e1df":               "a743e1df",         // 无前缀/后缀（旧 adb 输出兼容）
		"adb-ABCDEF0123456789":   "ABCDEF0123456789", // 16 位随机 identity（无后缀）
		"":                       "",
	}
	for in, want := range cases {
		if got := TlsServiceIdentity(in); got != want {
			t.Errorf("TlsServiceIdentity(%q) = %q, want %q", in, got, want)
		}
	}
}

// PairArchive：配对成功入档（mode=tls + wireless=tls + serials + tlsGuid），
// 落盘后重载仍在（write-back 验证）。
func TestPairArchiveWritesBackTls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	s := NewProfileStore(path)
	_ = s.Load()

	s.PairArchive("Xiaomi Pad 8 Pro", "a743e1df", "192.168.31.99:33895",
		"adb-a743e1df-Ab12Cd", "Xiaomi Pad 8 Pro", "25091RP04C")

	e, ok := s.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatalf("PairArchive 应新建 identity 档案: %v", s.Entries())
	}
	if e.Wireless != ModeTls || e.TlsGuid != "adb-a743e1df-Ab12Cd" {
		t.Fatalf("入档形态错误: %+v", e)
	}
	if !contains(e.Serials, "a743e1df") {
		t.Fatalf("serials 未累积: %+v", e.Serials)
	}
	found := false
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.99:33895" && e.Addrs[i].Mode == ModeTls &&
			e.Addrs[i].State == AddrStateActive {
			found = true
		}
	}
	if !found {
		t.Fatalf("tls 地址未入档 active: %+v", e.Addrs)
	}

	// 落盘重载：字段保留（JSON 序列化 round-trip）
	s2 := NewProfileStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	e2, ok := s2.Entry("Xiaomi Pad 8 Pro")
	if !ok || e2.Wireless != ModeTls || e2.TlsGuid != "adb-a743e1df-Ab12Cd" ||
		len(e2.Addrs) != 1 || e2.Addrs[0].Mode != ModeTls {
		t.Fatalf("落盘重载后 tls 字段丢失: %+v", e2)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"wireless": "tls"`) ||
		!strings.Contains(string(b), `"mode": "tls"`) ||
		!strings.Contains(string(b), `"tlsGuid"`) {
		t.Fatalf("profiles.json 未含 tls 形态字段:\n%s", b)
	}
}

// PairArchive 归并：已有 5555 档案（旧键/无市场名）配对接入 → 归入同 serial 档案，
// 不分裂（wireless 由 tcpip → tls；tls 优先观察）。
func TestPairArchiveMergesIntoExistingEntry(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	s.PairArchive("", "a743e1df", "192.168.31.99:33895", "adb-a743e1df-Ab12Cd", "Xiaomi Pad 8 Pro", "25091RP04C")
	entries := s.Entries()
	if len(entries) != 1 {
		t.Fatalf("配对接入不应分裂档案: %v", entries)
	}
	e := entries["Xiaomi Pad 8 Pro"]
	if e.Wireless != ModeTls {
		t.Fatalf("wireless 应为 tls: %+v", e)
	}
}

// OfflineCandidateAddrs：候选按 TLS 优先分层（tls 层在前）；
// gui15 健康过滤下历史失败地址（fail>=2）不参与（另行由 gui15 测试覆盖）。
func TestOfflineCandidateAddrsTlsFirst(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.mu.Lock()
	s.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.99:5555", State: AddrStateActive, LastOk: 200},
			{Addr: "192.168.31.99:33895", State: AddrStateActive, Fail: 0, LastOk: 100, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	}
	s.mu.Unlock()

	// gui52：active 地址=在线证据 → 无离线候选
	if got := s.OfflineCandidateAddrs(nil); len(got) != 0 {
		t.Fatalf("active 地址=在线证据，不应有离线候选: %+v", got)
	}
	// 全 stale → 离线候选（TLS 层优先）
	if !s.MarkAllAddrsStale("Xiaomi Pad 8 Pro") {
		t.Fatal("MarkAllAddrsStale 应有改动")
	}
	got := s.OfflineCandidateAddrs(nil)
	list := got["Xiaomi Pad 8 Pro"]
	if len(list) != 2 || list[0].Addr != "192.168.31.99:33895" || list[1].Addr != "192.168.31.99:5555" {
		t.Fatalf("离线候选应 TLS 优先: %+v", list)
	}
	// 旧签名兼容
	old := s.OfflineCandidates(nil)
	if len(old["Xiaomi Pad 8 Pro"]) != 2 {
		t.Fatalf("旧 OfflineCandidates 应兼容: %v", old)
	}
}

// AddrSuccessWithMode：成功回填形态 + wireless 更新（TLS 优先观察）。
func TestAddrSuccessWithModeBackfillsTls(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})
	s.AddrSuccessWithMode("a743e1df", "192.168.31.99:33895", ModeTls)
	e, _ := s.Entry("a743e1df")
	var got *AddrEntry
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.99:33895" {
			got = &e.Addrs[i]
		}
	}
	if got == nil || got.Mode != ModeTls {
		t.Fatalf("成功后地址应记 mode=tls: %+v", e.Addrs)
	}
	if e.Wireless != ModeTls {
		t.Fatalf("tls 成功后 wireless 应更新: %+v", e)
	}
	if s.AddrMode("192.168.31.99:33895") != ModeTls {
		t.Fatalf("AddrMode 应可查形态")
	}
	if s.AddrMode("unknown:1") != "" {
		t.Fatalf("未知地址形态应为空")
	}
}

// 形态字段 JSON 序列化（frontend 契约）。
func TestDeviceEntryTlsJsonTags(t *testing.T) {
	e := DeviceEntry{Wireless: ModeTls, TlsGuid: "adb-a743e1df-Ab12Cd", Serials: []string{}, Addrs: []AddrEntry{}}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"wireless":"tls"`) || !strings.Contains(s, `"tlsGuid":"adb-a743e1df-Ab12Cd"`) {
		t.Fatalf("JSON 字段缺失: %s", s)
	}
	var back DeviceEntry
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Wireless != ModeTls || back.TlsGuid != "adb-a743e1df-Ab12Cd" {
		t.Fatalf("round-trip 失败: %+v", back)
	}
}
