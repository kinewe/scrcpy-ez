package app

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// Explicit opt-in only: no real-LAN traffic in ordinary or CI test runs.
func TestLANPhysicalDiscoverySmoke(t *testing.T) {
	if os.Getenv("SCEZ_TEST_LAN_SMOKE") != "1" {
		t.Skip("set SCEZ_TEST_LAN_SMOKE=1 to probe directly attached physical LANs")
	}
	networks, err := adb.LanIPv4Networks()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("physical networks: %v", networks)
	if len(networks) == 0 {
		t.Fatal("no physical LAN interface")
	}
	targets := discovery.LANScanTargets(networks, map[string]*discovery.LANScanCursor{}, discovery.LANScanLimit)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var mu sync.Mutex
	var found []string
	discovery.ScanLAN5555(ctx, targets, discovery.ProbeADB5555, func(target discovery.LANProbeTarget, ok bool) {
		if ok {
			mu.Lock()
			found = append(found, target.Addr)
			mu.Unlock()
		}
	})
	t.Logf("probed=%d ADB5555=%v", len(targets), found)
	if ctx.Err() != nil {
		t.Fatal("LAN sweep did not finish within its time budget")
	}
}

func TestLANPhysicalDiscoveryFeedsPendingSnapshot(t *testing.T) {
	if os.Getenv("SCEZ_TEST_LAN_SMOKE") != "1" {
		t.Skip("real-LAN opt-in only")
	}
	a, _ := newWirelessApp()
	a.pendingLANOps = pendingLANOps{networks: adb.LanIPv4Networks, probe: discovery.ProbeADB5555}
	a.startPendingLANSearch(false)
	a.mu.RLock()
	s := a.pendingLAN
	a.mu.RUnlock()
	if s == nil {
		t.Fatal("LAN search did not start")
	}
	t.Cleanup(func() { stopTestLAN(t, a) })
	select {
	case <-s.done:
	case <-time.After(22 * time.Second):
		t.Fatal("application LAN discovery didn't complete")
	}
	snapshot := a.Snapshot()
	for _, p := range snapshot.Pending {
		if p.DirectAddr == "" || p.PairPort != "" || p.Guid != "" {
			t.Fatalf("LAN-only candidate lost direct mode: %+v", p)
		}
		t.Logf("pending key=%s directAddr=%s pairingPort=%q", p.Key, p.DirectAddr, p.PairPort)
	}
	t.Logf("mdns=%d pending=%d searching=%v", len(a.mdnsSnapshot()), len(snapshot.Pending), snapshot.PendingSearching)
}

// Exercises the production LAN -> directed mDNS -> pending-card flow without
// authenticating, connecting or archiving any physical device.
func TestLANPhysicalDiscoveryFeedsMdnsIdentity(t *testing.T) {
	if os.Getenv("SCEZ_TEST_LAN_SMOKE") != "1" {
		t.Skip("real-LAN opt-in only")
	}
	a, _ := newWirelessApp()
	a.pendingLANOps = pendingLANOps{networks: adb.LanIPv4Networks, probe: discovery.ProbeADB5555,
		refreshMdns: func(ctx context.Context, target discovery.LANProbeTarget) bool {
			accepted := a.adb.RequestMdnsRefresh(ctx, adb.MdnsRefreshTarget{IP: ipOfAddr(target.Addr), Source: target.Source})
			t.Logf("direct refresh target=%s source=%s accepted=%v", target.Addr, target.Source, accepted)
			return accepted
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		done <- a.adb.BrowseMdns(ctx, func(ev adb.MdnsTrackEvents) {
			a.onMdnsTrackEvents(ctx, ev)
			if ev.First {
				started <- struct{}{}
			}
		}, nil)
	}()
	t.Cleanup(func() {
		stopTestLAN(t, a)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("mDNS did not stop")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("mDNS did not start")
	}
	a.startPendingLANSearch(false)
	a.mu.RLock()
	s := a.pendingLAN
	a.mu.RUnlock()
	select {
	case <-s.done:
	case <-time.After(22 * time.Second):
		t.Fatal("LAN discovery did not finish")
	}
	wantIP, wantSerial := os.Getenv("SCEZ_TEST_MDNS_EXPECT_IP"), os.Getenv("SCEZ_TEST_MDNS_EXPECT_SERIAL")
	if wantIP != "" {
		waitFor(t, 10*time.Second, func() bool {
			for _, p := range a.Snapshot().Pending {
				if p.IP == wantIP && p.Serial == wantSerial && p.DirectAddr == wantIP+":5555" {
					return true
				}
			}
			return false
		}, "physical directed mDNS identity in pending card")
	}
	for _, p := range a.Snapshot().Pending {
		t.Logf("pending IP=%s serial=%q name=%q direct=%s pairPort=%q", p.IP, p.Serial, p.Name, p.DirectAddr, p.PairPort)
	}
}
