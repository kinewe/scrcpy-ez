package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui12：无线调试配对向导 + TLS 优先（app 层） ---

// newWirelessApp 构造注入 fake 配对操作的 App（不落盘/不跑真实 adb）。
func newWirelessApp() (*App, *fakeRunner) {
	a, f := newTestApp()
	return a, f
}

// waitPairPhase 等待配对状态到指定阶段（快照轮询；超时失败）。
func waitPairPhase(t *testing.T, a *App, want string) *PairStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := a.Snapshot().PairStatus
		if st != nil && st.Phase == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待配对阶段 %s 超时（当前 %+v）", want, st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// seedPending 按 mDNS 快照构建待配对卡并返回第一张（同时验证 buildPending 本身）。
func seedPending(t *testing.T, a *App, svcs []discovery.MdnsService) PendingDevice {
	t.Helper()
	a.mdnsMu.Lock()
	a.mdns = svcs
	a.mdnsMu.Unlock()
	a.buildPending(nil)
	if len(a.Snapshot().Pending) == 0 {
		t.Fatal("buildPending 应产出待配对卡")
	}
	return a.Snapshot().Pending[0]
}

func pairConnectSvcs() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:33895", Mode: discovery.MdnsModeTls},
		{Type: "_adb-tls-pairing._tcp", Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:37033", Mode: discovery.MdnsModePairing},
	}
}

// 配对成功全链路：mDNS 自动发现 → 只输配对码 → pair（配对端口自动从配对服务取）
// → connect（tls 端口）→ getprop 验证 → 入档 mode=tls + wireless=tls + serials
// + tlsGuid。gui52：成功尾段一次性学习 TLS+5555（并行探测）与设备短号；
// 待配对卡移除；结果带设备卡数据。
func TestPairConnectSuccessTlsArchive(t *testing.T) {
	a, _ := newWirelessApp()
	p := seedPending(t, a, pairConnectSvcs())

	var mu sync.Mutex
	var pairCalls []string
	var pairCode string
	var connCalls []string
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		mu.Lock()
		pairCalls = append(pairCalls, ip+":"+port)
		pairCode = code
		mu.Unlock()
		return "Successfully paired to 192.168.31.99:37033 [guid=adb-a743e1df-Ab12Cd]", nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		connCalls = append(connCalls, addr)
		mu.Unlock()
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		switch prop {
		case "ro.product.marketname":
			return "Xiaomi Pad 8 Pro", nil
		case "ro.product.manufacturer":
			return "Xiaomi", nil
		case "ro.product.model":
			return "25091RP04C", nil
		}
		return "", errors.New("unknown prop")
	}

	if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseSuccess)

	// pair 参数：配对端口自动从 _adb-tls-pairing 同 GUID 服务取
	mu.Lock()
	pairOK := len(pairCalls) == 1 && pairCalls[0] == "192.168.31.99:37033" && pairCode == "123456"
	mu.Unlock()
	if !pairOK {
		t.Fatalf("pair 调用参数错误: %v code=%q", pairCalls, pairCode)
	}
	// gui52-fix7：正式 connect（tls 33895）1 次；无线接入学习（TLS/5555 并行
	// 探测）已整体移除——无线地址权威=mDNS（MatchMdnsModes），由单测另行覆盖。
	// gui52-fix14：配对遮罩 connect 注入 +1（ip:5555）——共 2 次。
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(connCalls) == 2
	}, "正式 connect + 遮罩注入应完成（学习探测已移除）")
	mu.Lock()
	gotCalls := append([]string{}, connCalls...)
	mu.Unlock()
	if !contains(gotCalls, "192.168.31.99:33895") {
		t.Fatalf("正式 connect 应走 tls 端口: %v", gotCalls)
	}
	// 步骤：① 配对 ✓ ② 连接 ✓ ③ 完成 ✓
	if len(st.Steps) != 3 || !st.Steps[0].OK || !st.Steps[1].OK || !st.Steps[2].OK {
		t.Fatalf("步骤状态错误: %+v", st.Steps)
	}
	if st.Device == nil || st.Device.Marketname != "Xiaomi Pad 8 Pro" ||
		st.Device.Serial != "192.168.31.99:33895" || !st.Device.Tls || st.Device.WirelessForm != ModeTls {
		t.Fatalf("成功设备卡错误: %+v", st.Device)
	}
	// 档案 write-back：mode=tls + wireless=tls + serial + tlsGuid
	waitFor(t, 2*time.Second, func() bool {
		e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
		return ok && gui50Fix45EntryHasAddr(e, "192.168.31.99:33895", ModeTls)
	}, "TLS 应入档")
	e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if !ok || e.Wireless != ModeTls || e.TlsGuid != "adb-a743e1df-Ab12Cd" {
		t.Fatalf("档案形态未写回: %+v", e)
	}
	if !contains(e.Serials, "a743e1df") {
		t.Fatalf("真 serial 未入档: %+v", e.Serials)
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.99:33895" && e.Addrs[i].State != AddrStateActive {
			t.Fatalf("配对返回 TLS 地址应 active: %+v", e.Addrs[i])
		}
	}
	// 待配对卡已移除
	if len(a.Snapshot().Pending) != 0 {
		t.Fatalf("配对成功后待配对卡应移除: %+v", a.Snapshot().Pending)
	}
	// 形态标注可用（AddrMode 查询）
	if a.profiles.AddrMode("192.168.31.99:33895") != ModeTls {
		t.Fatal("AddrMode 应返回 tls")
	}
}

// 配对码错误：adb 输出 "Wrong password" → 分类码 pair-code（文案含 30 秒刷新提示）。
func TestPairConnectWrongCodeClassified(t *testing.T) {
	a, _ := newWirelessApp()
	p := seedPending(t, a, pairConnectSvcs())
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Failed: Wrong password or connection was dropped.", nil
	}
	if err := a.PairConnect(p.Key, "", "", "", "654321"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.ErrCode != PairErrCode || !strings.Contains(st.ErrText, "30 秒") {
		t.Fatalf("码错分类错误: %+v", st)
	}
}

// 配对端口缺失：无配对服务（快照+现场重扫都无）→ pair-port，不调用 pair。
func TestPairConnectPairPortMissing(t *testing.T) {
	a, _ := newWirelessApp()
	p := seedPending(t, a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:33895", Mode: discovery.MdnsModeTls},
	})
	called := false
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		called = true
		return "Successfully paired to x [guid=g]", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) { return nil, nil }
	if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.ErrCode != PairErrPort {
		t.Fatalf("应分类 pair-port: %+v", st)
	}
	if called {
		t.Fatal("配对端口缺失时不应调用 adb pair")
	}
}

// 连接端口缺失：手动场景（无卡）只给 ip+配对端口 → conn-port 分类码。
func TestPairConnectConnPortMissing(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) { return nil, nil }
	if err := a.PairConnect("", "192.168.31.9", "37033", "", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.ErrCode != PairErrConnPort {
		t.Fatalf("应分类 conn-port: %+v", st)
	}
}

// 手动场景全量（跨网段兜底）：devKey 空、ip/配对端口/连接端口全给 → 成功。
func TestPairConnectManualPortsFullFlow(t *testing.T) {
	a, _ := newWirelessApp()
	var pairArg string
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		pairArg = ip + ":" + port
		return "Successfully paired to " + ip + ":" + port + " [guid=g]", nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		if prop == "ro.product.marketname" {
			return "Redmi K80", nil
		}
		return "", nil
	}
	if err := a.PairConnect("", "10.0.0.8", "37033", "41234", "111222"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseSuccess)
	if pairArg != "10.0.0.8:37033" || st.Device == nil || st.Device.Serial != "10.0.0.8:41234" {
		t.Fatalf("手动配对流程错误: pairArg=%q %+v", pairArg, st)
	}
	e, ok := a.profiles.Entry("Redmi K80")
	if !ok || e.Wireless != ModeTls {
		t.Fatalf("手动配对也应入档 tls: %+v", e)
	}
}

// 配对超时：pairFn 阻塞至 ctx 到期 → timeout 分类码。
func TestPairConnectTimeoutClassified(t *testing.T) {
	old := pairTimeout
	pairTimeout = 300 * time.Millisecond
	defer func() { pairTimeout = old }()
	a, _ := newWirelessApp()
	p := seedPending(t, a, pairConnectSvcs())
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.ErrCode != PairErrTimeout {
		t.Fatalf("超时应分类 timeout: %+v", st)
	}
}

// 配对进行中防重入：第二次 PairConnect 拒绝（busy）。
func TestPairConnectBusyRejected(t *testing.T) {
	a, _ := newWirelessApp()
	p := seedPending(t, a, pairConnectSvcs())
	release := make(chan struct{})
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		select {
		case <-release:
			return "Successfully paired to x [guid=g]", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
		t.Fatal(err)
	}
	if err := a.PairConnect(p.Key, "", "", "", "654321"); err == nil || !strings.Contains(err.Error(), "配对进行中") {
		t.Fatalf("第二次应拒绝（busy）: %v", err)
	}
	close(release)
	waitPairPhase(t, a, PairPhaseFailed) // 第二次未消费；第一次继续（connect 缺 ops → 失败态）
	a.PairReset()
}

// 非法配对码：前端校验的后端兜底（6 位数字），不调用 pair。
func TestPairConnectInvalidCodeRejected(t *testing.T) {
	a, _ := newWirelessApp()
	called := false
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		called = true
		return "", nil
	}
	if err := a.PairConnect("", "192.168.31.9", "37033", "41234", "12ab"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.ErrCode != PairErrCode || called {
		t.Fatalf("非法码应拒绝且不调 pair: %+v", st)
	}
}

// 连接失败：connectFn 报错（模拟 Connector.ConnectOut 的输出判定语义）→ connect-failed 分类码。
func TestPairConnectConnectFailedClassified(t *testing.T) {
	a, _ := newWirelessApp()
	p := seedPending(t, a, pairConnectSvcs())
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to x [guid=g]", nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "cannot connect to " + addr + ": Connection refused",
			errors.New("connect 未成功: cannot connect to " + addr)
	}
	if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhaseFailed)
	if st.ErrCode != PairErrConnect || !strings.Contains(st.ErrText, "连接端口") {
		t.Fatalf("连接失败分类错误: %+v", st)
	}
}

// --- TLS 优先探测（runDiscovery 分层） ---

// 设备 mDNS 双广播在场（tls 连接服务 + 经典 _adb._tcp），gui23 广播优先 +
// 档案兜底：探测先试 tls 层（广播 41234 排前、档案 stale tls 33895 兜底其后），
// 全败才回退 5555 层（广播 5555 排前；档案 5555 已去重不重复）；回退成功时
// tls 地址不记失败。gui52：active=在线证据，离线候选必须先翻 stale。
func TestRunDiscoveryTlsPriorityFallback(t *testing.T) {
	a, _ := newWirelessApp()
	// 档案：serial + 档案 tls 地址 + 5555 地址（打 stale 后成为离线候选）
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.99:5555"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)
	if !a.profiles.MarkAllAddrsStale("Xiaomi Pad 8 Pro") {
		t.Fatal("MarkAllAddrsStale 应有改动")
	}

	var mu sync.Mutex
	var calls []string
	// 门闩：tls 层内广播 41234 先试、档案 33895 后兜底（确定性顺序断言）
	gate := make(chan struct{})
	var once sync.Once
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		if addr == "192.168.31.99:33895" {
			<-gate
		}
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.99:41234" {
			once.Do(func() { close(gate) })
		}
		if strings.HasSuffix(addr, ":5555") {
			return nil
		}
		return context.DeadlineExceeded
	}
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return []discovery.MdnsService{
			{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:41234", Mode: discovery.MdnsModeTls},
			{Type: "_adb._tcp", Name: "adb-a743e1df", Addr: "192.168.31.99:5555", Mode: discovery.MdnsModeTcpip},
		}, nil
	}

	a.runDiscovery(context.Background(), a.profiles.OfflineCandidateAddrs(nil))

	deadline := time.Now().Add(5 * time.Second)
	for {
		st := a.Snapshot().Discovery
		if st.Status == "found" && st.Found == "192.168.31.99:5555" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("回退 5555 应成功: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("应尝试 3 个地址（tls 层：广播 41234 + 档案 33895；5555 层：广播 5555）: %v", calls)
	}
	// tls 层内：广播 41234 排前先试，健康档案 tls 33895 兜底其后
	if calls[0] != "192.168.31.99:41234" {
		t.Fatalf("首个尝试应为 tls 广播地址: %v", calls)
	}
	if calls[1] != "192.168.31.99:33895" {
		t.Fatalf("tls 层内档案兜底地址应随后尝试: %v", calls)
	}
	// 层间串行：tls 层全败才回退 5555 层
	if calls[2] != "192.168.31.99:5555" {
		t.Fatalf("5555 应最后回退: %v", calls)
	}
	// 回退成功：tls 广播地址不判失败（未配对/端口过期 ≠ 离线），fail 不变
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	for i := range e.Addrs {
		if (e.Addrs[i].Addr == "192.168.31.99:33895" || e.Addrs[i].Addr == "192.168.31.99:41234") &&
			e.Addrs[i].Fail != 0 {
			t.Fatalf("回退成功时 tls 地址不应记失败: %+v", e.Addrs[i])
		}
	}
	// mDNS 新 tls 端口已归并入档案（mode=tls）
	e, _ = a.profiles.Entry("Xiaomi Pad 8 Pro")
	foundTls := false
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.99:41234" && e.Addrs[i].Mode == ModeTls {
			foundTls = true
		}
	}
	if !foundTls {
		t.Fatalf("mDNS 新 tls 端口未归并入档案: %+v", e.Addrs)
	}
}

// 全层失败：所有地址（含 tls）记一次失败并写 state=stale（保留既有全失败语义）。
func TestRunDiscoveryAllFailMarksTls(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.99:5555"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)
	// gui52：先翻 stale 才成为离线候选（active=在线证据不会触发探测）。
	if !a.profiles.MarkAllAddrsStale("Xiaomi Pad 8 Pro") {
		t.Fatal("MarkAllAddrsStale 应有改动")
	}
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return context.DeadlineExceeded }
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) { return nil, nil }

	a.runDiscovery(context.Background(), a.profiles.OfflineCandidateAddrs(nil))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if a.Snapshot().Discovery.Status == "notfound" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("全失败应 notfound")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	failSum := 0
	for i := range e.Addrs {
		failSum += e.Addrs[i].Fail
		if e.Addrs[i].State != AddrStateStale {
			t.Fatalf("全失败后各地址应 state=stale: %+v", e.Addrs)
		}
	}
	if failSum < 2 {
		t.Fatalf("全失败时各地址都应 fail++: %+v", e.Addrs)
	}
}

// --- StartCast TLS 优先 + 形态标注 ---

// 档案 tls 地址（active）优先于 5555 注入 SCEZ_ADDR；CastState.Tls=true。
// gui32：候选（tls 33895 → tcpip 5555）经验证链 fake 全部通过 → tls 先验证先选。
func TestStartCastInjectsTlsAddrFirst(t *testing.T) {
	a, f := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.99:5555"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.mu.Unlock()
	startVerifyAlwaysOK(a)

	if err := a.StartCast("192.168.31.99:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.99:33895" {
		t.Fatalf("SCEZ_ADDR 应注入 tls 地址: %+v", p)
	}
	s := a.Snapshot()
	if !s.Cast.Tls || s.Cast.Serial != "192.168.31.99:5555" {
		t.Fatalf("CastState.Tls 应为 true: %+v", s.Cast)
	}
}

// 只有 5555 地址 → 注入 5555；Tls=false（无 TLS 标签）。
func TestStartCastTcpipAddrNoTlsFlag(t *testing.T) {
	a, f := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.99:5555"},
	})
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.mu.Unlock()
	startVerifyAlwaysOK(a) // gui32 验证链：候选 5555 验证通过才选用

	if err := a.StartCast("192.168.31.99:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.99:5555" {
		t.Fatalf("无 tls 地址时应注入 5555: %+v", p)
	}
	if s := a.Snapshot(); s.Cast.Tls {
		t.Fatalf("5555 连接不应标 Tls: %+v", s.Cast)
	}
}

// --- 设备卡 TLS 标注 + 待配对卡构建 ---

// decorateTls：档案有 mode=tls 地址 / mDNS tls 服务命中 → d.Tls；形态字段透传。
// buildPending：已知设备（serial/tlsGuid 入档）跳过；未知 → 待配对卡（配对端口自动）。
func TestDecorateTlsAndBuildPending(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.99:5555"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.99:33895", ModeTls)
	svcs := []discovery.MdnsService{
		// 已知设备（serial 匹配）的 tls 服务（事件已写入档案 active）→ TLS 标识
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.99:33895", Mode: discovery.MdnsModeTls},
		// 未知设备 → 待配对卡（配对服务同 GUID）
		{Type: "_adb-tls-connect._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.77:41234", Mode: discovery.MdnsModeTls},
		{Type: "_adb-tls-pairing._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.77:37033", Mode: discovery.MdnsModePairing},
	}
	a.mdnsMu.Lock()
	a.mdns = svcs
	a.mdnsMu.Unlock()

	devs := []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.decorateTls(devs)
	if !devs[0].Tls {
		t.Fatalf("mDNS tls 服务命中的设备应有 TLS 标识: %+v", devs[0])
	}
	a.buildPending(devs)
	pend := a.Snapshot().Pending
	if len(pend) != 1 {
		t.Fatalf("应只有 1 张待配对卡（已知设备跳过）: %+v", pend)
	}
	if pend[0].Key != "192.168.31.77:41234" || pend[0].Name != "R58T00WA0YM" ||
		pend[0].IP != "192.168.31.77" || pend[0].PairPort != "37033" ||
		pend[0].Serial != "R58T00WA0YM" {
		t.Fatalf("待配对卡字段错误: %+v", pend[0])
	}
	// 已在线（tls addr）→ 不建待配对卡
	devs2 := []adb.Device{
		{Serial: "192.168.31.77:41234", State: "device", ConnType: "wifi"},
	}
	a.buildPending(devs2)
	if len(a.Snapshot().Pending) != 0 {
		t.Fatalf("在线设备不应有待配对卡: %+v", a.Snapshot().Pending)
	}
}

// 配对成功后 tlsGuid 入档 → 换端口重播同一 guid 不再出待配对卡（端口变化场景）。
func TestBuildPendingSkipsKnownTlsGuid(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.PairArchive("", "R58T00WA0YM", "192.168.31.77:41234",
		"adb-R58T00WA0YM-Xy9zQ2", "", "")
	svcs := []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.77:55534", Mode: discovery.MdnsModeTls},
	}
	a.mdnsMu.Lock()
	a.mdns = svcs
	a.mdnsMu.Unlock()
	a.buildPending(nil)
	if len(a.Snapshot().Pending) != 0 {
		t.Fatalf("tlsGuid 已入档的设备不应重复待配对: %+v", a.Snapshot().Pending)
	}
}

// 配对中状态透传快照（前端步骤渲染数据源）。
func TestPairStatusExposedInSnapshot(t *testing.T) {
	a, _ := newWirelessApp()
	p := seedPending(t, a, pairConnectSvcs())
	release := make(chan struct{})
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		<-release
		return "Successfully paired to x [guid=g]", nil
	}
	if err := a.PairConnect(p.Key, "", "", "", "123456"); err != nil {
		t.Fatal(err)
	}
	st := waitPairPhase(t, a, PairPhasePairing)
	if st.Phase != PairPhasePairing || len(st.Steps) != 1 || st.Steps[0].Name != PairStepPair {
		t.Fatalf("配对中状态错误: %+v", st)
	}
	close(release)
	waitPairPhase(t, a, PairPhaseFailed) // 后续 connect ops 无注入 → 失败态
	// PairReset 清空
	a.PairReset()
	if a.Snapshot().PairStatus != nil {
		t.Fatal("PairReset 应清空状态")
	}
}
