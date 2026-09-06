package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-mdns5 语义更新：90s 无信号→stale；冷启动只搜 5555 ---

func fix2Seed(a *App, addr string) {
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs:      []AddrEntry{{Addr: addr, State: AddrStateActive, LastOk: 100, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	})
}

func fix2Addr(a *App, addr string) *AddrEntry {
	e, ok := a.profiles.Entry("REDMI K80")
	if !ok {
		return nil
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == addr {
			return &e.Addrs[i]
		}
	}
	return nil
}

func TestGui48Mdns5IdleSignalStale(t *testing.T) {
	a, _ := newWirelessApp()
	addr := "192.168.31.197:45005"
	fix2Seed(a, addr)
	// gui48-mdns8：90s 无信号改为「静默问询」——TCP 探测不通才打 stale。
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool { return false }

	a.onMdnsIdleSignal(addr)
	waitForMdns(t, "静默问询不通应打 stale", func() bool {
		if ae := fix2Addr(a, addr); ae != nil {
			return ae.Stale
		}
		return false
	})
}

func TestGui48Mdns5ColdSearchAllAddrs(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
			{Addr: "192.168.31.197:45005", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})
	var mu sync.Mutex
	var calls []string
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.197:5555" {
			return "connected to " + addr, nil
		}
		return "cannot connect to " + addr, nil
	}

	a.coldSearch5555(context.Background())
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 2 || !(contains(got, "192.168.31.197:5555") && contains(got, "192.168.31.197:45005")) {
		t.Fatalf("冷启动应全量搜索全部地址（含 TLS 随机端口）: %v", got)
	}
	e, _ := a.profiles.Entry("REDMI K80")
	if ae := gui24FindAddr(e, "192.168.31.197:5555"); ae == nil || ae.State != AddrStateActive || ae.Stale {
		t.Fatalf("冷启动 5555 connect 成功应 active: %+v", ae)
	}
	// gui52fix4：TLS 地址也被探测——不通 → stale（不再保持僵尸 active）
	if ae := gui24FindAddr(e, "192.168.31.197:45005"); ae == nil || ae.State != AddrStateStale {
		t.Fatalf("冷启动 TLS connect 不通应 stale: %+v", ae)
	}
}

func TestGui48Mdns5ColdSearchFailureStale(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs:      []AddrEntry{{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		return "cannot connect to " + addr, nil
	}

	a.coldSearch5555(context.Background())
	e, _ := a.profiles.Entry("REDMI K80")
	ae := gui24FindAddr(e, "192.168.31.197:5555")
	if ae == nil || ae.State != AddrStateStale {
		t.Fatalf("冷启动 connect 失败应 stale（gui52fix4 硬事实降级）: %+v", ae)
	}
}

func TestGui48Mdns5ColdSearchOnlyOnce(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs:      []AddrEntry{{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	})
	var mu sync.Mutex
	calls := 0
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return "connected to " + addr, nil
	}

	a.coldSearch5555(context.Background())
	a.coldSearch5555(context.Background())
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("冷启动 5555 搜索只执行一次: calls=%d", n)
	}
}

func TestGui48Mdns5WirelessIPFilled(t *testing.T) {
	a, _ := newWirelessApp()
	fix2Seed(a, "192.168.31.197:45005")
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	}
	a.decorateTls(devs)
	if devs[0].WirelessIP != "192.168.31.197:45005" {
		t.Fatalf("WirelessIP 应填档案 active 排序地址: %+v", devs[0])
	}

	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:45005")
	devs = []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	}
	a.decorateTls(devs)
	if devs[0].WirelessIP != "" {
		t.Fatalf("无 active 地址时 WirelessIP 应为空（stale 不是在线显示证据）: %+v", devs[0])
	}
}
