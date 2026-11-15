package adb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/parallel"
)

func TestEnrichmentParallelProgressPreservesOrderAndOfflineStates(t *testing.T) {
	m := New("unused", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan string, 2), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(resume)
	m.runFn = func(ctx context.Context, args ...string) (string, error) {
		serial := args[1]
		if args[len(args)-1] == "ro.product.marketname" {
			entered <- serial
			if serial == "SLOW" {
				select {
				case <-release:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return serial, nil
		}
		if strings.Contains(strings.Join(args, " "), "battery") {
			return "level: 87", nil
		}
		if args[len(args)-1] == "size" {
			return "Physical size: 1080x2400", nil
		}
		return "120", nil
	}
	updates := make(chan []Device, 10)
	done := make(chan []Device, 1)
	go func() {
		done <- m.devicesFromOutputProgress(ctx, "SLOW device\nFAST device\nOFF offline\nAUTH unauthorized\n", func(d []Device) { updates <- d })
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("devices were enriched serially")
		}
	}
	for {
		select {
		case d := <-updates:
			if len(d) != 4 || d[0].Serial != "SLOW" || d[1].Serial != "FAST" || d[2].State != "offline" || d[3].State != "unauthorized" {
				t.Fatalf("unstable order/state: %+v", d)
			}
			if d[1].Battery == 87 {
				if d[0].Battery != 0 {
					t.Fatal("slow device was unexpectedly released")
				}
				resume()
				final := <-done
				if final[0].Battery != 87 {
					t.Fatal("slow final update missing")
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("fast metadata waited for the slow device")
		}
	}
}

func TestEnrichmentGlobalBoundAcrossConcurrentSnapshots(t *testing.T) {
	m := New("unused", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, peak atomic.Int32
	entered := make(chan struct{}, 40)
	m.runFn = func(ctx context.Context, _ ...string) (string, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	var wg sync.WaitGroup
	for batch := 0; batch < 2; batch++ {
		wg.Add(1)
		go func(batch int) {
			defer wg.Done()
			var s strings.Builder
			for i := 0; i < 20; i++ {
				fmt.Fprintf(&s, "B%dDEV%d device\n", batch, i)
			}
			m.devicesFromOutput(ctx, s.String())
		}(batch)
	}
	for i := 0; i < parallel.Devices; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("parallel workers missing")
		}
	}
	cancel()
	wg.Wait()
	if peak.Load() > parallel.Devices {
		t.Fatalf("unbounded property commands: %d", peak.Load())
	}
}

func TestEnrichmentTrackRemovesDeviceWhileOldQueryIsBlocked(t *testing.T) {
	m := New("unused", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	m.runFn = func(ctx context.Context, args ...string) (string, error) {
		if args[1] == "SLOW" {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return "", ctx.Err()
		}
		if args[len(args)-1] == "ro.product.marketname" {
			return "Fast", nil
		}
		return "", errors.New("unused metadata")
	}
	h := m.EventHub()
	h.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "SLOW", State: "device", Kind: "usb"}, {Serial: "FAST", State: "device", Kind: "usb"}}})
	seen := make(chan TrackEvents, 20)
	track := m.NewTrack(func(e TrackEvents) { seen <- e })
	done := make(chan error, 1)
	go func() { done <- track.runSnapshots(ctx, h.Subscribe(ctx), make(chan time.Time)) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("slow query did not start")
	}
	h.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "FAST", State: "device", Kind: "usb"}}})
	for {
		select {
		case e := <-seen:
			if len(e.Devices) == 1 && e.Devices[0].Serial == "FAST" {
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("track did not stop")
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("USB removal waited for stale property work")
		}
	}
}

func TestEnrichmentCacheDoesNotCrossTransportGeneration(t *testing.T) {
	m := New("unused", "")
	h := m.EventHub()
	serial := "192.0.2.10:5555"
	h.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: serial, State: "device", Kind: "wifi", ID: "1"}}})
	old := h.Current()
	ctx := snapshotContext(context.Background(), old)
	d := Device{Serial: serial, State: "device"}
	c := m.cacheFor(ctx, &d)
	c.name, c.battery = "Old owner", 99
	h.Publish(deviceevents.Snapshot{Epoch: 1, Available: true})
	h.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: serial, State: "device", Kind: "wifi", ID: "2"}}})
	fresh := Device{Serial: serial, State: "device"}
	m.peekDevice(snapshotContext(context.Background(), h.Current()), &fresh)
	if fresh.Name != "" || fresh.Battery != 0 {
		t.Fatal("new transport inherited stale metadata", fresh)
	}
	current := m.cache[serial]
	stale := m.cacheFor(ctx, &d)
	stale.name = "Late old reply"
	if m.cache[serial] != current || m.cache[serial].name != "" {
		t.Fatal("old generation replaced the new cache")
	}
}

func TestEnrichmentStartupListCannotSeedLaterRawConnection(t *testing.T) {
	m := New("unused", "")
	d := Device{Serial: "192.0.2.10:5555", State: "device"}
	old := m.cacheFor(context.Background(), &d)
	old.name, old.battery = "Before tracking", 98
	h := m.EventHub()
	h.Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: d.Serial, State: "device", Kind: "wifi", ID: "new"}}})
	m.peekDevice(snapshotContext(context.Background(), h.Current()), &d)
	if d.Name != "" || d.Battery != 0 || m.cache[d.Serial] == old {
		t.Fatal("unscoped startup query was attached to the new connection")
	}
	old.name = "Late startup reply"
	if m.cache[d.Serial].name != "" {
		t.Fatal("late startup query contaminated the live cache")
	}
}
