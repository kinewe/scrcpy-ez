package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49：拔线遮罩（「断开中…」卡） ---

func gui49SeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
}

func gui49UnplugActive(a *App) bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	_, ok := a.unplugging["REDMI K80"]
	return ok
}

func gui49UnplugAt(a *App) time.Time {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	return a.unplugging["REDMI K80"]
}

// gui49NoConfirm 注入 no-op connect 确认：让遮罩生命周期用例与 connect 异步动作解耦。
func gui49NoConfirm(a *App) {
	a.unplugConfirmFn = func(ctx context.Context, id, addr string, attempt int) {}
}

// 启动：USB removed 立即启动拔线遮罩（无防抖），显示无线形态「断开中」卡。
func TestGui49UnplugStartOnRemoved(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49NoConfirm(a)

	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6SettlePlug(a)  // 模拟稳定 device 2s 清插线遮罩，进入拔线场景
	a.applyTrackUpdate(nil) // 拔线：USB removed

	if !gui49UnplugActive(a) {
		t.Fatal("USB removed 应立即启动拔线遮罩")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 {
		t.Fatalf("拔线遮罩应盖成一张卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "unplug-REDMI K80" || d.ConnType != "wifi" || d.State != "device" ||
		!d.Connecting || d.WirelessIP != "192.168.31.197:5555" {
		t.Fatalf("拔线遮罩卡形态错误: %+v", d)
	}
}

// 真实拔线时序：devs 含该 identity 无线 offline 条目 + removed(usb) →
// offline 不是「无线恢复」，必须启动拔线遮罩。
func TestGui49UnplugStartsWithWirelessOfflineStillListed(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49NoConfirm(a)

	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6SettlePlug(a) // 模拟稳定 device 2s 清插线遮罩，进入拔线场景
	wifiOffline := adb.Device{Serial: "192.168.31.197:5555", State: "offline",
		ConnType: "wifi", Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifiOffline}) // USB removed + 无线 offline 仍在列表

	if !gui49UnplugActive(a) {
		t.Fatal("无线 offline 条目仍在列表时，removed(usb) 也应启动拔线遮罩")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || !out[0].Connecting || out[0].ConnType != "wifi" {
		t.Fatalf("应显示断开中遮罩卡（无线形态）: %+v", out)
	}
}

// 互斥：插线遮罩运行期内的 removed 被吸收，不启动拔线遮罩。
func TestGui49UnplugAbsorbedWhilePlugging(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()}) // 插线遮罩起
	if !teachfix3PlugActive(a) {
		t.Fatal("插线遮罩应启动")
	}
	a.applyTrackUpdate(nil) // adbd 重启瞬态：removed
	if gui49UnplugActive(a) {
		t.Fatal("插线遮罩运行期 removed 不得启动拔线遮罩")
	}
}

// 主清因：无线 device 恢复 → 清拔线遮罩 → 正常无线卡。
func TestGui49UnplugClearsOnWirelessDevice(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49NoConfirm(a)

	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6SettlePlug(a) // 模拟稳定 device 2s 清插线遮罩，进入拔线场景
	a.applyTrackUpdate(nil)
	if !gui49UnplugActive(a) {
		t.Fatal("应先启动拔线遮罩")
	}
	a.applyTrackUpdate([]adb.Device{teachfix3Wifi()}) // 无线恢复
	if gui49UnplugActive(a) {
		t.Fatal("无线 device 应清拔线遮罩")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].ConnType != "wifi" ||
		out[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("清遮罩后应立即呈现正常无线卡: %+v", out)
	}
}

// 10s 兜底：无线未恢复 → 超时清 → 档案态（此处 active → 无线卡）。
func TestGui49UnplugTimeoutFallsBack(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49NoConfirm(a)

	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6SettlePlug(a) // 模拟稳定 device 2s 清插线遮罩，进入拔线场景
	a.applyTrackUpdate(nil)
	if !gui49UnplugActive(a) {
		t.Fatal("应先启动拔线遮罩")
	}
	a.teachMu.Lock()
	a.unplugging["REDMI K80"] = time.Now().Add(-(unplugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.unplugTimeout("REDMI K80")
	if gui49UnplugActive(a) {
		t.Fatal("10s 兜底应清拔线遮罩")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].ConnType != "wifi" {
		t.Fatalf("兜底后应立即呈现档案态无线卡: %+v", out)
	}
}

// 幂等：运行期重复 removed/置位信号不重计时、不重启。
func TestGui49UnplugIdempotent(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49NoConfirm(a)

	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6SettlePlug(a) // 模拟稳定 device 2s 清插线遮罩，进入拔线场景
	a.applyTrackUpdate(nil)
	first := gui49UnplugAt(a)
	a.unplugStart("REDMI K80", time.Now().Add(time.Second), "removed(usb)")
	if got := gui49UnplugAt(a); !got.Equal(first) {
		t.Fatalf("重复 removed 不得重计时: first=%s got=%s", first, got)
	}
}

// gui49FastRetry 缩短 connect 失败重试间隔（事件驱动 timer 测试注入）。
func gui49FastRetry(t *testing.T) {
	t.Helper()
	old := unplugConnectRetryDelay
	unplugConnectRetryDelay = 50 * time.Millisecond
	t.Cleanup(func() { unplugConnectRetryDelay = old })
}

// connect 成功 → 地址直写 active（清 stale/失败节流）+ 清遮罩 → 无线卡。
func TestGui49ConnectConfirmSuccessActiveAndClear(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:5555") // 先 stale：验证复活
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)

	var mu sync.Mutex
	connects := 0
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		connects++
		mu.Unlock()
		return nil
	}

	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6SettlePlug(a)  // 模拟稳定 device 2s 清插线遮罩，进入拔线场景
	a.applyTrackUpdate(nil) // removed → 拔线遮罩 + connect 确认
	waitForMdns(t, "connect 成功应清拔线遮罩", func() bool { return !gui49UnplugActive(a) })
	waitForMdns(t, "connect 成功应写 active", func() bool {
		ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
		return ae != nil && ae.State == AddrStateActive && !ae.Stale
	})
	mu.Lock()
	n := connects
	mu.Unlock()
	if n == 0 {
		t.Fatal("应执行 connect 确认")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].ConnType != "wifi" ||
		out[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("connect 成功后应呈现正常无线卡: %+v", out)
	}
}

// connect 全失败 → 地址 stale + 有限重试（事件驱动 timer）→ 10s 兜底离线卡。
func TestGui49ConnectConfirmFailRetriesThenTimeoutOffline(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49FastRetry(t)

	var mu sync.Mutex
	attempts := 0
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		attempts++
		mu.Unlock()
		return errors.New("cannot connect")
	}

	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6SettlePlug(a) // 模拟稳定 device 2s 清插线遮罩，进入拔线场景
	a.applyTrackUpdate(nil)
	waitForMdns(t, "connect 应有限重试 3 次", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts >= unplugConnectAttempts
	})
	waitForMdns(t, "失败应打 stale", func() bool {
		ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
		return ae != nil && ae.Stale
	})
	if !gui49UnplugActive(a) {
		t.Fatal("重试失败期间遮罩应持续")
	}

	// 全部失败 → 10s 兜底 → 真实档案态（离线卡）。
	a.teachMu.Lock()
	a.unplugging["REDMI K80"] = time.Now().Add(-(unplugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.unplugTimeout("REDMI K80")
	if gui49UnplugActive(a) {
		t.Fatal("10s 兜底应清拔线遮罩")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].State != "offline" {
		t.Fatalf("全部失败兜底后应显示离线卡: %+v", out)
	}
}

// gui49-fix6：getprop 不再清插线遮罩；稳定 device 2s 清（不交叉影响拔线遮罩）。
func TestGui49PlugGetpropNoLongerClears(t *testing.T) {
	a, _ := newWirelessApp()
	gui49SeedK80(a)
	ops := &teachfix3Ops{port: "5555"}
	ops.install(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.plugCheckTcpipReady(context.Background(), "601c9f08")
	if !teachfix3PlugActive(a) {
		t.Fatal("getprop==5555 不得清插线遮罩（清因=稳定 device）")
	}
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6WaitPlugClear(t, a)
}
