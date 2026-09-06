package app

import (
	"testing"
)

// --- gui40（mdns5 保留部分）：recentOkAddr 二态收敛（gui52）---
// state 是唯一判据：active TLS → active tcpip；全 stale → 空串。
// lastOk/Stale 布尔不再参与显示。

func TestGui40RecentOkAddrStaleTlsSkip(t *testing.T) {
	cases := []struct {
		name  string
		addrs []AddrEntry
		want  string
	}{
		{
			name: "K80：tls stale → 回退 active tcpip 5555",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:35263", State: AddrStateStale, LastOk: 1787745659, Mode: ModeTls, Stale: true},
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 1787771715, Mode: ModeTcpip},
			},
			want: "192.168.31.197:5555",
		},
		{
			name: "平板：tls active → 仍显示 TLS（都 active 优先 tls）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.162:5555", State: AddrStateActive, LastOk: 1787771700, Mode: ModeTcpip},
				{Addr: "192.168.31.162:39419", State: AddrStateActive, LastOk: 1787771881, Mode: ModeTls},
			},
			want: "192.168.31.162:39419",
		},
		{
			name: "tls active 但 lastOk 更旧也优先（lastOk 不入判据）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 200, Mode: ModeTcpip},
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 100, Mode: ModeTls},
			},
			want: "192.168.31.197:42449",
		},
		{
			name: "tls stale + tcpip active → 回退 tcpip",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateStale, LastOk: 300, Mode: ModeTls, Stale: true},
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
			},
			want: "192.168.31.197:5555",
		},
		{
			name: "tls active lastOk=0 仍算 active（lastOk 不入判据）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 0, Mode: ModeTls},
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 1, Mode: ModeTcpip},
			},
			want: "192.168.31.197:42449",
		},
		{
			name: "全 stale（即使 lastOk>0）→ 空串",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateStale, LastOk: 200, Mode: ModeTls},
				{Addr: "192.168.31.197:5555", State: AddrStateStale, LastOk: 100, Mode: ModeTcpip},
			},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := recentOkAddr(DeviceEntry{Addrs: c.addrs}); got != c.want {
				t.Fatalf("recentOkAddr = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGui40RecentOkAddrTcpipStaleStillShown(t *testing.T) {
	e := DeviceEntry{Addrs: []AddrEntry{
		{Addr: "192.168.31.197:5555", State: AddrStateStale, LastOk: 100, Mode: ModeTcpip, Stale: true},
		{Addr: "192.168.31.197:42449", State: AddrStateStale, LastOk: 0, Mode: ModeTls, Stale: true},
	}}
	if got := recentOkAddr(e); got != "" {
		t.Fatalf("全 stale 应返回空串（离线候选不在显示主行）: %q", got)
	}
}
