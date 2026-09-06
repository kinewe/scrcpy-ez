package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui21：StartCast 无线地址选择 mDNS 广播优先（投屏与探测同一决策） ---

// k80Snap 复现实测现场：K80 开无线调试，mDNS 广播 TLS 端口 45005（未入档）
// + 经典 _adb._tcp 5555；档案只有 5555 tcpip active（BestAddr 会落 5555）。
func k80Snap() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-24117RK2CC-Kk80Xx", Addr: "192.168.31.197:45005", Mode: discovery.MdnsModeTls},
		{Type: "_adb._tcp", Name: "adb-24117RK2CC", Addr: "192.168.31.197:5555", Mode: discovery.MdnsModeTcpip},
	}
}

// seedK80Profile 播种 K80 档案：serial + 5555 tcpip active（45005 不在此——未入档）。
func seedK80Profile(a *App) {
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "24117RK2CC", State: "device", ConnType: "usb", Marketname: "Redmi K80",
			Wireless: "192.168.31.197:5555"},
	})
}

// seedK80WifiCard 设备列表放 K80 无线卡（serial=IP:5555，ConnType=wifi）。
func seedK80WifiCard(a *App) {
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "Redmi K80",
			Marketname: "Redmi K80", Identity: "Redmi K80"},
	}
	a.mu.Unlock()
}

// gui48-mdns4 档案化：TLS 45005 事件已写入档案 active → StartCast 直接选它。
func TestStartCastMdnsTlsBroadcastFirst(t *testing.T) {
	a, f := newWirelessApp()
	seedK80Profile(a)
	a.profiles.AddrSuccessWithMode("Redmi K80", "192.168.31.197:45005", ModeTls)
	seedK80WifiCard(a)

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:45005" {
		t.Fatalf("档案 active TLS 地址应作为主地址: %+v", p)
	}
	s := a.Snapshot()
	if !s.Cast.Tls {
		t.Fatalf("45005 连接应标 TLS: %+v", s.Cast)
	}
}

// 回归：mDNS 快照空（daemon 挂/设备哑巴）→ 档案候选验证后选 5555
// （gui32 验证链：候选 connect 验证通过才选用；gui12/gui15 原行为），不标 TLS。
func TestStartCastNoMdnsFallsBackToBestAddr(t *testing.T) {
	a, f := newWirelessApp()
	seedK80Profile(a)
	seedK80WifiCard(a) // 快照为空
	startVerifyAlwaysOK(a)

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("无广播时档案候选 5555 验证通过后应选用: %+v", p)
	}
	if s := a.Snapshot(); s.Cast.Tls {
		t.Fatalf("5555 明文连接不应标 TLS: %+v", s.Cast)
	}
}

// 回归：档案无 addrs（BestAddr 空）且无广播 → wifi 卡 serial 兜底。
func TestStartCastNoProfileAddrFallsBackToSerial(t *testing.T) {
	a, f := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "24117RK2CC", State: "device", ConnType: "usb", Marketname: "Redmi K80"},
	})
	seedK80WifiCard(a)

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("BestAddr 空时 wifi 卡应回退 serial: %+v", p)
	}
	if s := a.Snapshot(); s.Cast.Tls {
		t.Fatalf("5555 明文连接不应标 TLS: %+v", s.Cast)
	}
}

// gui48-mdns4：本机 tcpip 事件已写入档案 active（无 TLS active）→ StartCast
// 选该 5555；别家 TLS 不会串线（档案为唯一事实源）。
func TestStartCastMdnsTcpipBroadcastSecondTier(t *testing.T) {
	a, f := newWirelessApp()
	gui15Seed(a.profiles, "Redmi K80", &DeviceEntry{
		Marketname: "Redmi K80",
		Serials:    []string{"24117RK2CC"},
		Addrs:      []AddrEntry{{Addr: "192.168.31.88:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.88:5555", State: "device", ConnType: "wifi", Name: "Redmi K80",
			Marketname: "Redmi K80", Identity: "Redmi K80"},
	}
	a.mu.Unlock()

	if err := a.StartCast("192.168.31.88:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.88:5555" {
		t.Fatalf("档案 active tcpip 应作为主地址: %+v", p)
	}
	if s := a.Snapshot(); s.Cast.Tls {
		t.Fatalf("5555 明文不应标 TLS: %+v", s.Cast)
	}
}

// MdnsWirelessAddr 规则细节（与 MdnsAuthoritativeDevices 同归属规则）：
// tls 层优先、层内按出现序取首条；pairing/未解析地址不参与；tlsGuid 命中
// （serial 名对不上）仍属本机；地址已在档案的广播按规则①命中；无命中 → ""。
func TestMdnsWirelessAddrTiersAndRules(t *testing.T) {
	s := NewProfileStore("")
	s.SyncDevices([]adb.Device{
		{Serial: "24117RK2CC", State: "device", ConnType: "usb", Marketname: "Redmi K80",
			Wireless: "192.168.31.197:5555"},
	})
	svcs := []MdnsMatch{
		{Name: "adb-24117RK2CC", Addr: "192.168.31.88:5555", Mode: discovery.MdnsModeTcpip},
		{Name: "adb-24117RK2CC-Kk80Xx", Addr: "192.168.31.197:45005", Mode: discovery.MdnsModeTls},
		{Name: "adb-24117RK2CC-Kk80Xx", Addr: "192.168.31.197:45006", Mode: discovery.MdnsModeTls},
		{Name: "adb-24117RK2CC-Kk80Xx", Addr: "192.168.31.197:37033", Mode: discovery.MdnsModePairing},
		{Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.77:41234", Mode: discovery.MdnsModeTls},
		{Name: "adb-24117RK2CC", Addr: "", Mode: discovery.MdnsModeTcpip}, // 未解析 → 忽略
	}
	// tls 层优先于 tcpip，层内按出现序取首条（45005 在 45006 之前）
	if got := s.MdnsWirelessAddr("24117RK2CC", svcs); got != "192.168.31.197:45005" {
		t.Fatalf("tls 层首条应为 45005: %q", got)
	}
	// 无 tls → tcpip 广播（别家 tcpip 不串线）
	tcpOnly := []MdnsMatch{
		{Name: "adb-R58T00WA0YM", Addr: "192.168.31.77:5555", Mode: discovery.MdnsModeTcpip},
		{Name: "adb-24117RK2CC", Addr: "192.168.31.88:5555", Mode: discovery.MdnsModeTcpip},
	}
	if got := s.MdnsWirelessAddr("24117RK2CC", tcpOnly); got != "192.168.31.88:5555" {
		t.Fatalf("无 tls 时应取本机 tcpip 广播: %q", got)
	}
	// tlsGuid 命中（serial 名对不上——serial 不入档、仅 guid 入档）仍属本机
	s.PairArchive("", "", "192.168.31.77:41234", "adb-R58T00WA0YM-Xy9zQ2", "", "")
	guidSvc := []MdnsMatch{
		{Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.77:55534", Mode: discovery.MdnsModeTls},
	}
	if got := s.MdnsWirelessAddr("192.168.31.77:41234", guidSvc); got != "192.168.31.77:55534" {
		t.Fatalf("tlsGuid 命中应选该广播: %q", got)
	}
	// 无广播命中 → ""（调用方回退 BestAddr）
	if got := s.MdnsWirelessAddr("Redmi K80", nil); got != "" {
		t.Fatalf("无广播应返回空: %q", got)
	}
	// 规则①：地址已在档案的广播直接命中（实例名匹配不到也无妨）
	known := []MdnsMatch{
		{Name: "adb-unknown", Addr: "192.168.31.197:5555", Mode: discovery.MdnsModeTcpip},
	}
	if got := s.MdnsWirelessAddr("24117RK2CC", known); got != "192.168.31.197:5555" {
		t.Fatalf("地址已在档案的广播应命中: %q", got)
	}
	// 无档案 → ""（未建档设备走 BestAddr/serial 兜底，原行为）
	if got := s.MdnsWirelessAddr("nobody", svcs); got != "" {
		t.Fatalf("无档案应返回空: %q", got)
	}
}
