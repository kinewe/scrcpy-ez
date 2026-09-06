package app

import (
	"testing"
)

// --- gui48-addr2：投屏备选地址放宽——active 即备选（不看 Stale/LastOk） ---

func addr2Seed(a *App, addrs []AddrEntry) {
	gui15Seed(a.profiles, "REDMI K80", &DeviceEntry{
		Marketname: "REDMI K80",
		Serials:    []string{"601c9f08"},
		Addrs:      addrs,
		Profiles:   DefaultProfile(),
	})
}

// TestGui48Addr2HistoryNotCandidate：对侧形态 history（非 active）→ 不返回。
func TestGui48Addr2HistoryNotCandidate(t *testing.T) {
	a, _ := newWirelessApp()
	addr2Seed(a, []AddrEntry{
		{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
		{Addr: "192.168.31.197:5555", State: AddrStateHistory, LastOk: 100, Mode: ModeTcpip},
	})
	if got := a.secondaryWirelessAddr("REDMI K80"); got != "" {
		t.Fatalf("history 条目不应作备选: %q", got)
	}
}

// TestGui48Addr2PicksLatestLastOk（gui52 语义更新）：多条对侧 active 时按档案
// 顺序取第一条 active（LastOk 不入判据；正常链路同形态只留一条记忆）。
func TestGui48Addr2PicksLatestLastOk(t *testing.T) {
	a, _ := newWirelessApp()
	addr2Seed(a, []AddrEntry{
		{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
		{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip},
		{Addr: "192.168.31.198:5555", State: AddrStateActive, LastOk: 300, Mode: ModeTcpip},
	})
	if got := a.secondaryWirelessAddr("REDMI K80"); got != "192.168.31.197:5555" {
		t.Fatalf("应取档案顺序第一条对侧 active 地址: %q", got)
	}
}

// TestGui48Addr2MainEmptyReturnsEmpty：主地址为空 → 直接返回 ""。
func TestGui48Addr2MainEmptyReturnsEmpty(t *testing.T) {
	a, _ := newWirelessApp()
	addr2Seed(a, nil)
	if got := a.secondaryWirelessAddr("REDMI K80"); got != "" {
		t.Fatalf("主地址为空应返回空: %q", got)
	}
}

// TestGui48Addr2StaleAndZeroLastOkAllowed：本次 bug 场景——对侧 active 即使
// Stale=true 或 LastOk=0 也必须可作备选。
func TestGui48Addr2StaleAndZeroLastOkAllowed(t *testing.T) {
	for _, c := range []struct {
		name string
		alt  AddrEntry
	}{
		{"Stale", AddrEntry{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 100, Mode: ModeTcpip, Stale: true}},
		{"LastOk=0", AddrEntry{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 0, Mode: ModeTcpip}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newWirelessApp()
			addr2Seed(a, []AddrEntry{
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 200, Mode: ModeTls},
				c.alt,
			})
			if got := a.secondaryWirelessAddr("REDMI K80"); got != "192.168.31.197:5555" {
				t.Fatalf("对侧 active 应作备选: %q", got)
			}
		})
	}
}
