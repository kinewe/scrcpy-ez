package app

// gui47-fix F2'「插线遮罩事件驱动化」测试（gui48-p1 迁移版）。
// 喂法改为 applyTrackUpdate（track 回调分发主函数），断言语义保持：
//   1. 插线首见（offline）→ USB 连接中遮罩卡在场，无离线卡；
//   2. adbd 重启窗口（devs 无该设备）+ 已有离线卡 → 遮罩维持、离线卡让位；
//   3. USB State=device → 遮罩结束，真实 USB 卡接管；
//   4. 插线中在线无线卡（拔线事实，无 USB）→ 遮罩结束，无线卡接管；
//   5. 15s 兜底未 ready → 遮罩结束，离线卡接管；
//   6. 插线中在线无线卡 + USB 未 ready → 并入遮罩卡副行，不独立成卡。

import (
	"context"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// gui47fixPlug 直接设置插线状态（identity → 插线开始时刻；测试用，同 teachMu 保护）。
func gui47fixPlug(a *App, id string, at time.Time) {
	a.teachMu.Lock()
	a.plugging[id] = at
	a.teachMu.Unlock()
}

// TestGui47FixPlugFirstSeenOfflineShields：插线首见 offline USB → 合成 USB 连接中卡。
func TestGui47FixPlugFirstSeenOfflineShields(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	a.applyTrackUpdate([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb", Name: "REDMI K80"},
	})
	out := a.Snapshot().Devices
	if len(out) != 1 {
		t.Fatalf("插线首见应合成 1 张 USB 连接中卡: %+v", out)
	}
	d := out[0]
	if d.Serial != "601c9f08" || d.State != "device" || d.ConnType != "usb" ||
		d.Name != "REDMI K80" || d.Identity != "REDMI K80" || !d.Connecting {
		t.Fatalf("合成卡形态错误: %+v", d)
	}

	// 已有离线卡进 devs 也应让位（不出现离线卡）
	a.applyTrackUpdate([]adb.Device{{
		Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80",
	}})
	out = a.Snapshot().Devices
	if len(out) != 1 || !out[0].Connecting || out[0].Serial != "601c9f08" {
		t.Fatalf("离线卡应让位给 USB 连接中卡: %+v", out)
	}
}

// TestGui47FixPlugAdbdRestartKeepsShield：插线进行中 devs 无设备 + 已有离线卡 →
// 遮罩维持、离线卡让位。
func TestGui47FixPlugAdbdRestartKeepsShield(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	a.applyTrackUpdate([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb"},
	})
	// adbd 重启窗口：设备从列表干净消失
	a.applyTrackUpdate(nil)
	if _, ok := a.plugging["REDMI K80"]; !ok {
		t.Fatal("adbd 重启窗口不应清除插线状态")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 {
		t.Fatalf("应 1 张遮罩卡: %+v", out)
	}
	if out[0].Serial != "601c9f08" || !out[0].Connecting || out[0].State != "device" {
		t.Fatalf("遮罩卡应维持 USB 连接中: %+v", out)
	}
	if gui34Find(out, "192.168.31.197:5555") != nil && out[0].Serial == "192.168.31.197:5555" {
		t.Fatalf("离线卡不应独立保留: %+v", out)
	}
}

// TestGui47FixPlugUsbDeviceEnds：USB State=device → 插线结束，真实 USB 卡接管。
func TestGui47FixPlugUsbDeviceEnds(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb"},
	})
	real := adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{real})
	gui49fix6WaitPlugClear(t, a) // USB 连续 device 满稳定窗口 → 清遮罩
	waitForMdns(t, "真实 USB 卡应原样接管", func() bool {
		out := a.Snapshot().Devices
		return len(out) == 1 && out[0].Serial == real.Serial && !out[0].Connecting
	})
}

// TestGui47FixPlugWifiUnplugEnds（gui48-teachfix3/4 适配）：
// 在线无线卡 + 无 USB 不清遮罩（终点二已删）；removed 防抖确认也不清遮罩
// （拔线确认已删，adbd 重启瞬态不可误判）；真拔线由 10s 兜底清 → 档案态。
func TestGui47FixPlugWifiUnplugEnds(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return nil } // 10s 兜底 connect 成功

	a.applyTrackUpdate([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb"},
	})
	wifi := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifi})
	if !teachfix3PlugActive(a) {
		t.Fatal("在线无线卡 + 无 USB 不得清遮罩（终点二已删除；可能只是 adbd 重启窗口）")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || !out[0].Connecting {
		t.Fatalf("遮罩应维持连接中卡: %+v", out)
	}

	// removed + 2s 防抖确认：不再清遮罩（拔线确认路径已删除）。
	a.applyTrackUpdate(nil)
	a.onDropped("REDMI K80")
	if !teachfix3PlugActive(a) {
		t.Fatal("removed 防抖确认不得清遮罩（拔线确认已删除）")
	}

	// 真拔线/死机由 10s 兜底清（加速模拟：把置位时间改到超时点再触发）。
	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if teachfix3PlugActive(a) {
		t.Fatal("10s 兜底应清除遮罩")
	}
	a.commitDisplay(nil)
	out = a.Snapshot().Devices
	if len(out) != 1 || out[0].Connecting || out[0].ConnType != "wifi" || out[0].Serial != wifi.Serial {
		t.Fatalf("兜底清后应呈现档案态无线卡: %+v", out)
	}
}

// TestGui47FixPlugTimeoutFallsBack：15s 未 ready → 遮罩结束，离线卡/原卡接管。
func TestGui47FixPlugTimeoutFallsBack(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)
	gui47fixPlug(a, "REDMI K80", time.Now().Add(-(plugShieldTimeout + time.Second)))

	offline := adb.Device{Serial: "601c9f08", State: "offline", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{offline})
	out := a.Snapshot().Devices
	// gui49-fix5：USB 条目在列的 offline 瞬态渲染为「连接中…」，不显示离线。
	if len(out) != 1 || out[0].Serial != offline.Serial || out[0].ConnType != "usb" || !out[0].Connecting {
		t.Fatalf("超时后 USB offline 原卡应渲染连接中: %+v", out)
	}
	if _, ok := a.plugging["REDMI K80"]; ok {
		t.Fatalf("超时条目应清理: %+v", a.plugging)
	}
}

// TestGui47FixPlugWifiMergedIntoShield：插线中 USB 未 ready + 在线无线卡 →
// 无线卡并入遮罩卡副行，不独立成卡。
func TestGui47FixPlugWifiMergedIntoShield(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	usbNotReady := adb.Device{Serial: "601c9f08", State: "unauthorized", ConnType: "usb", Name: "REDMI K80"}
	wifi := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{usbNotReady, wifi})
	if _, ok := a.plugging["REDMI K80"]; !ok {
		t.Fatal("有 USB 条目时在线无线卡不应结束插线状态")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 {
		t.Fatalf("应 1 张遮罩卡（无线并入副行）: %+v", out)
	}
	d := out[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || !d.Connecting || d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("遮罩卡应含无线副行: %+v", d)
	}
	if gui34Find(out, "192.168.31.197:5555") != nil {
		t.Fatalf("无线卡不应独立成卡: %+v", out)
	}
}
