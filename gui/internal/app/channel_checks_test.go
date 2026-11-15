package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/discovery"
)

func channelTestApp() *App {
	a := newDisplayTestApp()
	a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) { return "already connected to " + addr, nil }
	a.pairOps.serialFn = func(context.Context, string) (string, error) { return "CARDTEST", nil }
	a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { return "CARDTEST", nil }
	return a
}

func channelTestState(t *testing.T, a *App, addr string) string {
	t.Helper()
	e, _ := a.profiles.Entry(displayTestKey)
	for _, candidate := range e.Addrs {
		if candidate.Addr == addr {
			return candidate.State
		}
	}
	t.Fatalf("missing channel %s: %+v", addr, e.Addrs)
	return ""
}

func channelTestServices() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb._tcp", Name: "adb-CARDTEST", Addr: displayTestTCP, Mode: ModeTcpip},
		{Type: "_adb-tls-connect._tcp", Name: "adb-CARDTEST-Ab12Cd", Addr: displayTestTLS, Mode: ModeTls},
	}
}

func TestChannelChecksIndependentAvailability(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tcp, tls bool
	}{
		{"both", true, true}, {"5555-only", true, false},
		{"TLS-only", false, true}, {"neither", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := channelTestApp()
			displayTestCommit(a, nil)
			a.disc.TcpProbeFn = func(_ context.Context, addr string) bool {
				if addr == displayTestTCP {
					return tc.tcp
				}
				return tc.tls
			}
			a.checkProfileChannels(context.Background(), displayTestKey, channelTestServices())
			for addr, active := range map[string]bool{displayTestTCP: tc.tcp, displayTestTLS: tc.tls} {
				if got := channelTestState(t, a, addr); (got == AddrStateActive) != active {
					t.Fatalf("%s state=%s expected active=%v", addr, got, active)
				}
			}
			d := displayTestCard(t, a)
			if d.Tls != tc.tls || (d.State == "device") != (tc.tcp || tc.tls) {
				t.Fatalf("independent channels produced incorrect card: %+v", d)
			}
			if tc.tcp && !tc.tls && d.WirelessIP != displayTestTCP {
				t.Fatalf("TLS failure must choose 5555: %+v", d)
			}
		})
	}
}

func TestChannelChecksParallelResultsPublishWithoutWaitingForTLS(t *testing.T) {
	a := channelTestApp()
	a.profiles.MarkAddrStale(displayTestKey, displayTestTCP)
	displayTestCommit(a, nil)
	entered := make(chan string, 2)
	release := make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool {
		entered <- addr
		if addr == displayTestTLS {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return false
		}
		return true
	}
	done := make(chan struct{})
	go func() { a.checkProfileChannels(context.Background(), displayTestKey, nil); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("channels did not start in parallel")
		}
	}
	waitForMdns(t, "5555 result must publish while TLS is blocked", func() bool {
		return channelTestState(t, a, displayTestTCP) == AddrStateActive
	})
	select {
	case <-done:
		t.Fatal("TLS check did not remain independent/in flight")
	default:
	}
	// A heartbeat on the other channel must not discard the TLS failure.
	a.profiles.AddrSuccessMode(displayTestKey, displayTestTCP, ModeTcpip)
	resume()
	displayTestWait(t, done)
	if d := displayTestCard(t, a); d.Tls || d.WirelessIP != displayTestTCP || d.State != "device" {
		t.Fatalf("slow failed TLS affected the working 5555 channel: %+v", d)
	}
}

func TestChannelChecksDroppedTLSRecheckedWhile5555StillOnline(t *testing.T) {
	a := channelTestApp()
	displayTestCommit(a, []adb.Device{{Serial: displayTestTCP, State: "device", ConnType: "wifi", StableSerial: "CARDTEST"}})
	a.disc.TcpProbeFn = func(_ context.Context, addr string) bool { return addr == displayTestTCP }
	a.onDropped(displayTestKey)
	waitForMdns(t, "remaining 5555 must not suppress TLS invalidation", func() bool {
		return channelTestState(t, a, displayTestTLS) == AddrStateStale
	})
	if d := displayTestCard(t, a); d.Tls || d.State != "device" || d.WirelessIP != displayTestTCP {
		t.Fatalf("incorrect remaining channel: %+v", d)
	}
}

func TestChannelChecksRawRemovalSurvivesCardFolding(t *testing.T) {
	for _, removed := range []string{displayTestTLS, displayTestTCP} {
		t.Run(removed, func(t *testing.T) {
			a := channelTestApp()
			remaining := displayTestTCP
			if removed == displayTestTCP {
				remaining = displayTestTLS
			}
			entered, release := make(chan struct{}, 2), make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			t.Cleanup(resume)
			a.disc.TcpProbeFn = func(_ context.Context, addr string) bool { entered <- struct{}{}; <-release; return addr == remaining }
			// Enrichment has already folded both transports to this same card.
			folded := []adb.Device{{Serial: remaining, State: "device", ConnType: "wifi", StableSerial: "CARDTEST"}}
			initial := snapshotFor(a,
				deviceevents.Transport{Serial: removed, State: "device", Kind: "wifi", ID: "1"},
				deviceevents.Transport{Serial: remaining, State: "device", Kind: "wifi", ID: "2"})
			a.applyDeviceSnapshot(context.Background(), folded, &initial, "test-initial")
			current := snapshotFor(a, deviceevents.Transport{Serial: remaining, State: "device", Kind: "wifi", ID: "2"})
			refreshCtx, cancel := context.WithCancel(context.Background())
			a.applyDeviceSnapshot(refreshCtx, folded, &current, "test-removed")
			displayTestWait(t, entered)
			cancel() // pollOnce cancels its short refresh context after committing
			resume()
			waitForMdns(t, "raw single-channel removal must be checked despite identical folded card", func() bool {
				return channelTestState(t, a, removed) == AddrStateStale
			})
			if channelTestState(t, a, remaining) != AddrStateActive {
				t.Fatal("removed channel invalidated the remaining channel")
			}
		})
	}
}

func TestChannelChecksDirectArchiveCannotTrustCachedTLS(t *testing.T) {
	a, _ := newWirelessApp()
	installDirectFakes(a)
	a.profiles.TcpipArchive("PHONE", "192.0.2.10:5555", "Phone", "")
	a.disc.TcpProbeFn = func(_ context.Context, addr string) bool { return addr == "192.0.2.10:5555" }
	a.archiveDirectMdns(context.Background(), "192.0.2.10", "PHONE")
	e, _ := a.profiles.Entry("PHONE")
	for _, addr := range e.Addrs {
		if addr.Mode == ModeTls && addr.State == AddrStateActive {
			t.Fatalf("cached TLS activated during 5555 direct connect: %+v", e.Addrs)
		}
	}
}

func TestChannelChecksCachedMdnsCannotResurrectClosedTLS(t *testing.T) {
	a := channelTestApp()
	displayTestCommit(a, []adb.Device{{Serial: displayTestTCP, State: "device", ConnType: "wifi", StableSerial: "CARDTEST"}})
	a.mu.Lock()
	a.adbOK = true
	a.mu.Unlock()
	a.disc.MdnsScanFn = func(context.Context, time.Duration) ([]discovery.MdnsService, error) {
		return channelTestServices(), nil
	}
	a.disc.TcpProbeFn = func(_ context.Context, addr string) bool { return addr == displayTestTCP }
	for i := 0; i < 3; i++ {
		a.reconcileMdnsSnapshot(context.Background())
		if d := displayTestCard(t, a); d.Tls || d.WirelessIP != displayTestTCP {
			t.Fatalf("cached snapshot revived TLS on round %d: %+v", i, d)
		}
	}
}

// rc.5 verified periodic checks but still trusted the next actual Added/QU
// event. A delayed TLS announcement must never undo a verified failure.
func TestChannelChecksDelayedMdnsCannotReviveClosedTLS(t *testing.T) {
	a := channelTestApp()
	displayTestCommit(a, []adb.Device{{Serial: displayTestTCP, State: "device", ConnType: "wifi", StableSerial: "CARDTEST"}})
	a.applyMdnsSnapshot(channelTestServices(), true, nil, nil)
	a.profiles.MarkAddrStale(displayTestKey, displayTestTLS)
	a.rebuildCurrentDisplay("TLS已确认关闭")
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	a.disc.TcpProbeFn = func(_ context.Context, addr string) bool {
		if addr == displayTestTLS {
			entered <- struct{}{}
			<-release
			return false
		}
		return true
	}
	svc := channelTestServices()[1]
	for repeat := 0; repeat < 3; repeat++ {
		a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
			Snapshot: channelTestServices(), Added: []adb.MdnsService{svc},
		})
		if d := displayTestCard(t, a); d.Tls || d.WirelessIP != displayTestTCP {
			t.Fatalf("delayed mDNS revived TLS before verification: %+v", d)
		}
		displayTestWait(t, entered)
		resume()
		a.channelCheckWG.Wait()
		if d := displayTestCard(t, a); d.Tls || d.State != "device" || d.WirelessIP != displayTestTCP {
			t.Fatalf("closed TLS announcement affected the working 5555 route: %+v", d)
		}
	}
}

func TestChannelChecksLateResultCannotReplaceNewTLSOrRecreatedArchive(t *testing.T) {
	for _, change := range []string{"new-port", "fresh-success", "archive-recreated"} {
		t.Run(change, func(t *testing.T) {
			a := channelTestApp()
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			t.Cleanup(resume)
			a.disc.TcpProbeFn = func(context.Context, string) bool { close(entered); <-release; return false }
			go func() {
				a.checkChannel(context.Background(), displayTestKey, displayTestTLS, ModeTls, "", "")
				close(done)
			}()
			displayTestWait(t, entered)
			want := displayTestTLS
			switch change {
			case "new-port":
				want = "192.0.2.159:46667"
				a.profiles.AddrSuccessMode(displayTestKey, want, ModeTls)
			case "fresh-success":
				a.profiles.AddrSuccessMode(displayTestKey, want, ModeTls)
			case "archive-recreated":
				gui15Seed(a.profiles, displayTestKey, &DeviceEntry{Serials: []string{"CARDTEST"}, Profiles: DefaultProfile()})
			}
			resume()
			displayTestWait(t, done)
			e, _ := a.profiles.Entry(displayTestKey)
			if change == "archive-recreated" {
				if len(e.Addrs) != 0 {
					t.Fatalf("old job wrote into recreated archive: %+v", e.Addrs)
				}
				return
			}
			if channelTestState(t, a, want) != AddrStateActive {
				t.Fatalf("late result overwrote newer TLS: %+v", e.Addrs)
			}
		})
	}
}

func TestChannelChecksOwnerMismatchDoesNotAffectOtherChannel(t *testing.T) {
	a := channelTestApp()
	displayTestCommit(a, nil)
	a.disc.TcpProbeFn = func(context.Context, string) bool { return true }
	a.pairOps.serialFn = func(_ context.Context, addr string) (string, error) {
		if addr == displayTestTLS {
			return "OTHER", nil
		}
		return "CARDTEST", nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, addr, _ string) (string, error) { return a.pairOps.serialFn(ctx, addr) }
	a.checkProfileChannels(context.Background(), displayTestKey, nil)
	if d := displayTestCard(t, a); d.Tls || d.State != "device" || d.WirelessIP != displayTestTCP {
		t.Fatalf("identity mismatch crossed channel boundary: %+v", d)
	}
}

func TestChannelChecksLateSuccessCannotReverseNewStale(t *testing.T) {
	a := channelTestApp()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	a.disc.TcpProbeFn = func(context.Context, string) bool { close(entered); <-release; return true }
	go func() {
		a.checkChannel(context.Background(), displayTestKey, displayTestTLS, ModeTls, "", "")
		close(done)
	}()
	displayTestWait(t, entered)
	a.profiles.MarkAddrStale(displayTestKey, displayTestTLS)
	resume()
	displayTestWait(t, done)
	if channelTestState(t, a, displayTestTLS) != AddrStateStale {
		t.Fatal("late successful check reversed newer TLS removal evidence")
	}
}

func TestChannelChecksOldPortFailureDoesNotSuppressNewPortSuccess(t *testing.T) {
	a := channelTestApp()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	newAddr := "192.0.2.159:46667"
	a.disc.TcpProbeFn = func(context.Context, string) bool { close(entered); <-release; return true }
	go func() {
		a.checkChannel(context.Background(), displayTestKey, newAddr, ModeTls, "CARDTEST", "adb-CARDTEST-Ab12Cd")
		close(done)
	}()
	displayTestWait(t, entered)
	// The old cached port fails while the new endpoint is being authenticated.
	a.profiles.AddrFailMode(displayTestKey, displayTestTLS, ModeTls)
	resume()
	displayTestWait(t, done)
	if channelTestState(t, a, newAddr) != AddrStateActive {
		t.Fatal("old-port failure suppressed the verified replacement")
	}
	e, _ := a.profiles.Entry(displayTestKey)
	for _, addr := range e.Addrs {
		if addr.Addr == displayTestTLS && addr.State == AddrStateActive {
			t.Fatal("retired TLS port stayed active after the replacement succeeded")
		}
	}
}

func TestChannelChecksCancellationAndShutdownDoNotMarkOffline(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "shutdown"}[shutdown], func(t *testing.T) {
			a := channelTestApp()
			entered, done := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			a.disc.TcpProbeFn = func(ctx context.Context, _ string) bool { calls.Add(1); close(entered); <-ctx.Done(); return false }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { a.checkChannel(ctx, displayTestKey, displayTestTLS, ModeTls, "", ""); close(done) }()
			displayTestWait(t, entered)
			if shutdown {
				a.closeChannelChecks()
			} else {
				cancel()
			}
			displayTestWait(t, done)
			if channelTestState(t, a, displayTestTLS) != AddrStateActive {
				t.Fatal("cancellation was interpreted as a failed TLS channel")
			}
			if shutdown {
				a.checkChannel(context.Background(), displayTestKey, displayTestTLS, ModeTls, "", "")
				if calls.Load() != 1 {
					t.Fatal("new network operation after shutdown")
				}
			}
		})
	}
}
