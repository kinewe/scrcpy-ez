package app

// gui35 测试：切换/重连事件清 closing——「正在关闭…」在自动切有线后不复位
// 卡住的修复（无线投屏 → 插线 → bat 自动切有线：旧的无线 scrcpy 窗口关闭置
// closing=true 后 bat 不退出、切到有线分支继续跑 → 原状态机无复位路径）。
// 修复口径：KindSwitchUSB/KindADBReset/KindDetect/KindReconnect（"重新检测/
// 切换"事件 = 同一投屏进入新阶段，不是关闭）与 KindSpec（切换完成的新分支
// 规格）到达 → closing=false（切换/重连顺带清 stopping）；普通窗口关闭路径
// （无切换事件）→ closing 保持 → bat 退出统一复位（回归不变）。

import (
	"testing"
)

const (
	gui35SwitchUSBLine = "[自动切换] 检测到 USB 插线，切换至有线投屏..."
	gui35ReconnectLine = "[提示] 检测到连接断开（退出码 1），2 秒后自动重连..."
	gui35ADBResetLine  = "[1] 重置 adb 服务..."
	gui35DetectLine    = "[2] 检测 USB 设备..."
	gui35WiredSpecLine = "[高清] 有线模式：检测到设备 2560x1708@120Hz，有线规格 h264/50M/2560/120fps（低延迟优化）"
)

// TestGui35SwitchUSBEventClearsClosing：置 closing（+stopping）后收到
// KindSwitchUSB（bat 自动切有线）→ closing=false、stopping=false（切换 = 投屏
// 继续，不是关闭/停止）；phaseText/Phase 照常更新为切换文案；随后新分支的
// 有线规格行到达 → 规格/模式刷新（closing 保持 false）；会话仍 Active（bat 未退）。
func TestGui35SwitchUSBEventClearsClosing(t *testing.T) {
	a, f := newTestApp()
	if err := a.StartCast("X"); err != nil {
		t.Fatal(err)
	}
	f.waitStarts(t, 1)

	// 前置：停止态 + 窗口关闭行 → stopping+closing 并存（同 gui6 并存口径）
	if err := a.StopCast("X"); err != nil {
		t.Fatal(err)
	}
	a.NotifyLine("X", windowCloseLine)
	s := sessionBySerial(t, a, "X")
	if !s.Closing || !s.Stopping || !s.Active {
		t.Fatalf("前置：窗口关闭后应 Closing+Stopping+Active: %+v", s)
	}

	// 自动切换事件 → closing/stopping 都清；Phase/PhaseText 照常更新
	a.NotifyLine("X", gui35SwitchUSBLine)
	s = sessionBySerial(t, a, "X")
	if s.Closing {
		t.Fatalf("KindSwitchUSB 应清 closing（切换 = 投屏继续）: %+v", s)
	}
	if s.Stopping {
		t.Fatalf("KindSwitchUSB 应顺带清 stopping（切换不是停止）: %+v", s)
	}
	if !s.Active {
		t.Fatalf("切换后会话应仍 Active（bat 切到有线分支继续跑）: %+v", s)
	}
	if s.Cast.Phase != "switch-usb" || s.Cast.PhaseText != "检测到 USB 插线，切换有线投屏…" {
		t.Fatalf("切换事件 Phase/PhaseText 应照常更新: %+v", s.Cast)
	}

	// 切换完成的新分支规格行 → Spec/Mode 刷新、closing 保持 false
	a.NotifyLine("X", gui35WiredSpecLine)
	s = sessionBySerial(t, a, "X")
	if s.Closing {
		t.Fatalf("规格行到达后 closing 应保持 false: %+v", s)
	}
	if s.Cast.Spec == nil || !s.Cast.Spec.Wired || s.Cast.Spec.Mbps != 50 {
		t.Fatalf("切换后有线规格应刷新: %+v", s.Cast.Spec)
	}
	if s.Cast.Mode != "usb" {
		t.Fatalf("有线规格行应把模式切到 usb: %q", s.Cast.Mode)
	}
}

// TestGui35SpecEventClearsClosing：哨兵行（KindUserClose）置 closing 后收到
// KindSpec（新分支规格）→ closing=false；规格/模式刷新正常；Spec 行不覆盖
// Phase（沿用既有口径——Phase 只由 phaseText 事件更新）。
func TestGui35SpecEventClearsClosing(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")
	a.NotifyLine("X", "===== 开始投屏：X =====") // phase=casting

	a.NotifyLine("X", "SCRCPY_EZ_USER_CLOSE")
	s := sessionBySerial(t, a, "X")
	if !s.Closing || !s.Active {
		t.Fatalf("前置：哨兵行应立即置 Closing 且保持 Active: %+v", s)
	}

	a.NotifyLine("X", gui35WiredSpecLine)
	s = sessionBySerial(t, a, "X")
	if s.Closing {
		t.Fatalf("KindSpec（新分支规格）应清 closing: %+v", s)
	}
	if s.Cast.Spec == nil || !s.Cast.Spec.Wired || s.Cast.Spec.Mbps != 50 {
		t.Fatalf("规格应刷新: %+v", s.Cast.Spec)
	}
	if s.Cast.Mode != "usb" {
		t.Fatalf("模式应切到 usb: %q", s.Cast.Mode)
	}
	if s.Cast.Phase != "casting" || s.Cast.PhaseText != "投屏中" {
		t.Fatalf("Spec 行不应覆盖 Phase（沿用既有口径）: %+v", s.Cast)
	}
}

// TestGui35ReconnectEventClearsClosing：断线重连（KindReconnect）→ closing=false
// （重连 = 同一投屏继续，不是关闭）；Phase/PhaseText 照常更新为重连文案。
func TestGui35ReconnectEventClearsClosing(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")

	a.NotifyLine("X", windowCloseLine)
	s := sessionBySerial(t, a, "X")
	if !s.Closing {
		t.Fatalf("前置：窗口关闭行应置 Closing: %+v", s)
	}

	a.NotifyLine("X", gui35ReconnectLine)
	s = sessionBySerial(t, a, "X")
	if s.Closing {
		t.Fatalf("KindReconnect 应清 closing（重连不是关闭）: %+v", s)
	}
	if !s.Active {
		t.Fatalf("重连后会话应仍 Active: %+v", s)
	}
	if s.Cast.Phase != "reconnect" || s.Cast.PhaseText != "连接断开，自动重连中…" {
		t.Fatalf("重连事件 Phase/PhaseText 应照常更新: %+v", s.Cast)
	}
}

// TestGui35ReDetectEventsClearClosing：同一分支里 bat 的"重新检测"事件
// （KindADBReset/KindDetect——切换/重连循环中的重扫阶段）同样清 closing；
// Phase/PhaseText 各自照常更新。
func TestGui35ReDetectEventsClearClosing(t *testing.T) {
	cases := []struct {
		line string
		ph   string
		txt  string
	}{
		{gui35ADBResetLine, "adb-reset", "正在准备 adb…"},
		{gui35DetectLine, "detect", "正在扫描设备…"},
	}
	for _, c := range cases {
		a, _ := newTestApp()
		_ = a.StartCast("X")
		a.NotifyLine("X", windowCloseLine)
		if s := sessionBySerial(t, a, "X"); !s.Closing {
			t.Fatalf("%q 前置：窗口关闭行应置 Closing: %+v", c.line, s)
		}

		a.NotifyLine("X", c.line)
		s := sessionBySerial(t, a, "X")
		if s.Closing {
			t.Fatalf("%q 重新检测事件应清 closing: %+v", c.line, s)
		}
		if s.Cast.Phase != c.ph || s.Cast.PhaseText != c.txt {
			t.Fatalf("%q Phase/PhaseText 应照常更新: %+v", c.line, s.Cast)
		}
	}
}

// TestGui35PlainWindowCloseKeepsClosing（回归）：置 closing 后没有切换事件——
// 普通日志行（KindNone）/检测到 USB 行（KindUSBFound）/普通 Done 行都不得清
// closing（用户主动关投屏语义不动）；bat 退出（OnBatExit）才统一复位。
func TestGui35PlainWindowCloseKeepsClosing(t *testing.T) {
	a, _ := newTestApp()
	_ = a.StartCast("X")

	a.NotifyLine("X", windowCloseLine)
	s := sessionBySerial(t, a, "X")
	if !s.Closing {
		t.Fatalf("窗口关闭行应置 Closing: %+v", s)
	}

	// 无切换事件的行序列：都不清 closing
	lines := []string{
		"普通日志行（未分类）",
		"[OK] 检测到 USB 设备：Xiaomi Pad（24117RK2CC）",
		"投屏已结束，感谢使用", // 普通 Done（非"已检测到窗口关闭"）：不置也不清
	}
	for _, l := range lines {
		a.NotifyLine("X", l)
		s = sessionBySerial(t, a, "X")
		if !s.Closing {
			t.Fatalf("%q 不是切换事件，closing 应保持（回归）: %+v", l, s)
		}
	}

	// bat 退出 → closing 统一复位（原语义）
	a.OnBatExit("X", 0)
	s = sessionBySerial(t, a, "X")
	if s.Closing || s.Active {
		t.Fatalf("bat 退出后 Closing/Active 应复位: %+v", s)
	}
}
