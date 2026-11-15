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
	return discovery.MdnsService{Type: "_adb._tcp", Name: "adb-TEST0002", Addr: ip + ":5555", Mode: discovery.MdnsModeTcpip}
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

// Presence events are candidates. Positive event tests also provide a reachable,
// authenticated endpoint and wait for the asynchronous verification result.
func installMdnsVerificationFakes(t *testing.T, a *App) {
	t.Helper()
	a.disc.TcpProbeFn = func(context.Context, string) bool { return true }
	a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) { return "connected to " + addr, nil }
	a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { return "", nil }
	t.Cleanup(a.closeChannelChecks)
}

func waitMdnsVerified(t *testing.T, a *App, key, addr string) {
	t.Helper()
	waitForMdns(t, "mDNS candidate must pass authenticated verification", func() bool {
		e, ok := a.profiles.Entry(key)
		if !ok {
			return false
		}
		for _, candidate := range e.Addrs {
			if candidate.Addr == addr && candidate.State == AddrStateActive {
				return true
			}
		}
		return false
	})
}

// TestGui48MdnsAddedBuildsPendingAndTlsMark：广播 added(tls) 且档案无此 identity →
// 待配对卡；档案有 identity → 入档 active 并点亮 TLS 标。
func TestGui48MdnsAddedBuildsPendingAndTlsMark(t *testing.T) {
	a, _ := newTestApp()
	installMdnsVerificationFakes(t, a)
	a.applyTrackUpdate(nil)

	// 未知 identity：待配对卡
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{mdnsTlsSvc("adb-abc123-Xy9zQ2", "192.0.2.77", "36329")},
		First:    true,
	})
	a.mu.RLock()
	pending := append([]PendingDevice(nil), a.pending...)
	a.mu.RUnlock()
	if len(pending) != 1 || pending[0].Addr != "192.0.2.77:36329" {
		t.Fatalf("未知 TLS 广播应进入待配对卡: %+v", pending)
	}

	// 已知 identity：档案入档 active + TLS 标
	gui31K80Profiles(a)
	wifi := adb.Device{Serial: "192.0.2.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifi})
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: []discovery.MdnsService{mdnsTlsSvc("adb-TEST0001-KWqpio", "192.0.2.197", "45005")},
		First:    false,
	})
	waitMdnsVerified(t, a, "REDMI K80", "192.0.2.197:45005")
	if mdnsTlsAddrStale(a, "REDMI K80", "192.0.2.197:45005") {
		t.Fatal("广播 added 后地址不应 stale")
	}
	devs := a.Snapshot().Devices
	found := false
	for _, d := range devs {
		if d.Identity == "REDMI K80" || d.Serial == "TEST0001" || d.Wireless == "192.0.2.197:45005" {
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
	installMdnsVerificationFakes(t, a)

	svc := mdnsTlsSvc("adb-TEST0001-KWqpio", "192.0.2.197", "45005")
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: []discovery.MdnsService{svc}, First: true})
	waitMdnsVerified(t, a, "REDMI K80", svc.Addr)
	a.disc.TcpProbeFn = func(context.Context, string) bool { return false }
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: false}) // gone

	waitForMdns(t, "gone 探测不通应打 stale", func() bool {
		return mdnsTlsAddrStale(a, "REDMI K80", svc.Addr)
	})

	a.disc.TcpProbeFn = func(context.Context, string) bool { return true }
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: []discovery.MdnsService{svc}, First: false}) // 再现并验证
	waitMdnsVerified(t, a, "REDMI K80", svc.Addr)
	if mdnsTlsAddrStale(a, "REDMI K80", svc.Addr) {
		t.Fatal("广播再现后地址应翻回 active（不 stale）")
	}
}

// A stored mDNS record cannot mask failed endpoint verification after removal.
func TestGui48DroppedRechecksDespiteMdnsPresence(t *testing.T) {
	a, _ := newTestApp()
	installMdnsVerificationFakes(t, a)
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "12345TESTA", []string{"TEST0001"}, []string{"192.0.2.197:45005"}),
	})
	a.applyTrackUpdate(nil)
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true})

	wifi := adb.Device{Serial: "192.0.2.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{wifi})
	svc := mdnsTlsSvc("adb-TEST0001-KWqpio", "192.0.2.197", "45005")
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: []discovery.MdnsService{svc}, First: false})
	waitMdnsVerified(t, a, "REDMI K80", svc.Addr)

	// Device disappears; the old announcement remains, but the endpoint fails.
	a.disc.TcpProbeFn = func(context.Context, string) bool { return false }
	a.applyTrackUpdate(nil)
	a.onDropped("REDMI K80")
	waitForMdns(t, "failed endpoint must be stale even with cached mDNS", func() bool { return mdnsTlsAddrStale(a, "REDMI K80", svc.Addr) })

	// mdns 也消失 → 广播 removed → TCP 探测不通 → 该地址打 stale
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool { return false }
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: false})
	a.onMdnsDropped(mdnsServiceDropKey(svc), svc)
	waitForMdns(t, "设备流+mdns 流均消失应打 stale", func() bool {
		return mdnsTlsAddrStale(a, "REDMI K80", svc.Addr)
	})
}

// USB remains usable while an independently failed TLS endpoint becomes stale.
func TestGui48DroppedTlsDoesNotAffectUSB(t *testing.T) {
	a, _ := newTestApp()
	a.disc.TcpProbeFn = func(context.Context, string) bool { return false }
	t.Cleanup(a.closeChannelChecks)
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "12345TESTA", []string{"TEST0001"}, []string{"192.0.2.197:45005"}),
	})

	usb := adb.Device{Serial: "TEST0001", State: "device", ConnType: "usb", Name: "REDMI K80", Identity: "REDMI K80"}
	a.applyTrackUpdate([]adb.Device{usb}) // 设备流首块即含 USB（在线事实）
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true})
	a.onDropped("REDMI K80")
	waitForMdns(t, "USB must not mask a failed TLS endpoint", func() bool { return mdnsTlsAddrStale(a, "REDMI K80", "192.0.2.197:45005") })
	devs := a.Snapshot().Devices
	if len(devs) != 1 || devs[0].ConnType != "usb" || devs[0].State != "device" {
		t.Fatalf("failed TLS affected USB: %+v", devs)
	}
}

// TestGui48StartupBaselineMarksAbsentStale：设备流首块 + mdns 流首块均到齐后，
// 档案设备既不在设备流也不在 mdns 广播集 → 一次性打 stale（K80 假在线回归）。
func TestGui48StartupBaselineMarksAbsentStale(t *testing.T) {
	a, _ := newTestApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "12345TESTA", []string{"TEST0001"}, []string{"192.0.2.197:45005"}),
	})
	a.applyTrackUpdate(nil)                                                                    // 设备流首块（空）
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: nil, First: true}) // mdns 首块（空）
	if !mdnsTlsAddrStale(a, "REDMI K80", "192.0.2.197:45005") {
		t.Fatal("启动基线对账应把既不在设备流也不在 mdns 流的档案条目标 stale（K80 假在线修复）")
	}
}

// TestGui48MdnsReconnectFirstSnapshotNoRemoved：流断重连首块全量对账，不误打 stale。
func TestGui48MdnsReconnectFirstSnapshotNoRemoved(t *testing.T) {
	a, _ := newTestApp()
	gui31K80Profiles(a)

	svc := mdnsTlsSvc("adb-TEST0001-KWqpio", "192.0.2.197", "45005")
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
