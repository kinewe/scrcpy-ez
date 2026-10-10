package deviceevents

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("SCEZ_TEST_TRACK") == "1" && len(os.Args) > 1 && os.Args[1] == "track-devices" {
		fmt.Fprint(os.Stdout, "zzzz")
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fragmented struct{ io.Reader }

func (r fragmented) Read(p []byte) (int, error) { return r.Reader.Read(p[:1]) }

func TestFrameBoundaries(t *testing.T) {
	block := "A\tunauthorized usb:1 transport_id:7\n192.0.2.1:5555\tdevice transport_id:8\n"
	for _, crlf := range []bool{false, true} {
		payload := block
		if crlf {
			payload = strings.ReplaceAll(payload, "\n", "\r\n")
		}
		br := bufio.NewReader(fragmented{strings.NewReader("0000" + fmt.Sprintf("%04x", len(block)) + payload + "0000")})
		for _, want := range []string{"", block, ""} {
			got, err := ReadBlock(br)
			if err != nil || got != want {
				t.Fatalf("got %q %v want %q", got, err, want)
			}
		}
	}
	for _, bad := range []string{"zzzz", "-001", "0010short", "0"} {
		if _, err := ReadBlock(bufio.NewReader(strings.NewReader(bad))); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestClassificationAndReadiness(t *testing.T) {
	got := Parse("A unauthorized usb:1 transport_id:1\nA-IP:device device usb:2\n192.0.2.1:5555 offline\nadb-A._adb-tls-connect._tcp device\nadb-A._adb-tls-pairing._tcp device\nemulator-5554 device\nadb-A._adb._tcp device\n")
	want := []string{"usb", "usb", "wifi", "wifi", "other", "other", "wifi"}
	for i, v := range got {
		if v.Kind != want[i] {
			t.Fatalf("%+v", v)
		}
	}
	if got[0].State != "unauthorized" || got[0].ID != "1" {
		t.Fatal(got[0])
	}
}

func TestGenerationsAndSlowSubscriber(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := h.Subscribe(ctx)
	<-ch
	pub := func(ts ...Transport) { h.Publish(Snapshot{Epoch: 1, Available: true, Transports: ts}) }
	a := Transport{Serial: "A", State: "device", Kind: "usb", ID: "1"}
	pub(a)
	first := <-ch
	pub(a)
	same := <-ch
	if first.Transports[0].Generation != same.Transports[0].Generation {
		t.Fatal("quiet snapshot changed generation")
	}
	for n := 0; n < 100; n++ {
		pub()
		pub(a)
	}
	var last Snapshot
	for len(ch) > 0 {
		last = <-ch
	}
	if last.Transports[0].Generation != 101 {
		t.Fatal(last)
	}
	h.Publish(Snapshot{Epoch: 2, Available: true, Transports: []Transport{a}})
	if s := <-ch; s.Transports[0].Generation != 102 {
		t.Fatal(s)
	}
}

func TestSnapshotTokenSurvivesLearningButRejectsReplug(t *testing.T) {
	h := NewLearningHub()
	a := Transport{Serial: "A", State: "device", Kind: "usb", ID: "1"}
	h.Publish(Snapshot{Epoch: 1, Sequence: 1, Available: true, Transports: []Transport{a}})
	before := h.Current()
	h.SetLearning("A", true)
	if !SameTransports(before, h.Current()) {
		t.Fatal("learning notification invalidated the raw connection token")
	}
	h.Publish(Snapshot{Epoch: 1, Sequence: 2, Available: true})
	h.Publish(Snapshot{Epoch: 1, Sequence: 3, Available: true, Transports: []Transport{a}})
	if SameTransports(before, h.Current()) {
		t.Fatal("removed/replugged USB reused the old token")
	}
	returned := h.Current()
	h.Publish(Snapshot{Epoch: 2, Sequence: 1, Available: true, Transports: []Transport{a}})
	if SameTransports(returned, h.Current()) {
		t.Fatal("ADB epoch restart reused the old token")
	}
}

func TestArrivalHoldEndsIfUSBIsRemovedBeforeLearningStarts(t *testing.T) {
	h := NewLearningHub()
	h.Publish(Snapshot{Epoch: 1, Available: true, Transports: []Transport{{Serial: "A", State: "device", Kind: "usb"}}})
	if len(h.Current().Learning) != 1 {
		t.Fatal("arrival must initially hold startup for USB learning")
	}
	h.Publish(Snapshot{Epoch: 1, Available: true})
	if len(h.Current().Learning) != 0 {
		t.Fatal("removed USB retained an orphaned automatic learning hold")
	}
}

func TestMalformedLiveClientIsCancelled(t *testing.T) {
	t.Setenv("SCEZ_TEST_TRACK", "1")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	began := time.Now()
	got, err := trackOnce(ctx, path, []string{"track-devices"}, 1, NewHub())
	if got || err == nil || !strings.Contains(err.Error(), "invalid track prefix") || time.Since(began) > 2*time.Second {
		t.Fatalf("got=%v err=%v duration=%v", got, err, time.Since(began))
	}
}

func TestLearningShieldCannotOverwriteRawState(t *testing.T) {
	h := NewLearningHub()
	h.Publish(Snapshot{Epoch: 1, Available: true, Transports: []Transport{{Serial: "A", State: "device", Kind: "usb"}}})
	version := h.holds["A"]
	h.SetLearning("A", true)
	h.Publish(Snapshot{Epoch: 1, Sequence: 2, Available: true})
	h.expireHold("A", version)
	if len(h.last.Transports) != 0 || len(h.last.Learning) != 1 || h.last.Sequence != 2 {
		t.Fatal(h.last)
	}
	h.SetLearning("A", false)
	if len(h.last.Learning) != 0 {
		t.Fatal(h.last)
	}
}

func TestBrokerPreservesGenerationAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := NewHub()
	a := Transport{Serial: "A", State: "device", Kind: "usb"}
	for n := 0; n < 10; n++ {
		source.Publish(Snapshot{Epoch: 1, Available: true})
		source.Publish(Snapshot{Epoch: 1, Available: true, Transports: []Transport{a}})
	}
	endpoint, token, err := Serve(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	destination := NewHub()
	events := destination.Subscribe(ctx)
	<-events
	fctx, fcancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- Follow(fctx, endpoint, token, destination) }()
	select {
	case s := <-events:
		if len(s.Transports) != 1 || s.Transports[0].Generation != 10 {
			t.Fatal(s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no baseline")
	}
	fcancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Follow did not cancel")
	}
	badctx, badcancel := context.WithTimeout(ctx, time.Second)
	defer badcancel()
	if err := Follow(badctx, endpoint, "wrong-token", NewHub()); err == nil {
		t.Fatal("unauthenticated stream accepted")
	}
}
