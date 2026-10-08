package adb

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
)

func batteryTrackFixture() (*Manager, *cachedSpec) {
	m := New("unused-adb", "")
	now := time.Now()
	c := &cachedSpec{identity: "device:TABLET", name: "Tablet", marketname: "Tablet", model: "Tablet", at: now, specAt: now, batAt: now, battery: 48, res: "1920x1280", fps: 60}
	m.cache["192.0.2.20:39263"] = c
	m.identityResolver = func(string) (string, string) { return "device:TABLET", "TABLET" }
	m.EventHub().Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "192.0.2.20:39263", State: "device", Kind: "wifi", ID: "11"}}})
	return m, c
}

func expireBattery(c *cachedSpec) {
	c.mu.Lock()
	c.batAt = time.Now().Add(-batteryTTL - time.Second)
	c.mu.Unlock()
}

func TestTrackRefreshesBatteryWithoutTopologyChange(t *testing.T) {
	m, c := batteryTrackFixture()
	var queries atomic.Int32
	m.runFn = func(_ context.Context, args ...string) (string, error) {
		if !reflect.DeepEqual(args, []string{"-s", "192.0.2.20:39263", "shell", "dumpsys", "battery"}) {
			t.Errorf("property refresh issued unrelated ADB command: %v", args)
			return "", errors.New("unexpected command")
		}
		queries.Add(1)
		return "Current Battery Service state:\n  level: 100\n  scale: 100\n", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := m.EventHub().Subscribe(ctx)
	ticks := make(chan time.Time)
	seen := make(chan TrackEvents, 4)
	track := m.NewTrack(func(ev TrackEvents) { seen <- ev })
	done := make(chan error, 1)
	go func() { done <- track.runSnapshots(ctx, events, ticks) }()
	receive := func() TrackEvents {
		t.Helper()
		select {
		case ev := <-seen:
			return ev
		case <-time.After(3 * time.Second):
			t.Fatal("property event missing")
			return TrackEvents{}
		}
	}
	initial := receive()
	if initial.Devices[0].Battery != 48 || queries.Load() != 0 {
		t.Fatal("fresh initial cache was not used")
	}
	expireBattery(c)
	ticks <- time.Now()
	updated := receive()
	if updated.Devices[0].Battery != 100 || len(updated.Changed) != 1 || len(updated.Added)+len(updated.Removed) != 0 || queries.Load() != 1 {
		t.Fatalf("same-port battery refresh failed: %+v, queries=%d", updated, queries.Load())
	}
	if !deviceevents.SameTransports(*initial.Raw, *updated.Raw) {
		t.Fatal("property tick changed transport generation")
	}
	// A fresh cache and a successful unchanged re-query must not emit duplicate cards.
	ticks <- time.Now()
	expireBattery(c)
	ticks <- time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not stop on cancellation")
	}
	if len(seen) != 0 {
		t.Fatal("unchanged battery caused a duplicate device event")
	}
}

func TestPropertyRefreshRejectsReusedPortAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		m, c := batteryTrackFixture()
		var count int
		track := m.NewTrack(func(TrackEvents) { count++ })
		if !track.consumeSnapshot(context.Background(), m.EventHub().Current()) {
			t.Fatal("initial snapshot rejected")
		}
		expireBattery(c)
		started, release := make(chan struct{}), make(chan struct{})
		m.runFn = func(context.Context, ...string) (string, error) {
			close(started)
			<-release
			return "level: 100\n", nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		old := m.EventHub().Current()
		done := make(chan bool, 1)
		go func() { done <- track.consumeSnapshotProperties(ctx, old, true) }()
		<-started
		if cancelled {
			cancel()
		} else {
			m.EventHub().Publish(deviceevents.Snapshot{Epoch: 1, Available: true})
			m.EventHub().Publish(old)
			if deviceevents.SameTransports(old, m.EventHub().Current()) {
				t.Fatal("fixture did not replace connection generation")
			}
		}
		close(release)
		if <-done || count != 1 {
			t.Fatal("late battery result committed to expired transport or cancelled request")
		}
		cancel()
	}
}

func TestPropertyRefreshFailureKeepsKnownBattery(t *testing.T) {
	m, c := batteryTrackFixture()
	track := m.NewTrack(nil)
	track.consumeSnapshot(context.Background(), m.EventHub().Current())
	expireBattery(c)
	m.runFn = func(context.Context, ...string) (string, error) {
		return "", errors.New("transport temporarily unavailable")
	}
	if !track.consumeSnapshotProperties(context.Background(), m.EventHub().Current(), true) || track.prev[0].Battery != 48 {
		t.Fatal("transient query failure erased known battery")
	}
}
