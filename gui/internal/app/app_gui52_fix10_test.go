package app

import (
	"context"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/discovery"
)

// gui52-fix10：配对入档前按 mdns 快照重解析权威 TLS 端口——
// pair 返回值/早期快照端口可能过时（HyperOS 配对期间重分配），
// 以设备自报的 _adb-tls-connect 服务为准入档（45287→35195 教训）。

func TestGui52Fix10PairArchiveUsesMdnsAuthorityPort(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to 192.168.1.2:33895", nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "", nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		if prop == "service.adb.tcp.port" {
			return "5555", nil // 已开（fix8 幂等分支；不触发 tcpip）
		}
		return "Xiaomi Pad 8 Pro", nil
	}
	// mdns 权威：设备实际广播 35195（配对界面/pair 返回的 33895 已过时）
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return []discovery.MdnsService{
			{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-newport", Addr: "192.168.1.2:35195", Mode: discovery.MdnsModeTls},
		}, nil
	}
	a.pairOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }

	svc := seedPending(t, a, pairConnectSvcs())
	if err := a.PairConnect(svc.Key, "192.168.1.2", "37033", "33895", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)

	// 档案 TLS 应为 mdns 权威端口 35195，而非 pair 返回的 33895
	waitFor(t, 2*time.Second, func() bool {
		e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
		return ok && gui50Fix45EntryHasAddr(e, "192.168.1.2:35195", ModeTls)
	}, "配对入档应用 mdns 权威 TLS 端口")
	e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatal("配对应建档")
	}
	if gui50Fix45EntryHasAddr(e, "192.168.1.2:33895", ModeTls) {
		t.Fatalf("过时的 pair 返回端口不应入档: %+v", e.Addrs)
	}
	if !contains(e.Serials, "a743e1df") {
		t.Fatalf("权威服务名应解析短号: %+v", e.Serials)
	}
	if e.TlsGuid == "" {
		t.Fatalf("TlsGuid 不应为空: %+v", e)
	}
}

func TestGui52Fix10NoMdnsServiceFallbackPairPort(t *testing.T) {
	// mdns 现场扫不到 TLS 服务（广播未回）→ 保持 pair 返回端口入档（回退不破）。
	a, _ := newWirelessApp()
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to 192.168.1.2:33895", nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		return "", nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		if prop == "service.adb.tcp.port" {
			return "5555", nil
		}
		return "Xiaomi Pad 8 Pro", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil
	}
	a.pairOps.tcpipFn = func(ctx context.Context, serial, port string) error { return nil }

	svc := seedPending(t, a, pairConnectSvcs())
	if err := a.PairConnect(svc.Key, "192.168.1.2", "37033", "33895", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)
	waitFor(t, 2*time.Second, func() bool {
		e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
		return ok && gui50Fix45EntryHasAddr(e, "192.168.1.2:33895", ModeTls)
	}, "无 mdns 权威时应回退 pair 返回端口入档")
	e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatal("配对应建档")
	}
	if !contains(e.Serials, "a743e1df") {
		t.Fatalf("应仍解析短号: %+v", e.Serials)
	}
}
