package app

import (
	"testing"
)

// --- gui38（mdns5 保留部分）：recentOkAddr 二态收敛（gui52）---
// 判据只认 state：active TLS 优先 → active tcpip；全 stale → 空串；
// LastOk/Fail/Stale 布尔一律不参与。

func TestGui38RecentOkAddrTlsFormPriority(t *testing.T) {
	e := DeviceEntry{
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 400, Mode: ModeTcpip},
			{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 300, Mode: ModeTls},
		},
	}
	if got := recentOkAddr(e); got != "192.168.31.197:42449" {
		t.Fatalf("TLS 形态优先，应返回 TLS 42449 而非 tcpip 5555: %q", got)
	}
}

func TestGui38RecentOkAddrNoTlsFallsBackTcpip(t *testing.T) {
	e := DeviceEntry{
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 400, Mode: ModeTcpip},
			{Addr: "192.168.31.162:5555", State: AddrStateStale, LastOk: 350, Mode: ModeTcpip},
			{Addr: "192.168.31.197:42449", State: AddrStateStale, LastOk: 0, Mode: ModeTls},
		},
	}
	if got := recentOkAddr(e); got != "192.168.31.197:5555" {
		t.Fatalf("TLS stale 后应回退 active tcpip 5555: %q", got)
	}
}

func TestGui38RecentOkAddrNoSuccessReturnsEmpty(t *testing.T) {
	e := DeviceEntry{
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:5555", State: AddrStateStale, LastOk: 100, Mode: ModeTcpip},
			{Addr: "192.168.31.197:42449", State: AddrStateStale, LastOk: 200, Mode: ModeTls},
		},
	}
	if got := recentOkAddr(e); got != "" {
		t.Fatalf("全 stale 应返回空串（离线卡回退 USB serial）: %q", got)
	}
}
