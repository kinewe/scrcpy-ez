package app

// gui48 Phase 1 显示层事件化测试：applyTrackUpdate（track 回调分发主函数）
// 事件序列驱动断言。
//
// 覆盖：
//   1. 插线 offline → device 序列：offline 置位遮罩，device 终点一清除；
//   2. 首见即 device：置位+终点一净效果为空，tcpip 学习成功补置位；
//   3. tcpip 成功补置位后 adbd 重启窗口遮罩维持；
//   4. removed → 2s 防抖 → 掉线探测（stale 打标）；
//   5. 防抖期设备再现 → 取消 timer（不误报）；
//   6. device 事件 + 无会话 → 弹窗（已建档设备需首块基线之后）。

import (
	"context"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// TestGui48PlugOfflineThenDeviceSequence：插线 offline→device 序列驱动遮罩。
func TestGui48PlugOfflineThenDeviceSequence(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)
	gui49fix6FastStable(t)

	a.applyTrackUpdate(nil) // 首块基线：无设备
	a.applyTrackUpdate([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb"},
	})
	if !teachfix3PlugActive(a) {
		t.Fatal("offline added 应置位 plugging")
	}
	out := a.Snapshot().Devices
	if len(out) != 1 || !out[0].Connecting || out[0].Serial != "601c9f08" {
		t.Fatalf("offline 阶段应遮罩成 USB 连接中卡: %+v", out)
	}

	real := adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{real})
	gui49fix6WaitPlugClear(t, a) // USB 连续 device 满稳定窗口 → 清 plugging
	waitForMdns(t, "device 阶段真实 USB 卡接管", func() bool {
		out := a.Snapshot().Devices
		return len(out) == 1 && out[0].Serial == "601c9f08" && !out[0].Connecting
	})
}

// TestGui48FirstSeenDeviceTeachSuccessRearmsPlug：首见即 device → 净效果为空；
// USB added 事件触发插线学习，tcpip 成功后补置位 → adbd 重启窗口遮罩维持。
func TestGui48FirstSeenDeviceTeachSuccessRearmsPlug(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)
	var tcpips int
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "5554", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		tcpips++
		return nil
	}

	a.applyTrackUpdate(nil)
	real := adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{real})
	if tcpips != 1 {
		t.Fatalf("USB added 事件应触发一次插线学习 tcpip: %d", tcpips)
	}
	if _, ok := a.plugging["REDMI K80"]; !ok {
		t.Fatalf("tcpip 学习成功应补置位 plugging（首见即 device 已被终点一清除）: %+v", a.plugging)
	}

	// adbd 重启窗口：设备从列表消失 → 遮罩维持（离线卡让位）
	a.applyTrackUpdate(nil)
	out := a.Snapshot().Devices
	if len(out) != 1 || !out[0].Connecting || out[0].Serial != "601c9f08" {
		t.Fatalf("adbd 重启窗口应维持 USB 连接中遮罩: %+v", out)
	}
}

// TestGui48RemovedDropNoStale（gui48-mdns4）：设备流 removed 防抖回调不再打
// stale、不再探测；地址状态交由 mDNS gone 事件分形态写入档案。
func TestGui48RemovedDropDebounceThenDetect(t *testing.T) {
	a, _ := newTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:45005"}),
	})

	a.applyTrackUpdate(nil)
	wifi := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifi})

	a.applyTrackUpdate(nil)
	a.dropMu.Lock()
	_, ok := a.dropTimers["REDMI K80"]
	a.dropMu.Unlock()
	if !ok {
		t.Fatalf("removed 事件应调度 2s 防抖 timer: %+v", a.dropTimers)
	}

	a.onDropped("REDMI K80")
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok {
		t.Fatal("档案应存在")
	}
	for i := range e.Addrs {
		if e.Addrs[i].Stale {
			t.Fatalf("设备流 removed 不应打 stale（由 mdns gone 分形态处理）: %+v", e.Addrs)
		}
	}
}

// TestGui48RemovedDebounceCancelledOnReappear：防抖期设备再现 → 取消 timer。
func TestGui48RemovedDebounceCancelledOnReappear(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	a.applyTrackUpdate(nil)
	wifi := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifi})
	a.applyTrackUpdate(nil) // removed → 调度防抖

	a.dropMu.Lock()
	_, ok := a.dropTimers["REDMI K80"]
	a.dropMu.Unlock()
	if !ok {
		t.Fatal("removed 事件应调度防抖 timer")
	}

	a.applyTrackUpdate([]adb.Device{wifi}) // 防抖期再现
	a.dropMu.Lock()
	_, ok = a.dropTimers["REDMI K80"]
	a.dropMu.Unlock()
	if ok {
		t.Fatalf("防抖期再现应取消 timer: %+v", a.dropTimers)
	}
}

// TestGui48DeviceEventPopupNoSession：device 事件 + 无活动会话 → 弹窗。
func TestGui48DeviceEventPopupNoSession(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	usb := adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate(nil)               // 首块基线
	a.applyTrackUpdate([]adb.Device{usb}) // USB device 事件（已建档，首块后）
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "601c9f08" || np.ConnType != "usb" {
		t.Fatalf("device 事件 + 无会话应弹窗（USB）: %+v", np)
	}
}
