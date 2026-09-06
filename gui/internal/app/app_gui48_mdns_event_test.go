package app

import (
	"context"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui48-p2 mDNS 服务流事件化 + 离线卡联合判据测试 ---

func mdnsTlsSvc(name, ip, port string) discovery.MdnsService {
	return discovery.MdnsService{Type: "_adb-tls-connect._tcp", Name: name, Addr: ip + ":" + port, Mode: discovery.MdnsModeTls}
}

func mdnsTcpipSvc(ip string) discovery.MdnsService {
	return discovery.MdnsService{Type: "_adb._tcp", Name: "adb-a743e1df", Addr: ip + ":5555", Mode: discovery.MdnsModeTcpip}
}

func mdnsTlsAddrStale(a *App, key, addr string) bool {
	e, ok := a.profiles.Entry(key)
	if !ok {
		return false
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == addr {
			return e.Addrs[i].Stale
		}
	}
	return false
}

// TestGui48MdnsAddedBuildsPendingAndTlsMark：广播 added(tls) 且档案无此 identity →
// 待配对卡；档案有 identity → 入档 active 并点亮 TLS 标。
func TestGui48MdnsAddedBuildsPendingAndTlsMark(t *testing.T) {
	a, _ := newTestApp()
	a.applyTrackUpdate(nil)

	// 未知 identity：待配对卡
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{mdnsTlsSvc("adb-abc123-Xy9zQ2", "192.168.31.77", "36329")},
		First:    true,
	})
	a.mu.RLock()
	pending := append([]PendingDevice(nil), a.pending...)
	a.mu.RUnlock()
	if len(pending) != 1 || pending[0].Addr != "192.168.31.77:36329" {
		t.Fatalf("未知 TLS 广播应进入待配对卡: %+v", pending)
	}

	// 已知 identity：档案入档 active + TLS 标
	gui31K80Profiles(a)
	wifi := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifi})
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")},
		First:    false,
	})
	if mdnsTlsAddrStale(a, "REDMI K80", "192.168.31.197:45005") {
		t.Fatal("广播 added 后地址不应 stale")
	}
	devs := a.Snapshot().Devices
	found := false
	for _, d := range devs {
		if d.Identity == "REDMI K80" || d.Serial == "601c9f08" || d.Wireless == "192.168.31.197:45005" {
			if !d.Tls {
				t.Fatalf("TLS 广播在播应点亮 TLS 标: %+v", d)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("应找到 K80 设备卡: %+v", devs)
	}
}

// TestGui48MdnsGoneProbeFailStaleAndReappearActive：广播 removed（Goodbye/门铃确认）
// → TCP 探测不通才打 stale（gui48-mdns8）；广播再现 → 翻回 active。
func TestGui48MdnsGoneProbeFailStaleAndReappearActive(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool { return false }

	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: []discovery.MdnsService{svc}, First: true})
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: false}) // gone

	waitForMdns(t, "gone 探测不通应打 stale", func() bool {
		return mdnsTlsAddrStale(a, "REDMI K80", svc.Addr)
	})

	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: []discovery.MdnsService{svc}, First: false}) // 再现
	if mdnsTlsAddrStale(a, "REDMI K80", svc.Addr) {
		t.Fatal("广播再现后地址应翻回 active（不 stale）")
	}
}

// TestGui48OfflineJointRequiresMdnsGone：设备流 removed + mdns 仍在 → 乐观在线
// （不打 stale）；mdns 也消失后才打 stale。
func TestGui48OfflineJointRequiresMdnsGone(t *testing.T) {
	a, _ := newTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:45005"}),
	})
	a.applyTrackUpdate(nil)
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true})

	wifi := adb.Device{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifi})
	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: []discovery.MdnsService{svc}, First: false})

	// 设备流 removed，但 mdns 仍在 → 不打 stale
	a.applyTrackUpdate(nil)
	a.onDropped("REDMI K80")
	if mdnsTlsAddrStale(a, "REDMI K80", svc.Addr) {
		t.Fatal("仅设备流 removed 而 mdns 仍有广播 → 不应打 stale（乐观在线）")
	}

	// mdns 也消失 → 广播 removed → TCP 探测不通 → 该地址打 stale
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool { return false }
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: false})
	a.onMdnsDropped(mdnsServiceDropKey(svc), svc)
	waitForMdns(t, "设备流+mdns 流均消失应打 stale", func() bool {
		return mdnsTlsAddrStale(a, "REDMI K80", svc.Addr)
	})
}

// TestGui48OfflineJointUsbExempt：设备流里有 USB 条目 → 联合判据不生效。
func TestGui48OfflineJointUsbExempt(t *testing.T) {
	a, _ := newTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:45005"}),
	})

	usb := adb.Device{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{usb}) // 设备流首块即含 USB（在线事实）
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true})
	a.onDropped("REDMI K80")
	if mdnsTlsAddrStale(a, "REDMI K80", "192.168.31.197:45005") {
		t.Fatal("USB 在线豁免：mdns 无广播也不应打 stale")
	}
}

// TestGui48StartupBaselineMarksAbsentStale：设备流首块 + mdns 流首块均到齐后，
// 档案设备既不在设备流也不在 mdns 广播集 → 一次性打 stale（K80 假在线回归）。
func TestGui48StartupBaselineMarksAbsentStale(t *testing.T) {
	a, _ := newTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:45005"}),
	})
	a.applyTrackUpdate(nil)                                                                    // 设备流首块（空）
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true}) // mdns 首块（空）
	if !mdnsTlsAddrStale(a, "REDMI K80", "192.168.31.197:45005") {
		t.Fatal("启动基线对账应把既不在设备流也不在 mdns 流的档案条目标 stale（K80 假在线修复）")
	}
}

// TestGui48MdnsReconnectFirstSnapshotNoRemoved：流断重连首块全量对账，不误打 stale。
func TestGui48MdnsReconnectFirstSnapshotNoRemoved(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	svc := mdnsTlsSvc("adb-601c9f08-KWqpio", "192.168.31.197", "45005")
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: []discovery.MdnsService{svc}, First: true})

	added, removed := a.applyMdnsSnapshot(nil, true, nil, nil) // 重连首块（空快照）
	if len(removed) != 0 {
		t.Fatalf("重连首块不应产生 removed 事件: %+v", removed)
	}
	if len(added) != 0 {
		t.Fatalf("空首块不应产生 added 事件: %+v", added)
	}
	if mdnsTlsAddrStale(a, "REDMI K80", svc.Addr) {
		t.Fatal("重连首块空快照不应误打 stale")
	}
}
