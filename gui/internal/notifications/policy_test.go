package notifications

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestPolicyFiltersNewEventsAndWithdrawsReusedKey(t *testing.T) {
	s := NewState("tablet", "Tablet", true)
	sink := &testSink{}
	s.policy = Policy{Mode: ModeWhitelist, Packages: []string{"com.example.mail"}}
	frames := []Frame{
		{Sequence: 1, Type: "hello", Cutoff: 100},
		{Sequence: 2, Type: "snapshot", Record: Record{Key: "old", Package: "com.example.mail", PostTime: 90, Body: "old mail"}},
		{Sequence: 3, Type: "snapshot", Record: Record{Key: "init", Package: "com.example.blocked", PostTime: 101, Body: "blocked initialization"}},
		{Sequence: 4, Type: "ready"},
		{Sequence: 5, Type: "post", Record: Record{Key: "reuse", Package: "com.example.mail", PostTime: 102, Body: "new mail"}},
		{Sequence: 6, Type: "post", Record: Record{Key: "reuse", Package: "com.example.service", DisplayPackage: "com.example.blocked", PostTime: 103, Body: "delegated update"}},
		{Sequence: 7, Type: "remove", Record: Record{Key: "reuse"}},
	}
	for _, frame := range frames {
		if err := s.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 1 || len(sink.removed) != 1 {
		t.Fatal("whitelist leaked an event or retained a formerly visible key")
	}
	if s.records["init"].value.Body != "" {
		t.Fatal("blocked plaintext retained after initialization")
	}
}

func TestOTPModeUsesAllAppsAndRespectsHiddenPreview(t *testing.T) {
	for _, preview := range []bool{true, false} {
		s := NewState("tablet", "Tablet", preview)
		s.policy = Policy{Mode: ModeOTP, Packages: []string{"com.example.other"}}
		sink := &testSink{}
		frames := []Frame{
			{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "ready"},
			{Sequence: 3, Type: "post", Record: Record{Key: "ordinary", Package: "com.example.mail", PostTime: 101, Body: "Your order 123456 has shipped."}},
			{Sequence: 4, Type: "post", Record: Record{Key: "code", Package: "com.example.mail", PostTime: 102, Body: "Your verification code is 246810. Valid for 10 minutes."}},
		}
		for _, frame := range frames {
			if err := s.Apply(frame, sink); err != nil {
				t.Fatal(err)
			}
		}
		if len(sink.shown) != 1 {
			t.Fatal("OTP mode accepted ordinary numbers or used the saved whitelist")
		}
		if preview && sink.shown[0].CopyCode != "246810" {
			t.Fatal("OTP copy action missing")
		}
		if !preview && (sink.shown[0].CopyCode != "" || sink.shown[0].Body != "打开手机查看消息内容") {
			t.Fatal("hidden preview exposed code")
		}
	}
}

func TestPolicyChangeClearsWithoutReplayingAndDelegateMatchesSource(t *testing.T) {
	s := NewState("tablet", "Tablet", true)
	sink := &testSink{}
	r := Record{Key: "a", Package: "com.example.service", DisplayPackage: "com.example.mail", PostTime: 101, Body: "message"}
	for _, f := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: r}} {
		if err := s.Apply(f, sink); err != nil {
			t.Fatal(err)
		}
	}
	policy := Policy{Mode: ModeWhitelist, Packages: []string{"com.example.mail"}}
	if err := s.SetPolicy(policy, true, sink); err != nil {
		t.Fatal(err)
	}
	policy.Packages[0] = "com.example.service"
	if err := s.Apply(Frame{Sequence: 4, Type: "post", Record: r}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.shown) != 1 || sink.cleared != 1 {
		t.Fatal("policy change replayed history")
	}
	r.Body, r.PostTime = "new message", 102
	if err := s.Apply(Frame{Sequence: 5, Type: "post", Record: r}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.shown) != 2 {
		t.Fatal("display source selection or defensive policy copy failed")
	}
	if (Policy{Mode: ModeWhitelist, Packages: []string{"com.example.service"}}).allows(r) {
		t.Fatal("delegate whitelist enabled all attributed apps")
	}
}

type policyTestSource struct {
	streams map[string]chan Record
	starts  atomic.Int32
}

func (s *policyTestSource) Run(ctx context.Context, target Target, emit func(Frame) error) error {
	s.starts.Add(1)
	for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "ready"}} {
		if err := emit(frame); err != nil {
			return err
		}
	}
	sequence := uint64(2)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case record := <-s.streams[target.Identity]:
			sequence++
			if err := emit(Frame{Sequence: sequence, Type: "post", Record: record}); err != nil {
				return err
			}
		}
	}
}

func TestManagerKeepsDevicePoliciesAndPrivacyIndependent(t *testing.T) {
	source := &policyTestSource{streams: map[string]chan Record{"tablet": make(chan Record, 8), "phone": make(chan Record, 8)}}
	sink := &testSink{}
	m := NewManager(context.Background(), source, func() (Sink, error) { return sink, nil })
	defer m.Close()
	hidden := false
	targets := []Target{{Identity: "tablet", Serial: "wifi-tablet"}, {Identity: "phone", Serial: "usb-phone"}}
	options := Options{Preview: true, Policies: map[string]Policy{
		"tablet": {Mode: ModeOTP},
		"phone":  {Mode: ModeWhitelist, Packages: []string{"com.example.mail"}, Preview: &hidden},
	}}
	m.Reconcile(targets, options)
	await(t, func() bool {
		statuses := m.Status()
		return len(statuses) == 2 && statuses[0].State == "active" && statuses[1].State == "active"
	})
	// Caller mutations must not change the manager's copied policies or privacy.
	options.Policies["phone"].Packages[0], hidden = "com.example.other", true
	for _, id := range []string{"tablet", "phone"} {
		source.streams[id] <- Record{Key: "ordinary", Package: "com.example.mail", PostTime: 101, Body: "Your order 123456 has shipped."}
		source.streams[id] <- Record{Key: "code", Package: "com.example.other", PostTime: 102, Body: "Verification code: 246810"}
		source.streams[id] <- Record{Key: "tail-" + id, Package: "com.example.mail", PostTime: 103, Body: "Verification code: 135790"}
	}
	await(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		tablet, phone := false, false
		for _, card := range sink.shown {
			tablet = tablet || card.Tag == ShortID("tail-tablet")
			phone = phone || card.Tag == ShortID("tail-phone")
		}
		return tablet && phone
	})
	sink.mu.Lock()
	if len(sink.shown) != 4 {
		t.Errorf("device rules leaked a notification: got %d cards", len(sink.shown))
	}
	for _, card := range sink.shown {
		if card.Group == ShortID("phone") && (card.CopyCode != "" || card.Body != "打开手机查看消息内容") {
			t.Error("one device inherited another device's preview")
		}
		if card.Group == ShortID("tablet") && card.CopyCode == "" {
			t.Error("OTP device lost its code")
		}
	}
	sink.mu.Unlock()
	hidden = false
	options.Policies["phone"] = Policy{Mode: ModeWhitelist, Packages: []string{"com.example.mail"}, Preview: &hidden}
	options.Policies["tablet"] = Policy{Mode: ModeAll}
	m.Reconcile(targets, options)
	await(t, func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.cleared >= 1 })
	if source.starts.Load() != 2 {
		t.Fatal("changing a device's policy restarted a listener")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.cleared != 1 || len(sink.shown) != 4 {
		t.Fatal("changing one rule cleared another device or replayed history")
	}
}

func TestOtherNotificationsAreExactCatalogComplement(t *testing.T) {
	p := Policy{Mode: ModeWhitelist, Packages: []string{"com.example.mail"}, Catalog: []string{"com.example.mail", "com.example.chat"}, Other: true}
	if !p.allows(Record{Package: "com.example.mail"}) || p.allows(Record{Package: "com.example.chat"}) || !p.allows(Record{Package: "com.example.background"}) {
		t.Fatal("other sources overlap with deselected catalog apps")
	}
	p.Packages = nil
	if !p.Enabled() || !p.allows(Record{Package: "com.example.background"}) || p.allows(Record{Package: "com.example.mail"}) {
		t.Fatal("other-only selection failed")
	}
	p.Other = false
	if p.Enabled() || p.allows(Record{Package: "com.example.background"}) {
		t.Fatal("empty selection enabled notifications")
	}
}
