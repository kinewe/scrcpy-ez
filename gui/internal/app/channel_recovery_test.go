package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestChannelRecoveryFreshSignalDuringFailedCheck(t *testing.T) {
	a := channelTestApp()
	a.profiles.MarkAddrStale(displayTestKey, displayTestTCP)
	a.profiles.MarkAddrStale(displayTestKey, displayTestTLS)
	displayTestCommit(a, nil)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	a.disc.TcpProbeFn = func(ctx context.Context, addr string) bool {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return false
		}
		return true
	}
	go func() {
		a.checkChannel(context.Background(), displayTestKey, displayTestTCP, ModeTcpip, "", "")
		close(done)
	}()
	displayTestWait(t, entered)
	// adbd was not ready when the first probe started, but announces readiness
	// while that probe is still returning. Keep one trailing check for the burst.
	for i := 0; i < 100; i++ {
		a.refreshChannel(context.Background(), displayTestKey, displayTestTCP, ModeTcpip, "CARDTEST", "adb-CARDTEST")
	}
	resume()
	displayTestWait(t, done)
	waitForMdns(t, "fresh signal must survive an in-flight failure", func() bool { return channelTestState(t, a, displayTestTCP) == AddrStateActive })
	a.closeChannelChecks()
	if calls.Load() != 2 {
		t.Fatalf("burst should produce one trailing check; calls=%d", calls.Load())
	}
	if d := displayTestCard(t, a); d.State != "device" || d.WirelessIP != displayTestTCP || d.Tls {
		t.Fatalf("recovery card disagrees with archive: %+v", d)
	}
}

func TestChannelRecoveryQueuedSignalCancelledOnShutdown(t *testing.T) {
	a := channelTestApp()
	entered, done := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a.disc.TcpProbeFn = func(ctx context.Context, _ string) bool {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-ctx.Done()
		return false
	}
	go func() {
		a.checkChannel(context.Background(), displayTestKey, displayTestTLS, ModeTls, "", "")
		close(done)
	}()
	displayTestWait(t, entered)
	a.refreshChannel(context.Background(), displayTestKey, displayTestTLS, ModeTls, "CARDTEST", "adb-CARDTEST-Ab12Cd")
	a.closeChannelChecks()
	displayTestWait(t, done)
	if calls.Load() != 1 || channelTestState(t, a, displayTestTLS) != AddrStateActive {
		t.Fatal("shutdown either ran queued network work or marked the channel offline")
	}
}

func TestChannelRecoveryWithoutAnnouncementAndAlongsideUSB(t *testing.T) {
	for _, usb := range []bool{false, true} {
		t.Run(map[bool]string{false: "wireless", true: "USB-stays-online"}[usb], func(t *testing.T) {
			a := channelTestApp()
			a.profiles.MarkAddrStale(displayTestKey, displayTestTLS)
			a.profiles.MarkAddrStale(displayTestKey, displayTestTCP)
			var devs []adb.Device
			if usb {
				devs = []adb.Device{{Serial: "CARDTEST", StableSerial: "CARDTEST", State: "device", ConnType: "usb"}}
			}
			displayTestCommit(a, devs)
			a.disc.TcpProbeFn = func(_ context.Context, addr string) bool { return addr == displayTestTCP }
			a.checkProfileChannelsFiltered(context.Background(), "", nil, true)
			if channelTestState(t, a, displayTestTCP) != AddrStateActive || channelTestState(t, a, displayTestTLS) != AddrStateStale {
				t.Fatal("no-announcement recovery missed 5555 or incorrectly activated TLS")
			}
			d := displayTestCard(t, a)
			if d.State != "device" || usb && d.ConnType != "usb" || !usb && d.WirelessIP != displayTestTCP {
				t.Fatalf("incorrect card transport after recovery: %+v", d)
			}
		})
	}
}

func TestChannelRecoveryUsesKnownNewPortButRejectsUnknownOwner(t *testing.T) {
	a := channelTestApp()
	a.profiles.MarkAddrStale(displayTestKey, displayTestTLS)
	displayTestCommit(a, nil)
	services := channelTestServices()
	services[1].Addr = "192.0.2.159:48888"
	var mu sync.Mutex
	var probed []string
	a.disc.TcpProbeFn = func(_ context.Context, addr string) bool {
		mu.Lock()
		probed = append(probed, addr)
		mu.Unlock()
		return addr == services[1].Addr
	}
	a.pairOps.serialFn = func(context.Context, string) (string, error) { return "WRONG_OWNER", nil }
	a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { return "WRONG_OWNER", nil }
	a.checkProfileChannelsFiltered(context.Background(), "", services, true)
	for _, addr := range probed {
		if addr == displayTestTCP {
			t.Fatal("fast recovery repeatedly probed the healthy 5555 channel")
		}
	}
	e, _ := a.profiles.Entry(displayTestKey)
	for _, addr := range e.Addrs {
		if addr.Mode == ModeTls && addr.State == AddrStateActive {
			t.Fatal("new port accepted without matching identity")
		}
	}
	if channelTestState(t, a, displayTestTCP) != AddrStateActive {
		t.Fatal("wrong TLS owner damaged the independent 5555 channel")
	}
}
