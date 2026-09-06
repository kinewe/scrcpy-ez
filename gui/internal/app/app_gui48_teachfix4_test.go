package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-teachfix4：删除拔线确认，清因收口 getprop + 10s 兜底双通道 ---

// 用例①：removed（含 2s 防抖确认）不再清遮罩——拔线确认路径已删除。
func TestTeachfix4RemovedNeverClearsMask(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	if !teachfix3PlugActive(a) {
		t.Fatal("插线应启动遮罩")
	}
	a.applyTrackUpdate(nil)
	a.onDropped("REDMI K80") // 2s 防抖确认（adbd 重启断档 3.45s~5s 也无法误杀）
	if !teachfix3PlugActive(a) {
		t.Fatal("removed 防抖确认不得清遮罩（拔线确认已删除）")
	}
}

// 用例②⑥：K80 重启后插线全链路——offline→adbd 重启瞬态（removed>2s→无线卡在线）
// →USB 回来→getprop 5555 清；起/清日志各恰一次，零闪烁。
func TestTeachfix4K80FullChainZeroFlicker(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	gui49fix6FastStable(t)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()}) // 起
	a.applyTrackUpdate(nil)                                 // adbd 重启断档
	a.onDropped("REDMI K80")                                // 防抖确认：不得清
	a.applyTrackUpdate([]adb.Device{teachfix3Wifi()})       // 无线卡在线：不得清（终点二已删）
	if !teachfix3PlugActive(a) {
		t.Fatal("重启瞬态全程遮罩必须保持")
	}
	teachfix3SetPort(ops, "5555")
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()}) // USB 回来 → 稳定 device 清
	gui49fix6WaitPlugClear(t, a)

	mdns8LogContains(t, logPath, "[app] 插线遮罩结束：REDMI K80")
	b, _ := os.ReadFile(logPath)
	logs := string(b)
	if strings.Count(logs, "[app] 插线遮罩开始：REDMI K80") != 1 {
		t.Fatalf("一次插线周期遮罩应只起一次:\n%s", logs)
	}
	if strings.Count(logs, "[app] 插线遮罩结束：REDMI K80") != 1 {
		t.Fatalf("一次插线周期遮罩应只清一次:\n%s", logs)
	}
}

// 用例③⑤：真拔线/设备死机 → 10s 兜底清 → 档案态（active 无线 → 无线卡）。
func TestTeachfix4TrueUnplugFallsBackAt10sToArchiveState(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	a.profiles.AddrSuccess("REDMI K80", "192.168.31.197:5555", ModeTcpip)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return nil } // 兜底 connect 成功

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.applyTrackUpdate(nil)
	a.onDropped("REDMI K80")
	if !teachfix3PlugActive(a) {
		t.Fatal("拔线后 10s 内遮罩应由兜底持有（不能靠 removed 提前清）")
	}
	// 加速模拟超时点，验证兜底通道与显示层档案态。
	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if teachfix3PlugActive(a) {
		t.Fatal("10s 兜底应清除遮罩")
	}
	a.commitDisplay(nil)
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].ConnType != "wifi" ||
		out[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("兜底清后应呈现档案态无线卡: %+v", out)
	}
}

// 用例④（gui49-fix6 适配）：getprop 不再清；USB 连续 device 满稳定窗口清。
func TestTeachfix4GetpropNoLongerClearsStableDoes(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.plugCheckTcpipReady(context.Background(), "601c9f08")
	if !teachfix3PlugActive(a) {
		t.Fatal("getprop==5555 不得清遮罩（清因改为稳定 device）")
	}
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6WaitPlugClear(t, a)
}

// 用例③：plugShieldTimeout 已收口为 10s（adbd 重启断档上限 5s + 余量）。
func TestTeachfix4PlugShieldTimeoutIs10s(t *testing.T) {
	if plugShieldTimeout != 10*time.Second {
		t.Fatalf("兜底超时应为 10s，当前 %s", plugShieldTimeout)
	}
}
