package app

// gui55：配对学习残缺档案 / mDNS 广播认不了亲 / 探测 IP 误认 三件联合问题的回归测试。
//
// 三个修复点：
//   ① 配对学习闭环：短号多来源（mDNS 服务名 _adb-tls-connect / _adb._tcp + get-serialno
//      直读）+ 重试；学不到 → 明确报错不入档；遮罩放行 = device 稳定 且 名称已入档。
//   ② mDNS 广播写档先找"对应的设备"：短号命中 → 写档（原行为）；不中 → 同 IP 档案
//      认领 + 短号自举回填；都不中 → 待配对卡。
//   ③ 纯探测（冷启动搜索 / 缺席验尸 / 静默问询 / 遮罩 probe）写 active 前验身：
//      直读设备序列号与档案身份比对，对不上不写（DHCP 复用误认）。
//
// 全部使用脱敏占位值（TEST0001/TEST0002、192.0.2.x）。

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// gui55SerialByAddr：按地址给出"设备自报短号"的测试 double（生产=adb get-serialno）。
// 地址未列出时返回 def（空=读不到 → 验身无据 → 不写档）。
func gui55SerialByAddr(m map[string]string, def string) func(ctx context.Context, addr string) (string, error) {
	return func(ctx context.Context, addr string) (string, error) {
		if s, ok := m[addr]; ok {
			return s, nil
		}
		return def, nil
	}
}

// ---------------------------------------------------------------- ② mDNS 按 IP 认领

// 经典 _adb._tcp 广播（K80 场景）：档案只有一条旧 TLS 地址、serials/tlsGuid 全空
// → 同 IP 广播被认领：写新地址 active + 短号回填（自举，之后短号直认）。
func TestGui55MdnsClaimByIPClassicBackfillsSerial(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:39377", State: AddrStateStale, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})

	got := a.profiles.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-TEST0001", Addr: "192.0.2.159:5555", Mode: discovery.MdnsModeTcpip},
	})
	if len(got) != 1 || got[0].Addr != "192.0.2.159:5555" || got[0].Mode != ModeTcpip {
		t.Fatalf("经典广播应按 IP 认领为候选: %+v", got)
	}
	e, ok := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if !ok {
		t.Fatal("主档案应保留")
	}
	if !contains(e.Serials, "TEST0001") {
		t.Fatalf("短号应回填进档案（自举）: %+v", e.Serials)
	}
	ae := gui24FindAddr(e, "192.0.2.159:5555")
	if ae == nil || ae.State != AddrStateActive {
		t.Fatalf("认领地址应为 active: %+v", e.Addrs)
	}
	if _, ok := a.profiles.Entries()["192.0.2.159:5555"]; ok {
		t.Fatalf("不得新建 IP:port 键档案: %v", a.profiles.Entries())
	}

	// 第二次广播（同一短号）→ 短号直认（自举后不再依赖 IP）。
	got2 := a.profiles.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-TEST0001", Addr: "192.0.2.160:5555", Mode: discovery.MdnsModeTcpip},
	})
	if len(got2) != 1 || got2[0].Addr != "192.0.2.160:5555" {
		t.Fatalf("短号直认应命中新 IP: %+v", got2)
	}
	e, _ = a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if gui24FindAddr(e, "192.0.2.160:5555") == nil {
		t.Fatalf("新 IP 应写入同一档案: %+v", e.Addrs)
	}
}

// TLS 广播认领：同时记 tlsGuid（端口变化后仍可匹配本机）+ 无线形态 tls。
func TestGui55MdnsClaimByIPTlsBackfillsGuid(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:39377", State: AddrStateStale, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	got := a.profiles.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-TEST0001-KWqpio", Addr: "192.0.2.159:41999", Mode: discovery.MdnsModeTls},
	})
	if len(got) != 1 || got[0].Mode != ModeTls {
		t.Fatalf("TLS 广播应认领为 tls 候选: %+v", got)
	}
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if !contains(e.Serials, "TEST0001") || e.TlsGuid != "adb-TEST0001-KWqpio" {
		t.Fatalf("短号/tlsGuid 应回填: serials=%v guid=%q", e.Serials, e.TlsGuid)
	}
	if e.Wireless != ModeTls {
		t.Fatalf("无线形态应为 tls: %q", e.Wireless)
	}
	ae := gui24FindAddr(e, "192.0.2.159:41999")
	if ae == nil || ae.State != AddrStateActive || ae.Mode != ModeTls {
		t.Fatalf("认领地址应为 active/tls: %+v", e.Addrs)
	}
}

// 短号冲突（DHCP 复用：B 设备占了 A 档案的 IP）→ 拒绝认领：不写档、不出候选。
func TestGui55MdnsClaimRefusedOnSerialConflict(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"TEST0002"},
		TlsGuid:    "adb-TEST0002-KWqpio",
		// 档案记的是该 IP 上的旧 TLS 端口；广播来自同 IP 的 5555（换端口/换设备）
		Addrs:    []AddrEntry{{Addr: "192.0.2.242:40725", State: AddrStateStale, Mode: ModeTls}},
		Profiles: DefaultProfile(),
	})
	got := a.profiles.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-TEST0001", Addr: "192.0.2.242:5555", Mode: discovery.MdnsModeTcpip},
	})
	if len(got) != 0 {
		t.Fatalf("短号冲突时不得认领: %+v", got)
	}
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "Xiaomi Pad 8 Pro"))
	if contains(e.Serials, "TEST0001") {
		t.Fatalf("冲突短号不得混入档案: %+v", e.Serials)
	}
	if ae := gui24FindAddr(e, "192.0.2.242:5555"); ae != nil {
		t.Fatalf("冲突短号的地址不得写入档案: %+v", e.Addrs)
	}
	if ae := gui24FindAddr(e, "192.0.2.242:40725"); ae == nil || ae.State != AddrStateStale {
		t.Fatalf("原地址应保持 stale: %+v", e.Addrs)
	}
}

// 认领候选的待配对卡语义：mdnsServiceIdentity/buildPending 不把可认领设备当"真新设备"。
func TestGui55ClaimableDeviceIsNotPending(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:39377", State: AddrStateStale, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	svc := discovery.MdnsService{Type: "_adb._tcp", Name: "adb-TEST0001", Addr: "192.0.2.159:5555", Mode: discovery.MdnsModeTcpip}
	if id := a.mdnsServiceIdentity(&svc); id != "REDMI K80" {
		t.Fatalf("同 IP 残缺档案应被解析为可认领身份: %q", id)
	}
	a.mdnsMu.Lock()
	a.mdns = []discovery.MdnsService{svc}
	a.mdnsMu.Unlock()
	a.buildPending(nil)
	if len(a.pending) != 0 {
		t.Fatalf("可认领设备不应出现在待配对卡: %+v", a.pending)
	}
}

// 无线 transport 下 `adb get-serialno` 返回的是地址串（实测 K80）——必须丢弃，
// 否则验身会把本机设备误判成陌生设备；ro.serialno 才是设备自报的真序列号。
func TestGui55WirelessGetSerialNoAddressIgnored(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"TEST0001"},
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:5555", State: AddrStateActive, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		if prop == "ro.serialno" {
			return "TEST0001", nil
		}
		return "", nil
	}
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return addr, nil // 无线 transport 的 get-serialno 就是地址串
	}
	got := a.readDeviceSerials(context.Background(), "192.0.2.159:5555")
	if len(got) != 1 || got[0] != "TEST0001" {
		t.Fatalf("地址串必须被丢弃、只留 ro.serialno: %v", got)
	}
	okv, serial := a.probeOwnerOK(context.Background(), "REDMI K80", "192.0.2.159:5555")
	if !okv || serial != "TEST0001" {
		t.Fatalf("本机设备应验身通过: ok=%v serial=%q", okv, serial)
	}
}

// 60s 快照对账（adb mdns services 来源）也要走事件管线写档：自建监听在防火墙下
// 收不到组播时，这是"广播认不了亲"的唯一兜底路径。
func TestGui55ReconcileSnapshotClaimsByIP(t *testing.T) {
	a, _ := newWirelessApp()
	a.disc.TcpProbeFn = func(context.Context, string) bool { return true }
	a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) { return "connected to " + addr, nil }
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:39377", State: AddrStateStale, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	a.mu.Lock()
	a.adbOK = true
	a.mu.Unlock()
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return []discovery.MdnsService{{
			Type: "_adb._tcp", Name: "adb-TEST0001",
			Addr: "192.0.2.159:5555", Mode: discovery.MdnsModeTcpip,
		}}, nil
	}
	a.reconcileMdnsSnapshot(context.Background())
	e, ok := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if !ok {
		t.Fatal("档案应保留")
	}
	if !contains(e.Serials, "TEST0001") {
		t.Fatalf("对账应把广播短号补进档案: %+v", e.Serials)
	}
	if ae := gui24FindAddr(e, "192.0.2.159:5555"); ae == nil || ae.State != AddrStateActive {
		t.Fatalf("对账应写入广播地址 active: %+v", e.Addrs)
	}
}

// 现场实锤（K80 配对 00:03）：配对服务（_adb-tls-pairing._tcp）的端口被 IP 级认领
// 误写进档案（mode=pairing 的 active 条目）——配对端点不是连接地址，必须整类跳过。
func TestGui55PairingServiceNeverArchived(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:5555", State: AddrStateActive, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	got := a.profiles.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-TEST0001-KWqpio", Addr: "192.0.2.159:36405", Mode: discovery.MdnsModePairing},
	})
	if len(got) != 0 {
		t.Fatalf("配对服务不得作为候选: %+v", got)
	}
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if gui24FindAddr(e, "192.0.2.159:36405") != nil {
		t.Fatalf("配对端点不得写进档案: %+v", e.Addrs)
	}
	// 自愈：误入档的历史条目在 normalize（Load 路径）时被清掉
	e.Addrs = append(e.Addrs, AddrEntry{Addr: "192.0.2.159:36405", State: AddrStateActive, Mode: discovery.MdnsModePairing})
	gui15Seed(a.profiles, "REDMI K80", &e)
	a.profiles.mu.Lock()
	changed := a.profiles.normalizeLocked()
	a.profiles.mu.Unlock()
	if !changed {
		t.Fatal("normalize 应清理 mode=pairing 条目")
	}
	e2, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if gui24FindAddr(e2, "192.0.2.159:36405") != nil {
		t.Fatalf("normalize 后不得残留配对端点: %+v", e2.Addrs)
	}
}

// 现场实锤（K80 23:53）：设备流已把广播地址写进档案（active），但档案没有短号
// ——"地址已在档"分支必须按广播自称短号自举补学，否则残缺档案永远补不上。
func TestGui55MatchMdnsLearnsSerialWhenAddrAlreadyArchived(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:5555", State: AddrStateActive, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	got := a.profiles.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-TEST0001", Addr: "192.0.2.159:5555", Mode: discovery.MdnsModeTcpip},
	})
	if len(got) != 1 {
		t.Fatalf("地址已在档仍应作为候选: %+v", got)
	}
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if !contains(e.Serials, "TEST0001") {
		t.Fatalf("应按广播自举补学短号: %+v", e.Serials)
	}
}

// 同一分支的反面：地址在档但广播自称别的短号（DHCP 复用）→ 不补学、不复活。
func TestGui55MatchMdnsAddrConflictNotRevived(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"TEST0002"},
		Addrs:      []AddrEntry{{Addr: "192.0.2.242:5555", State: AddrStateStale, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	got := a.profiles.MatchMdnsModes([]MdnsMatch{
		{Name: "adb-TEST0001", Addr: "192.0.2.242:5555", Mode: discovery.MdnsModeTcpip},
	})
	if len(got) != 0 {
		t.Fatalf("短号冲突不得作为候选（不得复活）: %+v", got)
	}
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "Xiaomi Pad 8 Pro"))
	if contains(e.Serials, "TEST0001") {
		t.Fatalf("冲突短号不得混入档案: %+v", e.Serials)
	}
	if ae := gui24FindAddr(e, "192.0.2.242:5555"); ae == nil || ae.State != AddrStateStale {
		t.Fatalf("冲突地址不得被复活: %+v", e.Addrs)
	}
}

// 现场实锤（2026-09-17 23:49 K80）：事件链已把广播写进 a.mdns、但没走写档
// （对账 diff added=0）——对账必须仍按全量快照补写，否则残缺档案永远补不上。
func TestGui55ReconcileWritesEvenWhenDiffEmpty(t *testing.T) {
	a, _ := newWirelessApp()
	a.disc.TcpProbeFn = func(context.Context, string) bool { return true }
	a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) { return "connected to " + addr, nil }
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:39377", State: AddrStateStale, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	a.mu.Lock()
	a.adbOK = true
	a.mu.Unlock()
	svc := discovery.MdnsService{Type: "_adb._tcp", Name: "adb-TEST0001",
		Addr: "192.0.2.159:5555", Mode: discovery.MdnsModeTcpip}
	// 模拟事件链：快照已更新（含该广播），但写档路径被吞（added 为空）。
	a.mdnsMu.Lock()
	a.mdns = []discovery.MdnsService{svc}
	a.mdnsFirstDone = true
	a.mdnsMu.Unlock()
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return []discovery.MdnsService{svc}, nil
	}
	a.reconcileMdnsSnapshot(context.Background())
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if !contains(e.Serials, "TEST0001") {
		t.Fatalf("diff 为空也必须按全量快照补写短号: %+v", e.Serials)
	}
	if ae := gui24FindAddr(e, "192.0.2.159:5555"); ae == nil || ae.State != AddrStateActive {
		t.Fatalf("应写入广播地址 active: %+v", e.Addrs)
	}
}

// ---------------------------------------------------------------- ① 配对学习闭环

// 配对成功但 mDNS 完全不给服务名 → get-serialno 直读保底，档案含短号 + 地址。
func TestGui55PairLearnsSerialViaGetSerialNo(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		if prop == "ro.product.marketname" {
			return "REDMI K80", nil
		}
		return "", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil
	}
	calls := 0
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		calls++
		return "TEST0001", nil
	}

	if err := a.PairConnect("", "192.0.2.159", "37033", "41234", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseSuccess)
	if st.Device == nil || st.Device.Serial != "192.0.2.159:41234" {
		t.Fatalf("配对成功态错误: %+v", st)
	}
	if calls == 0 {
		t.Fatal("应走 get-serialno 直读保底")
	}
	e, ok := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	if !ok {
		t.Fatalf("档案应入档: %v", a.profiles.Entries())
	}
	if !contains(e.Serials, "TEST0001") {
		t.Fatalf("档案必须含短号: %+v", e.Serials)
	}
	if ae := gui24FindAddr(e, "192.0.2.159:41234"); ae == nil || ae.State != AddrStateActive {
		t.Fatalf("档案必须含配对地址: %+v", e.Addrs)
	}
}

// 第一次扫描还没有广播、第二次才出现（典型配对时序）→ 重试必须学到。
func TestGui55PairLearnsSerialByMdnsRetry(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "", errors.New("getprop unavailable")
	}
	scans := 0
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		scans++
		if scans == 1 {
			return nil, nil // 配对刚成功：手机还没开始广播
		}
		return []discovery.MdnsService{{
			Type: "_adb-tls-connect._tcp", Name: "adb-TEST0001-KWqpio",
			Addr: "192.0.2.159:41234", Mode: discovery.MdnsModeTls,
		}}, nil
	}
	// 最硬来源也拿不到：只有重试 mDNS 才能学到。
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "", errors.New("get-serialno unavailable")
	}

	if err := a.PairConnect("", "192.0.2.159", "37033", "41234", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)
	if scans < 2 {
		t.Fatalf("服务名解析应重试（scans=%d）", scans)
	}
	e, ok := a.profiles.Entry(fixtureArchiveKey(a.profiles, "TEST0001"))
	if !ok {
		t.Fatalf("应以短号建档: %v", a.profiles.Entries())
	}
	if e.TlsGuid != "adb-TEST0001-KWqpio" {
		t.Fatalf("guid 应入档: %q", e.TlsGuid)
	}
	if ae := gui24FindAddr(e, "192.0.2.159:41234"); ae == nil || ae.State != AddrStateActive {
		t.Fatalf("地址应入档 active: %+v", e.Addrs)
	}
}

// 遮罩放行联合判据：device 稳定但名称未入档 → 不放行；名称学到后才放行。
func TestGui55PairShieldWaitsForName(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:41234", State: AddrStateActive, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "", errors.New("get-serialno unavailable") // 补学也拿不到 → 不放行
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil
	}
	a.pairShieldStart("REDMI K80", "192.0.2.159")

	dev := adb.Device{Serial: "192.0.2.159:41234", State: "device", ConnType: "wifi"}
	a.pairStabilityUpdate([]adb.Device{dev})
	// 2s 稳定到点：名称未入档 → 遮罩必须保持。
	time.Sleep(pairShieldStableDuration + 700*time.Millisecond)
	a.teachMu.Lock()
	_, active := a.pairing["REDMI K80"]
	a.teachMu.Unlock()
	if !active {
		t.Fatal("名称未入档时不得放行遮罩（联合判据）")
	}

	// 名称学到（模拟补学成功）→ 下一拍复查放行。
	if !a.profiles.LearnIdentity("REDMI K80", "TEST0001", "") {
		t.Fatal("补学应写入短号")
	}
	waitFor(t, 4*time.Second, func() bool {
		a.teachMu.Lock()
		_, on := a.pairing["REDMI K80"]
		a.teachMu.Unlock()
		return !on
	}, "名称入档后应放行遮罩")
}

// 10s 兜底：仍学不到名称 → 明确报错（可见），不静默留残缺档案。
func TestGui55PairShieldFallbackReportsError(t *testing.T) {
	old := pairShieldTimeout
	pairShieldTimeout = 300 * time.Millisecond
	defer func() { pairShieldTimeout = old }()

	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:41234", State: AddrStateActive, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "", errors.New("get-serialno unavailable")
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil
	}
	a.mu.Lock()
	a.pair = &PairStatus{Phase: PairPhaseSuccess, Steps: []PairStep{{Name: PairStepPair, OK: true}}}
	a.mu.Unlock()

	a.pairShieldStart("REDMI K80", "192.0.2.159")
	waitFor(t, 3*time.Second, func() bool {
		st := a.Snapshot().PairStatus
		return st != nil && st.Phase == PairPhaseFailed
	}, "兜底学不到名称应报错")
	st := a.Snapshot().PairStatus
	if st.ErrCode != PairErrSerial || st.ErrText == "" {
		t.Fatalf("应分类 serial 且给出可见文案: %+v", st)
	}
}

// ---------------------------------------------------------------- ③ 纯探测验身

// 缺席验尸：探测通但设备自报短号与档案不符（IP 被别的设备复用）→ 不写 active。
func TestGui55ProbeVerifyRejectsForeignDevice(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"TEST0002"},
		Addrs: []AddrEntry{
			{Addr: "192.0.2.242:5555", State: AddrStateStale, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	// 纯探测（静默问询）：IP 上有设备应答（别的设备漂移到平板的旧 IP）。
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool { return true }
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "TEST0001", nil // 设备自报：不是平板
	}

	a.startMdnsProbe("Xiaomi Pad 8 Pro", "192.0.2.242:5555", mdnsProbeKindIdle)
	time.Sleep(200 * time.Millisecond) // 等探测 goroutine 落定
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "Xiaomi Pad 8 Pro"))
	ae := gui24FindAddr(e, "192.0.2.242:5555")
	if ae == nil || ae.State != AddrStateStale {
		t.Fatalf("验身不符不得写活（平板不得莫名在线）: %+v", ae)
	}
	if contains(e.Serials, "TEST0001") {
		t.Fatalf("陌生设备短号不得混入档案: %+v", e.Serials)
	}
}

// 同一档案设备自报短号一致 → 照常保持 active（回归：真在线不受影响）。
func TestGui55ProbeVerifyAcceptsOwnDevice(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"TEST0002"},
		Addrs: []AddrEntry{
			{Addr: "192.0.2.242:44125", State: AddrStateStale, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool { return true }
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "TEST0002", nil
	}
	a.startMdnsProbe("Xiaomi Pad 8 Pro", "192.0.2.242:44125", mdnsProbeKindIdle)
	waitFor(t, 2*time.Second, func() bool {
		e, ok := a.profiles.Entry(fixtureArchiveKey(a.profiles, "Xiaomi Pad 8 Pro"))
		if !ok {
			return false
		}
		ae := gui24FindAddr(e, "192.0.2.242:44125")
		return ae != nil && ae.State == AddrStateActive
	}, "验身一致应翻回 active")
}

// 冷启动全量搜索：无短号可比的残缺档案 + 探测通 → 也不写 active（等广播自举）。
func TestGui55ColdSearchSkipsUnverifiable(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:39377", State: AddrStateStale, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		return "connected to " + addr, nil
	}
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "TEST0001", nil // 读到了设备短号，但档案里没有可比的短号
	}
	a.coldSearch5555(context.Background())
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	ae := gui24FindAddr(e, "192.0.2.159:39377")
	if ae == nil || ae.State != AddrStateStale {
		t.Fatalf("残缺档案无短号可比时不得仅凭 IP 写 active: %+v", ae)
	}
	if contains(e.Serials, "TEST0001") {
		t.Fatalf("探测路径不得替档案认领短号（认领只走 mDNS 广播）: %+v", e.Serials)
	}
}

// 冷启动全量搜索：档案短号一致 → active（回归）。
func TestGui55ColdSearchVerifiesOwnDevice(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"TEST0001"},
		Addrs:      []AddrEntry{{Addr: "192.0.2.159:5555", State: AddrStateStale, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		return "connected to " + addr, nil
	}
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) {
		return "TEST0001", nil
	}
	a.coldSearch5555(context.Background())
	e, _ := a.profiles.Entry(fixtureArchiveKey(a.profiles, "REDMI K80"))
	ae := gui24FindAddr(e, "192.0.2.159:5555")
	if ae == nil || ae.State != AddrStateActive {
		t.Fatalf("验身一致应写 active: %+v", ae)
	}
}
