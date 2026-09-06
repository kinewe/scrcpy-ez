package app

import (
	"testing"
)

// --- gui42：无线双地址投递（SCEZ_ADDR + SCEZ_ADDR2） ---

// TestGui42SecondaryWirelessAddr：备选地址=与主地址不同形态的 active 地址；
// 无对侧 active → 空；Stale/lastOk=0 不再拦备选。
func TestGui42SecondaryWirelessAddr(t *testing.T) {
	cases := []struct {
		name  string
		addrs []AddrEntry
		want  string
	}{
		{
			name: "主 tls + tcpip active → 备=tcpip",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
			},
			want: "192.168.31.197:5555",
		},
		{
			name: "仅 tls active → 备=空",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
			},
			want: "",
		},
		{
			name: "仅 tcpip active → 备=空",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 200, Mode: ModeTcpip},
			},
			want: "",
		},
		{
			name: "tls active + tcpip Stale → 备=tcpip（stale 不再拦备选）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip, Stale: true},
			},
			want: "192.168.31.197:5555",
		},
		{
			name: "tls active + tcpip lastOk=0 → 备=tcpip（lastOk 不再拦备选）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 0, Mode: ModeTcpip},
			},
			want: "192.168.31.197:5555",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newWirelessApp()
			gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
				Marketname: "REDMI K80",
				Serials:    []string{"601c9f08"},
				Addrs:      c.addrs,
				Profiles:   DefaultProfile(),
			})
			if got := a.secondaryWirelessAddr("REDMI K80"); got != c.want {
				t.Fatalf("secondaryWirelessAddr = %q, want %q", got, c.want)
			}
		})
	}
}

// TestGui42StartCastCarriesDualAddr：StartCast 无线路径同时注入 Addr（主）与
// Addr2（备），批次参数带两枚地址。
func TestGui42StartCastCarriesDualAddr(t *testing.T) {
	a, f := newWirelessApp()
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
			{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	k80Gui32WifiCard(a)

	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:42449" {
		t.Fatalf("主地址应为 TLS 42449: %+v", p)
	}
	if p.Addr2 != "192.168.31.197:5555" {
		t.Fatalf("备用地址应为 tcpip 5555: %+v", p)
	}
}
