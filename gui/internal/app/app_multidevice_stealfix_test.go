package app

import (
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- 多设备互抢根治：弹窗 identity 判据 + 市场名 + StopCast 杀服门 ---

// seedProfiles 直接给内存档案库种入身份条目（复现 16:53 实况的档案分裂形态：
// "REDMI K80" 只有 USB serial、无线地址 197 被市场名读取失败时记到了
// "Xiaomi 24117RK2CC" 键下）。
func seedProfiles(a *App, devices map[string]*DeviceEntry) {
	a.profiles.mu.Lock()
	defer a.profiles.mu.Unlock()
	for k, e := range devices {
		a.profiles.data.Devices[k] = e
	}
}

func mkEntry(marketname, model string, serials, addrs []string) *DeviceEntry {
	e := &DeviceEntry{
		Marketname: marketname,
		Model:      model,
		Serials:    serials,
		Addrs:      []AddrEntry{},
		Profiles:   DefaultProfile(),
	}
	for _, ad := range addrs {
		e.Addrs = append(e.Addrs, AddrEntry{Addr: ad, State: AddrStateActive, LastOk: 1})
	}
	return e
}

// 同设备 USB+无线双 transport（同一档案 identity）→ 绝不弹"新设备"；
// 即使无线卡的 marketname 本轮读不到（Identity/Name 都是 man+model 回退值），
// 档案市场名仍能对上会话 identity（16:53 实况：K80 无线 197 在 K80 会话中反复弹窗）。
// 判据 v2：K80 无线 = 已建档仅无线 → 不弹；平板 USB 插线（本轮新增条目）→ 弹。
func TestNewDevicePopupSameIdentityDualTransportNoPopup(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80":        mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C", []string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	})
	// K80 USB 会话已存在
	setDevices(a, []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"},
	})
	if err := a.StartCast("601c9f08"); err != nil {
		t.Fatal(err)
	}
	if got := sessionBySerial(t, a, "601c9f08").Identity; got != "REDMI K80" {
		t.Fatalf("会话 identity 应为市场名: %q", got)
	}

	// 基线轮询：仅 K80 USB（已开会话）→ 无弹窗（平板尚未插线）
	a.applyTrackUpdate([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"},
	})
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("基线轮询不应弹窗: %+v", a.Snapshot().NewDevice)
	}

	// 下一轮轮询：K80 无线 transport 上线（marketname 读不到 → 回退 identity/名）
	// + 平板 USB 插线。K80 无线绝不能弹，平板才是候选（插线事件）。
	devs := []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "Xiaomi 24117RK2CC", Identity: "Xiaomi 24117RK2CC",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"}, // 本轮 marketname 读不到（回退值）
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	setDevices(a, devs)
	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "a743e1df" {
		t.Fatalf("K80 无线（同身份）不应弹窗，应弹平板: %+v", np)
	}

	// 弹掉平板后（暂不）：K80 无线同身份仍绝不弹
	a.DismissNewDevice("a743e1df")
	a.applyTrackUpdate(devs)
	if np = a.Snapshot().NewDevice; np != nil {
		t.Fatalf("同身份设备在会话中绝不弹（含暂不后）: %+v", np)
	}
}

// 弹窗文本必须用市场名：卡片 Name 是 man+model 回退值时，档案有 marketname
// 就用市场名（"REDMI K80" 而非 "Xiaomi 24117RK2CC"）。
// 判据 v2：K80 已建档 → 经 USB 插线事件触发弹窗。
func TestNewDevicePopupUsesMarketNameText(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	// 平板会话在投
	setDevices(a, []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	})
	_ = a.StartCast("a743e1df")
	// 基线：平板（会话中）+ K80 无线（已建档仅无线 → 不弹）
	baseline := []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "Xiaomi 24117RK2CC", Identity: "Xiaomi 24117RK2CC",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	}
	setDevices(a, baseline)
	a.applyTrackUpdate(baseline)
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("已建档仅无线出现不应弹: %+v", a.Snapshot().NewDevice)
	}
	// K80 USB 插线（本轮 marketname 读不到 → Name/Identity 回退 man+model）→ 弹
	devs := []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "Xiaomi 24117RK2CC", Identity: "Xiaomi 24117RK2CC",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	}
	setDevices(a, devs)
	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil {
		t.Fatal("K80 USB 插线应弹")
	}
	if np.Name != "REDMI K80" {
		t.Fatalf("弹窗文本必须用市场名（禁用 model/厂商名）: %q", np.Name)
	}
	if np.Identity != "REDMI K80" {
		t.Fatalf("弹窗 identity 应为档案市场名: %q", np.Identity)
	}
	if np.Serial != "601c9f08" || np.ConnType != "usb" {
		t.Fatalf("弹窗目标卡错误: %+v", np)
	}
}

// 【暂不】按档案市场名 identity 生效：下一轮 marketname 读取抖动（identity
// 翻成 man+model）也不会重新弹——防"暂不了"的反复弹窗（16:47-16:54 每 30s 重弹）。
// 判据 v2：K80 已建档 → 经 USB 插线事件触发弹窗；identity 由档案市场名稳定。
func TestNewDevicePopupDismissStableAcrossIdentityFlip(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	devsFallback := []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "Xiaomi 24117RK2CC", Identity: "Xiaomi 24117RK2CC",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	}
	devsMarket := []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "REDMI K80", Identity: "REDMI K80", Marketname: "REDMI K80",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	}
	setDevices(a, nil)
	a.applyTrackUpdate(nil) // 基线（无设备）
	setDevices(a, devsMarket)
	a.applyTrackUpdate(devsMarket) // USB 插线 → 首次弹
	if a.Snapshot().NewDevice == nil {
		t.Fatal("首次应弹")
	}
	a.DismissNewDevice("601c9f08")
	if a.Snapshot().NewDevice != nil {
		t.Fatal("暂不后应关闭")
	}
	// 两个方向翻转（marketname 读不到 ↔ 读到）都不再弹（档案市场名恒定 identity）
	setDevices(a, devsFallback)
	a.applyTrackUpdate(devsFallback)
	setDevices(a, devsMarket)
	a.applyTrackUpdate(devsMarket)
	if a.Snapshot().NewDevice != nil {
		t.Fatal("identity 抖动不应绕过暂不")
	}
}

// 锁定会话注入档案身份（SCEZ_MARKET/SCEZ_MODEL）：bat 无线回退防抢与
// watcher 比对本尊用；未锁定（自由会话）不注入。
func TestStartCastInjectsMarketModelForLockedSession(t *testing.T) {
	a, rec := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	setDevices(a, []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"},
	})
	if err := a.StartCast("601c9f08"); err != nil {
		t.Fatal(err)
	}
	p := rec.serial("601c9f08")[0].waitParams(t, 1)
	if p.Serial != "601c9f08" || p.Market != "REDMI K80" || p.Model != "24117RK2CC" {
		t.Fatalf("锁定会话应注入 SCEZ_SERIAL+SCEZ_MARKET+SCEZ_MODEL: %+v", p)
	}
	// 档案 addrs 归并后：SCEZ_ADDR 同步注入（无线分支直连本尊，不读共享 config.txt）
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("锁定会话应注入 SCEZ_ADDR=档案最近成功 addr: %+v", p)
	}
	// 档案无 marketname（新设备只读到 model）：Market 不注、Model 注
	a2, rec2 := multiTestApp()
	seedProfiles(a2, map[string]*DeviceEntry{
		"Xiaomi 24117RK2CC": mkEntry("", "24117RK2CC", []string{"601c9f08"}, nil),
	})
	setDevices(a2, []adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "Xiaomi 24117RK2CC",
			Identity: "Xiaomi 24117RK2CC", Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	})
	if err := a2.StartCast("601c9f08"); err != nil {
		t.Fatal(err)
	}
	p2 := rec2.serial("601c9f08")[0].waitParams(t, 1)
	if p2.Market != "" || p2.Model != "24117RK2CC" {
		t.Fatalf("无市场名档案：只注 MODEL: %+v", p2)
	}
}

// StopCast 兜底 kill-server 门：存在其他活动会话 → 判定 false（不杀共享 adb
// server）；无其他活动会话（含对方已结束）→ true（保持原清理语义）。
func TestStopCastServerKillGateMultiSession(t *testing.T) {
	a, rec := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb"}, {Serial: "B", State: "device", ConnType: "usb"}})
	_ = a.StartCast("A")
	_ = a.StartCast("B")

	fA := rec.serial("A")[0]
	fB := rec.serial("B")[0]
	if gateA := fA.lastCanKill(); gateA == nil {
		t.Fatal("A 未注入杀服判定回调")
	}
	if gateB := fB.lastCanKill(); gateB == nil {
		t.Fatal("B 未注入杀服判定回调")
	}

	// A/B 并存：停 A → A 的判定 false（B 仍在投屏）
	if err := a.StopCast("A"); err != nil {
		t.Fatal(err)
	}
	fA.waitStopped(t) // gui5：杀树异步
	if gate := fA.lastCanKill(); gate() {
		t.Fatal("B 活动时停 A 不应允许杀共享 adb server")
	}

	// B 结束后：A 的判定 true（无其他活动会话）
	a.OnBatExit("B", 0)
	if gate := fA.lastCanKill(); !gate() {
		t.Fatal("无其他活动会话时应允许兜底杀服")
	}

	// 单会话（B 已结束、A 已停）：新会话 C 的判定默认 true（无并存会话）
	a.OnBatExit("A", 0)
	a.ForgetSession("A")
	a.ForgetSession("B")
	setDevices(a, []adb.Device{{Serial: "C", State: "device", ConnType: "usb"}})
	_ = a.StartCast("C")
	if gate := rec.serial("C")[0].lastCanKill(); gate == nil || !gate() {
		t.Fatal("单会话应允许兜底杀服")
	}
}

// StartCast 同 identity 拒绝、identity 解析走档案市场名（会话 serial 旧键场景）。
func TestStartCastIdentityViaProfileMarketname(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	// K80 无线卡（本轮 marketname 读不到 → 卡片 Identity 是 man+model 回退值）
	setDevices(a, []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "Xiaomi 24117RK2CC", Identity: "Xiaomi 24117RK2CC",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	})
	startVerifyAlwaysOK(a) // gui32 验证链：档案候选 5555 验证通过才选用
	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	if got := sessionBySerial(t, a, "192.168.31.197:5555").Identity; got != "REDMI K80" {
		t.Fatalf("会话 identity 应从档案市场名解析: %q", got)
	}
	// 同一设备旧 USB serial 再开会话 → 按档案 identity 拒绝
	if err := a.StartCast("601c9f08"); err == nil || !strings.Contains(err.Error(), "投屏已在运行") {
		t.Fatalf("同档案 identity 重复开会话应拒绝: %v", err)
	}
}

// 档案分裂自愈（16:53 实况的档案形态）：marketname 读不到时无线地址被记到
// man+model 键下（"Xiaomi 24117RK2CC"）→ 市场名恢复读取后 SyncDevices 必须
// 归并回 "REDMI K80"（addr 回本尊档案）→ BestAddr 才有值 → SCEZ_ADDR 才注得上；
// 且 marketname 仍读不到时档案市场名优先，identity 不抖动、不分裂。
func TestProfileSyncHealsMarketnameSplit(t *testing.T) {
	s := NewProfileStore("")
	// 直接构造分裂态（与实况 profiles.json 一致）
	s.mu.Lock()
	s.data.Devices["REDMI K80"] = mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, nil)
	s.data.Devices["Xiaomi 24117RK2CC"] = mkEntry("", "24117RK2CC", nil, []string{"192.168.31.197:5555"})
	s.mu.Unlock()

	// ① marketname 仍读不到：档案市场名优先 → 197 卡身份保持 REDMI K80，
	// 不新建/不重键回 man+model（identity 稳定）；卡片 Marketname/Identity 被补正
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "Xiaomi 24117RK2CC", Identity: "Xiaomi 24117RK2CC",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	}
	if !s.SyncDevices(devs) {
		t.Fatal("SyncDevices 应有改动（addr 成功记录）")
	}
	if devs[0].Identity != "REDMI K80" || devs[0].Marketname != "REDMI K80" {
		t.Fatalf("卡片身份应补正为档案市场名: id=%q mkt=%q", devs[0].Identity, devs[0].Marketname)
	}
	if e, ok := s.Entry("192.168.31.197:5555"); !ok || e.Marketname != "REDMI K80" {
		t.Fatalf("197 应归到 REDMI K80 档案: %+v/%v", e, ok)
	}

	// ② marketname 恢复读取：归并完成，旧 man+model 键消失
	devs2 := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "REDMI K80", Identity: "REDMI K80", Marketname: "REDMI K80",
			Manufacturer: "Xiaomi", Model: "24117RK2CC"},
	}
	s.SyncDevices(devs2)
	s.mu.Lock()
	_, split := s.data.Devices["Xiaomi 24117RK2CC"]
	s.mu.Unlock()
	if split {
		t.Fatal("归并后 man+model 分裂键应消失")
	}
	if got := s.BestAddr("601c9f08"); got != "192.168.31.197:5555" {
		t.Fatalf("BestAddr(601c9f08) 应回到本尊无线地址: %q", got)
	}
	if got := s.BestAddr("192.168.31.197:5555"); got != "192.168.31.197:5555" {
		t.Fatalf("BestAddr(197) 应可用: %q", got)
	}
}

// --- 会话操作只作用于被点击的会话（多会话隔离加固） ---

// 空串号兜底拒绝：空 serial 会建 key="" 会话且无 SCEZ 注入 → bat 自由检测
// 抓第一台（"点了 A 投到 B"隐患）——StartCast/RestartCast/StartCastParallel 全拒。
func TestStartCastEmptySerialRejected(t *testing.T) {
	a, _ := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb"}})
	if err := a.StartCast(""); err == nil || !strings.Contains(err.Error(), "未指定设备") {
		t.Fatalf("空串号 StartCast 应拒绝: %v", err)
	}
	if err := a.StartCastParallel(""); err == nil {
		t.Fatalf("空串号 StartCastParallel 应拒绝: %v", err)
	}
	if err := a.RestartCast(""); err == nil {
		t.Fatalf("空串号 RestartCast 应拒绝: %v", err)
	}
	if len(a.Snapshot().Sessions) != 0 {
		t.Fatalf("空串号不应创建会话: %+v", a.Snapshot().Sessions)
	}
}

// RestartCast(A) 只动 A：B 的 runner 不被 Stop、不被重启；A 的 restarting
// 闩锁幂等（连点两次只杀一次树）；重启完成后 A/B 各自一个活动会话。
func TestMultiSessionRestartLatchOnlyTarget(t *testing.T) {
	a, rec := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb"}, {Serial: "B", State: "device", ConnType: "usb"}})
	_ = a.StartCast("A")
	_ = a.StartCast("B")

	// 连点两次 A（闩锁幂等）+ 期间 B 完全不动
	if err := a.RestartCast("A"); err != nil {
		t.Fatal(err)
	}
	if err := a.RestartCast("A"); err != nil {
		t.Fatalf("重复 RestartCast 应幂等: %v", err)
	}
	rec.serial("A")[0].waitStopped(t) // gui5：重启杀树异步
	if rec.serial("B")[0].isStopped() {
		t.Fatal("RestartCast(A) 不应杀 B 的 bat")
	}
	if rec.count("A") != 1 || rec.count("B") != 1 {
		t.Fatalf("重启前不应有新 Start: A=%d B=%d", rec.count("A"), rec.count("B"))
	}

	// A 退出回调 → 重跑 A；B 不受影响
	a.OnBatExit("A", 1)
	waitCount(t, rec, "A", 2)
	if rec.count("B") != 1 {
		t.Fatalf("A 重启不应重跑 B: %d", rec.count("B"))
	}
	ss := a.Snapshot().Sessions
	if len(ss) != 2 || !ss[0].Active || !ss[1].Active {
		t.Fatalf("重启后应两个活动会话: %+v", ss)
	}
}

// --- 来源观测（90s 自动重投偶发的现场抓取） ---

// callerChain 深 skip 安全（超出栈深不 panic、返回空串）——
// 帧序/短名契约详见 app_trace_test.go（traceCallerOfStart 模拟 StartCast 位）。
func TestCallerChainDeepSkipSafe(t *testing.T) {
	if callerChain(100) != "" {
		t.Fatalf("深 skip 应安全返回空串: %q", callerChain(100))
	}
	if strings.Contains(callerChain(0), "scrcpy-ez/gui/internal/app.") {
		t.Fatalf("调用链应只保留短名: %q", callerChain(0))
	}
}
