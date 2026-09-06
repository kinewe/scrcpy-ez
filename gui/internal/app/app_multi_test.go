package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- StartCast 注入扩展（SCEZ_SERIAL / SCEZ_ADDR / SCEZ_NO_WATCH） ---

// USB 在线设备 → 注入 SCEZ_SERIAL=USB serial（bat 仅当该 serial 实际在线才锁）；
// 同时注入 SCEZ_ADDR=档案最近成功 addr（拔线后无线分支兜底直连，修复断线循环）。
func TestStartCastInjectsSerialForUSB(t *testing.T) {
	a, f := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro",
			Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "Redmi K80",
			Marketname: "Redmi K80", Identity: "Redmi K80"},
	}
	a.mu.Unlock()
	// 档案：无线地址已探测成功（BestAddr 来源）
	if err := a.profiles.Save("a743e1df", DefaultProfile()); err != nil {
		t.Fatal(err)
	}
	a.profiles.AddrSuccess("a743e1df", "192.168.31.162:5555")

	if err := a.StartCast("a743e1df"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Serial != "a743e1df" {
		t.Fatalf("USB 设备应注入 SCEZ_SERIAL=USB serial: %+v", p)
	}
	if p.Addr != "192.168.31.162:5555" {
		t.Fatalf("USB 设备也应注入 SCEZ_ADDR=档案最近成功 addr（拔线无线兜底）: %+v", p)
	}
	// watcher 改造后：普通会话不再按 identity 判据注入 NO_WATCH
	// （watcher 常开，bat 内"对上才切"自判——即使 K80 USB 同时在线也不注）
	if p.NoWatch {
		t.Fatalf("普通会话不应注入 NO_WATCH（watcher 常开自判）: %+v", p)
	}

	// 档案无 addr：USB 设备只注入 SCEZ_SERIAL（无线分支走 bat 原逻辑）；
	// 列表内无其他 USB 在线 → 不注入 NO_WATCH
	f2 := &fakeRunner{exitCode: -1}
	a2 := New(Config{})
	a2.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f2, nil })
	a2.mu.Lock()
	a2.devices = []adb.Device{{Serial: "a743e1df", State: "device", ConnType: "usb"}}
	a2.mu.Unlock()
	_ = a2.StartCast("a743e1df")
	if p2 := f2.waitParams(t, 1); p2.Serial != "a743e1df" || p2.Addr != "" || p2.NoWatch {
		t.Fatalf("档案无 addr/无他机 USB 在线时不应注入 SCEZ_ADDR/NO_WATCH: %+v", p2)
	}
}

// watcher 改造后回归：普通会话一律不注 NO_WATCH（watcher 常开，由 bat"对上才切"自判）——
// 无论"异 identity USB 在线"（K80 插着投平板无线）还是"同 identity USB 在线"（平板插回）
// 都不再影响注入；仅并行会话（StartCastParallel 预留）仍注 NO_WATCH。
func TestStartCastNoWatchOnlyForParallel(t *testing.T) {
	// 异 identity USB 在线（K80 插着）+ 平板无线目标 → 不注（bat watcher 自判忽略 K80）
	a, f := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Identity: "Redmi K80"},
	}
	a.mu.Unlock()
	_ = a.StartCast("192.168.31.162:5555")
	if p := f.waitParams(t, 1); p.NoWatch {
		t.Fatalf("普通会话不应注入 NO_WATCH（即使异 identity USB 在线）: %+v", p)
	}

	// 同 identity USB 在线（平板插回）+ 平板无线目标 → 不注（watcher 自动切有线）
	f2 := &fakeRunner{exitCode: -1}
	a2 := New(Config{})
	a2.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f2, nil })
	a2.mu.Lock()
	a2.devices = []adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "a743e1df", State: "device", ConnType: "usb", Identity: "Xiaomi Pad 8 Pro"},
	}
	a2.mu.Unlock()
	_ = a2.StartCast("192.168.31.162:5555")
	if p := f2.waitParams(t, 1); p.NoWatch {
		t.Fatalf("同 identity USB 在线普通会话也不注 NO_WATCH: %+v", p)
	}

	// 有线目标 + 异 identity USB 在线 → 不注
	f4 := &fakeRunner{exitCode: -1}
	a4 := New(Config{})
	a4.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f4, nil })
	a4.mu.Lock()
	a4.devices = []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Identity: "Redmi K80"},
	}
	a4.mu.Unlock()
	_ = a4.StartCast("a743e1df")
	if p := f4.waitParams(t, 1); p.NoWatch {
		t.Fatalf("有线目标普通会话也不注 NO_WATCH: %+v", p)
	}
}

// 仅无线在线设备 → 注入 SCEZ_ADDR=档案成功 addr（探测结果）；
// 档案缺失时回退当前在线地址。
func TestStartCastInjectsAddrForWifi(t *testing.T) {
	a, f := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi",
			Name: "Xiaomi Pad 8 Pro", Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	}
	a.mu.Unlock()
	// 档案：162 是最近成功 addr（历史成功），99 是当前在线新 addr（轮询归并后 active）
	p := DefaultProfile()
	if err := a.profiles.Save("192.168.31.99:5555", p); err != nil {
		t.Fatal(err)
	}
	a.profiles.SyncDevices(a.snapshotRaw().Devices)
	// 162 先成功，99（当前在线 addr）后成功 → BestAddr=最近成功者 99
	a.profiles.AddrSuccess("192.168.31.99:5555", "192.168.31.162:5555")
	a.profiles.AddrSuccess("192.168.31.99:5555", "192.168.31.99:5555")
	startVerifyAlwaysOK(a) // gui32 验证链：候选 99 验证通过才选用

	if err := a.StartCast("192.168.31.99:5555"); err != nil {
		t.Fatal(err)
	}
	p1 := f.waitParams(t, 1)
	if p1.Addr != "192.168.31.99:5555" {
		t.Fatalf("无线设备应注入 SCEZ_ADDR=最近成功 addr: %+v", p1)
	}
	if p1.Serial != "" || p1.NoWatch {
		t.Fatalf("无线设备不应注入 SCEZ_SERIAL/NO_WATCH: %+v", p1)
	}

	// 档案无地址记录 → 回退当前在线地址
	f2 := &fakeRunner{exitCode: -1}
	a2 := New(Config{})
	a2.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f2, nil })
	a2.mu.Lock()
	a2.devices = []adb.Device{{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi"}}
	a2.mu.Unlock()
	_ = a2.StartCast("192.168.31.99:5555")
	if p2 := f2.waitParams(t, 1); p2.Addr != "192.168.31.99:5555" {
		t.Fatalf("档案无地址应回退在线地址: %+v", p2)
	}
}

// 设备列表外（未命中）→ 不注入 SCEZ_SERIAL/SCEZ_ADDR（bat 原逻辑，回归兼容）。
func TestStartCastNoInjectionWithoutDevice(t *testing.T) {
	a, f := newTestApp()
	if err := a.StartCast("X"); err != nil {
		t.Fatal(err)
	}
	if p := f.waitParams(t, 1); p.Serial != "" || p.Addr != "" || p.NoWatch {
		t.Fatalf("未命中设备不应注入锁定参数: %+v", p)
	}
}

// NO_WATCH 已废弃（v3）：StartCastParallel 与普通 StartCast 一律不注入
// SCEZ_NO_WATCH=1——watcher 会话化后（独立 TAG/flag/只关本会话 + 市场名
// "对上才切"）并行会话各自 watcher 互不干扰，插线切换对每个会话都生效。
func TestStartCastParallelNoWatchDeprecated(t *testing.T) {
	a, f := newTestApp()
	if err := a.StartCastParallel("X"); err != nil {
		t.Fatal(err)
	}
	if p := f.waitParams(t, 1); p.NoWatch {
		t.Fatalf("并行会话不应注入 SCEZ_NO_WATCH=1（已废弃）: %+v", p)
	}
	// 结束态重新投屏（普通 StartCast）同样不注入
	a.OnBatExit("X", 0)
	_ = a.StartCast("X")
	if p := f.waitParams(t, 2); p.NoWatch {
		t.Fatalf("普通会话不应注入 NO_WATCH: %+v", p)
	}
}

// --- 无线探测（mDNS + 并行 connect）经 pollOnce 全链路 ---

// GUI 启动即迁移：New 读旧结构 profiles.json → 新结构内存档案可查（旧结构删除）。
func TestAppStartLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	legacy := `{"a743e1df": {"usb": {"res": 2400, "fps": 75, "bitrate": 55, "custom": true,
	  "baseline": {"res": 2560, "fps": 120, "bitrate": 80}},
	  "wifi": {"res": 1920, "fps": 60, "bitrate": 15, "custom": false,
	  "baseline": {"res": 1920, "fps": 60, "bitrate": 15}}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(Config{ProfilesPath: path, Version: "test"})
	p := a.GetProfile("a743e1df")
	if !p.Usb.Custom || p.Usb.FPS != 75 {
		t.Fatalf("启动迁移后参数未保留: %+v", p)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["devices"]; !ok {
		t.Fatalf("旧结构未删除/新结构未落盘:\n%s", b)
	}
	if _, ok := raw["a743e1df"]; ok {
		t.Fatalf("旧顶层 serial 键仍存在:\n%s", b)
	}
}

// 模式状态只跟"最近一次真实模式行"：有线流程中"保存无线地址"学习行不改变模式；
// 反向真无线流程 → wifi；规格行定案。
func TestModeFollowsRealModeLines(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	// 有线流程（保存重投）：学习有线 → 保存无线地址（不改模式）→ 开始投屏 → 高清
	for _, l := range []string{
		"[1] 重置 adb 服务...",
		"[2] 检测 USB 设备...",
		"[OK] 检测到 USB 设备：Xiaomi Pad 8 Pro（a743e1df）",
		"[学习] 有线模式：检测手机 WiFi IP...",
		"[OK] 检测到手机 WiFi IP：192.168.31.197",
		"[OK] 已保存无线地址到 config.txt：192.168.31.162:5555",
	} {
		a.NotifyLine("X", l)
	}
	if s := a.Snapshot().Cast; s.Mode != "usb" {
		t.Fatalf("有线学习行后 Mode 应为 usb: %q", s.Mode)
	}
	// 关键：学习行（保存无线地址）不得改变模式
	a.NotifyLine("X", "[提示] 已保存无线地址到 config.txt：192.168.31.162:5555")
	if s := a.Snapshot().Cast; s.Mode != "usb" {
		t.Fatalf("保存无线地址行不得改变模式: %q", s.Mode)
	}
	// 规格定案
	a.NotifyLine("X", "===== 开始投屏：Xiaomi Pad 8 Pro（a743e1df） =====")
	a.NotifyLine("X", "[高清] 有线模式：检测到设备 3200x2136@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化）")
	s := a.Snapshot().Cast
	if s.Mode != "usb" || s.Spec == nil || !s.Spec.Wired {
		t.Fatalf("有线规格后 Mode/spec 错误: %+v", s)
	}

	// 反向：真无线流程 → wifi（中间穿插保存无线地址行）
	a2, _ := newTestApp()
	_ = a2.StartCast("Y")
	for _, l := range []string{
		"[提示] 未检测到 USB 设备，进入无线模式",
		"[3] 无线模式：尝试连接上次保存的地址...",
		"[OK] 连接成功：Xiaomi Pad 8 Pro（192.168.31.162:5555）",
		"[OK] 已保存无线地址到 config.txt：192.168.31.162:5555",
		"===== 开始投屏：Xiaomi Pad 8 Pro（192.168.31.162:5555） =====",
		"[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps）",
	} {
		a2.NotifyLine("Y", l)
	}
	s = a2.Snapshot().Cast
	if s.Mode != "wifi" || s.Spec == nil || s.Spec.Wired {
		t.Fatalf("无线流程 Mode/spec 错误: %+v", s)
	}
}

// 恢复期徽标兜底：StartCast 捕获 NativeRes（卡片 Res），设备列表消失后仍保持
// （前端按此比例换算 WxH——修复恢复期"长边 1920"）。
func TestStartCastCapturesNativeRes(t *testing.T) {
	a, _ := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro",
			Identity: "Xiaomi Pad 8 Pro", Wireless: "192.168.31.162:5555", Res: "3200x2136"},
	}
	a.mu.Unlock()
	_ = a.StartCast("a743e1df")
	if s := a.Snapshot().Cast; s.NativeRes != "3200x2136" {
		t.Fatalf("StartCast 应捕获 NativeRes: %q", s.NativeRes)
	}
	// 恢复期：设备列表清空（设备从 adb 临时消失）→ NativeRes 保持
	a.mu.Lock()
	a.devices = nil
	a.mu.Unlock()
	if s := a.Snapshot().Cast; s.NativeRes != "3200x2136" {
		t.Fatalf("设备列表消失后 NativeRes 应保持: %q", s.NativeRes)
	}
}

// 设备列表为空/无 Res（恢复期重投）：档案持久化 Res 兜底
// （SyncDevices 会把卡片 Res 写进档案；StartCast 读取）。
func TestStartCastNativeResFromProfile(t *testing.T) {
	a, f := newTestApp()
	// 真实流程：无线卡在线时 SyncDevices 建 identity 档案并持久化 Res
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi",
			Marketname: "Xiaomi Pad 8 Pro", Model: "25091RP04C", Res: "3200x2136"},
	})
	if e, ok := a.profiles.Entry("192.168.31.162:5555"); !ok || e.Res != "3200x2136" {
		t.Fatalf("SyncDevices 应持久化 Res: %+v", e)
	}
	_ = a.StartCast("192.168.31.162:5555") // 恢复期：设备列表为空
	if s := a.Snapshot().Cast; s.NativeRes != "3200x2136" {
		t.Fatalf("档案 Res 兜底失败: %q", s.NativeRes)
	}
	_ = f.waitParams(t, 1)
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- Bug 1：徽标统一 WxH（Texture 真实值优先） ---

// scrcpy-server 的 INFO: Texture: WxH 行 → 补丁当前规格的 Res（真实纹理值）：
// [custom] 长边档（MaxSize 只有长边）得到真实 WxH；[高清] 原生档同样被真实值覆盖。
// 规格未到达时（c.Spec==nil）忽略。
func TestTexturePatchesSpecRes(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps）")
	s := a.Snapshot().Cast
	if s.Spec == nil || s.Spec.Res != "" {
		t.Fatalf("[流畅] 行不应带 Res: %+v", s.Spec)
	}
	// 自定义长边档 + Texture 行 → Res=真实 WxH
	a.NotifyLine("X", "[custom] wireless res=1080 fps=60 bitrate=15 (wifi)")
	a.NotifyLine("X", "INFO: Texture: 1080x720")
	s = a.Snapshot().Cast
	if s.Spec == nil || s.Spec.Res != "1080x720" || s.Spec.MaxSize != 1080 || !s.Spec.Custom {
		t.Fatalf("Texture 未补丁自定义档 Res: %+v", s.Spec)
	}
	// 有线 [高清] + Texture → Res 也换成真实纹理
	a.NotifyLine("X", "[高清] 有线模式：检测到设备 3200x2136@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化）")
	a.NotifyLine("X", "INFO: Texture: 2560x1708")
	s = a.Snapshot().Cast
	if s.Spec == nil || s.Spec.Res != "2560x1708" || !s.Spec.Wired {
		t.Fatalf("Texture 未补丁有线档 Res: %+v", s.Spec)
	}

	// 规格未到达时 Texture 行忽略（不 panic、不产生规格）
	a2, _ := newTestApp()
	_ = a2.StartCast("Y")
	a2.NotifyLine("Y", "INFO: Texture: 1920x1280")
	if s := a2.Snapshot().Cast; s.Spec != nil {
		t.Fatalf("无规格时 Texture 不应生成规格: %+v", s.Spec)
	}
}

// --- Bug 2：USB 在线必注 SCEZ_SERIAL（保存重投先无线根因） ---

// 卡片重键场景：会话 serial 是旧无线 IP，而设备卡已因 USB 插线重键为 USB serial
// （Serial=USB、Wireless=旧 IP）→ StartCast(旧 IP) 必须仍注入 SCEZ_SERIAL=USB serial。
func TestStartCastInjectsSerialWhenCardRekeyed(t *testing.T) {
	a, f := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro",
			Identity: "Xiaomi Pad 8 Pro", Wireless: "192.168.31.162:5555", Res: "3200x2136"},
	}
	a.mu.Unlock()
	if err := a.profiles.Save("a743e1df", DefaultProfile()); err != nil {
		t.Fatal(err)
	}
	a.profiles.AddrSuccess("a743e1df", "192.168.31.162:5555")

	// 旧 IP 启动（RestartCast 场景）：目标经 Wireless 字段匹配 → USB transport 在线 → 注 SERIAL
	_ = a.StartCast("192.168.31.162:5555")
	p := f.waitParams(t, 1)
	if p.Serial != "a743e1df" {
		t.Fatalf("卡片重键后应注入 SCEZ_SERIAL=USB serial: %+v", p)
	}
	if p.Addr == "" {
		t.Fatalf("无线兜底 SCEZ_ADDR 应注入: %+v", p)
	}
	if p.NoWatch {
		t.Fatalf("普通会话不应注入 NO_WATCH: %+v", p)
	}
}

// 双卡场景：目标卡是无线（wifi），同 identity 的独立 USB 卡在线 →
// 仍注入 SCEZ_SERIAL=该 USB serial（判据=identity 对应 USB transport 在线）。
func TestStartCastInjectsSerialFromUsbCard(t *testing.T) {
	a, f := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro",
			Identity: "Xiaomi Pad 8 Pro"},
	}
	a.mu.Unlock()
	if err := a.profiles.Save("192.168.31.162:5555", DefaultProfile()); err != nil {
		t.Fatal(err)
	}
	a.profiles.AddrSuccess("192.168.31.162:5555", "192.168.31.162:5555")
	startVerifyAlwaysOK(a) // gui32 验证链：候选 162 验证通过才选用

	_ = a.StartCast("192.168.31.162:5555")
	p := f.waitParams(t, 1)
	if p.Serial != "a743e1df" {
		t.Fatalf("同 identity USB 卡在线应注入 SCEZ_SERIAL: %+v", p)
	}
	if p.Addr == "" {
		t.Fatalf("SCEZ_ADDR 应注入: %+v", p)
	}
}

// 档案 identity 兜底：设备卡只有 USB（Wireless 为空），旧 IP serial 查不到目标卡，
// 但档案（serials+addrs）能对上 → 仍注入 SERIAL。
func TestStartCastFindsTargetByIdentity(t *testing.T) {
	a, f := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro",
			Identity: "Xiaomi Pad 8 Pro"},
	}
	a.mu.Unlock()
	if err := a.profiles.Save("a743e1df", DefaultProfile()); err != nil {
		t.Fatal(err)
	}
	a.profiles.AddrSuccess("a743e1df", "192.168.31.162:5555")

	_ = a.StartCast("192.168.31.162:5555") // 旧 IP：卡片 Serial/Wireless 都不匹配
	p := f.waitParams(t, 1)
	if p.Serial != "a743e1df" {
		t.Fatalf("identity 兜底应注入 SCEZ_SERIAL=USB serial: %+v", p)
	}
}

// USB 离线（卡片无线、无 USB transport）→ 不注 SERIAL（bat 无线分支走 SCEZ_ADDR）。
func TestStartCastNoSerialWhenUsbOffline(t *testing.T) {
	a, f := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
			Identity: "Xiaomi Pad 8 Pro"},
	}
	a.mu.Unlock()
	_ = a.StartCast("192.168.31.162:5555")
	p := f.waitParams(t, 1)
	if p.Serial != "" {
		t.Fatalf("USB 离线不应注入 SCEZ_SERIAL: %+v", p)
	}
	if p.Addr != "192.168.31.162:5555" {
		t.Fatalf("无线设备应注入 SCEZ_ADDR: %+v", p)
	}
}
