package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- 轮 B 多会话测试设施 ---

// fakeRecorder 每次工厂调用都新建一个 fakeRunner 并按 serial 记录
// （重启/重投会创建新实例；旧实例可断言 Stop 被调用）。
type fakeRecorder struct {
	mu sync.Mutex
	by map[string][]*fakeRunner
}

func (r *fakeRecorder) add(serial string, f *fakeRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.by[serial] = append(r.by[serial], f)
}

func (r *fakeRecorder) serial(serial string) []*fakeRunner {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*fakeRunner{}, r.by[serial]...)
}

func (r *fakeRecorder) count(serial string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.by[serial])
}

func multiTestApp() (*App, *fakeRecorder) {
	rec := &fakeRecorder{by: map[string][]*fakeRunner{}}
	a := New(Config{BatPath: "C:\\x\\投屏启动.bat", AdbPath: "C:\\x\\adb.exe", Version: "test"})
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		f := &fakeRunner{exitCode: -1}
		rec.add(serial, f)
		return f, nil
	})
	return a, rec
}

func waitCount(t *testing.T, rec *fakeRecorder, serial string, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if rec.count(serial) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待会话 %s 第 %d 次 Start 超时（当前 %d）", serial, n, rec.count(serial))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sessionBySerial(t *testing.T, a *App, serial string) Session {
	t.Helper()
	for _, s := range a.Snapshot().Sessions {
		if s.Serial == serial {
			return s
		}
	}
	t.Fatalf("快照中找不到会话 %s: %+v", serial, a.Snapshot().Sessions)
	return Session{}
}

func setDevices(a *App, devs []adb.Device) {
	a.mu.Lock()
	a.devices = devs
	a.mu.Unlock()
}

// --- 多会话状态机 ---

// 不同设备可并行开会话；NO_WATCH 已废弃（v3）：普通/并行一律不注入
// SCEZ_NO_WATCH=1——watcher 会话化后（独立 TAG/flag/只关本会话 + 市场名
// "对上才切"）并行会话各自 watcher 互不干扰，插线切换对每个会话都生效
// （修复"插线不切有线"）。两个会话都注入各自的 SCEZ_SERIAL。
func TestMultiSessionParallelStart(t *testing.T) {
	a, rec := multiTestApp()
	setDevices(a, []adb.Device{
		{Serial: "A", State: "device", ConnType: "usb", Name: "设备A", Identity: "devA"},
		{Serial: "B", State: "device", ConnType: "usb", Name: "设备B", Identity: "devB"},
	})
	if err := a.StartCast("A"); err != nil {
		t.Fatal(err)
	}
	if err := a.StartCast("B"); err != nil {
		t.Fatal(err)
	}
	pA := rec.serial("A")[0].waitParams(t, 1)
	pB := rec.serial("B")[0].waitParams(t, 1)
	if pA.NoWatch {
		t.Fatalf("NO_WATCH 已废弃：首个会话不应注入: %+v", pA)
	}
	if pB.NoWatch {
		t.Fatalf("NO_WATCH 已废弃：并行会话也不应注入（watcher 会话化互不干扰）: %+v", pB)
	}
	if pA.Serial != "A" || pB.Serial != "B" {
		t.Fatalf("SCEZ_SERIAL 应按会话注入: %+v / %+v", pA, pB)
	}
	ss := a.Snapshot().Sessions
	if len(ss) != 2 || !ss[0].Active || !ss[1].Active {
		t.Fatalf("应有 2 个活动会话: %+v", ss)
	}
}

// 同 serial 重复 Start 拒绝"已在投屏"；同 identity（卡片重键/双卡）也拒绝——
// 同一设备不能开两个会话。
func TestMultiSessionSameDeviceRejected(t *testing.T) {
	a, _ := multiTestApp()
	setDevices(a, []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro",
			Identity: "Xiaomi Pad 8 Pro", Wireless: "192.168.31.162:5555"},
	})
	_ = a.StartCast("a743e1df")
	if err := a.StartCast("a743e1df"); err == nil || !strings.Contains(err.Error(), "投屏已在运行") {
		t.Fatalf("同 serial 重复 Start 应拒绝: %v", err)
	}
	// 卡片重键：同 identity 的旧无线 serial 再开 → 拒绝
	if err := a.StartCast("192.168.31.162:5555"); err == nil || !strings.Contains(err.Error(), "投屏已在运行") {
		t.Fatalf("同 identity 并行会话应拒绝: %v", err)
	}
	// 结束态后可再开（原地替换，不开第二条）
	a.OnBatExit("a743e1df", 0)
	if err := a.StartCast("a743e1df"); err != nil {
		t.Fatalf("结束态重新投屏应成功: %v", err)
	}
	if len(a.Snapshot().Sessions) != 1 {
		t.Fatalf("结束态重投不应产生第二个会话条目: %+v", a.Snapshot().Sessions)
	}
}

// 事件按会话分发：A 的规格/日志/阶段不污染 B；无关串号安全忽略。
func TestMultiSessionEventRouting(t *testing.T) {
	a, _ := multiTestApp()
	_ = a.StartCast("A")
	_ = a.StartCast("B")

	a.NotifyLine("A", "[高清] 有线模式：检测到设备 2136x3200@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化）")
	a.NotifyLine("B", "[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps）")

	sA := sessionBySerial(t, a, "A")
	sB := sessionBySerial(t, a, "B")
	if sA.Cast.Spec == nil || !sA.Cast.Spec.Wired || sA.Cast.Spec.Mbps != 80 {
		t.Fatalf("A 的规格被污染: %+v", sA.Cast.Spec)
	}
	if sB.Cast.Spec == nil || sB.Cast.Spec.Wired || sB.Cast.Spec.Mbps != 15 {
		t.Fatalf("B 的规格被污染: %+v", sB.Cast.Spec)
	}
	if sA.Cast.Mode != "usb" || sB.Cast.Mode != "wifi" {
		t.Fatalf("模式按会话判定错误: A=%q B=%q", sA.Cast.Mode, sB.Cast.Mode)
	}
	if len(sA.Cast.Log) != 1 || len(sB.Cast.Log) != 1 {
		t.Fatalf("日志未按会话独立: A=%d B=%d", len(sA.Cast.Log), len(sB.Cast.Log))
	}

	// 独立日志缓冲：A 灌 80 行截断为 60；B 保持 1 行
	for i := 0; i < 80; i++ {
		a.NotifyLine("A", "普通日志行 "+itoa(i))
	}
	sA = sessionBySerial(t, a, "A")
	sB = sessionBySerial(t, a, "B")
	if len(sA.Cast.Log) != 60 || !strings.Contains(sA.Cast.Log[59], "79") {
		t.Fatalf("A 日志应截断为最新 60 行: %d", len(sA.Cast.Log))
	}
	if len(sB.Cast.Log) != 1 {
		t.Fatalf("B 日志不应受 A 影响: %d", len(sB.Cast.Log))
	}

	// 无关串号：不 panic、不产生会话
	a.NotifyLine("C", "随便一行")
	a.OnBatExit("C", 0)
	if len(a.Snapshot().Sessions) != 2 {
		t.Fatalf("无关串号不应产生会话: %+v", a.Snapshot().Sessions)
	}

	// 兼容字段 Cast = 最近启动的活动会话（B 后启动 → B）
	a.mu.Lock()
	a.sessions["A"].startedAt = time.Unix(1000, 0)
	a.sessions["B"].startedAt = time.Unix(2000, 0)
	a.mu.Unlock()
	if c := a.Snapshot().Cast; c.Serial != "B" {
		t.Fatalf("Snapshot.Cast 应取最近启动的活动会话: %q", c.Serial)
	}
}

// 参数覆盖（nextParams）按 serial 隔离：A 的保存不注入 B 的会话。
func TestMultiSessionParamsPerSerial(t *testing.T) {
	a, rec := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb"}, {Serial: "B", State: "device", ConnType: "usb"}})
	_ = a.StartCast("A")
	_ = a.StartCast("B")
	rec.serial("A")[0].waitParams(t, 1)
	rec.serial("B")[0].waitParams(t, 1)

	if err := a.SaveProfileAndRestart("A", "usb", 2400, 75, 55, true); err != nil {
		t.Fatal(err)
	}
	a.OnBatExit("A", 1)
	waitCount(t, rec, "A", 2)
	// 重启后的 A 会话带 usb 覆盖参数（SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB）
	if p := rec.serial("A")[1].waitParams(t, 1); !p.Usb.Set || p.Usb.Res != 2400 || p.Usb.FPS != 75 || p.Usb.Bitrate != 55 {
		t.Fatalf("A 重启未注入保存的 usb 覆盖: %+v", p)
	}
	// B 的会话完全不受影响（仍只有 1 次 Start，无覆盖参数）
	if rec.count("B") != 1 {
		t.Fatalf("B 不应被重启: %d", rec.count("B"))
	}
}

// --- 并行 Stop / Restart（按会话） ---

func TestMultiSessionStopOnlyTarget(t *testing.T) {
	a, rec := multiTestApp()
	_ = a.StartCast("A")
	_ = a.StartCast("B")
	if err := a.StopCast("A"); err != nil {
		t.Fatal(err)
	}
	rec.serial("A")[0].waitStopped(t) // gui5：杀树异步
	if rec.serial("B")[0].isStopped() {
		t.Fatal("StopCast(A) 不应停止 B 的 bat")
	}
	// 不存在串号：幂等静默
	if err := a.StopCast("NOPE"); err != nil {
		t.Fatalf("不存在串号应幂等: %v", err)
	}
}

func TestMultiSessionRestartOnlyTarget(t *testing.T) {
	a, rec := multiTestApp()
	_ = a.StartCast("A")
	_ = a.StartCast("B")
	if err := a.RestartCast("A"); err != nil {
		t.Fatal(err)
	}
	rec.serial("A")[0].waitStopped(t) // gui5：重启杀树也异步
	if rec.serial("B")[0].isStopped() {
		t.Fatal("RestartCast(A) 不应杀 B 的 bat")
	}
	// 模拟 A 的 waitLoop 退出回调 → restartAfterExit 重跑 A
	a.OnBatExit("A", 1)
	waitCount(t, rec, "A", 2)
	if rec.count("B") != 1 {
		t.Fatalf("B 不应被重启: %d", rec.count("B"))
	}
	sA := sessionBySerial(t, a, "A")
	if !sA.Active {
		t.Fatalf("A 重启后应活动: %+v", sA)
	}
	sB := sessionBySerial(t, a, "B")
	if !sB.Active {
		t.Fatalf("B 不应受影响: %+v", sB)
	}
}

// NO_WATCH 已废弃（v3）：并行会话（StartCastParallel）与重启都不再注入
// SCEZ_NO_WATCH=1——重启后 watcher 照常开启（会话化隔离已足够安全）。
func TestMultiSessionRestartNoNoWatch(t *testing.T) {
	a, rec := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb"}, {Serial: "B", State: "device", ConnType: "usb"}})
	_ = a.StartCast("A")
	_ = a.StartCastParallel("B")
	if p := rec.serial("B")[0].waitParams(t, 1); p.NoWatch {
		t.Fatalf("StartCastParallel 不应注入 NO_WATCH（已废弃）: %+v", p)
	}
	if err := a.RestartCast("B"); err != nil {
		t.Fatal(err)
	}
	a.OnBatExit("B", 1)
	waitCount(t, rec, "B", 2)
	if p := rec.serial("B")[1].waitParams(t, 1); p.NoWatch {
		t.Fatalf("并行会话重启也不应注入 NO_WATCH: %+v", p)
	}
}

// 多会话并发操作（Stop/Restart/Start/NotifyLine/Snapshot 混合）-race 下无死锁/无 panic。
func TestMultiSessionConcurrentOps(t *testing.T) {
	a, _ := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb"}, {Serial: "B", State: "device", ConnType: "usb"}})
	_ = a.StartCast("A")
	_ = a.StartCast("B")

	var wg sync.WaitGroup
	serials := []string{"A", "B"}
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ser := serials[i%len(serials)]
			switch i % 5 {
			case 0:
				a.NotifyLine(ser, "[提示] 检测到连接断开（退出码 1），2 秒后自动重连...")
			case 1:
				_ = a.StopCast(ser)
			case 2:
				_ = a.RestartCast(ser) // 可能触发后台 restartAfterExit（无害）
			case 3:
				a.OnBatExit(ser, 1) // 触发 waitLoop 回调 → 重启链完成
			case 4:
				_ = a.StartCast(ser) // 已在投屏 → 部分失败无妨
			}
			a.Snapshot()
		}(i)
	}
	wg.Wait()
	// 收尾：收敛重启链（Stop + 退出回调循环直至无活动会话且无重启闩锁）
	settled := false
	for i := 0; i < 60 && !settled; i++ {
		a.mu.RLock()
		active, restarting := 0, 0
		for _, st := range a.sessions {
			if st.runner != nil {
				active++
			}
			if st.restarting {
				restarting++
			}
		}
		a.mu.RUnlock()
		if active == 0 && restarting == 0 {
			settled = true
			break
		}
		for _, ser := range serials {
			_ = a.StopCast(ser)
			a.OnBatExit(ser, 1)
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.ResetCast()
	if !settled {
		t.Fatalf("并发操作后未收敛: %+v", a.Snapshot().Sessions)
	}
	if len(a.Snapshot().Sessions) != 0 {
		t.Fatalf("并发操作后清空失败: %+v", a.Snapshot().Sessions)
	}
}

// --- 淡出生命周期 ---

// 会话结束 → 条目保留为 inactive（供前端淡出动画）→ ForgetSession 移除；
// 运行中会话 ForgetSession 不移除；结束态超时 GC 兜底防泄漏。
func TestMultiSessionFadeLifecycle(t *testing.T) {
	a, _ := multiTestApp()
	_ = a.StartCast("A")
	_ = a.StartCast("B")

	a.OnBatExit("A", 0)
	sA := sessionBySerial(t, a, "A")
	if sA.Active {
		t.Fatalf("A 退出后应 inactive（前端淡出信号）: %+v", sA)
	}
	sB := sessionBySerial(t, a, "B")
	if !sB.Active {
		t.Fatalf("B 不应受影响: %+v", sB)
	}

	// 淡出完成 → 后端移除
	a.ForgetSession("A")
	if len(a.Snapshot().Sessions) != 1 {
		t.Fatalf("ForgetSession(A) 后应只剩 B: %+v", a.Snapshot().Sessions)
	}

	// 运行中会话 ForgetSession 不移除（重启复活/动画误触发防御）
	a.ForgetSession("B")
	if len(a.Snapshot().Sessions) != 1 {
		t.Fatalf("运行中会话不应被 ForgetSession 移除: %+v", a.Snapshot().Sessions)
	}

	// 结束态超时 GC：endedAt 推到保留期前 → promptTick 移除
	a.OnBatExit("B", 1)
	a.mu.Lock()
	a.sessions["B"].endedAt = time.Now().Add(-deadSessionKeep - time.Second)
	a.mu.Unlock()
	a.promptTick()
	if len(a.Snapshot().Sessions) != 0 {
		t.Fatalf("超时 GC 应移除结束态会话: %+v", a.Snapshot().Sessions)
	}
}

// --- 会话顺序稳定 ---

// Sessions 按 StartCast 时间排序；同刻按 serial 稳定（设备名键序）。
func TestMultiSessionOrdering(t *testing.T) {
	a, _ := multiTestApp()
	_ = a.StartCast("B")
	_ = a.StartCast("A")
	a.mu.Lock()
	a.sessions["B"].startedAt = time.Unix(1000, 0)
	a.sessions["A"].startedAt = time.Unix(1000, 0) // 同刻 → serial 升序
	a.mu.Unlock()
	ss := a.Snapshot().Sessions
	if len(ss) != 2 || ss[0].Serial != "A" || ss[1].Serial != "B" {
		t.Fatalf("同刻应按 serial 稳定排序: %+v", ss)
	}
	a.mu.Lock()
	a.sessions["A"].startedAt = time.Unix(2000, 0)
	a.mu.Unlock()
	ss = a.Snapshot().Sessions
	if len(ss) != 2 || ss[0].Serial != "B" || ss[1].Serial != "A" {
		t.Fatalf("应按 StartCast 时间排序: %+v", ss)
	}
}

// --- 新设备弹窗（轮 B 目标 1） ---

// 设备轮询发现"在线且无会话"的设备 → 弹窗；一次一个；
// 【开始投屏】= StartCastParallel → 并行会话（NO_WATCH 已废弃不注入）+ 弹窗清除 → 下一个设备再弹。
func TestNewDevicePopupFlow(t *testing.T) {
	a, rec := multiTestApp()
	devs := []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "Redmi K80", Identity: "Redmi K80"},
	}
	setDevices(a, devs)

	// 首次检测：弹第一台
	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "a743e1df" || np.Name != "Xiaomi Pad 8 Pro" || np.ConnType != "usb" {
		t.Fatalf("首台在线设备应弹窗: %+v", np)
	}

	// 已有弹窗：不覆盖（一次一个）
	a.applyTrackUpdate(devs)
	if np = a.Snapshot().NewDevice; np == nil || np.Serial != "a743e1df" {
		t.Fatalf("已有弹窗不应被第二台覆盖: %+v", np)
	}

	// 【开始投屏】→ 并行会话 + 弹窗清除
	if err := a.StartCastParallel(np.Serial); err != nil {
		t.Fatal(err)
	}
	if a.Snapshot().NewDevice != nil {
		t.Fatal("开始投屏后弹窗应清除")
	}
	if p := rec.serial("a743e1df")[0].waitParams(t, 1); p.NoWatch || p.Serial != "a743e1df" {
		t.Fatalf("弹窗开始投屏应走并行会话（SCEZ_SERIAL；NO_WATCH 已废弃不注入）: %+v", p)
	}

	// 第二台设备：事件驱动需要一次新的 device 事件（快照变化）才会弹出
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	np = a.Snapshot().NewDevice
	if np == nil || np.Serial != "601c9f08" || np.Name != "Redmi K80" {
		t.Fatalf("第二台设备应接着弹: %+v", np)
	}
}

// 【暂不】→ 本在线周期不再弹（用户可手动点投屏）；设备离线再上线 → 新周期可再弹。
func TestNewDevicePopupDismissCycle(t *testing.T) {
	a, _ := multiTestApp()
	devs := []adb.Device{{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"}}
	setDevices(a, devs)

	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice == nil {
		t.Fatal("应弹窗")
	}
	a.DismissNewDevice("a743e1df")
	if a.Snapshot().NewDevice != nil {
		t.Fatal("暂不后弹窗应关闭")
	}
	// 本在线周期：不再弹
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatal("暂不后同在线周期不应再弹")
	}

	// 设备离线（列表空）→ 新在线周期 → 可再弹
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	if np := a.Snapshot().NewDevice; np == nil || np.Serial != "a743e1df" {
		t.Fatalf("离线再上线应重新允许弹窗: %+v", np)
	}
}

// 防重复：同设备 30s 内不重复弹；弹窗未处理 30s 自动消失后可再弹。
func TestNewDevicePopupThrottleAndExpire(t *testing.T) {
	a, _ := multiTestApp()
	devs := []adb.Device{{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"}}
	setDevices(a, devs)

	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice == nil {
		t.Fatal("应弹窗")
	}
	// 弹窗被清（用户忽略/过期）后 30s 内不重复弹
	a.mu.Lock()
	a.popup.info = nil
	a.mu.Unlock()
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatal("30s 防重复窗口内不应再弹")
	}

	// 30s 后：可再弹（需要一次新的 device 事件）
	a.mu.Lock()
	a.popup.lastShown["Xiaomi Pad 8 Pro"] = time.Now().Add(-31 * time.Second)
	a.mu.Unlock()
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	if np := a.Snapshot().NewDevice; np == nil || np.Serial != "a743e1df" {
		t.Fatalf("30s 后应允许再弹: %+v", np)
	}

	// 未处理弹窗 30s 自动消失（下一次事件判定）
	a.mu.Lock()
	a.popup.shownAt = time.Now().Add(-31 * time.Second)
	a.popup.lastShown["Xiaomi Pad 8 Pro"] = time.Now().Add(-31 * time.Second)
	a.mu.Unlock()
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	if np := a.Snapshot().NewDevice; np == nil {
		t.Fatal("未处理超时后应清除并允许重新弹出")
	}
}

// 已有会话的设备不弹窗（identity 匹配；含刚结束未 GC 的会话——防"停止后又弹"）。
func TestNewDevicePopupSkipsCastingDevice(t *testing.T) {
	a, _ := multiTestApp()
	devs := []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "Redmi K80", Identity: "Redmi K80"},
	}
	setDevices(a, devs)
	_ = a.StartCast("a743e1df")

	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "601c9f08" {
		t.Fatalf("投屏中的设备不应弹窗，应弹 K80: %+v", np)
	}

	// 停止后（结束态条目仍在）下一轮仍不弹该设备
	a.DismissNewDevice("601c9f08") // 清掉 K80 弹窗
	a.OnBatExit("a743e1df", 0)
	a.applyTrackUpdate(devs)
	np = a.Snapshot().NewDevice
	if np != nil && np.Serial == "a743e1df" {
		t.Fatalf("刚结束的设备不应立刻再弹: %+v", np)
	}

	// 结束态 GC 后（新会话周期）→ 需要一次新的 device 事件才可再弹
	a.ForgetSession("a743e1df")
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	np = a.Snapshot().NewDevice
	if np == nil || np.Serial != "a743e1df" {
		t.Fatalf("结束态移除后应可再弹: %+v", np)
	}
}

// pollOnce 全链路：假 adb 输出一台在线设备 → 弹窗出现；【开始投屏】→ 会话创建。
func TestPollOnceDetectsNewDevicePopup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n601c9f08\\tdevice\\n'; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := &fakeRecorder{by: map[string][]*fakeRunner{}}
	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		f := &fakeRunner{exitCode: -1}
		rec.add(serial, f)
		return f, nil
	})

	a.pollOnce(context.Background())
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "601c9f08" || np.ConnType != "usb" {
		t.Fatalf("pollOnce 应发现新设备弹窗: %+v", np)
	}
	if err := a.StartCastParallel(np.Serial); err != nil {
		t.Fatal(err)
	}
	if a.Snapshot().NewDevice != nil {
		t.Fatal("开始投屏后弹窗应清除")
	}
	if p := rec.serial("601c9f08")[0].waitParams(t, 1); p.NoWatch {
		t.Fatalf("弹窗开始投屏不应注入 NO_WATCH（已废弃）: %+v", p)
	}
	if len(a.Snapshot().Sessions) != 1 || !a.Snapshot().Sessions[0].Active {
		t.Fatalf("弹窗开始投屏应创建会话: %+v", a.Snapshot().Sessions)
	}
}
