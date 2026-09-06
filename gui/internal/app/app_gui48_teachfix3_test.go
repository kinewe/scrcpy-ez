package app

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-teachfix3：删除终点二，遮罩清因收口三通道 ---

type teachfix3Ops struct {
	mu      sync.Mutex
	port    string
	tcpipFn func(ctx context.Context, serial, port string) error
}

func (o *teachfix3Ops) install(a *App) {
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.port, nil
	}
	if o.tcpipFn != nil {
		a.teachOps.tcpipFn = o.tcpipFn
	} else {
		a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }
	}
	a.teachOps.shellFn = func(ctx context.Context, serial string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "settings" {
			return "1\n", nil
		}
		return "", nil
	}
	// probeFn 不注入：走旧测试路径（候选读不到时不会触达真实网络）。
}

func teachfix3SetPort(o *teachfix3Ops, port string) {
	o.mu.Lock()
	o.port = port
	o.mu.Unlock()
}

func teachfix3SeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
}

func teachfix3OfflineUSB() adb.Device {
	return adb.Device{Serial: "601c9f08", State: "offline", ConnType: "usb",
		Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"}
}

func teachfix3DeviceUSB() adb.Device {
	d := teachfix3OfflineUSB()
	d.State = "device"
	return d
}

func teachfix3Wifi() adb.Device {
	return adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
		Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"}
}

func teachfix3PlugActive(a *App) bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	_, ok := a.plugging["REDMI K80"]
	return ok
}

// 用例①：终点二已删除——在线无线卡事件 + 无 USB 不得清遮罩。
func TestTeachfix3WifiOnlineDoesNotClearMask(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	if !teachfix3PlugActive(a) {
		t.Fatal("插线应启动遮罩")
	}
	// adbd 重启窗口：USB 从设备流消失，无线卡在线——这不是拔线。
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate([]adb.Device{teachfix3Wifi()})
	if !teachfix3PlugActive(a) {
		t.Fatal("在线无线卡 + 无 USB 不得清遮罩（终点二已删除）")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || !out[0].Connecting {
		t.Fatalf("遮罩应维持连接中卡: %+v", out)
	}
}

// 用例②③：adbd 重启窗口完整序列——遮罩起一次、清一次，无置位-清-置位闪烁。
func TestTeachfix3RestartWindowNoFlicker(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	gui49fix6FastStable(t)
	logPath := mdns8StartLogCapture(t)

	// 插线：offline added → 起遮罩（端口丢失 → tcpip，遮罩持续）。
	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	// adbd 重启窗口：USB 短暂消失 + 无线卡在线（假拔线，不清）。
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate([]adb.Device{teachfix3Wifi()})
	if !teachfix3PlugActive(a) {
		t.Fatal("重启窗口遮罩必须保持")
	}
	// USB 重新枚举（added）→ plugStart 幂等，不产生第二次起。
	// gui49-fix6：USB 连续 device 满稳定窗口 → 清遮罩（getprop 不再清）。
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
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

// 用例④（gui48-teachfix4 适配）：removed+2s 防抖确认不再清遮罩
// （拔线确认已删除）；真拔线由 10s 兜底清。
func TestTeachfix3RemovedNoLongerClearsMask(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	if !teachfix3PlugActive(a) {
		t.Fatal("插线应启动遮罩")
	}
	a.applyTrackUpdate(nil)
	a.onDropped("REDMI K80")
	if !teachfix3PlugActive(a) {
		t.Fatal("removed 防抖确认不得清遮罩（拔线确认已删除）")
	}
	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if teachfix3PlugActive(a) {
		t.Fatal("10s 兜底应清除遮罩")
	}
}

// 用例⑤（gui49-fix6 适配）：getprop==5555 不再清遮罩；USB 连续 device
// 满稳定窗口才清（清因①）。
func TestTeachfix3GetpropNoLongerClearsStableDoes(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	if !teachfix3PlugActive(a) {
		t.Fatal("插线应启动遮罩")
	}
	a.plugCheckTcpipReady(context.Background(), "601c9f08")
	if !teachfix3PlugActive(a) {
		t.Fatal("getprop==5555 不得清遮罩（清因改为稳定 device）")
	}
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6WaitPlugClear(t, a)
}

// 用例⑥：10s 兜底保险丝仍在（设备死机/事件链断时遮罩不永久挂起）。
func TestTeachfix3TimeoutFuseStillWorks(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	gui47fixPlug(a, "REDMI K80", time.Now().Add(-(plugShieldTimeout + time.Second)))
	a.plugTimeout("REDMI K80")
	if teachfix3PlugActive(a) {
		t.Fatal("10s 兜底应清除遮罩")
	}
}
