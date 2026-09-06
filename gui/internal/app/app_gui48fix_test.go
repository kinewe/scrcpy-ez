package app

import (
	"context"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-p2fix：基线 stale once / 弹窗超时 timer / ForceDiscover 回退权威列表 ---

func fixSeedK80Tls(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:45005"}),
	})
}

func fixK80TlsStale(a *App) bool {
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok {
		return false
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.197:45005" {
			return e.Addrs[i].Stale
		}
	}
	return false
}

// TestGui48FixStartupBaselineOnceDeviceFirst：设备流首块先到、mdns 首块后到 → 执行一次；
// mdns 重连首块（First=true 第二次）不重复打标。
func TestGui48FixStartupBaselineOnceDeviceFirst(t *testing.T) {
	a, _ := newTestApp()
	fixSeedK80Tls(a)

	a.applyTrackUpdate(nil) // 设备流首块先到（mdns 未到 → 不执行）
	if fixK80TlsStale(a) {
		t.Fatal("mdns 首块未到，基线对账不应执行")
	}

	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true})
	if !fixK80TlsStale(a) {
		t.Fatal("双首块首次到齐应执行一次并打 stale")
	}

	// 模拟广播再现翻回 active 后，mdns 重连首块不得重复跑基线对账
	a.profiles.AddrSuccessMode("REDMI K80", "192.168.31.197:45005", ModeTls)
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true})
	if fixK80TlsStale(a) {
		t.Fatal("mdns 重连首块不得重复跑基线 stale 对账")
	}
}

// TestGui48FixStartupBaselineOnceMdnsFirst：mdns 首块先到、设备流首块后到 → 执行一次。
func TestGui48FixStartupBaselineOnceMdnsFirst(t *testing.T) {
	a, _ := newTestApp()
	fixSeedK80Tls(a)

	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true})
	if fixK80TlsStale(a) {
		t.Fatal("设备流首块未到，基线对账不应执行")
	}

	a.applyTrackUpdate(nil)
	if !fixK80TlsStale(a) {
		t.Fatal("双首块到齐应执行一次并打 stale")
	}
}

// TestGui48FixPopupExpireTimer：弹窗显示时起一次性 timer，无事件也会自动消失。
func TestGui48FixPopupExpireTimer(t *testing.T) {
	old := popupExpireTimeout
	popupExpireTimeout = 50 * time.Millisecond
	defer func() { popupExpireTimeout = old }()

	a, _ := newTestApp()
	a.applyTrackUpdate(nil)
	dev := adb.Device{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"}
	a.applyTrackUpdate([]adb.Device{dev})
	if a.Snapshot().NewDevice == nil {
		t.Fatal("应弹窗")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if a.Snapshot().NewDevice == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("弹窗 30s（测试注入 50ms）timer 应自动清除弹窗，且不依赖事件")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
