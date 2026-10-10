package notifications

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDetailSwitchKeepsDeviceCardsAndOTPCopiesIndependent(t *testing.T) {
	source := &policyTestSource{streams: map[string]chan Record{"tablet": make(chan Record, 8), "phone": make(chan Record, 8)}}
	sink := &testSink{}
	m := NewManager(context.Background(), source, func() (Sink, error) { return sink, nil })
	defer m.Close()
	var opened atomic.Int32
	m.SetOpener(func(context.Context, OpenRequest) error { opened.Add(1); return nil })
	targets := []Target{{Identity: "tablet", Serial: "wifi-tablet"}, {Identity: "phone", Serial: "usb-phone"}}
	options := Options{Preview: true, Policies: map[string]Policy{"tablet": {Mode: ModeAll}, "phone": {Mode: ModeAll}}}
	m.Reconcile(targets, options)
	await(t, func() bool {
		statuses := m.Status()
		return len(statuses) == 2 && statuses[0].State == "active" && statuses[1].State == "active"
	})
	ordinary := Record{Key: "ordinary", Package: "com.example.mail", PostTime: 101, Body: "新详情", OpenToken: strings.Repeat("a", 32)}
	code := Record{Key: "code", Package: "com.example.mail", PostTime: 102, Body: "验证码：123456", OpenToken: strings.Repeat("b", 32)}
	source.streams["tablet"] <- ordinary
	source.streams["tablet"] <- code
	source.streams["phone"] <- ordinary
	await(t, func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.shown) == 3 })
	var tablet, phone, otp Card
	sink.mu.Lock()
	for _, card := range sink.shown {
		if card.Group == ShortID("phone") {
			phone = card
		} else if card.CopyCode != "" {
			otp = card
		} else {
			tablet = card
		}
	}
	cleared := sink.cleared
	sink.mu.Unlock()
	if tablet.Open == nil || phone.Open == nil || otp.Open != nil || otp.CopyCode == "" {
		t.Fatal("default detail/copy routing failed")
	}
	if err := tablet.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opened.Load() != 1 {
		t.Fatal("default enabled card did not open")
	}
	// Old Windows cards must stop opening before worker reconciliation completes.
	enabled := false
	options.Policies["tablet"] = Policy{Mode: ModeAll, OpenEnabled: &enabled}
	m.Reconcile(targets, options)
	enabled = true // Caller mutation must not re-enable the copied rule.
	if err := tablet.Open(context.Background()); err != nil {
		t.Fatal("disabled click must silently dismiss:", err)
	}
	if opened.Load() != 1 {
		t.Fatal("old card bypassed disabled device rule")
	}
	if err := phone.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opened.Load() != 2 {
		t.Fatal("other device inherited disabled setting")
	}
	// Direct state check also ensures an open-only edit never clears OTP tokens.
	state := NewState("test", "Test", true)
	state.policy = Policy{Mode: ModeAll}
	stateSink := &testSink{}
	enabled = false
	if err := state.SetPolicy(Policy{Mode: ModeAll, OpenEnabled: &enabled}, true, stateSink); err != nil {
		t.Fatal(err)
	}
	if stateSink.cleared != 0 || state.policy.DetailEnabled() {
		t.Fatal("detail-only toggle cleared cards or did not apply")
	}
	// Reconcile back on, followed by a blocking marker, to check no cards were
	// replayed, cleared, or listeners restarted by either open-only change.
	options.Policies["tablet"] = Policy{Mode: ModeAll}
	m.Reconcile(targets, options)
	ordinary.Key, ordinary.Body, ordinary.PostTime = "marker", "marker", 103
	source.streams["tablet"] <- ordinary
	await(t, func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.shown) == 4 })
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.cleared != cleared || source.starts.Load() != 2 {
		t.Fatal("detail toggle cleared cards or restarted notification listeners")
	}
}
