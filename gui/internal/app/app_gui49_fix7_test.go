package app

import (
	"context"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49-fix7：显示提交串行化 + 诊断日志 ---

// track 线程与校准轮并发进入 applyTrackUpdateCtx 时必须串行：
// A 阻塞在学习步骤内时，B 不得完成提交（不能出现「先有线、遮罩后到」乱序帧）。
func TestGui49Fix7ApplyTrackUpdateSerialized(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)

	started := make(chan struct{})
	release := make(chan struct{})
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		close(started)
		<-release
		return "5555", nil
	}
	a.teachOps.shellFn = func(ctx context.Context, serial string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "settings" {
			return "1\n", nil
		}
		return "", nil
	}

	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()}) // track 线程批次
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("批次 A 应进入学习步骤")
	}

	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()}) // 校准轮批次
	}()

	// B 必须等待 A 完成（串行）；A 仍阻塞，B 不得完成。
	select {
	case <-doneB:
		t.Fatal("并发批次未串行化：B 在 A 阻塞期间完成")
	case <-time.After(80 * time.Millisecond):
	}

	close(release)
	select {
	case <-doneA:
	case <-time.After(2 * time.Second):
		t.Fatal("批次 A 超时")
	}
	select {
	case <-doneB:
	case <-time.After(2 * time.Second):
		t.Fatal("批次 B 超时")
	}
}

// commitDisplay / reapplyDisplay 诊断日志：提交源 + 插线遮罩活跃数。
func TestGui49Fix7DisplayCommitDiagnosticLogs(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	logPath := mdns8StartLogCapture(t)

	a.plugStart("REDMI K80", time.Now(), "测试插线")
	a.commitDisplay(nil, "单元测试提交")
	a.reapplyDisplay("单元测试来源")
	mdns8LogContains(t, logPath, "[app] 显示提交：单元测试提交（插线遮罩活跃=1")
	mdns8LogContains(t, logPath, "[app] reapplyDisplay：来源=单元测试来源")
	a.plugClear("REDMI K80", "测试清理")
}
