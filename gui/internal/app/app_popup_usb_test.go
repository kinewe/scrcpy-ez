package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- 弹窗判据 v2（新设备 + USB 插线触发，用户定稿） ---

func usbDev(serial, market, wireless string) adb.Device {
	return adb.Device{Serial: serial, State: "device", ConnType: "usb", Name: market,
		Marketname: market, Identity: market, Wireless: wireless}
}

func wifiDev(addr, market string) adb.Device {
	return adb.Device{Serial: addr, State: "device", ConnType: "wifi", Name: market,
		Marketname: market, Identity: market}
}

// 新设备（身份不在档案中）无条件弹：无线出现的新设备也弹
// （"仅无线不弹"只约束已建档设备）。
func TestPopupNewDeviceUnconditional(t *testing.T) {
	a, _ := multiTestApp()
	a.applyTrackUpdate([]adb.Device{wifiDev("192.168.31.197:5555", "Redmi K80")})
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "192.168.31.197:5555" || np.ConnType != "wifi" || np.Name != "Redmi K80" {
		t.Fatalf("新设备应无条件弹: %+v", np)
	}
}

// 已建档设备仅无线出现（无新 USB 插线）→ 不弹（用户从列表手动点投屏）。
func TestPopupProfiledWifiOnlyNoPop(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	devs := []adb.Device{wifiDev("192.168.31.197:5555", "REDMI K80")}
	setDevices(a, devs)
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("已建档仅无线出现不应弹: %+v", a.Snapshot().NewDevice)
	}
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("持续无线在线也不应弹: %+v", a.Snapshot().NewDevice)
	}
}

// 已建档设备 USB 插线事件 → 弹（USB）：上轮无线就绪、本轮 USB 条目新出现
// （用户原话：档案里有、无线就绪、插上线=大概率要投屏）。
func TestPopupProfiledUSBPlugPops(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C", []string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	})
	// 上轮：仅无线在线（无线就绪）→ 不弹
	devsWifi := []adb.Device{wifiDev("192.168.31.162:5555", "Xiaomi Pad 8 Pro")}
	setDevices(a, devsWifi)
	a.applyTrackUpdate(devsWifi)
	if a.Snapshot().NewDevice != nil {
		t.Fatal("无线就绪阶段不应弹")
	}
	// 本轮：USB 插线（新 USB 条目出现，合并卡 USB 主 transport）→ 弹（USB）
	devsBoth := []adb.Device{usbDev("a743e1df", "Xiaomi Pad 8 Pro", "192.168.31.162:5555")}
	setDevices(a, devsBoth)
	a.applyTrackUpdate(devsBoth)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "a743e1df" || np.ConnType != "usb" {
		t.Fatalf("USB 插线应弹（USB）: %+v", np)
	}
	// 30s 防重复窗口内不重复弹（插线事件只触发一次）
	a.mu.Lock()
	a.popup.info = nil
	a.mu.Unlock()
	a.applyTrackUpdate(devsBoth)
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("30s 防重复窗口内不应再弹: %+v", a.Snapshot().NewDevice)
	}
}

// 已建档设备：USB 在线 + 无线就绪，但启动前就插着线（首轮基线、无插线事件）→
// 不弹（v3 修复：删"无线就绪未投屏"状态判定——静止状态不骚扰，只认真实拔插）。
func TestPopupProfiledUSBWirelessReadyFirstPollNoPop(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C", []string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	})
	devs := []adb.Device{usbDev("a743e1df", "Xiaomi Pad 8 Pro", "192.168.31.162:5555")}
	setDevices(a, devs)
	a.applyTrackUpdate(devs) // 首轮：无插线事件（基线）→ 不弹
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("启动前已插线（无线就绪但无插线事件）不应弹: %+v", a.Snapshot().NewDevice)
	}
	// 拔线再插 = 新插线事件 → 弹（USB）
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "a743e1df" || np.ConnType != "usb" {
		t.Fatalf("拔线再插应弹（USB）: %+v", np)
	}
}

// 已建档设备：USB-only 启动前已插线 → 首轮基线不弹；拔线再插 = 新插线事件 → 弹。
func TestPopupProfiledUSBReplugPops(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"Redmi K80": mkEntry("Redmi K80", "24117RK2CC", []string{"601c9f08"}, nil),
	})
	devs := []adb.Device{usbDev("601c9f08", "Redmi K80", "")}
	setDevices(a, devs)
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("启动前已插线（首轮基线）不应弹: %+v", a.Snapshot().NewDevice)
	}
	// 拔线（无设备）
	a.applyTrackUpdate(nil)
	// 再插 = 新插线事件 → 弹
	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "601c9f08" || np.ConnType != "usb" {
		t.Fatalf("拔线再插应弹（USB）: %+v", np)
	}
}

// 21:09 实况回归：设备线一直插着（从未拔插），投屏会话结束后（停止投屏）→
// 绝不弹（v3 只认真实拔插事件）；拔掉再插才弹。
func TestPopupNoRepopAfterStopCastWhilePlugged(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	// K80 USB+无线合并卡（线一直插着、无线也在线——v2 曾因"无线就绪未投屏"误弹）
	devs := []adb.Device{usbDev("601c9f08", "REDMI K80", "192.168.31.197:5555")}
	setDevices(a, devs)
	a.applyTrackUpdate(devs) // 启动基线：已插 → 不弹
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("启动基线不应弹: %+v", a.Snapshot().NewDevice)
	}

	// 投屏中（事件驱动弹窗已取代快照 diff）
	if err := a.StartCast("601c9f08"); err != nil {
		t.Fatal(err)
	}
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("投屏中不应弹: %+v", a.Snapshot().NewDevice)
	}

	// 停止投屏：线仍插着（无拔插事件）→ 不弹
	a.OnBatExit("601c9f08", 0)
	a.applyTrackUpdate(devs)
	if np := a.Snapshot().NewDevice; np != nil && np.Serial == "601c9f08" {
		t.Fatalf("停止投屏后线仍插着不应弹（实况回归）: %+v", np)
	}
	// 结束态 GC 后（会话移除）：线仍插着 → 仍不弹（从未拔除）
	a.ForgetSession("601c9f08")
	a.applyTrackUpdate(devs)
	if np := a.Snapshot().NewDevice; np != nil && np.Serial == "601c9f08" {
		t.Fatalf("会话移除后线仍插着也不应弹: %+v", np)
	}

	// 拔掉再插 = 真实拔插事件 → 弹（新周期）
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "601c9f08" || np.ConnType != "usb" {
		t.Fatalf("拔掉再插应弹（USB）: %+v", np)
	}
}

// "暂不"后本插线周期不再弹；拔线再插 = 新周期 → 重新弹（清除暂不+防重复窗口）。
func TestPopupDismissReplugNewCycle(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"Redmi K80": mkEntry("Redmi K80", "24117RK2CC", []string{"601c9f08"}, nil),
	})
	devs := []adb.Device{usbDev("601c9f08", "Redmi K80", "")}
	setDevices(a, devs)
	a.applyTrackUpdate(nil)  // 基线（无设备）
	a.applyTrackUpdate(devs) // 插线 → 弹
	if a.Snapshot().NewDevice == nil {
		t.Fatal("插线应弹")
	}
	a.DismissNewDevice("601c9f08")
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatal("暂不后同插线周期不应弹")
	}
	// 拔线再插 → 新周期可弹
	a.applyTrackUpdate(nil)
	a.applyTrackUpdate(devs)
	if np := a.Snapshot().NewDevice; np == nil || np.Serial != "601c9f08" {
		t.Fatalf("拔线再插应重新弹: %+v", np)
	}
}

// 已开会话（投屏中）设备绝不弹；其他设备插线正常弹。
func TestPopupCastingDeviceNeverPops(t *testing.T) {
	a, _ := multiTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C", []string{"a743e1df"}, []string{"192.168.31.162:5555"}),
		"Redmi K80":        mkEntry("Redmi K80", "24117RK2CC", []string{"601c9f08"}, nil),
	})
	setDevices(a, []adb.Device{usbDev("a743e1df", "Xiaomi Pad 8 Pro", "192.168.31.162:5555")})
	if err := a.StartCast("a743e1df"); err != nil {
		t.Fatal(err)
	}
	// 基线轮询：仅平板（已开会话）
	a.applyTrackUpdate([]adb.Device{usbDev("a743e1df", "Xiaomi Pad 8 Pro", "192.168.31.162:5555")})
	// 平板投屏中：插 K80 USB → K80 弹（无会话），平板绝不弹
	devs := []adb.Device{
		usbDev("a743e1df", "Xiaomi Pad 8 Pro", "192.168.31.162:5555"),
		usbDev("601c9f08", "Redmi K80", ""),
	}
	setDevices(a, devs)
	a.applyTrackUpdate(devs)
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "601c9f08" {
		t.Fatalf("应弹 K80（平板投屏中绝不弹）: %+v", np)
	}
	// K80 也开会话后：都不弹
	a.DismissNewDevice("601c9f08")
	if err := a.StartCast("601c9f08"); err != nil {
		t.Fatal(err)
	}
	a.applyTrackUpdate(devs)
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("全部投屏中不应弹: %+v", a.Snapshot().NewDevice)
	}
}

// pollOnce 全链路（判据 v2 + 弹窗先于档案同步的顺序契约）：
// 已建档设备仅无线出现 → 不弹；同设备 USB 插线（本轮新增 USB 条目）→ 弹（USB）。
// 同时回归"弹窗必须在 SyncDevices 之前"：首见设备不得因同步入档而漏弹。
func TestPollOnceProfiledUSBPlugPops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n%s\\tdevice\\n' \"$FAKE_DEV\"; exit 0; fi\n" +
		"if [ \"$1\" = \"connect\" ]; then sleep 1; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	seedProfiles(a, map[string]*DeviceEntry{
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C", []string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	})

	// 轮询 1：已建档设备仅无线出现 → 不弹
	os.Setenv("FAKE_DEV", "192.168.31.162:5555")
	defer os.Unsetenv("FAKE_DEV")
	a.pollOnce(context.Background())
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("已建档仅无线出现不应弹: %+v", a.Snapshot().NewDevice)
	}
	// 轮询 2：USB 插线（本轮新增 USB 条目）→ 弹（USB）
	os.Setenv("FAKE_DEV", "a743e1df")
	a.pollOnce(context.Background())
	np := a.Snapshot().NewDevice
	if np == nil || np.Serial != "a743e1df" || np.ConnType != "usb" {
		t.Fatalf("USB 插线应弹（USB）: %+v", np)
	}
}
