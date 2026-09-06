package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func hasCard(a *App, serial string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for i := range a.devices {
		if a.devices[i].Serial == serial {
			return true
		}
	}
	return false
}

// gui52-fix16：删除 USB 在线设备（线还插着=设备流持续推送）后，显示层应隐藏该卡；
// 拔线再插（added 清删除标记）后恢复显示（重新学习）。
func TestGui52Fix16DeletedUsbHiddenUntilReplug(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.mu.Lock()
	a.profiles.data.Devices["HUAWEI FLA-TL10"] = &DeviceEntry{
		Marketname: "HUAWEI FLA-TL10",
		Serials:    []string{"H9RNW18604002288"},
		Profiles:   DefaultProfile(),
	}
	a.profiles.data.DeviceOrder = []string{"HUAWEI FLA-TL10"}
	a.profiles.mu.Unlock()

	usb := []adb.Device{{Serial: "H9RNW18604002288", State: "device", ConnType: "usb"}}
	a.applyTrackUpdate(usb)
	if !hasCard(a, "H9RNW18604002288") {
		t.Fatalf("删除前应显示华为卡")
	}

	if err := a.DeleteDevices([]string{"HUAWEI FLA-TL10"}); err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}
	a.applyTrackUpdate(usb) // 线还插着：设备流继续推送
	if hasCard(a, "H9RNW18604002288") {
		t.Fatalf("删除后设备流卡应隐藏（拔线重插前不显示）")
	}

	// 拔线（removed usb）：删除后拔线 = 正常收尾——不得启动拔线遮罩（断开中卡）。
	a.applyTrackUpdate(nil)
	a.teachMu.Lock()
	_, unplugShield := a.unplugging["H9RNW18604002288"]
	a.teachMu.Unlock()
	if unplugShield {
		t.Fatalf("删除后拔线不应启动拔线遮罩（断开中卡幽灵）")
	}

	// 重插（added）：设备流带市场名/identity——学习入档后档案键（=HUAWEI FLA-TL10）会重建，
	// 残留删除标记若只清 serial 会按身份键命中导致「插回来不显示」（fix16b 修复点）。
	replug := []adb.Device{{Serial: "H9RNW18604002288", State: "device", ConnType: "usb",
		Marketname: "HUAWEI FLA-TL10", Identity: "HUAWEI FLA-TL10"}}
	a.applyTrackUpdate(replug)
	if !hasCard(a, "H9RNW18604002288") {
		t.Fatalf("重插后应恢复显示华为卡（重新学习）")
	}
}

// gui52-fix16d：残留下限——added/offline 帧无 marketname（clearDeletedUsbForAdded
// 清不全身份键）→ 学习入档重建档案键 → identityOf 命中残留键「再也找不回」。
// 学习完成（对齐档案）必须清该设备全部键。
func TestGui52Fix16dLearnClearsAllKeys(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.mu.Lock()
	a.profiles.data.Devices["HUAWEI FLA-TL10"] = &DeviceEntry{
		Marketname: "HUAWEI FLA-TL10",
		Serials:    []string{"H9RNW18604002288"},
		Profiles:   DefaultProfile(),
	}
	a.profiles.data.DeviceOrder = []string{"HUAWEI FLA-TL10"}
	a.profiles.mu.Unlock()

	// 删除（USB 在线）→ 标记双键
	a.applyTrackUpdate([]adb.Device{{Serial: "H9RNW18604002288", State: "device", ConnType: "usb"}})
	if err := a.DeleteDevices([]string{"HUAWEI FLA-TL10"}); err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}
	if !a.deletedUsbMarked("HUAWEI FLA-TL10") || !a.deletedUsbMarked("H9RNW18604002288") {
		t.Fatalf("删除标记应双键在档")
	}

	// 拔线 → 重插（added/offline 帧无 marketname——模拟真实 adb 枚举）
	a.applyTrackUpdate(nil)
	offline := []adb.Device{{Serial: "H9RNW18604002288", State: "offline", ConnType: "usb"}}
	a.applyTrackUpdate(offline)
	// gui52-fix17b：added 帧按索引全清——offline 首帧即清身份键（不再需要等学习完成）
	if a.deletedUsbMarked("HUAWEI FLA-TL10") {
		t.Fatalf("fix17b 后 offline added 帧应已按索引全清（含市场名键）")
	}

	// 学习入档（档案键重建）+ fix16d 清全部键
	a.profiles.mu.Lock()
	a.profiles.data.Devices["HUAWEI FLA-TL10"] = &DeviceEntry{
		Marketname: "HUAWEI FLA-TL10",
		Serials:    []string{"H9RNW18604002288"},
		Profiles:   DefaultProfile(),
	}
	a.profiles.mu.Unlock()
	// 模拟学习完成（alignWirelessIP 路径）：档案键就绪后清全部
	a.alignWirelessIP("H9RNW18604002288", "192.168.31.242")
	if a.deletedUsbMarked("HUAWEI FLA-TL10") || a.deletedUsbMarked("H9RNW18604002288") {
		t.Fatalf("学习完成后应清空全部删除标记")
	}

	// 下一帧 device（带市场名）——不再被过滤，卡恢复
	dev := []adb.Device{{Serial: "H9RNW18604002288", State: "device", ConnType: "usb",
		Marketname: "HUAWEI FLA-TL10"}}
	a.applyTrackUpdate(dev)
	if !hasCard(a, "H9RNW18604002288") {
		t.Fatalf("学习完成后卡应恢复显示（不再被残留键过滤）")
	}
}
func TestGui52Fix16DeletedUsbMatchBySerial(t *testing.T) {
	a, _ := newWirelessApp()
	a.setDeletedUsb("H9RNW18604002288") // 直接按序列号键（模拟删除时记录）
	d := &adb.Device{Serial: "H9RNW18604002288", State: "device", ConnType: "usb"}
	if !a.deletedUsbMatch(d) {
		t.Fatalf("序列号键应命中删除标记")
	}
	d2 := &adb.Device{Serial: "a743e1df", State: "device", ConnType: "usb"}
	if a.deletedUsbMatch(d2) {
		t.Fatalf("无关设备不应命中")
	}
}
