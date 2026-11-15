package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/discovery"
)

func directServices() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Name: "adb-PHONE", Addr: "192.0.2.10:5555", Mode: ModeTcpip},
		{Name: "adb-PHONE-Ab12Cd", Addr: "192.0.2.10:41000", Mode: ModeTls},
		{Name: "adb-PHONE-Ab12Cd", Addr: "192.0.2.10:42000", Mode: discovery.MdnsModePairing},
	}
}

func installDirectFakes(a *App) {
	a.disc.TcpProbeFn = func(context.Context, string) bool { return true }
	a.disc.ConnectOutFn = func(_ context.Context, addr string) (string, error) { return "connected to " + addr, nil }
	a.pairOps.serialFn = func(context.Context, string) (string, error) { return "PHONE", nil }
	a.pairOps.getpropFn = func(_ context.Context, addr, prop string) (string, error) {
		switch prop {
		case "ro.serialno":
			return "PHONE", nil
		case "ro.product.marketname":
			return "Phone", nil
		case "service.adb.tcp.port":
			return "5555", nil
		}
		return "", nil
	}
	a.pairOps.mdnsScanFn = func(context.Context, time.Duration) ([]discovery.MdnsService, error) { return directServices(), nil }
}

func TestPendingTcpipProbeAndDualServiceDeduplication(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		a, _ := newWirelessApp()
		var calls atomic.Int32
		a.pairOps.probeFn = func(_ context.Context, addr string) bool {
			if addr != "192.0.2.10:5555" {
				t.Errorf("unexpected target %s", addr)
			}
			calls.Add(1)
			return true
		}
		svcs := directServices()
		if reverse {
			svcs[0], svcs[1] = svcs[1], svcs[0]
		}
		seedPending(t, a, svcs)
		waitFor(t, 2*time.Second, func() bool { p := a.Snapshot().Pending; return len(p) == 1 && p[0].DirectAddr == "192.0.2.10:5555" }, "verified direct capability")
		p := a.Snapshot().Pending[0]
		if p.Addr != "192.0.2.10:41000" || p.PairPort != "42000" || p.Serial != "PHONE" {
			t.Fatalf("dual service data lost: %+v", p)
		}
		a.buildPending(nil)
		if calls.Load() != 1 {
			t.Fatal("unchanged discovery repeated a TCP probe")
		}
	}
}

func TestPendingClosedTcpipDoesNotOfferDirect(t *testing.T) {
	a, _ := newWirelessApp()
	done := make(chan struct{})
	a.pairOps.probeFn = func(context.Context, string) bool { close(done); return false }
	seedPending(t, a, directServices()[:1])
	<-done
	waitFor(t, time.Second, func() bool { a.mu.RLock(); defer a.mu.RUnlock(); return !a.pendingTcpip["192.0.2.10"].busy }, "probe completion")
	if a.Snapshot().Pending[0].DirectAddr != "" {
		t.Fatal("closed port offered direct connect")
	}
}

func TestDirectConnectArchivesTcpipAndMatchingTls(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "auto", true: "manual"}[manual], func(t *testing.T) {
			a, _ := newWirelessApp()
			installDirectFakes(a)
			p := seedPending(t, a, directServices())
			a.mu.Lock()
			a.pending[0].DirectAddr = "192.0.2.10:5555"
			a.mu.Unlock()
			a.pairOps.pairFn = func(context.Context, string, string, string) (string, error) {
				t.Error("direct connect called adb pair")
				return "", errors.New("unexpected")
			}
			a.pairOps.connectFn = func(_ context.Context, addr string) (string, error) {
				if addr != "192.0.2.10:5555" {
					t.Errorf("not TCP/IP: %s", addr)
				}
				return "connected", nil
			}
			key, port := p.Key, ""
			if manual {
				key, port = "", "5555"
			}
			if err := a.PairConnect(key, p.IP, port, "", ""); err != nil {
				t.Fatal(err)
			}
			st := waitPairPhase(t, a, PairPhaseSuccess)
			if !st.Direct || st.Device.Tls || st.Device.Serial != "192.0.2.10:5555" {
				t.Fatalf("wrong transport %+v", st)
			}
			e, ok := a.profiles.Entry("PHONE")
			if !ok || !gui50Fix45EntryHasAddr(e, "192.0.2.10:5555", ModeTcpip) || !gui50Fix45EntryHasAddr(e, "192.0.2.10:41000", ModeTls) {
				t.Fatalf("both modes not archived: %+v", e)
			}
		})
	}
}

func TestCodeOn5555MustUseRealPairingPort(t *testing.T) {
	for _, success := range []bool{false, true} {
		a, _ := newWirelessApp()
		installDirectFakes(a)
		seedPending(t, a, directServices())
		var paired atomic.Bool
		var connects atomic.Int32
		a.pairOps.pairFn = func(_ context.Context, ip, port, code string) (string, error) {
			if port != "42000" || code != "123456" {
				t.Errorf("not the wireless pairing endpoint: %s/%s", port, code)
			}
			paired.Store(true)
			if !success {
				return "Failed: Wrong password or connection was dropped.", errors.New("wrong password")
			}
			return "Successfully paired to " + ip + ":" + port, nil
		}
		a.pairOps.connectFn = func(_ context.Context, addr string) (string, error) {
			if !paired.Load() || !success || connects.Add(1) == 1 && addrPort(addr) == "5555" {
				t.Errorf("code bypassed pairing through %s", addr)
			}
			return "connected", nil
		}
		a.PairConnect("", "192.0.2.10", "5555", "5555", "123456")
		phase := PairPhaseFailed
		if success {
			phase = PairPhaseSuccess
		}
		st := waitPairPhase(t, a, phase)
		if st.Direct || !paired.Load() {
			t.Fatalf("code became direct: %+v", st)
		}
		if !success && st.ErrCode != PairErrCode {
			t.Fatalf("wrong classification: %+v", st)
		}
	}
}

func TestPairInvalidEndpointAndEmptyCodeCannotBypass(t *testing.T) {
	for _, tc := range []struct{ ip, port, code, want string }{
		{"192.0.2.10", "0", "123456", PairErrAddr},
		{"192.0.2.10", "65536", "123456", PairErrAddr},
		{"192.0.2.10", "abc", "123456", PairErrAddr},
		{"192.0.2.10", "-1", "123456", PairErrAddr},
		{"999.0.2.10", "5555", "", PairErrAddr},
		{"192.0.2.10", "05555", "", PairErrCode},
		{"192.0.2.10", "41000", "", PairErrCode},
		{"192.0.2.10", "5555", "123", PairErrCode},
	} {
		a, _ := newWirelessApp()
		a.pairOps.connectFn = func(context.Context, string) (string, error) { t.Error("invalid request connected"); return "", nil }
		a.PairConnect("", tc.ip, tc.port, "", tc.code)
		if st := waitPairPhase(t, a, PairPhaseFailed); st.ErrCode != tc.want {
			t.Fatalf("%+v: %+v", tc, st)
		}
	}
}

func TestDirectUntrustedOrForeignDeviceNeverArchived(t *testing.T) {
	for _, serial := range []string{"", "FOREIGN"} {
		a, _ := newWirelessApp()
		installDirectFakes(a)
		p := seedPending(t, a, directServices())
		a.mu.Lock()
		a.pending[0].DirectAddr = p.IP + ":5555"
		a.mu.Unlock()
		a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { return serial, nil }
		a.pairOps.serialFn = nil
		a.pairOps.connectFn = func(context.Context, string) (string, error) { return "connected", nil }
		a.PairConnect(p.Key, "", "", "", "")
		st := waitPairPhase(t, a, PairPhaseFailed)
		if st.ErrCode != PairErrSerial || len(a.profiles.Entries()) != 0 {
			t.Fatalf("unverified device archived: %+v", st)
		}
	}
}

func TestDirectPairReportsAuthorizationInsteadOfSerialFailure(t *testing.T) {
	for _, tc := range []struct {
		name, output, state  string
		connectErr, stateErr error
	}{
		{"connect-rejected", "failed to authenticate to 192.0.2.10:5555", "", errors.New("authentication failed"), nil},
		{"already-connected-unauthorized", "already connected to 192.0.2.10:5555", "error: device unauthorized", nil, errors.New("exit status 1")},
		{"authorizing", "connected to 192.0.2.10:5555", "authorizing", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newWirelessApp()
			installDirectFakes(a)
			a.pairOps.connectFn = func(context.Context, string) (string, error) { return tc.output, tc.connectErr }
			a.pairOps.stateFn = func(context.Context, string) (string, error) { return tc.state, tc.stateErr }
			a.pairOps.getpropFn = func(context.Context, string, string) (string, error) {
				t.Error("unauthorized device must not be read or archived")
				return "", nil
			}
			_ = a.PairConnect("", "192.0.2.10", "5555", "", "")
			if st := waitPairPhase(t, a, PairPhaseFailed); st.ErrCode != PairErrAuth || len(a.profiles.Entries()) != 0 {
				t.Fatalf("authorization failure misclassified: code=%s", st.ErrCode)
			}
		})
	}
}

func TestDirectPairCanRetryAfterAuthorizationInSameModal(t *testing.T) {
	a, _ := newWirelessApp()
	installDirectFakes(a)
	var authorized atomic.Bool
	a.pairOps.connectFn = func(context.Context, string) (string, error) { return "already connected", nil }
	a.pairOps.stateFn = func(context.Context, string) (string, error) {
		if !authorized.Load() {
			return "device unauthorized", errors.New("exit status 1")
		}
		return "device", nil
	}
	_ = a.PairConnect("", "192.0.2.10", "5555", "", "")
	if st := waitPairPhase(t, a, PairPhaseFailed); st.ErrCode != PairErrAuth {
		t.Fatal("missing authorization failure")
	}
	authorized.Store(true)
	_ = a.PairConnect("", "192.0.2.10", "5555", "", "")
	waitPairPhase(t, a, PairPhaseSuccess)
	if _, ok := a.profiles.Entry("PHONE"); !ok {
		t.Fatal("authorized retry did not archive verified device")
	}
}

func TestDirectLearningRejectsForeignMdnsAndUsesLivePort(t *testing.T) {
	a, _ := newWirelessApp()
	installDirectFakes(a)
	a.profiles.TcpipArchive("PHONE", "192.0.2.10:5555", "Phone", "")
	a.mdns = []discovery.MdnsService{{Name: "adb-PHONE-Ab12Cd", Addr: "192.0.2.10:41001", Mode: ModeTls}, {Name: "adb-FOREIGN-Xx12Cd", Addr: "192.0.2.10:49000", Mode: ModeTls}}
	a.archiveDirectMdns(context.Background(), "192.0.2.10", "PHONE")
	e, _ := a.profiles.Entry("PHONE")
	if !gui50Fix45EntryHasAddr(e, "192.0.2.10:41001", ModeTls) || gui50Fix45EntryHasAddr(e, "192.0.2.10:49000", ModeTls) || gui50Fix45EntryHasAddr(e, "192.0.2.10:41000", ModeTls) {
		t.Fatalf("wrong mDNS endpoint learned: %+v", e)
	}
}

func TestDirectWithoutWirelessDebuggingConnectsButCodeFails(t *testing.T) {
	a, _ := newWirelessApp()
	installDirectFakes(a)
	a.pairOps.mdnsScanFn = func(context.Context, time.Duration) ([]discovery.MdnsService, error) { return nil, nil }
	a.pairOps.connectFn = func(context.Context, string) (string, error) { return "connected", nil }
	a.PairConnect("", "192.0.2.10", "5555", "", "123456")
	if st := waitPairPhase(t, a, PairPhaseFailed); st.ErrCode != PairErrPort {
		t.Fatalf("missing wireless debugging: %+v", st)
	}
	a.PairConnect("", "192.0.2.10", "5555", "", "")
	waitPairPhase(t, a, PairPhaseSuccess)
	e, _ := a.profiles.Entry("PHONE")
	if len(e.Addrs) != 1 || e.Addrs[0].Mode != ModeTcpip {
		t.Fatalf("fake TLS archive: %+v", e)
	}
}
