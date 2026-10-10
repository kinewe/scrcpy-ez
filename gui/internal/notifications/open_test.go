package notifications

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenRoutingKeepsAllOTPCardsCopyOnly(t *testing.T) {
	for _, tc := range []struct {
		body                                 string
		preview, enabled, wantOpen, wantCopy bool
	}{
		{"来自邮件的详情", true, true, true, false},
		{"来自邮件的详情", true, false, false, false},
		{"验证码：123456", true, true, false, true},
		{"验证码：123456", true, false, false, true},
		{"验证码：123456", false, true, false, false},
		{"验证码：123456", false, false, false, false},
		{"验证码为123456或654321", true, true, false, false},
	} {
		state := NewState("tablet", "Pad", tc.preview)
		state.policy.OpenEnabled = &tc.enabled
		var request OpenRequest
		state.opener = func(_ context.Context, r OpenRequest) error { request = r; return nil }
		sink := &testSink{}
		frames := []Frame{{Session: "session", Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: Record{Key: "key", Package: "com.example.owner", OpenPackage: "com.example.mail", PostTime: 101, Body: tc.body, OpenToken: strings.Repeat("a", 32)}}}
		for _, f := range frames {
			if err := state.Apply(f, sink); err != nil {
				t.Fatal(err)
			}
		}
		card := sink.shown[0]
		if (card.Open != nil) != tc.wantOpen || (card.CopyCode != "") != tc.wantCopy {
			t.Fatalf("routing body=%q preview=%v enabled=%v", tc.body, tc.preview, tc.enabled)
		}
		if card.Open != nil {
			if err := card.Open(context.Background()); err != nil {
				t.Fatal(err)
			}
			if request.Identity != "tablet" || request.Session != "session" || request.Key != "key" || request.Package != "com.example.mail" {
				t.Fatal("lost device/session/original target binding")
			}
		}
	}
}

func TestOpenTokenRefreshIsSilentAndSeparateFromCopy(t *testing.T) {
	state := NewState("tablet", "Pad", true)
	state.opener = func(context.Context, OpenRequest) error { return nil }
	sink := &testSink{}
	for _, f := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "ready"},
		{Sequence: 3, Type: "post", Record: Record{Key: "same", Package: "com.example.mail", Body: "same content", PostTime: 101, OpenToken: strings.Repeat("a", 32)}},
		{Sequence: 4, Type: "post", Record: Record{Key: "same", Package: "com.example.mail", Body: "same content", PostTime: 102, OpenToken: strings.Repeat("b", 32)}}} {
		if err := state.Apply(f, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 2 || sink.shown[0].Silent || !sink.shown[1].Silent {
		t.Fatal("capability refresh caused another alert")
	}
	var store openStore
	card := sink.shown[0]
	now := time.Now()
	old := store.Issue(card, now)
	latest := store.Issue(card, now)
	if _, ok := store.Take(old, now); ok {
		t.Fatal("superseded activation still valid")
	}
	if _, ok := store.Take(latest, now); !ok {
		t.Fatal("latest activation lost")
	}
	if _, ok := store.Take(latest, now); ok {
		t.Fatal("activation replay")
	}
	card.CopyCode = "123456"
	if store.Issue(card, now) != "" {
		t.Fatal("OTP received open capability")
	}
	card.CopyCode = ""
	token := store.Issue(card, now)
	store.Remove(card.Group, "")
	if _, ok := store.Take(token, now); ok {
		t.Fatal("cleared device retained open action")
	}
	token = store.Issue(card, now)
	if _, ok := store.Take(token, now.Add(25*time.Hour)); ok {
		t.Fatal("expired activation accepted")
	}
}

func TestPhoneActionIsBoundToLiveDeviceAndSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := OpenRequest{Identity: "tablet", Session: "session", Key: "key", Token: strings.Repeat("a", 32)}
	source := &ADBSource{connections: map[string]*openConnection{"session": {ctx: ctx, identity: "tablet", tokens: map[string]string{"key": request.Token}}}}
	if !source.Current(request) {
		t.Fatal("valid action rejected")
	}
	wrong := request
	wrong.Identity = "phone"
	if source.Current(wrong) {
		t.Fatal("cross-device action")
	}
	wrong = request
	wrong.Session = "old"
	if source.Current(wrong) {
		t.Fatal("cross-session action")
	}
	wrong = request
	wrong.Token = strings.Repeat("b", 32)
	if source.Current(wrong) {
		t.Fatal("updated notification action")
	}
	cancel()
	if source.Current(request) {
		t.Fatal("disconnected source accepted")
	}
}

func TestNotificationCapabilityFailureSeparatesLifecycleCauses(t *testing.T) {
	request := OpenRequest{Identity: "tablet", Session: "session", Key: "key", Token: strings.Repeat("a", 32)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &openConnection{ctx: ctx, identity: request.Identity, tokens: map[string]string{request.Key: request.Token}}
	source := &ADBSource{connections: map[string]*openConnection{request.Session: c}}
	assertReason := func(want string) {
		t.Helper()
		connection, reason := source.currentLockedReason(request)
		if reason != want || (want == "") != (connection != nil) {
			t.Fatalf("capability state: %q, want %q", reason, want)
		}
	}
	assertReason("")
	c.tokens[request.Key] = strings.Repeat("b", 32)
	assertReason("notification-updated")
	delete(c.tokens, request.Key)
	assertReason("notification-removed")
	c.tokens[request.Key] = request.Token
	cancel()
	assertReason("session-canceled")
	delete(source.connections, request.Session)
	assertReason("session-missing")
}

func TestCapabilityRotationNeverReplaysQuietBaseline(t *testing.T) {
	state := NewState("tablet", "Pad", true)
	state.opener = func(context.Context, OpenRequest) error { return nil }
	sink := &testSink{}
	for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "snapshot", Record: Record{Key: "old", Package: "com.example.mail", Body: "old message", PostTime: 90, OpenToken: strings.Repeat("a", 32)}}, {Sequence: 3, Type: "ready"}, {Sequence: 4, Type: "post", Record: Record{Key: "old", Package: "com.example.mail", Body: "old message", PostTime: 101, OpenToken: strings.Repeat("b", 32)}}} {
		if err := state.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 0 {
		t.Fatal("action metadata replayed historical notification")
	}
}

func TestToastActivationNeverContainsPhoneCapability(t *testing.T) {
	card := Card{Title: "邮件", Open: func(context.Context) error { return nil }, OpenToken: strings.Repeat("a", 32)}
	if !strings.Contains(ToastXML(card), `launch="open:`+card.OpenToken+`"`) {
		t.Fatal("ordinary card not clickable")
	}
	card.CopyCode = "123456"
	card.Token = strings.Repeat("b", 32)
	xml := ToastXML(card)
	if strings.Contains(xml, "open:") || !strings.Contains(xml, `launch="copy:`) {
		t.Fatal("OTP activation changed")
	}
}
