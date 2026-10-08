package adb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
)

// Opt-in, read-only integration test. Seed only the test Manager's cache, never
// Android's battery service or the installed GUI's profiles/settings.
func TestLiveWirelessBatteryRefresh(t *testing.T) {
	path := os.Getenv("SCEZ_LIVE_ADB")
	if path == "" {
		t.Skip("set SCEZ_LIVE_ADB to test an already connected wireless tablet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	m := New(path, "")
	transports, err := m.Transports(ctx)
	if err != nil {
		t.Fatal("cannot read current ADB transports")
	}
	var serial string
	for _, tr := range transports {
		if tr.ConnType != "wifi" || tr.State != "device" {
			continue
		}
		name, _ := m.Getprop(ctx, tr.Serial, "ro.product.marketname")
		if strings.Contains(name, "Pad") {
			serial = tr.Serial
			break
		}
	}
	if serial == "" {
		t.Fatal("no connected wireless tablet; do not change device transport to satisfy test")
	}
	out, err := m.Shell(ctx, serial, "dumpsys", "battery")
	if err != nil {
		t.Fatal("cannot read tablet battery")
	}
	want := ParseBatteryLevel(out)
	if want < 0 || want > 100 || want == 48 {
		t.Fatal("battery unsuitable for distinct stale-cache integration test")
	}
	now := time.Now()
	c := &cachedSpec{identity: "device:TABLET", name: "Tablet", marketname: "Tablet", model: "Tablet", at: now, specAt: now, batAt: now, battery: 48, res: "1920x1280", fps: 60}
	m.cache[serial] = c
	m.identityResolver = func(string) (string, string) { return "device:TABLET", "TABLET" }
	m.EventHub().Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: serial, State: "device", Kind: "wifi"}}})
	seen := make(chan TrackEvents, 4)
	track := m.NewTrack(func(ev TrackEvents) { seen <- ev })
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	done := make(chan error, 1)
	go func() { done <- track.runSnapshots(ctx, m.EventHub().Subscribe(ctx), ticker.C) }()
	select {
	case ev := <-seen:
		if ev.Devices[0].Battery != 48 {
			t.Fatal("initial test cache not used")
		}
	case <-ctx.Done():
		t.Fatal("initial event missing")
	}
	expireBattery(c)
	select {
	case ev := <-seen:
		if ev.Devices[0].Battery != want || len(ev.Changed) != 1 || len(ev.Added)+len(ev.Removed) != 0 {
			t.Fatal("same-connection real battery query did not update cached value")
		}
		t.Logf("wireless tablet: stale test cache 48%% -> real ADB %d%% after periodic refresh; no reconnect or battery override", want)
	case <-ctx.Done():
		t.Fatal("periodic real battery update missing")
	}
	cancel()
	<-done
}
