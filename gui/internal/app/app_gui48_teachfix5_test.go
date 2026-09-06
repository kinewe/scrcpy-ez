package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-teachfix5：遮罩进程模型（触发唯一化 + 信号吸收 + 立即刷新） ---
// 断言已对齐 gui49-fix6 拍板语义：getprop 不再清遮罩，清因①=USB 连续
// device 满 2s（plugStabilityUpdate），10s 兜底=connect 确认。

func teachfix5SeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
}

func teachfix5PlugActive(a *App) bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	_, ok := a.plugging["REDMI K80"]
	return ok
}

func teachfix5PlugAt(a *App) time.Time {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	return a.plugging["REDMI K80"]
}

// 用例①：置位入口唯一=added；device→offline（跌回/拔线）不再启动遮罩。
// （getprop==5555 已不参与清遮罩，见用例⑤。）
func TestTeachfix5DropTransitionDoesNotStartMask(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix5SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49fix6FastStable(t)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate(nil)
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()}) // added：起遮罩（getprop 不清）
	if !teachfix5PlugActive(a) {
		t.Fatal("added 应置位遮罩")
	}
	// 拔线/跌回：device → offline（changed 非 device）不得启动新进程（幂等吸收）。
	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	if !teachfix5PlugActive(a) {
		t.Fatal("跌回不得清遮罩（进程持续）")
	}
	mdns8LogContains(t, logPath, "[app] 插线遮罩开始：REDMI K80")
	b, _ := os.ReadFile(logPath)
	if strings.Count(string(b), "[app] 插线遮罩开始：REDMI K80") != 1 {
		t.Fatalf("跌回不得产生第二次遮罩开始:\n%s", string(b))
	}
	// 收尾：USB 连续 device 满稳定窗口清遮罩。
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6WaitPlugClear(t, a)
	waitForMdns(t, "稳定清后的显示提交完成", func() bool {
		out := a.Snapshot().Devices
		return len(out) == 1 && out[0].Serial == "601c9f08" && !out[0].Connecting
	})
}

// 用例②：运行期重复信号幂等吸收——不重计时、不重复日志、首次来源唯一。
func TestTeachfix5RepeatedSignalsAbsorbed(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix5SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	first := teachfix5PlugAt(a)
	time.Sleep(30 * time.Millisecond)

	// USB 重新枚举（added 重放）：吸收，不重新计时。
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	if got := teachfix5PlugAt(a); !got.Equal(first) {
		t.Fatalf("重复 added 不得重计时: first=%s got=%s", first, got)
	}
	// 进程内正常置位（tcpip 学习成功）也吸收。
	a.plugStart("REDMI K80", time.Now().Add(time.Second), "tcpip成功")
	if got := teachfix5PlugAt(a); !got.Equal(first) {
		t.Fatalf("tcpip 学习置位不得重计时: first=%s got=%s", first, got)
	}

	mdns8LogContains(t, logPath, "[app] 插线遮罩开始：REDMI K80")
	b, _ := os.ReadFile(logPath)
	if strings.Count(string(b), "[app] 插线遮罩开始：REDMI K80") != 1 {
		t.Fatalf("重复信号不得重复遮罩开始日志:\n%s", string(b))
	}
}

// 用例③：遮罩状态变化立即刷新显示层——plugClear 后无需任何额外提交，
// Snapshot 立即呈现档案态（无线卡）。
func TestTeachfix5PlugClearRefreshesImmediately(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix5SeedK80(a)
	a.applyTrackUpdate(nil) // lastTrack=空，后续无任何设备流事件

	a.plugStart("REDMI K80", time.Now(), "added(usb)")
	out := a.Snapshot().Devices
	if len(out) != 1 || !out[0].Connecting {
		t.Fatalf("plugStart 应立即刷新出连接中卡: %+v", out)
	}

	a.plugClear("REDMI K80", "稳定device2s")
	out = a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].ConnType != "wifi" ||
		out[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("plugClear 应立即刷新出档案态无线卡（不等 60s 校准）: %+v", out)
	}
}

// 用例④（fix6 语义）：拔线不触发遮罩；10s 兜底=connect 确认。
// connect 失败 → stale → 离线卡（诚实显示）。
func TestTeachfix5UnplugNoTriggerAnd10sFallback(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix5SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		return context.DeadlineExceeded
	}

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()}) // 连线信号：起
	a.applyTrackUpdate(nil)                                 // 拔线：不干预遮罩
	a.onDropped("REDMI K80")
	if !teachfix5PlugActive(a) {
		t.Fatal("拔线不得清遮罩（由 10s 兜底 connect 确认结束）")
	}

	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if teachfix5PlugActive(a) {
		t.Fatal("10s 兜底 connect 确认后应清遮罩")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].State != "offline" {
		t.Fatalf("connect 失败应诚实显示离线卡: %+v", out)
	}
}

// 用例④b：10s 兜底 connect 成功 → active → 无线卡。
func TestTeachfix5Unplug10sFallbackConnectSuccess(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix5SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return nil }

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.applyTrackUpdate(nil)
	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if teachfix5PlugActive(a) {
		t.Fatal("兜底 connect 后应清遮罩")
	}
	waitForMdns(t, "connect 成功后应呈现无线卡", func() bool {
		out := a.Snapshot().Devices
		return len(out) == 1 && !out[0].Connecting && out[0].ConnType == "wifi" &&
			out[0].Serial == "192.168.31.197:5555"
	})
}

// 用例⑤（fix6 语义）：getprop==5555 不再清遮罩；USB 连续 device 满稳定窗口才清。
func TestTeachfix5GetpropReadyStillClears(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix5SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.plugCheckTcpipReady(context.Background(), "601c9f08")
	if !teachfix5PlugActive(a) {
		t.Fatal("getprop==5555 不得清遮罩（fix6 清因=稳定 device）")
	}
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6WaitPlugClear(t, a)
}

// 用例⑥（fix6 语义）：K80 重启后插线全链路——adbd 重启瞬态（removed>2s→无线在线）
// 遮罩保持，USB 回来连续 device 满稳定窗口清；起/清日志各恰一次（零闪）。
func TestTeachfix5K80FullChainZeroFlicker(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix5SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	gui49fix6FastStable(t)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()}) // 起
	a.applyTrackUpdate(nil)                                 // adbd 重启断档
	a.onDropped("REDMI K80")                                // 不清（拔线确认已删）
	a.applyTrackUpdate([]adb.Device{teachfix3Wifi()})       // 不清（终点二已删）
	if !teachfix5PlugActive(a) {
		t.Fatal("重启瞬态遮罩必须保持")
	}
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
