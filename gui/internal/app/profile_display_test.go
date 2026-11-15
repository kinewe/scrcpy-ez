package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

const (
	displayTestKey = "device:CARDTEST"
	displayTestTLS = "192.0.2.159:43371"
	displayTestTCP = "192.0.2.159:5555"
)

func newDisplayTestApp() *App {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, displayTestKey, &DeviceEntry{
		Marketname: "Card Phone", Serials: []string{"CARDTEST"},
		Addrs: []AddrEntry{
			{Addr: displayTestTLS, State: AddrStateActive, Mode: ModeTls},
			{Addr: displayTestTCP, State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	return a
}

func displayTestCommit(a *App, devs []adb.Device) {
	a.mu.Lock()
	a.lastTrack = append([]adb.Device(nil), devs...)
	a.mu.Unlock()
	a.commitDisplay(devs, "test")
}

func displayTestCard(t *testing.T, a *App) adb.Device {
	t.Helper()
	s := a.Snapshot()
	if len(s.Devices) != 1 {
		t.Fatalf("expected one card: %+v", s.Devices)
	}
	return s.Devices[0]
}

func displayTestOffline(t *testing.T, a *App) {
	t.Helper()
	d := displayTestCard(t, a)
	if d.State != "offline" || d.Tls || d.WirelessIP != "" || d.WirelessForm != "" || d.Connecting {
		t.Fatalf("stale archives must disable casting and clear wireless decorations: %+v", d)
	}
}

// Reproduces reboot with no subsequent transport/mDNS event. Previously only
// the archive changed, while Snapshot kept the old enabled cast button and TLS.
func TestProfileDisplayAllProbeFailuresPublishOffline(t *testing.T) {
	a := newDisplayTestApp()
	displayTestCommit(a, nil)
	if d := displayTestCard(t, a); d.State != "device" || !d.Tls {
		t.Fatalf("expected initial active TLS card: %+v", d)
	}
	a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) {
		return "cannot connect to " + addr, nil
	}
	a.probeProfileActiveAddrs(context.Background(), displayTestKey)
	displayTestOffline(t, a)
	e, _ := a.profiles.Entry(displayTestKey)
	if profileHasActiveAddr(e) {
		t.Fatalf("card and archive disagree: %+v", e.Addrs)
	}
}

func TestProfileDisplayConnectWithoutOwnerEvidenceIsNotOnline(t *testing.T) {
	for _, serial := range []string{"", "ANOTHERPHONE"} {
		t.Run("reported-serial="+serial, func(t *testing.T) {
			a := newDisplayTestApp()
			displayTestCommit(a, nil)
			a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) {
				return "already connected to " + addr, nil
			}
			a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { return serial, nil }
			a.pairOps.serialFn = func(context.Context, string) (string, error) { return serial, nil }
			a.probeProfileActiveAddrs(context.Background(), displayTestKey)
			displayTestOffline(t, a)
			e, _ := a.profiles.Entry(displayTestKey)
			if profileHasActiveAddr(e) || len(e.Serials) != 1 || e.Serials[0] != "CARDTEST" {
				t.Fatalf("unverified connection must not retain active state or change identity: %+v", e)
			}
		})
	}
}

func TestProfileDisplayArchiveMutationRepairsSnapshot(t *testing.T) {
	a := newDisplayTestApp()
	// Deliberately leave an old 'device' transport in the display inputs.
	displayTestCommit(a, []adb.Device{{Serial: displayTestTLS, State: "device", ConnType: "wifi", StableSerial: "CARDTEST"}})
	var connectCalls atomic.Int32
	a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) {
		connectCalls.Add(1)
		return "connected to " + addr, nil
	}
	a.profiles.MarkAddrStale(displayTestKey, displayTestTLS)
	d := displayTestCard(t, a)
	if d.State != "device" || d.ConnType != "wifi" || d.Serial != displayTestTCP || d.Tls || d.WirelessForm != ModeTcpip {
		t.Fatalf("TLS failure must retain the active tcpip card: %+v", d)
	}
	a.profiles.MarkAddrStale(displayTestKey, displayTestTCP)
	displayTestOffline(t, a)
	const newTLS = "192.0.2.159:46667"
	a.profiles.AddrSuccessMode(displayTestKey, newTLS, ModeTls)
	d = displayTestCard(t, a)
	if d.State != "device" || !d.Tls || d.Serial != newTLS || d.WirelessIP != newTLS {
		t.Fatalf("new archive evidence must restore the card: %+v", d)
	}
	if n := connectCalls.Load(); n != 0 {
		t.Fatalf("reading Snapshot must not launch recovery: %d connects", n)
	}
}

func TestProfileDisplayPhysicalUSBWins(t *testing.T) {
	for _, state := range []string{"device", "offline", "unauthorized"} {
		t.Run(state, func(t *testing.T) {
			a := newDisplayTestApp()
			displayTestCommit(a, []adb.Device{
				{Serial: "CARDTEST", ConnType: "usb", State: state},
				{Serial: displayTestTLS, ConnType: "wifi", State: "device", StableSerial: "CARDTEST"},
			})
			a.profiles.MarkAllAddrsStale(displayTestKey)
			d := displayTestCard(t, a)
			if d.Serial != "CARDTEST" || d.ConnType != "usb" || d.State != state || d.Tls || d.WirelessIP != "" {
				t.Fatalf("wireless failures must preserve the physical USB state: %+v", d)
			}
			if d.Connecting != (state == "offline") {
				t.Fatalf("USB offline must retain the existing connecting presentation: %+v", d)
			}
		})
	}
}

func displayTestBlockedProbe(t *testing.T, a *App) (release func(), done <-chan struct{}) {
	t.Helper()
	entered := make(chan struct{}, 2)
	resume := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(resume) }) }
	t.Cleanup(release)
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		entered <- struct{}{}
		select {
		case <-resume:
		case <-ctx.Done():
		}
		return "cannot connect to " + addr, nil
	}
	finished := make(chan struct{})
	go func() {
		a.probeProfileActiveAddrs(context.Background(), displayTestKey)
		close(finished)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("probe did not start")
		}
	}
	return release, finished
}

func displayTestWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("display/probe deadlocked")
	}
}

func TestProfileDisplayLateFailureCannotReplaceNewEvidence(t *testing.T) {
	for _, source := range []string{"same-address-success", "transport-heartbeat", "TLS-port-rotation"} {
		t.Run(source, func(t *testing.T) {
			a := newDisplayTestApp()
			// Repeated success can be a no-op for persisted fields within the
			// same second; its runtime observation still has to supersede probes.
			a.profiles.AddrSuccessMode(displayTestKey, displayTestTLS, ModeTls)
			displayTestCommit(a, nil)
			release, done := displayTestBlockedProbe(t, a)
			want := displayTestTLS
			switch source {
			case "same-address-success":
				a.profiles.AddrSuccessMode(displayTestKey, displayTestTLS, ModeTls)
			case "transport-heartbeat":
				a.profiles.SyncDevices([]adb.Device{{Serial: displayTestTLS, ConnType: "wifi", State: "device", StableSerial: "CARDTEST"}})
			case "TLS-port-rotation":
				want = "192.0.2.159:48889"
				a.profiles.AddrSuccessMode(displayTestKey, want, ModeTls)
			}
			release()
			displayTestWait(t, done)
			d := displayTestCard(t, a)
			if d.State != "device" || !d.Tls || d.WirelessIP != want {
				t.Fatalf("late failure replaced newer evidence: %+v", d)
			}
			e, _ := a.profiles.Entry(displayTestKey)
			if source == "TLS-port-rotation" && gui24FindAddr(e, displayTestTLS) != nil {
				t.Fatalf("late result recreated retired TLS port: %+v", e.Addrs)
			}
		})
	}
}

func TestProfileDisplayLateSuccessCannotRestoreRetiredOrStaleAddress(t *testing.T) {
	for _, change := range []string{"port-rotation", "marked-stale", "archive-recreated"} {
		t.Run(change, func(t *testing.T) {
			a := newDisplayTestApp()
			a.profiles.MarkAddrStale(displayTestKey, displayTestTCP)
			displayTestCommit(a, nil)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			t.Cleanup(resume)
			a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
				return "connected to " + addr, nil
			}
			go func() { a.probeProfileActiveAddrs(context.Background(), displayTestKey); close(done) }()
			displayTestWait(t, entered)
			switch change {
			case "port-rotation":
				a.profiles.AddrSuccessMode(displayTestKey, "192.0.2.159:48889", ModeTls)
			case "marked-stale":
				a.profiles.MarkAllAddrsStale(displayTestKey)
			case "archive-recreated":
				gui15Seed(a.profiles, displayTestKey, &DeviceEntry{Serials: []string{"CARDTEST"}, Profiles: DefaultProfile()})
			}
			resume()
			displayTestWait(t, done)
			e, _ := a.profiles.Entry(displayTestKey)
			if old := gui24FindAddr(e, displayTestTLS); old != nil && old.State == AddrStateActive {
				t.Fatalf("late success revived the old address after %s: %+v", change, e.Addrs)
			}
			if change == "port-rotation" {
				if d := displayTestCard(t, a); d.WirelessIP != "192.0.2.159:48889" || !d.Tls {
					t.Fatalf("late success replaced new TLS address: %+v", d)
				}
			} else {
				displayTestOffline(t, a)
			}
		})
	}
}

func TestProfileDisplayProbeCompletionKeepsNewUSB(t *testing.T) {
	a := newDisplayTestApp()
	displayTestCommit(a, nil)
	release, done := displayTestBlockedProbe(t, a)
	displayTestCommit(a, []adb.Device{{Serial: "CARDTEST", State: "device", ConnType: "usb"}})
	release()
	displayTestWait(t, done)
	if d := displayTestCard(t, a); d.Serial != "CARDTEST" || d.State != "device" || d.ConnType != "usb" || d.Tls {
		t.Fatalf("old wireless completion replaced the new USB card: %+v", d)
	}
}

func TestProfileDisplayPersistenceFailureStillInvalidates(t *testing.T) {
	a := newDisplayTestApp()
	displayTestCommit(a, nil)
	blocked := filepath.Join(t.TempDir(), "file-instead-of-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.profiles.mu.Lock()
	a.profiles.path = filepath.Join(blocked, "profiles.json")
	a.profiles.mu.Unlock()
	a.profiles.MarkAllAddrsStale(displayTestKey)
	displayTestOffline(t, a)
	if a.Snapshot().ProfileSaveError == "" {
		t.Fatal("save failure must remain visible while card follows in-memory state")
	}
}

func TestProfileDisplaySnapshotDoesNotResetADBHealth(t *testing.T) {
	a := newDisplayTestApp()
	displayTestCommit(a, nil)
	a.mu.Lock()
	a.adbOK, a.adbFail, a.adbHealTried = false, 5, true
	a.mu.Unlock()
	a.profiles.MarkAllAddrsStale(displayTestKey)
	displayTestOffline(t, a)
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.adbOK || a.adbFail != 5 || !a.adbHealTried {
		t.Fatal("projection refresh must not fabricate ADB recovery")
	}
}

func TestProfileDisplayConcurrentRefresh(t *testing.T) {
	a := newDisplayTestApp()
	displayTestCommit(a, nil)
	var wg sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				switch worker {
				case 0:
					a.Snapshot()
				case 1:
					a.profiles.MarkAllAddrsStale(displayTestKey)
					a.profiles.AddrSuccessMode(displayTestKey, displayTestTLS, ModeTls)
				case 2:
					displayTestCommit(a, []adb.Device{{Serial: "CARDTEST", State: "device", ConnType: "usb"}})
					a.rebuildCurrentDisplay("concurrent test")
				}
			}
		}(worker)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	displayTestWait(t, done)
	displayTestCommit(a, nil)
	a.profiles.MarkAllAddrsStale(displayTestKey)
	displayTestOffline(t, a)
}
