package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

type fakeRunner struct {
	started  string
	starts   int
	params   bridge.CastParams
	paramsN  int
	stopped  bool
	stops    int
	exitCode int

	canKill func() bool // StopCast 兜底 kill-server 判定回调（SetCanKillServer 注入）

	mu        sync.Mutex
	paramsLog []bridge.CastParams // 每次 Start 的参数顺序记录（诊断注入链）
}

// SetCanKillServer 记录 App 注入的兜底 kill-server 判定回调（测试断言用）。
func (f *fakeRunner) SetCanKillServer(fn func() bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canKill = fn
}

// lastCanKill 返回注入的判定回调（互斥访问，race 安全）。
func (f *fakeRunner) lastCanKill() func() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canKill
}

func (f *fakeRunner) Start(serial string, p bridge.CastParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = serial
	f.starts++
	f.params = p
	f.paramsN++
	f.paramsLog = append(f.paramsLog, p)
	return nil
}

// waitParams 等待第 n 次 Start 的参数（互斥访问，race 安全）。
func (f *fakeRunner) waitParams(t *testing.T, n int) bridge.CastParams {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		p := f.params
		cnt := f.paramsN
		f.mu.Unlock()
		if cnt >= n {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 Start 参数超时: n=%d", cnt)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitStarts 等待 Start 调用次数达到 n（互斥访问，race 安全）。
func (f *fakeRunner) waitStarts(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		cnt := f.starts
		f.mu.Unlock()
		if cnt >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 Start 次数超时: n=%d", cnt)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startedSerial 返回最近一次 Start 的 serial（互斥访问，race 安全）。
func (f *fakeRunner) startedSerial() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

// startsN 返回 Start 调用次数（互斥访问，race 安全）。
func (f *fakeRunner) startsN() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// isStopped 返回 Stop 是否被调用（互斥访问，race 安全）。
func (f *fakeRunner) isStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// stopsN 返回 Stop 调用次数（互斥访问，race 安全）。
func (f *fakeRunner) stopsN() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

// waitStopped 等待 Stop 被调用（gui5：StopCast/RestartCast 的杀树已异步，立即断言会竞态）。
func (f *fakeRunner) waitStopped(t *testing.T) {
	t.Helper()
	f.waitStops(t, 1)
}

// waitStops 等待 Stop 调用次数达到 n（互斥访问，race 安全）。
func (f *fakeRunner) waitStops(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if f.stopsN() >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 Stop 次数超时: n=%d（当前 %d）", n, f.stopsN())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fakeRunner) lastParams() bridge.CastParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.paramsLog) == 0 {
		return bridge.CastParams{}
	}
	return f.paramsLog[len(f.paramsLog)-1]
}

// paramsLogCopy 返回 Start 参数记录副本（互斥访问，race 安全）。
func (f *fakeRunner) paramsLogCopy() []bridge.CastParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bridge.CastParams{}, f.paramsLog...)
}
func (f *fakeRunner) Stop() error {
	f.mu.Lock()
	f.stopped = true
	f.stops++
	f.mu.Unlock()
	return nil
}
func (f *fakeRunner) ExitCode() int { return f.exitCode }

func newTestApp() (*App, *fakeRunner) {
	f := &fakeRunner{exitCode: -1}
	a := New(Config{BatPath: "C:\\x\\投屏启动.bat", AdbPath: "C:\\x\\adb.exe", Version: "test"})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f, nil })
	return a, f
}

func TestCastFlow(t *testing.T) {
	a, f := newTestApp()
	if err := a.StartCast("24117RK2CC"); err != nil {
		t.Fatal(err)
	}
	// Start 在独立 goroutine 调用：等待异步传递完成
	f.waitStarts(t, 1)
	if got := f.startedSerial(); got != "24117RK2CC" {
		t.Fatalf("serial 未传递: %q", got)
	}

	// 模拟 bat 输出序列
	lines := []string{
		"[1] 重置 adb 服务...",
		"[2] 检测 USB 设备...",
		"[OK] 检测到 USB 设备：Xiaomi Pad 8 Pro（24117RK2CC）",
		"===== 开始投屏：Xiaomi Pad 8 Pro（24117RK2CC） =====",
		"[高清] 有线模式：检测到设备 2560x1708@120Hz，有线规格 h264/50M/2560/120fps（低延迟优化）",
		"[键盘模式] Android SDK=34 -> uhid legacy=",
	}
	for _, l := range lines {
		a.NotifyLine("24117RK2CC", l)
	}

	s := a.Snapshot().Cast
	if !s.Active || s.Phase != "casting" {
		t.Fatalf("phase 错误: %+v", s)
	}
	if s.Spec == nil || s.Spec.Mbps != 50 || s.Spec.Wired != true {
		t.Fatalf("spec 未捕获: %+v", s.Spec)
	}
	if s.Mode != "usb" {
		t.Fatalf("模式未按规格行判定: %q", s.Mode)
	}
	if s.KeyboardMode != "uhid" {
		t.Fatalf("键盘模式未捕获: %q", s.KeyboardMode)
	}

	// 无线规格行 → 模式切换 wifi（参数浮窗自动定位依据）
	a.NotifyLine("24117RK2CC", "[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps）")
	s = a.Snapshot().Cast
	if s.Mode != "wifi" {
		t.Fatalf("无线规格行未切换模式: %q", s.Mode)
	}
}

// 提示行只做展示：识别 prompt 状态（前端据此渲染会话级按钮），
// 不写 stdin（Runner 接口已无 SendKey；fakeRunner 不记录任何按键）。
func TestPromptDisplayOnly(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "请选择 [1]重新检测 [2]配对向导 [3]退出：")
	s := a.Snapshot().Cast
	if !s.WaitingInput || s.Prompt != bridge.PromptMenu123 {
		t.Fatalf("prompt 未识别: %+v", s)
	}
	// 展示状态不触发任何对 bat 的输入（无 keys 字段可断言——fakeRunner 已无 SendKey）
	if f.isStopped() {
		t.Fatal("提示展示不应杀树")
	}
	// 后续进展行消费提示展示
	a.NotifyLine("X", "[1] 重置 adb 服务...")
	s = a.Snapshot().Cast
	if s.WaitingInput || s.Prompt != bridge.PromptNone {
		t.Fatalf("进展行应消费 prompt 展示: %+v", s)
	}
}

func TestRetryPromptQRDisplay(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "[提示] 检测到连接断开（退出码 1），2 秒后自动重连...")
	a.NotifyLine("X", "[自动切换] 2 秒后自动重新检测并投屏 Q=退出循环 R=立即重投：")
	s := a.Snapshot().Cast
	if !s.WaitingInput || s.Prompt != bridge.PromptRetryQR || s.Phase != "reconnect" {
		t.Fatalf("重连 prompt 识别错误: %+v", s)
	}
}

func TestLogTrimmed(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	for i := 0; i < 80; i++ {
		a.NotifyLine("X", "普通日志行 "+itoa(i))
	}
	s := a.Snapshot().Cast
	if len(s.Log) != 60 {
		t.Fatalf("日志应截断为 60, got %d", len(s.Log))
	}
	if !strings.Contains(s.Log[59], "79") {
		t.Fatalf("应保留最新行, tail=%q", s.Log[59])
	}
}

func TestStopCast(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	// gui5：杀树已异步——受理即返回，Stop 调用稍后落地
	f.waitStopped(t)
}

// 投屏正常结束（exit 0）→ 未在投屏、可回列表；StopCast 幂等不报错。
func TestCastEndState(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	a.OnBatExit("X", 0)
	s := a.Snapshot().Cast
	if s.Active {
		t.Fatalf("结束态应未在投屏: %+v", s)
	}
	if s.ExitCode != 0 || s.Phase != "done" || s.PhaseText != "投屏已结束" || s.ExitText != "窗口已关闭，投屏正常结束" {
		t.Fatalf("结束态文案错误: %+v", s)
	}
	if s.WaitingInput || s.Prompt != bridge.PromptNone {
		t.Fatalf("结束态应清空输入等待: %+v", s)
	}
	// 结束态再点停止按钮：静默成功，不报"停止失败"
	if err := a.StopCast("X"); err != nil {
		t.Fatalf("结束态 StopCast 应幂等无错: %v", err)
	}
	if f.isStopped() {
		t.Fatal("结束态不应再杀已 nil 的 runner")
	}

	// 返回设备列表：重置状态供下次投屏
	a.ResetCast()
	s = a.Snapshot().Cast
	if s.Active || s.ExitCode != -1 || s.Phase != "" || len(s.Log) != 0 || s.LastError != "" {
		t.Fatalf("ResetCast 后应为初始态: %+v", s)
	}
}

// 空闲态 StopCast：不 panic、不报错（幂等）。
func TestStopCastIdle(t *testing.T) {
	a, _ := newTestApp()
	if err := a.StopCast("X"); err != nil {
		t.Fatalf("空闲态 StopCast 应静默成功: %v", err)
	}
}

// 投屏异常结束（exit 1）→ 同样进 done 结束态，主标题"投屏已结束"，
// 副文提示可能因无线离线/异常，可查原始输出。
func TestCastEndStateExit1(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	a.OnBatExit("X", 1)
	s := a.Snapshot().Cast
	if s.Active || s.Phase != "done" || s.PhaseText != "投屏已结束" {
		t.Fatalf("exit 1 应进 done 结束态: %+v", s)
	}
	if !strings.Contains(s.ExitText, "退出码 1") || !strings.Contains(s.ExitText, "原始输出") {
		t.Fatalf("exit 1 副文错误: %q", s.ExitText)
	}
}

// 轮询到 0 台 + config.txt 有记忆地址 → 触发 adb connect 自恢复（30s 节流）。
// 用假的 adb 可执行脚本验证 app.pollOnce 的完整链路（Linux only）。
func TestPollOnceTriggersWirelessRecover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "calls.txt")
	cfg := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(cfg, []byte("192.168.31.162:5555\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\necho \"$@\" >> \"" + rec + "\"\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n\\n'; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := New(Config{AdbPath: fake, ConfigPath: cfg, Version: "test"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.pollOnce(ctx)
	a.pollOnce(ctx) // 立即第二次：30s 节流内不应再 connect

	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(b))
	// 第二次 pollOnce 只做 devices 轮询（30s 节流内跳过 connect）
	want := "devices -l\nconnect 192.168.31.162:5555\ndevices -l"
	if got != want {
		t.Fatalf("自恢复链路错误:\n got %q\nwant %q", got, want)
	}
}

// 运行中 ResetCast 不应误清投屏状态。
func TestResetCastGuardedWhileRunning(t *testing.T) {
	a, _ := newTestApp()
	if err := a.StartCast("X"); err != nil {
		t.Fatal(err)
	}
	a.ResetCast()
	s := a.Snapshot().Cast
	if !s.Active {
		t.Fatalf("运行中 ResetCast 不应清状态: %+v", s)
	}
}

// 轮 B 多会话：同 serial 重复 Start 拒绝；不同 serial（不同设备）允许并行会话。
func TestStartTwiceRejected(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	if err := a.StartCast("X"); err == nil || !strings.Contains(err.Error(), "投屏已在运行") {
		t.Fatalf("同 serial 重复 Start 应报'已在投屏': %v", err)
	}
	// 不同 serial：多设备并行，允许（轮 B）
	if err := a.StartCast("Y"); err != nil {
		t.Fatalf("不同设备并行 Start 应成功: %v", err)
	}
}

func TestStartNoFactory(t *testing.T) {
	a := New(Config{})
	if err := a.StartCast("X"); err == nil {
		t.Fatal("未注入工厂应失败")
	}
}

func TestPhaseTextTable(t *testing.T) {
	for _, k := range []bridge.Kind{
		bridge.KindADBReset, bridge.KindDetect, bridge.KindUSBFound, bridge.KindUSBHint,
		bridge.KindNoUSB, bridge.KindWifiTry, bridge.KindWifiOK, bridge.KindWifiFail,
		bridge.KindScanWifi, bridge.KindGuide, bridge.KindLearning, bridge.KindWatchOn,
		bridge.KindSwitchUSB, bridge.KindReconnect,
	} {
		if txt, ok := phaseText(k); !ok || txt == "" {
			t.Fatalf("phaseText(%v) 缺失", k)
		}
	}
}

func TestNotifyLineError(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "[失败] adb 启动失败，请确认 adb 可用")
	s := a.Snapshot().Cast
	if s.Phase != "error" || s.LastError == "" {
		t.Fatalf("错误未捕获: %+v", s)
	}
}

var errFake = errors.New("boom")

func TestStartFactoryError(t *testing.T) {
	a := New(Config{})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return nil, errFake })
	if err := a.StartCast("X"); err != errFake {
		t.Fatalf("工厂错误应同步返回给调用方: %v", err)
	}
	s := a.Snapshot().Cast
	if s.Active {
		t.Fatalf("工厂失败不应进入投屏态: %+v", s)
	}
}

type failingRunner struct{ fakeRunner }

func (f *failingRunner) Start(string, bridge.CastParams) error { return errFake }

func TestStartAsyncError(t *testing.T) {
	a := New(Config{})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return &failingRunner{}, nil })
	if err := a.StartCast("X"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := a.Snapshot().Cast
		if s.LastError != "" && !s.Active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("异步启动失败未落状态: %+v", s)
		}
		time.Sleep(time.Millisecond)
	}
}

// 重连后旧规格作废：新规格行到达即整体替换；期间无规格（前端显示"等待规格…"）。
func TestReconnectRefreshesSpec(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")

	// 有线分支规格
	a.NotifyLine("X", "[高清] 有线模式：检测到设备 2136x3200@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化）")
	s := a.Snapshot().Cast
	if s.Spec == nil || !s.Spec.Wired || s.Spec.Mbps != 80 || s.Spec.Res != "2136x3200" {
		t.Fatalf("有线规格未捕获: %+v", s.Spec)
	}

	// 断开重连 → 旧（有线）规格作废，不残留旧值
	a.NotifyLine("X", "[提示] 检测到连接断开（退出码 1），2 秒后自动重连...")
	s = a.Snapshot().Cast
	if s.Spec != nil {
		t.Fatalf("重连后旧规格应清空: %+v", s.Spec)
	}
	if s.Phase != "reconnect" {
		t.Fatalf("重连 phase 错误: %+v", s)
	}

	// 新无线规格行 → 替换为当前分支的值
	a.NotifyLine("X", "[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps，剪贴板自动同步）")
	s = a.Snapshot().Cast
	if s.Spec == nil || s.Spec.Wired || s.Spec.Mbps != 15 || s.Spec.FPS != 60 {
		t.Fatalf("重连后无线规格未刷新: %+v", s.Spec)
	}

	// 插线切换 → 同样作废旧规格
	a.NotifyLine("X", "[自动切换] 检测到 USB 插线，切换至有线投屏...")
	s = a.Snapshot().Cast
	if s.Spec != nil {
		t.Fatalf("插线切换后旧规格应清空: %+v", s.Spec)
	}
}

// --- 只读展示架构：无输出提示 + 用户会话级操作 ---

// 无输出 40s → 只置 Stalled 提示，不杀树（用户点"重启投屏"才杀树重跑）。
func TestWatchdogHintOnly(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "[提示] 检测到连接断开（退出码 1），2 秒后自动重连...") // phase=reconnect
	a.mu.Lock()
	a.sessions["X"].lastLineAt = time.Now().Add(-41 * time.Second)
	a.mu.Unlock()
	a.promptTick()
	if f.isStopped() {
		t.Fatal("无输出提示不应自动杀树")
	}
	s := a.Snapshot().Cast
	if !s.Stalled || s.StallSecs < 40 {
		t.Fatalf("应置停滞提示: %+v", s)
	}
	// 新行到达 → 提示解除
	a.NotifyLine("X", "[1] 重置 adb 服务...")
	s = a.Snapshot().Cast
	if s.Stalled {
		t.Fatalf("新输出应解除停滞提示: %+v", s)
	}
}

// 看门狗不误伤正常投屏：casting/watch-on 阶段 bat 长时间静默是正常的，不置提示。
func TestWatchdogSkipsCasting(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "===== 开始投屏：X =====") // phase=casting
	a.mu.Lock()
	a.sessions["X"].lastLineAt = time.Now().Add(-5 * time.Minute)
	a.mu.Unlock()
	a.promptTick()
	s := a.Snapshot().Cast
	if s.Stalled {
		t.Fatal("投屏中静默不应置停滞提示")
	}
	// watch-on 同样豁免
	a.NotifyLine("X", "[自动切换] 无线投屏中，已开启 USB 插线监测（每 2 秒检测一次）")
	a.mu.Lock()
	a.sessions["X"].lastLineAt = time.Now().Add(-5 * time.Minute)
	a.mu.Unlock()
	a.promptTick()
	s = a.Snapshot().Cast
	if s.Stalled {
		t.Fatal("插线监测中静默不应置停滞提示")
	}
	if f.isStopped() {
		t.Fatal("提示路径不应杀树")
	}
}

// 用户点"重启投屏"（重新检测/配对向导/立即重投共用）：杀树 → 等退出 → 重跑新会话。
func TestRestartCastKillsAndRestarts(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "请选择 [1]重新检测 [2]配对向导 [3]退出：")
	if err := a.RestartCast("X"); err != nil {
		t.Fatal(err)
	}
	f.waitStopped(t) // gui5：重启杀树异步
	// 模拟桥接 waitLoop 回调（真实场景由 taskkill 触发）
	a.OnBatExit("X", 1)
	f.waitStarts(t, 2)
	s := a.Snapshot().Cast
	if !s.Active {
		t.Fatalf("用户重启未生效: active=%v", s.Active)
	}
	if got := f.startedSerial(); got != "X" {
		t.Fatalf("重启目标错误: %q", got)
	}
}

// 重启进行中重复点击幂等：只重启一次，不报错。
func TestRestartCastIdempotent(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	if err := a.RestartCast("X"); err != nil {
		t.Fatal(err)
	}
	if err := a.RestartCast("X"); err != nil { // 重启进行中再点
		t.Fatalf("重复点击应幂等: %v", err)
	}
	a.OnBatExit("X", 1)
	f.waitStarts(t, 2)
	time.Sleep(50 * time.Millisecond)
	if got := f.startsN(); got != 2 {
		t.Fatalf("重复点击应只重启一次: starts=%d", got)
	}
}

// 结束态"重新投屏"：RestartCast 直接开新会话（无进程可杀，不调 Stop）。
func TestRestartCastFromEndState(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	a.OnBatExit("X", 0) // 结束态：runner=nil、exitCode=0、serial 保留
	if err := a.RestartCast("X"); err != nil {
		t.Fatal(err)
	}
	f.waitStarts(t, 2)
	s := a.Snapshot().Cast
	if !s.Active {
		t.Fatalf("结束态重新投屏未生效: active=%v", s.Active)
	}
	if got := f.startedSerial(); got != "X" {
		t.Fatalf("重启目标错误: %q", got)
	}
	if f.isStopped() {
		t.Fatal("结束态重启不应杀树（已无进程）")
	}
}

// 菜单"退出"= StopCast：杀树不重启。
func TestPromptExitStopsNoRestart(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "请选择 [1]重新检测 [2]配对向导 [3]退出：")
	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	f.waitStopped(t) // gui5：杀树异步
	a.OnBatExit("X", 1)
	time.Sleep(50 * time.Millisecond)
	if got := f.startsN(); got != 1 {
		t.Fatalf("退出不应重启: starts=%d", got)
	}
}

// bat 突然退出（stdout EOF → waitLoop → OnBatExit）：所有回调安全返回，不 panic。
func TestBatAbruptExitSafe(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "普通输出行")
	a.OnBatExit("X", 1)           // 进程消失
	a.NotifyLine("X", "退出后残留输出行") // readLoop 可能晚到的一行
	a.OnBatExit("X", 1)           // 重复回调也应安全
	if err := a.StopCast("X"); err != nil {
		t.Fatalf("退出后 StopCast 应幂等无错: %v", err)
	}
	s := a.Snapshot().Cast
	if s.Active || s.Phase != "done" {
		t.Fatalf("bat 退出后应进结束态: %+v", s)
	}
}

// 桥接 goroutine panic：guard 恢复 + 写崩溃日志 + 状态收尾（GUI 不再静默死）。
type panicRunner struct{ fakeRunner }

func (p *panicRunner) Start(string, bridge.CastParams) error { panic("boom") }

func TestStartPanicRecoveredWithCrashLog(t *testing.T) {
	dir := t.TempDir()
	a := New(Config{CrashDir: dir, Version: "test"})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return &panicRunner{}, nil })
	if err := a.StartCast("X"); err != nil {
		t.Fatal(err)
	}
	// 等状态 + 等崩溃日志落盘（同一轮询循环，防文件晚于状态）
	var got string
	deadline := time.Now().Add(3 * time.Second)
	for {
		s := a.Snapshot().Cast
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "crash-") {
				b, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err == nil {
					got = string(b)
				}
			}
		}
		if !s.Active && s.LastError != "" && strings.Contains(got, "boom") &&
			strings.Contains(got, "StartCast panic") &&
			strings.Contains(got, "----- 日志尾部 50 行 -----") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("panic 后未进入失败收尾状态或崩溃日志未落盘: state=%+v got=%q", s, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 原断言保留为兜底（got 已非空时应通过）
	if !strings.Contains(got, "boom") || !strings.Contains(got, "StartCast panic") ||
		!strings.Contains(got, "----- 日志尾部 50 行 -----") {
		t.Fatalf("崩溃日志内容不完整:\n%s", got)
	}
}

// 会话标签列表（多会话预留：当前 ≤1）：空闲 0 条；投屏中 1 条 active；
// bat 退出后条目保留为 inactive（供前端标签淡出动画）；ResetCast 后清空。
func TestSnapshotSessions(t *testing.T) {
	a, _ := newTestApp()
	if got := len(a.Snapshot().Sessions); got != 0 {
		t.Fatalf("空闲态应无会话: %d", got)
	}
	_ = a.StartCast("X")
	ss := a.Snapshot().Sessions
	if len(ss) != 1 || ss[0].Serial != "X" || !ss[0].Active {
		t.Fatalf("投屏中会话错误: %+v", ss)
	}
	a.OnBatExit("X", 0)
	ss = a.Snapshot().Sessions
	if len(ss) != 1 || ss[0].Active {
		t.Fatalf("结束态应保留会话条目(inactive)供淡出: %+v", ss)
	}
	a.ResetCast()
	if got := len(a.Snapshot().Sessions); got != 0 {
		t.Fatalf("ResetCast 后应无会话: %d", got)
	}
}

// --- 参数浮窗：阶梯序列 / 设备记忆 / 注入重启 ---

// 阶梯逻辑（主人定稿规则）：x ≥ 上限 → 替换上限（级数不变）；
// x < 上限 → 上级档忽略（级数变少）。
func TestLadderSeq(t *testing.T) {
	cases := []struct {
		std  []int
		x    int
		want []int
	}{
		{FpsTiers, 80, []int{80, 60, 30}},              // 低于上限：忽略 120/90
		{FpsTiers, 90, []int{90, 60, 30}},              // 档位列表构造：baseline 90 置顶+更小档
		{FpsTiers, 200, []int{200, 90, 60, 30}},        // 高于上限：替换 120，级数不变
		{BitTiers, 55, []int{55, 40, 15}},              // 码率低于上限
		{BitTiers, 80, []int{80, 60, 40, 15}},          // baseline 80 → 120 不显示
		{BitTiers, 150, []int{150, 80, 60, 40, 15}},    // 码率替换上限，级数不变
		{BitTiers, 25, []int{25, 15}},                  // 很低：只剩更小档
		{BitTiers, 120, []int{120, 80, 60, 40, 15}},    // 边界：等于上限=替换头（原样）
		{ResTiers, 2560, []int{2560, 1920, 1080, 720}}, // 边界：等于上限
		{FpsTiers, 0, []int{0}},                        // 非法输入不 panic
	}
	for _, c := range cases {
		got := LadderSeq(c.std, c.x)
		if len(got) != len(c.want) {
			t.Fatalf("LadderSeq(%v, %d) = %v, want %v", c.std, c.x, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("LadderSeq(%v, %d) = %v, want %v", c.std, c.x, got, c.want)
			}
		}
	}
}

// 设备参数记忆：未配置=默认档；保存后重新加载还原（有线/无线两套独立）。
func TestProfileStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	s := NewProfileStore(path)
	_ = s.Load()
	if got := s.Get("A"); got != DefaultProfile() {
		t.Fatalf("未配置应为默认档: %+v", got)
	}
	p := s.Get("A")
	p.Usb = ModeProfile{Res: 2400, FPS: 75, Bitrate: 55, Custom: true}
	if err := s.Save("A", p); err != nil {
		t.Fatal(err)
	}
	s2 := NewProfileStore(path)
	_ = s2.Load()
	got := s2.Get("A")
	if got.Usb != p.Usb || got.Wifi != DefaultProfile().Wifi {
		t.Fatalf("重载后参数丢失: %+v", got)
	}
}

// 保存并重新投屏：custom=true 注入覆盖参数并重启；custom=false 自动档。
func TestSaveProfileAndRestartInjectsParams(t *testing.T) {
	dir := t.TempDir()
	f := &fakeRunner{exitCode: -1}
	a := New(Config{ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f, nil })
	_ = a.StartCast("X")

	if err := a.SaveProfileAndRestart("X", "usb", 2400, 75, 55, true); err != nil {
		t.Fatal(err)
	}
	a.OnBatExit("X", 1) // 模拟 waitLoop 回调 → restartAfterExit 重跑
	f.waitStarts(t, 2)
	f.waitParams(t, 2)
	want := bridge.CastParams{Usb: bridge.ModeParams{Res: 2400, FPS: 75, Bitrate: 55, Set: true}}
	// 两次 Start 的 goroutine 调度顺序不定：重启那一次必须带该模式覆盖参数
	// （usb 套；wifi 未保存自定义 → 不注入）
	found := false
	log := f.paramsLogCopy()
	for _, p := range log {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("注入参数错误(记录 %+v): want %+v", log, want)
	}
	if got := a.GetProfile("X"); got.Usb.Res != 2400 || got.Usb.FPS != 75 || got.Usb.Bitrate != 55 || !got.Usb.Custom {
		t.Fatalf("profile 未保存: %+v", got)
	}

	// 非法参数拒绝
	if err := a.SaveProfileAndRestart("X", "usb", 0, 60, 60, true); err == nil {
		t.Fatal("非法参数应拒绝")
	}
}

// 设备记忆：已保存的自定义档在每次 StartCast（含重启）自动注入（按模式分套）：
// usb/wifi 各自独立；只有无线自定义时有线不注入（修复 USB 自愈切有线误用无线参数）。
func TestStartCastAppliesSavedProfile(t *testing.T) {
	dir := t.TempDir()
	profilesPath := filepath.Join(dir, "profiles.json")

	// 两模式都自定义：两套都注入
	f := &fakeRunner{exitCode: -1}
	a := New(Config{ProfilesPath: profilesPath, Version: "test"})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f, nil })
	p := DefaultProfile()
	p.Usb = ModeProfile{Res: 2400, FPS: 75, Bitrate: 55, Custom: true}
	p.Wifi = ModeProfile{Res: 1280, FPS: 30, Bitrate: 15, Custom: true}
	if err := a.profiles.Save("X", p); err != nil {
		t.Fatal(err)
	}
	_ = a.StartCast("X")
	got := f.waitParams(t, 1)
	want := bridge.CastParams{
		Usb:  bridge.ModeParams{Res: 2400, FPS: 75, Bitrate: 55, Set: true},
		Wifi: bridge.ModeParams{Res: 1280, FPS: 30, Bitrate: 15, Set: true},
	}
	if got != want {
		t.Fatalf("记忆档未按模式注入: %+v, want %+v", got, want)
	}

	// 只有无线自定义（用户实测 bug 场景）：wifi 注入、usb 不注入（bat 有线基准）
	f2 := &fakeRunner{exitCode: -1}
	a2 := New(Config{ProfilesPath: profilesPath, Version: "test"})
	a2.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f2, nil })
	p2 := DefaultProfile()
	p2.Wifi = ModeProfile{Res: 720, FPS: 30, Bitrate: 15, Custom: true}
	if err := a2.profiles.Save("W", p2); err != nil {
		t.Fatal(err)
	}
	_ = a2.StartCast("W")
	got2 := f2.waitParams(t, 1)
	want2 := bridge.CastParams{Wifi: bridge.ModeParams{Res: 720, FPS: 30, Bitrate: 15, Set: true}}
	if got2 != want2 {
		t.Fatalf("仅无线自定义应只注入 wifi 套: %+v, want %+v", got2, want2)
	}

	// 未自定义：不注入（自动档）
	f3 := &fakeRunner{exitCode: -1}
	a3 := New(Config{ProfilesPath: profilesPath, Version: "test"})
	a3.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f3, nil })
	_ = a3.StartCast("Y") // Y 无记忆 → 自动档
	if got3 := f3.waitParams(t, 1); got3.Usb.Set || got3.Wifi.Set {
		t.Fatalf("未自定义不应注入: %+v", got3)
	}
}

// --- 动态默认值（baseline） ---

// bat [高清]/[流畅] 行 → 更新该设备该模式 baseline（bat 实际值）；
// [custom] 回显不覆盖；未自定义时值跟随 baseline；baseline 持久化。
func TestBaselineFromSpecLine(t *testing.T) {
	dir := t.TempDir()
	f := &fakeRunner{exitCode: -1}
	a := New(Config{ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f, nil })
	_ = a.StartCast("X")

	a.NotifyLine("X", "[高清] 有线模式：检测到设备 2136x3200@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化）")
	if got := a.GetProfile("X").Usb.Baseline; got != (Baseline{Res: 2560, FPS: 120, Bitrate: 80}) {
		t.Fatalf("有线 baseline 未按 bat 实际值更新: %+v", got)
	}
	a.NotifyLine("X", "[custom] wired res=1920 fps=90 bitrate=40")
	if got := a.GetProfile("X").Usb.Baseline; got != (Baseline{Res: 2560, FPS: 120, Bitrate: 80}) {
		t.Fatalf("[custom] 行不应覆盖 baseline: %+v", got)
	}
	a.NotifyLine("X", "[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps）")
	if got := a.GetProfile("X").Wifi.Baseline; got != (Baseline{Res: 1920, FPS: 60, Bitrate: 15}) {
		t.Fatalf("无线 baseline 未更新: %+v", got)
	}

	// 修复（ABR 日志污染）：[高清] 后跟 100 行 server 日志（FRAME/ABR/PULSE）
	// 与 bat"使用默认规格"回退行 → baseline 必须仍是 80M（只认真实检测行）。
	for i := 0; i < 100; i++ {
		switch i % 4 {
		case 0:
			a.NotifyLine("X", "[server] INFO: FRAME: type=P pts=639775223012 delayDelta=72ms size=5KB")
		case 1:
			a.NotifyLine("X", "[server] INFO: ABR: bitrate 50000000 -> 39200000")
		case 2:
			a.NotifyLine("X", "[server] INFO: PULSE: pts=639777249807")
		case 3:
			a.NotifyLine("X", "[高清] 有线模式：设备规格读取失败，使用默认规格 h264/50M/2560/120fps（低延迟优化）")
		}
	}
	if got := a.GetProfile("X").Usb.Baseline; got != (Baseline{Res: 2560, FPS: 120, Bitrate: 80}) {
		t.Fatalf("ABR/FRAME/默认规格行污染了 baseline: %+v", got)
	}
	if got := a.GetProfile("X").Wifi.Baseline; got != (Baseline{Res: 1920, FPS: 60, Bitrate: 15}) {
		t.Fatalf("server 日志污染了无线 baseline: %+v", got)
	}
	// 投屏中规格展示（c.Spec）同样不被日志行覆盖（最后一条合法规格=无线 15M）
	if s := a.Snapshot().Cast.Spec; s == nil || s.Mbps != 15 || s.Wired {
		t.Fatalf("日志行覆盖了当前规格展示: %+v", s)
	}
	// 未自定义时值跟随 baseline（浮窗默认显示）
	p := a.GetProfile("X")
	if p.Usb.Res != 2560 || p.Usb.FPS != 120 || p.Usb.Bitrate != 80 {
		t.Fatalf("默认值未跟随 baseline: %+v", p.Usb)
	}
	// baseline 持久化：新实例重载
	a2 := New(Config{ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	if got := a2.profiles.Get("X").Usb.Baseline; got != (Baseline{Res: 2560, FPS: 120, Bitrate: 80}) {
		t.Fatalf("baseline 未持久化: %+v", got)
	}
}

// 未投屏过：baseline 由 adb 检测值推导（res 长边/Hz + 默认码率；无线固定档）。
func TestBaselineFallbackFromDevice(t *testing.T) {
	a, _ := newTestApp()
	a.mu.Lock()
	a.devices = []adb.Device{{Serial: "X", Res: "3200x2136", FPS: 120}}
	a.mu.Unlock()
	p := a.GetProfile("X")
	if p.Usb.Baseline != (Baseline{Res: 3200, FPS: 120, Bitrate: 60}) {
		t.Fatalf("有线推导 baseline 错误: %+v", p.Usb.Baseline)
	}
	if p.Wifi.Baseline != (Baseline{Res: 1920, FPS: 60, Bitrate: 15}) {
		t.Fatalf("无线固定 baseline 错误: %+v", p.Wifi.Baseline)
	}
	if p.Usb.Res != 3200 || p.Usb.FPS != 120 || p.Usb.Bitrate != 60 {
		t.Fatalf("默认值未跟随推导 baseline: %+v", p.Usb)
	}
	p2 := a.GetProfile("Y")
	if p2.Usb.Baseline != (Baseline{Res: 2560, FPS: 120, Bitrate: 60}) {
		t.Fatalf("无设备信息基线错误: %+v", p2.Usb.Baseline)
	}
}

// 真空期去抖（任务一）：连续失败未达阈值（3 轮≈6s）时保持 adbOK=true +
// AdbFailing=true（前端"刷新中"软提示不亮红条）；达到阈值转 adbOK=false（红条）；
// 恢复成功即复位。用假的 adb 可执行脚本验证 pollOnce 全链路（Linux only）。
func TestAdbFailDebounceVacuumThenRecover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "count.txt")
	fake := filepath.Join(dir, "adb")
	// devices -l 前 3 次失败（模拟 bat kill-server+start-server 真空），第 4 次起成功
	script := "#!/bin/sh\n" +
		"if [ \"$1\" != \"devices\" ]; then exit 0; fi\n" +
		"c=$(cat \"" + counter + "\" 2>/dev/null); c=${c:-0}\n" +
		"c=$((c+1)); echo $c > \"" + counter + "\"\n" +
		"if [ $c -le 3 ]; then echo 'adb server down' >&2; exit 1; fi\n" +
		"printf 'List of devices attached\\n12345678 device usb:2-1 product:Fake model:Fake device:Fake\\n\\n'\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := New(Config{AdbPath: fake, Version: "test"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 初始态：首轮轮询未回前视为"可用/刷新中"，不闪红条
	if s := a.Snapshot(); !s.AdbOK || s.AdbFailing {
		t.Fatalf("初始快照应 adbOK=true 且未失败: %+v", s)
	}

	a.pollOnce(ctx) // 失败 #1
	s := a.Snapshot()
	if !s.AdbOK || !s.AdbFailing || len(s.Devices) != 0 {
		t.Fatalf("失败 1 轮应保持可用+刷新中（真空期去抖）: adbOK=%v failing=%v devices=%d",
			s.AdbOK, s.AdbFailing, len(s.Devices))
	}

	a.pollOnce(ctx) // 失败 #2
	s = a.Snapshot()
	if !s.AdbOK || !s.AdbFailing {
		t.Fatalf("失败 2 轮应仍保持可用+刷新中: adbOK=%v failing=%v", s.AdbOK, s.AdbFailing)
	}

	a.pollOnce(ctx) // 失败 #3 → 达阈值
	s = a.Snapshot()
	if s.AdbOK || s.AdbFailing {
		t.Fatalf("失败达阈值应转不可用（红条）: adbOK=%v failing=%v", s.AdbOK, s.AdbFailing)
	}

	a.pollOnce(ctx) // 恢复成功
	s = a.Snapshot()
	if !s.AdbOK || s.AdbFailing || len(s.Devices) != 1 {
		t.Fatalf("恢复后应复位并刷新设备: adbOK=%v failing=%v devices=%d",
			s.AdbOK, s.AdbFailing, len(s.Devices))
	}
}

// 真空期去抖（任务一）：真空期间保留上次设备列表（不因失败清空），
// 失败计数不跨成功复位。
func TestAdbFailDebounceKeepsLastDevices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "count.txt")
	fake := filepath.Join(dir, "adb")
	// devices -l：第 1 次成功（带 1 台设备），第 2-3 次失败（真空），第 4 次起成功
	script := "#!/bin/sh\n" +
		"if [ \"$1\" != \"devices\" ]; then exit 0; fi\n" +
		"c=$(cat \"" + counter + "\" 2>/dev/null); c=${c:-0}\n" +
		"c=$((c+1)); echo $c > \"" + counter + "\"\n" +
		"if [ $c -ge 2 ] && [ $c -le 3 ]; then echo 'adb server down' >&2; exit 1; fi\n" +
		"printf 'List of devices attached\\n12345678 device usb:2-1 product:Fake model:Fake device:Fake\\n\\n'\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := New(Config{AdbPath: fake, Version: "test"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	a.pollOnce(ctx) // 成功：基线设备列表
	if s := a.Snapshot(); !s.AdbOK || s.AdbFailing || len(s.Devices) != 1 {
		t.Fatalf("首轮成功后应可用且有 1 台设备: %+v", s)
	}

	a.pollOnce(ctx) // 失败 #1（真空）
	s := a.Snapshot()
	if !s.AdbOK || !s.AdbFailing || len(s.Devices) != 1 {
		t.Fatalf("真空期应保留上次设备列表: adbOK=%v failing=%v devices=%d",
			s.AdbOK, s.AdbFailing, len(s.Devices))
	}

	a.pollOnce(ctx) // 失败 #2（仍在阈值内）
	s = a.Snapshot()
	if !s.AdbOK || !s.AdbFailing || len(s.Devices) != 1 {
		t.Fatalf("失败 2 轮应仍保留列表: adbOK=%v failing=%v devices=%d",
			s.AdbOK, s.AdbFailing, len(s.Devices))
	}

	a.pollOnce(ctx) // 恢复成功
	s = a.Snapshot()
	if !s.AdbOK || s.AdbFailing || len(s.Devices) != 1 {
		t.Fatalf("恢复后应复位: adbOK=%v failing=%v devices=%d", s.AdbOK, s.AdbFailing, len(s.Devices))
	}
}
