package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui49-fix6 测试辅助：稳定确认加速 / 遮罩手动结算 ---

// gui49fix6FastStable 把插线遮罩「USB 连续 device 满 2s」的稳定窗口缩短到 30ms。
func gui49fix6FastStable(t *testing.T) {
	t.Helper()
	old := plugStableDuration
	plugStableDuration = 30 * time.Millisecond
	t.Cleanup(func() { plugStableDuration = old })
}

// gui49fix6SettlePlug 模拟「稳定 device 2s 已确认」：直接清插线遮罩
// （用于拔线遮罩/后续场景测试，避免等待真实稳定窗口）。
func gui49fix6SettlePlug(a *App) {
	a.plugClear("REDMI K80", "测试稳定结算")
}

func gui49fix6PlugActive(a *App) bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	_, ok := a.plugging["REDMI K80"]
	return ok
}

func gui49fix6WaitPlugClear(t *testing.T, a *App) {
	t.Helper()
	waitForMdns(t, "等待插线遮罩稳定清", func() bool { return !gui49fix6PlugActive(a) })
	// plugClear 的 map 删除先于 refreshDisplayForPlug；等提交完成再返回，
	// 避免测试清理时与提交日志/快照写入竞态（-race 稳定）。
	waitForMdns(t, "等待稳定清后的显示提交完成", func() bool {
		out := a.Snapshot().Devices
		for i := range out {
			if out[i].Connecting {
				return false
			}
		}
		return true
	})
}

// 遮罩期 removed 被吸收：不启动拔线遮罩；USB 回来后稳定 device 2s 清。
func TestGui49Fix6RemovedAbsorbedThenStableClear(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()}) // 插线遮罩起
	a.applyTrackUpdate(nil)                                 // 折腾期 removed
	if !gui49fix6PlugActive(a) {
		t.Fatal("遮罩期 removed 不得清插线遮罩")
	}
	if gui49UnplugActive(a) {
		t.Fatal("遮罩期 removed 不得启动拔线遮罩")
	}

	// USB 回来，连续 device 满稳定窗口 → 清插线遮罩。
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6WaitPlugClear(t, a)
}

// 稳定计时：device 后又跌回 offline → 重置；再次 device 才满 2s 清。
func TestGui49Fix6StableTimerResetsOnFluctuation(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()}) // 开始稳定计时
	time.Sleep(10 * time.Millisecond)
	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()}) // 波动 → 重置
	if !gui49fix6PlugActive(a) {
		t.Fatal("稳定窗口内跌回 offline 遮罩必须保持")
	}
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	gui49fix6WaitPlugClear(t, a)
}

// 10s 兜底 connect 成功 → 地址 active + 清遮罩 → 档案态无线卡。
func TestGui49Fix6TimeoutConnectSuccess(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:5555")
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return nil }

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.applyTrackUpdate(nil) // 真拔线：removed 吸收，遮罩由兜底结束
	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if gui49fix6PlugActive(a) {
		t.Fatal("兜底 connect 后插线遮罩应退出")
	}
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
	if ae == nil || ae.State != AddrStateActive || ae.Stale {
		t.Fatalf("connect 成功应写 active: %+v", ae)
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].ConnType != "wifi" ||
		out[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("兜底后应呈现档案态无线卡: %+v", out)
	}
}

// 10s 兜底 connect 失败 → 地址 stale + 清遮罩 → 离线卡。
func TestGui49Fix6TimeoutConnectFail(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	ops := &teachfix3Ops{port: "0"}
	ops.install(a)
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return errors.New("no") }

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.applyTrackUpdate(nil)
	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if gui49fix6PlugActive(a) {
		t.Fatal("兜底 connect 失败后插线遮罩也应退出")
	}
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
	if ae == nil || !ae.Stale {
		t.Fatalf("connect 失败应写 stale: %+v", ae)
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].State != "offline" {
		t.Fatalf("全失败后应显示离线卡: %+v", out)
	}
}

// 遮罩期离线观察豁免：wifi offline 不打 stale、不记失败节流。
func TestGui49Fix6MaskPeriodOfflineObservationExempt(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	a.plugStart("REDMI K80", time.Now(), "测试插线")

	a.profiles.SyncDevicesExempt([]adb.Device{
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Identity: "REDMI K80"},
	}, a.plugExemptIDs())
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
	if ae == nil || ae.Stale || ae.LastFail != 0 || ae.Fail != 0 {
		t.Fatalf("遮罩期 wifi offline 应豁免（不打标/不节流）: %+v", ae)
	}
}

// 未学 tcpip 场景不回归：端口≠5555 仍学习一次，遮罩由稳定 device 清。
func TestGui49Fix6UnlearnedTcpipStillLearns(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix3SeedK80(a)
	tcpips := 0
	ops := &teachfix3Ops{
		port: "0",
		tcpipFn: func(ctx context.Context, serial, port string) error {
			tcpips++
			return nil
		},
	}
	ops.install(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{teachfix3OfflineUSB()})
	a.applyTrackUpdate([]adb.Device{teachfix3DeviceUSB()})
	if tcpips != 1 {
		t.Fatalf("端口非 5555 应执行一次 tcpip: %d", tcpips)
	}
	gui49fix6WaitPlugClear(t, a)
}
