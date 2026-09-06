package app

import (
	"errors"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- 任务四：停止投屏状态反馈（"正在终止…"）Go 侧状态机 ---

// errStopRunner 模拟 Stop 报错（杀树失败）的 runner：记录调用次数并返回错误。
type errStopRunner struct {
	fakeRunner
}

func (e *errStopRunner) Stop() error {
	e.fakeRunner.mu.Lock()
	e.fakeRunner.stopped = true
	e.fakeRunner.stops++
	e.fakeRunner.mu.Unlock()
	return errors.New("kill failed")
}

// 停止链路：StopCast 受理 → Snapshot.Stopping=true（Active 仍 true）→
// OnBatExit → Stopping/Active 双复位（前端"正在终止…" → 标签淡出）。
// gui5：杀树已异步——受理即返回，Stop 调用稍后落地（waitStopped）。
func TestStopCastStoppingFlagFlow(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	f.waitStarts(t, 1)

	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	f.waitStopped(t)
	s := sessionBySerial(t, a, "X")
	if !s.Active || !s.Stopping {
		t.Fatalf("停止受理后应 Active+Stopping: %+v", s)
	}

	a.OnBatExit("X", 0)
	s = sessionBySerial(t, a, "X")
	if s.Active || s.Stopping {
		t.Fatalf("bat 退出后应双复位: %+v", s)
	}
}

// 停止中重复点击：幂等静默，不二次杀树（前端同步禁用按钮；后端兜底闸）。
func TestStopCastIdempotentWhileStopping(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	f.waitStarts(t, 1)
	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	f.waitStops(t, 1) // 等首次异步杀树落地
	if err := a.StopCast("X"); err != nil {
		t.Fatalf("停止中重复点击应幂等: %v", err)
	}
	time.Sleep(80 * time.Millisecond) // 幂等路径不触 runner：给足观察窗口
	if n := f.stopsN(); n != 1 {
		t.Fatalf("停止中重复点击不应二次杀树: %d", n)
	}
}

// Stop 报错（杀树失败）→ 复位 Stopping，按钮恢复"停止投屏"可重试；
// 会话状态不变（Active 仍 true——现行为）。
// gui5：报错已异步（受理即返回 nil，错误只记日志不回传 RPC），
// 复位由快照轮询（~700ms）送达前端。
func TestStopCastErrorResetsStopping(t *testing.T) {
	e := &errStopRunner{}

	a := New(Config{})
	a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return e, nil })
	_ = a.StartCast("X")
	e.waitStarts(t, 1)

	if err := a.StopCast("X"); err != nil {
		t.Fatalf("异步 StopCast 受理即返回 nil: %v", err)
	}
	e.waitStops(t, 1)
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := sessionBySerial(t, a, "X")
		if !s.Stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("杀树失败后未复位 Stopping")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s := sessionBySerial(t, a, "X")
	if !s.Active {
		t.Fatalf("杀树失败后应保持 Active（会话状态不变，可重试）: %+v", s)
	}
	// 可重试：第二次 Stop 再次调 runner
	if err := a.StopCast("X"); err != nil {
		t.Fatalf("重试受理应返回 nil: %v", err)
	}
	e.waitStops(t, 2)
}

// Stop 成功但 bat 超时未退出 → 兜底复位 Stopping
// （会话状态不变：Active 仍 true、runner 未释放），按钮恢复可重试。
func TestStopCastTimeoutResetsStopping(t *testing.T) {
	old := stopResetDuration()
	setStopResetDuration(60 * time.Millisecond)
	defer setStopResetDuration(old)

	a, f := newTestApp()
	_ = a.StartCast("X")
	f.waitStarts(t, 1)
	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	if s := sessionBySerial(t, a, "X"); !s.Stopping {
		t.Fatalf("受理后应立即 Stopping: %+v", s)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		s := sessionBySerial(t, a, "X")
		if !s.Stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("停止超时未复位 Stopping")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := sessionBySerial(t, a, "X")
	if !s.Active {
		t.Fatalf("超时复位只影响按钮状态，会话应保持活动: %+v", s)
	}
	a.mu.RLock()
	runnerAlive := a.sessions["X"] != nil && a.sessions["X"].runner != nil
	a.mu.RUnlock()
	if !runnerAlive {
		t.Fatal("超时复位不应释放 runner（会话状态不变）")
	}
}

// 卡片重键场景：会话键=无线地址、StopCast 传 USB serial → identity 兜底
// 定位到会话并置 Stopping（前端设备卡按 identity 绑定按钮状态）。
func TestStopCastStoppingViaIdentityFallback(t *testing.T) {
	a, rec := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C",
			[]string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	})
	setDevices(a, []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Identity: "Xiaomi Pad 8 Pro"},
	})
	if err := a.StartCast("192.168.31.162:5555"); err != nil {
		t.Fatal(err)
	}
	rec.serial("192.168.31.162:5555")[0].waitStarts(t, 1)

	if err := a.StopCast("a743e1df"); err != nil {
		t.Fatal(err)
	}
	s := sessionBySerial(t, a, "192.168.31.162:5555")
	if !s.Stopping || !s.Active {
		t.Fatalf("identity 兜底停止应置 Stopping: %+v", s)
	}
	rec.serial("192.168.31.162:5555")[0].waitStopped(t) // gui5：杀树异步
}

// --- 任务（gui6）：closing 状态（投屏窗口点 X 关闭 → "正在关闭…"） ---

const windowCloseLine = "[提示] 已检测到窗口关闭（退出码 0），投屏已结束，退出投屏循环"

// 窗口 X 关闭：KindDone 且含"已检测到窗口关闭" → Closing=true 透传 Snapshot
// （Active 仍 true——bat 清理阶段）；OnBatExitFor 统一复位 Closing（连同 Stopping）。
func TestWindowCloseSetsClosingAndOnExitResets(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")

	a.NotifyLine("X", windowCloseLine)
	s := sessionBySerial(t, a, "X")
	if !s.Closing || !s.Active {
		t.Fatalf("窗口关闭后应 Active+Closing（清理阶段）: %+v", s)
	}
	if s.Stopping {
		t.Fatalf("窗口关闭路径不应置 Stopping: %+v", s)
	}

	a.OnBatExit("X", 0)
	s = sessionBySerial(t, a, "X")
	if s.Closing || s.Active || s.Stopping {
		t.Fatalf("bat 退出后 Closing/Active/Stopping 应全复位: %+v", s)
	}
}

// closing 与 stopping 并存：主动停止 + 窗口关闭行都到达 → 两标志都透传
// （前端显示 stopping 优先"正在终止…"）；bat 退出统一复位。
func TestClosingAndStoppingCoexistThenReset(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	f.waitStarts(t, 1)

	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	a.NotifyLine("X", windowCloseLine)
	s := sessionBySerial(t, a, "X")
	if !s.Stopping || !s.Closing || !s.Active {
		t.Fatalf("stopping+closing 应并存透传: %+v", s)
	}
	f.waitStopped(t)

	a.OnBatExit("X", 0)
	s = sessionBySerial(t, a, "X")
	if s.Stopping || s.Closing || s.Active {
		t.Fatalf("onExit 应同时复位 stopping+closing: %+v", s)
	}
}

// 其余"投屏已结束"Done 行（非窗口关闭）不置 Closing（避免误报）。
func TestGenericDoneLineDoesNotSetClosing(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")

	a.NotifyLine("X", "投屏已结束，感谢使用")
	s := sessionBySerial(t, a, "X")
	if s.Closing {
		t.Fatalf("普通 Done 行不应置 Closing: %+v", s)
	}
	if !s.Active {
		t.Fatalf("普通 Done 行会话应仍 Active（bat 清理中）: %+v", s)
	}
}

// 哨兵行（SCRCPY_EZ_USER_CLOSE）：用户关窗瞬间立即置 Closing——
// Active/Phase 不动（scrcpy 可能还没退出，正在投屏中）；
// bat 随后的"已检测到窗口关闭"再置位幂等；onExit 统一复位。
func TestUserCloseSentinelSetsClosingImmediately(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "===== 开始投屏：X =====") // phase=casting

	a.NotifyLine("X", "SCRCPY_EZ_USER_CLOSE")
	s := sessionBySerial(t, a, "X")
	if !s.Closing || !s.Active {
		t.Fatalf("哨兵行应立即置 Closing 且保持 Active: %+v", s)
	}
	if s.Cast.Phase != "casting" || s.Cast.PhaseText != "投屏中" {
		t.Fatalf("哨兵行不应动 Phase（scrcpy 尚未退出）: %+v", s.Cast)
	}

	// bat 行后到：幂等仍 closing；onExit 复位
	a.NotifyLine("X", windowCloseLine)
	s = sessionBySerial(t, a, "X")
	if !s.Closing {
		t.Fatalf("bat 窗口关闭行后 Closing 应保持: %+v", s)
	}
	a.OnBatExit("X", 0)
	s = sessionBySerial(t, a, "X")
	if s.Closing || s.Active {
		t.Fatalf("onExit 应复位 Closing/Active: %+v", s)
	}
}

// 哨兵行不得影响其他会话（多会话隔离：只有目标会话置 closing）。
func TestUserCloseSentinelSessionIsolation(t *testing.T) {
	a, _ := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb"}, {Serial: "B", State: "device", ConnType: "usb"}})
	_ = a.StartCast("A")
	_ = a.StartCast("B")

	a.NotifyLine("A", "SCRCPY_EZ_USER_CLOSE")
	sA := sessionBySerial(t, a, "A")
	sB := sessionBySerial(t, a, "B")
	if !sA.Closing || sB.Closing {
		t.Fatalf("哨兵行只应置 A 的 Closing: A=%+v B=%+v", sA, sB)
	}
}

// --- 任务一：设备参数管理页 Go 侧（SaveProfile 仅保存 + 档案列表） ---

// SaveProfile 仅写 profiles.json：不杀树、不重启、不注入参数覆盖；
// 另一模式档不受影响；非法参数报错。
func TestSaveProfileOnlyWritesNoRestart(t *testing.T) {
	a, f := newTestApp()
	_ = a.StartCast("X")
	f.waitStarts(t, 1)

	if err := a.SaveProfile("X", "usb", 2400, 90, 55, true); err != nil {
		t.Fatal(err)
	}
	p := a.profiles.Get("X")
	if !p.Usb.Custom || p.Usb.Res != 2400 || p.Usb.FPS != 90 || p.Usb.Bitrate != 55 {
		t.Fatalf("usb 档未保存: %+v", p.Usb)
	}
	if p.Wifi.Custom || p.Wifi.Res != 1920 {
		t.Fatalf("wifi 档不应被改: %+v", p.Wifi)
	}
	if f.isStopped() {
		t.Fatal("SaveProfile 不应杀树")
	}
	if f.startsN() != 1 {
		t.Fatalf("SaveProfile 不应重启（Start 次数=%d）", f.startsN())
	}
	a.mu.RLock()
	np := a.nextParams["X"]
	a.mu.RUnlock()
	if np.Usb.Set {
		t.Fatalf("SaveProfile 不应注入参数覆盖: %+v", np)
	}
	if err := a.SaveProfile("X", "usb", 0, 90, 55, true); err == nil {
		t.Fatal("非法参数应报错")
	}
}

// 档案列表：Snapshot.Profiles 含全部档案（含离线）、按展示名排序、
// 展示名=市场名、addrs 透传（active 优先）。
func TestListProfilesInSnapshot(t *testing.T) {
	a, _ := multiTestApp()
	if got := a.Snapshot().Profiles; len(got) != 0 {
		t.Fatalf("空档案应为空列表: %+v", got)
	}

	seedProfiles(a, map[string]*DeviceEntry{
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C",
			[]string{"a743e1df"}, []string{"192.168.31.162:5555"}),
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC",
			[]string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	items := a.Snapshot().Profiles
	if len(items) != 2 {
		t.Fatalf("应有 2 个档案条目: %+v", items)
	}
	// 排序稳定：按展示名升序（REDMI K80 < Xiaomi Pad 8 Pro）
	if items[0].Key != "REDMI K80" || items[0].Name != "REDMI K80" ||
		items[1].Key != "Xiaomi Pad 8 Pro" || items[1].Name != "Xiaomi Pad 8 Pro" {
		t.Fatalf("排序/命名错误: %+v", items)
	}
	if items[1].Model != "25091RP04C" || len(items[1].Serials) != 1 ||
		items[1].Serials[0] != "a743e1df" || len(items[1].Addrs) != 1 ||
		items[1].Addrs[0] != "192.168.31.162:5555" {
		t.Fatalf("条目内容错误: %+v", items[1])
	}
}
