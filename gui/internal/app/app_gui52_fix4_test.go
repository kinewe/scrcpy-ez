package app

import (
	"context"
	"sync"
	"testing"
)

// gui52fix4：缺席验尸——档案 active 地址全部探测失败 → 全部降级 stale。
func TestGui52Fix4ProbeActiveAllFailToStale(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:44125", State: AddrStateActive, Mode: ModeTls},
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		return "cannot connect to " + addr, nil
	}

	a.probeProfileActiveAddrs(context.Background(), "Xiaomi Pad 8 Pro")
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if ae := gui24FindAddr(e, "192.168.31.162:44125"); ae == nil || ae.State != AddrStateStale {
		t.Fatalf("缺席验尸：TLS 探测不通应 stale: %+v", ae)
	}
	if ae := gui24FindAddr(e, "192.168.31.162:5555"); ae == nil || ae.State != AddrStateStale {
		t.Fatalf("缺席验尸：5555 探测不通应 stale: %+v", ae)
	}
}

// gui52fix4：一通则保持 active，不通则 stale（每地址独立定生死）。
func TestGui52Fix4ProbeActiveMixed(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:44125", State: AddrStateActive, Mode: ModeTls},
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		if addr == "192.168.31.162:44125" {
			return "cannot connect to " + addr, nil
		}
		return "connected to " + addr, nil
	}

	a.probeProfileActiveAddrs(context.Background(), "Xiaomi Pad 8 Pro")
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if ae := gui24FindAddr(e, "192.168.31.162:44125"); ae == nil || ae.State != AddrStateStale {
		t.Fatalf("缺席验尸：44125 探测不通应 stale: %+v", ae)
	}
	if ae := gui24FindAddr(e, "192.168.31.162:5555"); ae == nil || ae.State != AddrStateActive {
		t.Fatalf("缺席验尸：5555 探测通应保持 active: %+v", ae)
	}
}

// gui52fix4：stale 地址不参与探测（幂等/零浪费）。
func TestGui52Fix4ProbeSkipsStale(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:44125", State: AddrStateStale, Mode: ModeTls},
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return "connected to " + addr, nil
	}

	a.probeProfileActiveAddrs(context.Background(), "Xiaomi Pad 8 Pro")
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "192.168.31.162:5555" {
		t.Fatalf("缺席验尸只应探测 active 地址（stale 跳过）: %v", got)
	}
}

// gui52fix4：无 active 地址/无档案 → 零探测零副作用。
func TestGui52Fix4ProbeNoActiveNoop(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs:      []AddrEntry{{Addr: "192.168.31.162:44125", State: AddrStateStale, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
	calls := 0
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		calls++
		return "connected to " + addr, nil
	}

	a.probeProfileActiveAddrs(context.Background(), "Xiaomi Pad 8 Pro")
	a.probeProfileActiveAddrs(context.Background(), "nonexistent")
	if calls != 0 {
		t.Fatalf("无 active/无档案时不应有任何探测: calls=%d", calls)
	}
}
