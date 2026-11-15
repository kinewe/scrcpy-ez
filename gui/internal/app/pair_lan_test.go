package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

func installLANFake(a *App, probe func(context.Context, discovery.LANProbeTarget) bool) {
	a.pendingLANOps = pendingLANOps{
		networks: func() ([]net.IPNet, error) {
			return []net.IPNet{{IP: net.ParseIP("192.0.2.1"), Mask: net.CIDRMask(24, 32)}}, nil
		},
		probe: probe, interval: time.Hour,
	}
}

func TestLANDiscoveryRequestsMdnsWithoutDelayingDirectCandidate(t *testing.T) {
	for _, tc := range []struct {
		name, mode, serviceName, addr, wantSerial string
	}{
		{"classic", discovery.MdnsModeTcpip, "adb-PHONE", "192.0.2.159:5555", "PHONE"},
		{"tls", discovery.MdnsModeTls, "adb-PHONE-ABC123", "192.0.2.159:42001", "PHONE"},
		{"other host", discovery.MdnsModeTls, "adb-OTHER-ABC123", "192.0.2.160:42001", ""},
		{"no response", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newWirelessApp()
			installLANFake(a, func(_ context.Context, target discovery.LANProbeTarget) bool {
				return target.Addr == "192.0.2.159:5555"
			})
			t.Cleanup(func() { stopTestLAN(t, a) })
			var requests atomic.Int32
			a.pendingLANOps.refreshMdns = func(ctx context.Context, target discovery.LANProbeTarget) bool {
				if target.Addr != "192.0.2.159:5555" || target.Source != "192.0.2.1" {
					t.Errorf("refresh lost verified host/source: %+v", target)
				}
				requests.Add(1)
				pending := a.Snapshot().Pending
				if len(pending) != 1 || pending[0].DirectAddr != "192.0.2.159:5555" {
					t.Error("mDNS refresh blocked publication of direct candidate")
				}
				if tc.addr != "" {
					svc := discovery.MdnsService{Name: tc.serviceName, Addr: tc.addr, Mode: tc.mode}
					a.onMdnsTrackEvents(ctx, adb.MdnsTrackEvents{First: true, Snapshot: []adb.MdnsService{svc}})
				}
				return true
			}
			a.startPendingLANSearch(false)
			a.mu.RLock()
			s := a.pendingLAN
			a.mu.RUnlock()
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
				t.Fatal("metadata refresh stalled LAN discovery")
			}
			if requests.Load() != 1 {
				t.Fatalf("missing immediate refresh: %d", requests.Load())
			}
			var found *PendingDevice
			for _, p := range a.Snapshot().Pending {
				if p.IP == "192.0.2.159" {
					copy := p
					found = &copy
				}
			}
			if found == nil || found.DirectAddr != "192.0.2.159:5555" || found.Serial != tc.wantSerial {
				t.Fatalf("wrong enrichment: %+v", found)
			}
			if tc.wantSerial == "" && found.Name != "TCP/IP 设备" || tc.wantSerial != "" && found.Name != tc.wantSerial {
				t.Fatalf("wrong display name: %+v", found)
			}
			if _, ok := a.profiles.Entry("PHONE"); ok {
				t.Fatal("discovery archived an unverified identity")
			}
		})
	}
}

func TestLANDiscoverySkipsRefreshWhenMdnsIdentityAlreadyKnown(t *testing.T) {
	a, _ := newWirelessApp()
	seedPending(t, a, pairConnectSvcs())
	installLANFake(a, func(_ context.Context, target discovery.LANProbeTarget) bool { return target.Addr == "192.0.2.99:5555" })
	a.pendingLANOps.refreshMdns = func(context.Context, discovery.LANProbeTarget) bool { t.Error("redundant identity query"); return true }
	t.Cleanup(func() { stopTestLAN(t, a) })
	a.startPendingLANSearch(false)
	a.mu.RLock()
	s := a.pendingLAN
	a.mu.RUnlock()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("search stalled")
	}
}

func stopTestLAN(t *testing.T, a *App) {
	t.Helper()
	a.mu.RLock()
	s := a.pendingLAN
	a.mu.RUnlock()
	a.PairReset()
	if s != nil {
		select {
		case <-s.done:
		case <-time.After(time.Second):
			t.Fatal("LAN search leaked after close")
		}
	}
}

func TestLANOnly5555DiscoveredWithoutAnyMdnsAndRequiresCodeAuthentication(t *testing.T) {
	a, _ := newWirelessApp()
	installLANFake(a, func(_ context.Context, target discovery.LANProbeTarget) bool {
		return target.Addr == "192.0.2.159:5555"
	})
	t.Cleanup(func() { stopTestLAN(t, a) })
	a.pairOps.mdnsScanFn = func(context.Context, time.Duration) ([]discovery.MdnsService, error) { return nil, nil }
	a.pairOps.pairFn = func(context.Context, string, string, string) (string, error) {
		t.Error("5555 was used as pairing port")
		return "", errors.New("unexpected pair")
	}
	var connects atomic.Int32
	a.pairOps.connectFn = func(_ context.Context, addr string) (string, error) {
		if addr != "192.0.2.159:5555" {
			t.Errorf("wrong address %s", addr)
		}
		connects.Add(1)
		return "connected", nil
	}
	a.pairOps.getpropFn = func(_ context.Context, addr, prop string) (string, error) {
		if prop == "ro.serialno" {
			return "PHONE", nil
		}
		return "", nil
	}
	if err := a.PairConnect("__qr_open__", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return len(a.Snapshot().Pending) == 1 }, "LAN-only device without TLS/ADB mDNS")
	p := a.Snapshot().Pending[0]
	if p.IP != "192.0.2.159" || p.DirectAddr != "192.0.2.159:5555" || p.Guid != "" || p.PairPort != "" {
		t.Fatalf("bad standalone target: %+v", p)
	}
	a.PairConnect(p.Key, "", "", "", "123456")
	if st := waitPairPhase(t, a, PairPhaseFailed); st.ErrCode != PairErrPort || connects.Load() != 0 {
		t.Fatalf("code bypassed real pairing: %+v connect=%d", st, connects.Load())
	}
	a.PairConnect(p.Key, "", "", "", "")
	st := waitPairPhase(t, a, PairPhaseSuccess)
	if !st.Direct || st.Device.Serial != p.DirectAddr || connects.Load() != 1 {
		t.Fatalf("empty code didn't connect: %+v", st)
	}
	e, ok := a.profiles.Entry("PHONE")
	if !ok || len(e.Addrs) != 1 || e.Addrs[0].Mode != ModeTcpip {
		t.Fatalf("unexpected archive: %+v", e)
	}
}

func TestLANSearchCoalescesRefreshAndCancelsWithoutLateResults(t *testing.T) {
	a, _ := newWirelessApp()
	var networkCalls, active atomic.Int32
	entered := make(chan struct{}, 1)
	installLANFake(a, func(ctx context.Context, _ discovery.LANProbeTarget) bool {
		active.Add(1)
		defer active.Add(-1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return true
	})
	base := a.pendingLANOps.networks
	a.pendingLANOps.networks = func() ([]net.IPNet, error) { networkCalls.Add(1); return base() }
	a.startPendingLANSearch(true)
	<-entered
	a.mu.RLock()
	old := a.pendingLAN
	a.mu.RUnlock()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); a.startPendingLANSearch(false) }()
	}
	wg.Wait()
	if networkCalls.Load() != 1 || !a.Snapshot().PendingSearching {
		t.Fatal("repeated refresh duplicated scans or missing progress")
	}
	a.PairReset()
	<-old.done
	if active.Load() != 0 || len(a.Snapshot().Pending) != 0 || a.Snapshot().PendingSearching {
		t.Fatal("canceled search published a late device")
	}
}

func TestLANSearchMergesTlsWithoutDuplicateCards(t *testing.T) {
	a, _ := newWirelessApp()
	installLANFake(a, func(_ context.Context, target discovery.LANProbeTarget) bool { return target.Addr == "192.0.2.99:5555" })
	t.Cleanup(func() { stopTestLAN(t, a) })
	seedPending(t, a, pairConnectSvcs())
	a.startPendingLANSearch(true)
	waitFor(t, time.Second, func() bool { p := a.Snapshot().Pending; return len(p) == 1 && p[0].DirectAddr != "" }, "merge protocol discovery with TLS")
	p := a.Snapshot().Pending[0]
	if p.PairPort != "37033" || p.Addr != "192.0.2.99:33895" || p.Serial != "TEST0002" {
		t.Fatalf("TLS metadata lost: %+v", p)
	}
}

func TestLANSearchInterfaceChangeDropsOldCandidates(t *testing.T) {
	a, _ := newWirelessApp()
	var network atomic.Value
	network.Store("192.0.2.1")
	installLANFake(a, func(_ context.Context, target discovery.LANProbeTarget) bool {
		return strings.HasSuffix(target.Addr, ".159:5555")
	})
	a.pendingLANOps.networks = func() ([]net.IPNet, error) {
		return []net.IPNet{{IP: net.ParseIP(network.Load().(string)), Mask: net.CIDRMask(24, 32)}}, nil
	}
	t.Cleanup(func() { stopTestLAN(t, a) })
	a.startPendingLANSearch(true)
	waitFor(t, time.Second, func() bool { s := a.Snapshot(); return len(s.Pending) == 1 && !s.PendingSearching }, "first LAN sweep")
	network.Store("198.51.100.1")
	a.startPendingLANSearch(false)
	waitFor(t, time.Second, func() bool {
		s := a.Snapshot()
		return len(s.Pending) == 1 && s.Pending[0].IP == "198.51.100.159" && !s.PendingSearching
	}, "new LAN supersedes old candidates")
}

func TestLANOnlyCandidatesDoNotRecreateKnownOrOnlineDevices(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.TcpipArchive("PHONE", "192.0.2.159:5555", "Phone", "")
	installLANFake(a, func(_ context.Context, target discovery.LANProbeTarget) bool {
		return target.Addr == "192.0.2.159:5555"
	})
	t.Cleanup(func() { stopTestLAN(t, a) })
	a.startPendingLANSearch(false)
	a.mu.RLock()
	s := a.pendingLAN
	a.mu.RUnlock()
	<-s.done
	if len(a.Snapshot().Pending) != 0 {
		t.Fatal("known archive appeared as an unpaired LAN device")
	}
}
