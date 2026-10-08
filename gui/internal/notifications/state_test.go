package notifications

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testSink struct {
	mu      sync.Mutex
	shown   []Card
	removed []string
	cleared int
}

func (s *testSink) Show(c Card) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shown = append(s.shown, c)
	return nil
}
func (s *testSink) Remove(g, t string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, g+"/"+t)
	return nil
}
func (s *testSink) Clear(string) error { s.mu.Lock(); defer s.mu.Unlock(); s.cleared++; return nil }
func (s *testSink) Close() error       { return nil }

func TestQuietBaselineUpdateAndRemoval(t *testing.T) {
	s := NewState("phone", "K80", true)
	sink := &testSink{}
	frames := []Frame{{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "snapshot", Record: Record{Key: "a", PostTime: 90, Body: "old"}}, {Sequence: 3, Type: "ready"}, {Sequence: 4, Type: "post", Record: Record{Key: "a", PostTime: 101, Body: "old"}}, {Sequence: 5, Type: "post", Record: Record{Key: "a", PostTime: 102, Body: "new"}}, {Sequence: 6, Type: "post", Record: Record{Key: "a", PostTime: 103, Body: "newer"}}, {Sequence: 7, Type: "remove", Record: Record{Key: "a"}}}
	for _, frame := range frames {
		if err := s.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 2 || sink.shown[0].Silent || sink.shown[1].Silent || sink.shown[0].Tag != sink.shown[1].Tag || len(sink.removed) != 1 {
		t.Fatalf("wrong lifecycle: %+v / %v", sink.shown, sink.removed)
	}
}

func TestArrivalDuringInitializationIsNotLost(t *testing.T) {
	s := NewState("phone", "K80", false)
	sink := &testSink{}
	for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 100}, {Sequence: 2, Type: "snapshot", Record: Record{Key: "a", PostTime: 101, Body: "secret"}}, {Sequence: 3, Type: "ready"}, {Sequence: 4, Type: "post", Record: Record{Key: "a", PostTime: 101, Body: "secret"}}} {
		if err := s.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 1 || sink.shown[0].Body == "secret" || sink.shown[0].Title != "收到新消息" {
		t.Fatalf("initialization/preview wrong: %+v", sink.shown)
	}
}

func TestStaleUpdateAndProtocolGaps(t *testing.T) {
	s := NewState("phone", "K80", true)
	sink := &testSink{}
	for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: Record{Key: "a", PostTime: 5, Body: "new"}}, {Sequence: 4, Type: "post", Record: Record{Key: "a", PostTime: 4, Body: "stale"}}} {
		if err := s.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 1 {
		t.Fatal("stale update replaced notification")
	}
	if err := s.Apply(Frame{Sequence: 6, Type: "ready"}, sink); err != ErrProtocol {
		t.Fatal("missing sequence accepted")
	}
}

func TestProtocolRejectsInvalidWithoutExposingBody(t *testing.T) {
	session := strings.Repeat("a", 32)
	valid := Frame{Version: ProtocolVersion, Session: session, Sequence: 1, Type: "post", Record: Record{Key: "a", PostTime: 1, Body: "private code 762184"}}
	data, _ := json.Marshal(valid)
	if _, err := ParseFrame(data, session); err != nil {
		t.Fatal(err)
	}
	valid.Version = 99
	data, _ = json.Marshal(valid)
	_, err := ParseFrame(data, session)
	if err != ErrProtocol || strings.Contains(err.Error(), "762184") {
		t.Fatal("invalid protocol/error privacy")
	}
	if _, err := ParseFrame([]byte(strings.Repeat("x", MaxFrame+1)), session); err != ErrProtocol {
		t.Fatal("large frame accepted")
	}
}

type testSource struct {
	starts atomic.Int32
	stops  atomic.Int32
}

func (s *testSource) Run(ctx context.Context, target Target, emit func(Frame) error) error {
	s.starts.Add(1)
	defer s.stops.Add(1)
	if err := emit(Frame{Sequence: 1, Type: "hello", Cutoff: 100}); err != nil {
		return err
	}
	if err := emit(Frame{Sequence: 2, Type: "ready"}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestManagerOneListenerPerDeviceAndDisable(t *testing.T) {
	source := &testSource{}
	sink := &testSink{}
	m := NewManager(context.Background(), source, func() (Sink, error) { return sink, nil })
	defer m.Close()
	target := Target{Identity: "phone", Name: "K80", Serial: "usb", Epoch: 1}
	m.Reconcile([]Target{target, target}, Options{})
	await(t, func() bool {
		return source.starts.Load() == 1 && len(m.Status()) == 1 && m.Status()[0].State == "active"
	})
	m.Reconcile([]Target{target}, Options{})
	time.Sleep(10 * time.Millisecond)
	if source.starts.Load() != 1 {
		t.Fatal("duplicate device listener")
	}
	m.Reconcile([]Target{target}, Options{Preview: true})
	await(t, func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.cleared > 0 })
	if source.starts.Load() != 1 {
		t.Fatal("preview setting restarted the listener")
	}
	m.Reconcile(nil, Options{})
	await(t, func() bool { return source.stops.Load() == 1 && len(m.Status()) == 0 })
	m.Reconcile([]Target{target}, Options{})
	await(t, func() bool { return source.starts.Load() == 2 })
}

func TestOnlyAlertOnceAndPreviewChange(t *testing.T) {
	s := NewState("phone", "K80", true)
	sink := &testSink{}
	frames := []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: Record{Key: "a", PostTime: 2, Body: "first"}}, {Sequence: 4, Type: "post", Record: Record{Key: "a", PostTime: 3, Body: "second", OnlyAlertOnce: true}}}
	for _, frame := range frames {
		if err := s.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	if !sink.shown[1].Silent {
		t.Fatal("ignored phone only-alert-once flag")
	}
	if err := s.SetPreview(false, sink); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(Frame{Sequence: 5, Type: "post", Record: Record{Key: "a", PostTime: 4, Body: "third"}}, sink); err != nil {
		t.Fatal(err)
	}
	if sink.shown[2].Body == "third" || sink.shown[2].Silent {
		t.Fatal("privacy/new message policy")
	}
	if s.records["a"].value.Body != "" {
		t.Fatal("message body retained after delivery")
	}
}

func TestToastXMLDoesNotAcceptTextAsMarkup(t *testing.T) {
	xml := ToastXML(Card{App: "Mail & chat", Device: "K80", Title: `<actions>`, Body: "hello\x00world"})
	if strings.Contains(xml, "<actions>") || strings.ContainsRune(xml, 0) || !strings.Contains(xml, "&amp;") {
		t.Fatal("unescaped foreign notification")
	}
	xml = ToastXML(Card{App: strings.Repeat("&", 4096), Device: strings.Repeat("&", 4096), Title: strings.Repeat("&", 4096), Body: strings.Repeat("&", 4096)})
	if len(xml) > 5000 {
		t.Fatal("oversized escaped toast")
	}
}

func TestFailedWindowsCapabilityIsNotRetriedOnEverySnapshot(t *testing.T) {
	var attempts atomic.Int32
	m := NewManager(context.Background(), &testSource{}, func() (Sink, error) { attempts.Add(1); return nil, ErrUnavailable })
	defer m.Close()
	target := Target{Identity: "phone", Serial: "usb", Epoch: 1}
	m.Reconcile([]Target{target}, Options{})
	await(t, func() bool { return attempts.Load() == 1 })
	for i := 0; i < 20; i++ {
		m.Reconcile([]Target{target}, Options{})
	}
	time.Sleep(20 * time.Millisecond)
	if attempts.Load() != 1 {
		t.Fatal("repeated Windows probe")
	}
	target.Epoch = 2
	m.Reconcile([]Target{target}, Options{})
	await(t, func() bool { return attempts.Load() == 2 })
}

type delayedSource struct {
	active  atomic.Int32
	maximum atomic.Int32
	starts  atomic.Int32
}

func (s *delayedSource) Run(ctx context.Context, target Target, emit func(Frame) error) error {
	s.starts.Add(1)
	count := s.active.Add(1)
	for old := s.maximum.Load(); count > old; old = s.maximum.Load() {
		if s.maximum.CompareAndSwap(old, count) {
			break
		}
	}
	defer s.active.Add(-1)
	<-ctx.Done()
	time.Sleep(70 * time.Millisecond) // Simulates listener unregister and owned-file cleanup.
	return ctx.Err()
}

func TestReplacementWaitsForOldOwnershipAcrossRapidToggles(t *testing.T) {
	source := &delayedSource{}
	m := NewManager(context.Background(), source, func() (Sink, error) { return &testSink{}, nil })
	target := Target{Identity: "phone", Serial: "usb", Epoch: 1}
	m.Reconcile([]Target{target}, Options{})
	await(t, func() bool { return source.active.Load() == 1 })
	for i := 0; i < 8; i++ {
		m.Reconcile(nil, Options{})
		time.Sleep(2 * time.Millisecond)
		target.Epoch++
		m.Reconcile([]Target{target}, Options{})
		time.Sleep(2 * time.Millisecond)
	}
	await(t, func() bool { return source.starts.Load() >= 2 })
	m.Close()
	if source.maximum.Load() != 1 || source.active.Load() != 0 {
		t.Fatal("overlapping/leaked phone listeners")
	}
}
