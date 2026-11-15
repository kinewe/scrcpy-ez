package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/discovery"
	"scrcpy-ez/gui/internal/wirelessconnect"
)

func rc10Device(t *testing.T, a *App, n int) (string, string) {
	t.Helper()
	serial, addr := fmt.Sprintf("RC10PHONE%d", n), fmt.Sprintf("192.0.2.%d:5555", 10+n)
	key := "device:" + serial
	gui15Seed(a.profiles, key, &DeviceEntry{Serials: []string{serial}, Marketname: serial, Profiles: DefaultProfile(), Addrs: []AddrEntry{{Addr: addr, Mode: ModeTcpip, State: AddrStateStale}}})
	return key, addr
}

func TestRC10DiscoveryConnectVerifyAndPublishIndependentDevices(t *testing.T) {
	for _, slowStage := range []string{"connect", "identity"} {
		t.Run(slowStage, func(t *testing.T) {
			a, _ := newWirelessApp()
			ids, addrs := []string{}, []string{}
			owners := map[string]string{}
			for i := 0; i < 3; i++ {
				id, addr := rc10Device(t, a, i)
				ids = append(ids, id)
				addrs = append(addrs, addr)
				owners[addr] = fmt.Sprintf("RC10PHONE%d", i)
			}
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			t.Cleanup(resume)
			block := func(ctx context.Context, addr, stage string) {
				if addr == addrs[0] && stage == slowStage {
					select {
					case entered <- struct{}{}:
					default:
					}
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
			}
			a.disc.ConnectFn = func(ctx context.Context, addr string) error { block(ctx, addr, "connect"); return ctx.Err() }
			a.disc.MdnsScanFn = func(context.Context, time.Duration) ([]discovery.MdnsService, error) { return nil, nil }
			a.pairOps.getpropFn = func(ctx context.Context, addr, _ string) (string, error) {
				block(ctx, addr, "identity")
				return owners[addr], ctx.Err()
			}
			a.pairOps.serialFn = func(_ context.Context, addr string) (string, error) { return owners[addr], nil }
			displayTestCommit(a, nil)
			done := make(chan struct{})
			go func() { a.runDiscovery(context.Background(), a.profiles.OfflineCandidateAddrs(nil)); close(done) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("slow stage did not start")
			}
			waitFor(t, 3*time.Second, func() bool {
				for i := 1; i < 3; i++ {
					e, _ := a.profiles.Entry(ids[i])
					if ae := gui24FindAddr(e, addrs[i]); ae == nil || ae.State != AddrStateActive {
						return false
					}
				}
				a.mu.RLock()
				defer a.mu.RUnlock()
				if a.discoveryDevices[ids[1]] || a.discoveryDevices[ids[2]] {
					return false
				}
				return true
			}, "other devices must verify while the first is blocked")
			online := 0
			for _, d := range a.Snapshot().Devices {
				if d.State == "device" {
					online++
				}
			}
			if online != 2 {
				t.Fatalf("fast devices did not independently publish: %+v", a.Snapshot().Devices)
			}
			select {
			case <-done:
				t.Fatal("slow device unexpectedly finished")
			default:
			}
			resume()
			displayTestWait(t, done)
		})
	}
}

func TestRC10RecoveryRejectsLateResultAndCancellation(t *testing.T) {
	for _, scenario := range []string{"new-port", "recreated", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			a, _ := newWirelessApp()
			key, addr := rc10Device(t, a, 0)
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			t.Cleanup(resume)
			a.disc.MdnsScanFn = func(context.Context, time.Duration) ([]discovery.MdnsService, error) { return nil, nil }
			a.disc.ConnectFn = func(context.Context, string) error { return nil }
			a.pairOps.getpropFn = func(ctx context.Context, _, _ string) (string, error) {
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-ctx.Done():
				}
				return "RC10PHONE0", ctx.Err()
			}
			a.pairOps.serialFn = func(context.Context, string) (string, error) { return "RC10PHONE0", nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { a.runDiscovery(ctx, a.profiles.OfflineCandidateAddrs(nil)); close(done) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("verification did not start")
			}
			switch scenario {
			case "new-port":
				a.profiles.AddrSuccessMode(key, "192.0.2.20:5555", ModeTcpip)
			case "recreated":
				gui15Seed(a.profiles, key, &DeviceEntry{Serials: []string{"RC10PHONE0"}, Profiles: DefaultProfile()})
			case "cancel":
				cancel()
			}
			resume()
			displayTestWait(t, done)
			e, _ := a.profiles.Entry(key)
			if old := gui24FindAddr(e, addr); old != nil && old.State == AddrStateActive {
				t.Fatal("old result resurrected a retired/cancelled address", e.Addrs)
			}
			if scenario == "new-port" {
				if fresh := gui24FindAddr(e, "192.0.2.20:5555"); fresh == nil || fresh.State != AddrStateActive {
					t.Fatal("newer success lost")
				}
			}
		})
	}
}

func TestRC10ColdSearchDoesNotGateNewMdnsDevice(t *testing.T) {
	a, _ := newWirelessApp()
	_, slow := rc10Device(t, a, 0)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	a.disc.ConnectOutFn = func(ctx context.Context, addr string) (string, error) {
		if addr == slow {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return "connected to " + addr, ctx.Err()
	}
	a.pairOps.getpropFn = func(_ context.Context, addr, _ string) (string, error) {
		if addr == slow {
			return "RC10PHONE0", nil
		}
		return "RC10PHONE1", nil
	}
	a.pairOps.serialFn = func(ctx context.Context, addr string) (string, error) { return a.pairOps.getpropFn(ctx, addr, "") }
	coldDone := make(chan struct{})
	go func() { a.coldSearch5555(context.Background()); close(coldDone) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cold search did not start")
	}
	key, fast := rc10Device(t, a, 1)
	a.mdnsMu.Lock()
	a.mdns = []discovery.MdnsService{{Name: "adb-RC10PHONE1", Addr: fast, Mode: ModeTcpip}}
	a.mdnsMu.Unlock()
	a.adb.EventHub().Publish(deviceevents.Snapshot{Epoch: 1, Available: true})
	target := wirelessconnect.Target{Identity: key, DeviceSerial: "RC10PHONE1", ServerEpoch: 1, Addresses: []string{fast}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.connectDiscoveredWireless(ctx, target); err != nil {
		t.Fatalf("unrelated cold device blocked discovery: %v", err)
	}
	resume()
	displayTestWait(t, coldDone)
}

func TestRC10USBLearningDoesNotHoldTopologyCommit(t *testing.T) {
	a, _ := newTestApp()
	defer a.Close()
	entered := make(chan string, 3)
	a.teachOps.getpropFn = func(ctx context.Context, serial, _ string) (string, error) {
		entered <- serial
		<-ctx.Done()
		return "", ctx.Err()
	}
	devs := []adb.Device{{Serial: "RC10USB0", State: "device", ConnType: "usb"}, {Serial: "RC10USB1", State: "device", ConnType: "usb"}, {Serial: "RC10USB2", State: "device", ConnType: "usb"}}
	done := make(chan struct{})
	go func() { a.applyTrackUpdate(devs); close(done) }()
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("USB devices did not start learning independently")
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("topology commit waited for USB learning I/O")
	}
}

func TestRC10DeletedUSBRejectsLateLearningReply(t *testing.T) {
	a, usb := learningTestApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	a.teachOps.getpropFn = func(context.Context, string, string) (string, error) { close(entered); <-release; return "5555", nil }
	a.applyTrackUpdate([]adb.Device{usb})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("learning query did not start")
	}
	key := a.profiles.ResolveKey(usb.Serial)
	if err := a.DeleteDevices([]string{key}); err != nil {
		t.Fatal(err)
	}
	resume()
	a.learningWG.Wait()
	if _, ok := a.profiles.Entry(key); ok {
		t.Fatal("late learning recreated the deleted archive")
	}
	if !a.deletedUsbMarked(usb.Serial) {
		t.Fatal("late learning cleared the deletion fence")
	}
}
