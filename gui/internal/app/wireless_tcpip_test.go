package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func TestKnownTLSRecoveryLearnsTcpipOnceAndAgainAfterOffline(t *testing.T) {
	a, _ := newWirelessApp()
	installDirectFakes(a)
	a.profiles.PairArchive("", "PHONE", "192.0.2.10:41000", "adb-PHONE-Ab12Cd", "Phone", "")
	a.mdns = directServices()
	var enabled atomic.Bool
	var toggles, connects atomic.Int32
	a.pairOps.getpropFn = func(_ context.Context, addr, prop string) (string, error) {
		if prop == "ro.serialno" {
			return "PHONE", nil
		}
		if prop == "service.adb.tcp.port" {
			if enabled.Load() {
				return "5555", nil
			}
			return "-1", nil
		}
		return "", nil
	}
	a.pairOps.tcpipFn = func(_ context.Context, addr, port string) error {
		if addr != "192.0.2.10:41000" || port != "5555" {
			t.Errorf("wrong tcpip target %s %s", addr, port)
		}
		toggles.Add(1)
		enabled.Store(true)
		return nil
	}
	a.pairOps.probeFn = func(context.Context, string) bool { return enabled.Load() }
	a.pairOps.connectFn = func(_ context.Context, addr string) (string, error) {
		if addr != "192.0.2.10:5555" {
			t.Errorf("wrong backup %s", addr)
		}
		connects.Add(1)
		return "connected", nil
	}
	devs := []adb.Device{{Serial: "192.0.2.10:41000", ConnType: "wifi", State: "device"}}
	for range 3 {
		a.maybeLearnWirelessTcpip(devs)
	}
	waitFor(t, 2*time.Second, func() bool {
		e, _ := a.profiles.Entry("PHONE")
		return gui50Fix45EntryHasAddr(e, "192.0.2.10:5555", ModeTcpip)
	}, "TCP/IP learned after TLS recovery")
	for range 3 {
		a.maybeLearnWirelessTcpip(devs)
	}
	if toggles.Load() != 1 || connects.Load() != 1 {
		t.Fatalf("duplicated learning: %d/%d", toggles.Load(), connects.Load())
	}
	waitFor(t, time.Second, func() bool { a.mu.RLock(); defer a.mu.RUnlock(); return !a.wirelessTcpipLearning["device:PHONE"].busy }, "learning complete")
	a.maybeLearnWirelessTcpip(nil)
	enabled.Store(false)
	a.maybeLearnWirelessTcpip(devs)
	waitFor(t, 2*time.Second, func() bool { return toggles.Load() == 2 && connects.Load() == 2 }, "learn again after reboot")
}

func TestKnownTLSLearningRejectsForeignAndDoesNotArchiveClosed5555(t *testing.T) {
	for _, serial := range []string{"FOREIGN", "PHONE"} {
		a, _ := newWirelessApp()
		a.profiles.PairArchive("", "PHONE", "192.0.2.10:41000", "adb-PHONE-Ab12Cd", "Phone", "")
		var toggles atomic.Int32
		a.pairOps.getpropFn = func(_ context.Context, addr, prop string) (string, error) {
			if prop == "ro.serialno" {
				return serial, nil
			}
			return "-1", nil
		}
		a.pairOps.probeFn = func(context.Context, string) bool { return false }
		a.pairOps.tcpipFn = func(context.Context, string, string) error { toggles.Add(1); return errors.New("offline") }
		a.startWirelessTcpipLearning("device:PHONE", "192.0.2.10:41000", "PHONE")
		waitFor(t, 2*time.Second, func() bool { a.mu.RLock(); defer a.mu.RUnlock(); return !a.wirelessTcpipLearning["device:PHONE"].busy }, "failed learning")
		e, _ := a.profiles.Entry("PHONE")
		if gui50Fix45EntryHasAddr(e, "192.0.2.10:5555", ModeTcpip) {
			t.Fatal("unavailable endpoint archived")
		}
		if serial == "FOREIGN" && toggles.Load() != 0 {
			t.Fatal("command issued to foreign device")
		}
	}
}

func TestNativeTLSAliasLearnsOpen5555AndLostRestartAck(t *testing.T) {
	for _, alreadyOpen := range []bool{true, false} {
		a, _ := newWirelessApp()
		a.profiles.PairArchive("", "PHONE", "192.0.2.10:41000", "adb-PHONE-Ab12Cd", "Phone", "")
		a.mdns = directServices()
		var toggles atomic.Int32
		a.pairOps.getpropFn = func(_ context.Context, addr, prop string) (string, error) {
			if prop == "ro.serialno" {
				return "PHONE", nil
			}
			if alreadyOpen {
				return "5555\n", nil
			}
			return "-1", nil
		}
		a.pairOps.tcpipFn = func(context.Context, string, string) error {
			toggles.Add(1)
			return errors.New("adbd restarted before ack")
		}
		a.pairOps.probeFn = func(context.Context, string) bool { return true }
		a.pairOps.connectFn = func(context.Context, string) (string, error) { return "connected", nil }
		a.maybeLearnWirelessTcpip([]adb.Device{{Serial: "adb-PHONE-Ab12Cd._adb-tls-connect._tcp", ConnType: "wifi", State: "device"}})
		waitFor(t, time.Second, func() bool {
			e, _ := a.profiles.Entry("PHONE")
			return gui50Fix45EntryHasAddr(e, "192.0.2.10:5555", ModeTcpip)
		}, "native TLS alias backup learned")
		want := int32(1)
		if alreadyOpen {
			want = 0
		}
		if toggles.Load() != want {
			t.Fatalf("tcpip commands=%d, want=%d", toggles.Load(), want)
		}
	}
}

func TestTLSLearningDoesNotRestartAdbdDuringPairVerification(t *testing.T) {
	a, _ := newWirelessApp()
	a.pairOps.probeFn = func(context.Context, string) bool { t.Error("unexpected probe"); return false }
	a.pairOps.tcpipFn = func(context.Context, string, string) error { t.Error("restarted adbd during pairing"); return nil }
	a.pair = &PairStatus{Phase: PairPhaseVerify, Addr: "192.0.2.10:41000"}
	a.startWirelessTcpipLearning("device:PHONE", "192.0.2.10:41000", "PHONE")
	if len(a.wirelessTcpipLearning) != 0 {
		t.Fatal("pairing must own initial tcpip setup")
	}
}
