package notifications

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
)

func TestDisplayAttributionRetainsOwnerAndWithdrawalIdentity(t *testing.T) {
	record := Record{Key: "source-key", Package: "com.miui.systemAdSolution", DisplayPackage: "com.synthetic.shop", App: "Synthetic shop", PostTime: 2, Title: "Offer"}
	frame := Frame{Version: ProtocolVersion, Session: "synthetic", Sequence: 3, Type: "post", Record: record}
	data, _ := json.Marshal(frame)
	parsed, err := ParseFrame(data, "synthetic")
	if err != nil || parsed.Record.Package != record.Package || parsed.Record.DisplayPackage != record.DisplayPackage {
		t.Fatal("attribution replaced source identity")
	}
	state := NewState("synthetic-device", "Synthetic device", true)
	sink := &testSink{}
	for _, f := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "ready"}, parsed, {Sequence: 4, Type: "remove", Record: Record{Key: record.Key}}} {
		if err := state.Apply(f, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 1 || sink.shown[0].Package != record.Package || sink.shown[0].App != record.App {
		t.Fatal("display override changed ownership")
	}
	if len(sink.removed) != 1 || sink.removed[0] != ShortID("synthetic-device")+"/"+ShortID(record.Key) {
		t.Fatal("display attribution changed withdrawal key")
	}
	record.DisplayPackage = strings.Repeat("x", 513)
	frame.Record = record
	data, _ = json.Marshal(frame)
	if _, err := ParseFrame(data, "synthetic"); err != ErrProtocol {
		t.Fatal("unbounded display metadata accepted")
	}
}

func TestSingleFooterNormalizesAndEscapesMetadata(t *testing.T) {
	for _, c := range []struct {
		card Card
		want string
	}{
		{Card{Device: "Xiaomi Pad 8 Pro", App: "QQ", Connection: "无线 · 192.0.2.1:5555"}, "Xiaomi Pad 8 Pro · QQ | 无线 · 192.0.2.1:5555"},
		{Card{Device: "  Test\nDevice ", App: "A&B", Connection: "USB · SYNTHETIC"}, "Test Device · A&B | USB · SYNTHETIC"},
		{Card{App: "Only app"}, "Only app"},
		{Card{Connection: "USB · SYNTHETIC"}, "USB · SYNTHETIC"},
		{Card{}, ""},
	} {
		payload := toastDeviceFooter(c.card)
		if c.want == "" {
			if payload != "" {
				t.Fatal("empty metadata created a blank row")
			}
			continue
		}
		var parsed struct {
			Texts []string `xml:"subgroup>text"`
		}
		if err := xml.Unmarshal([]byte(payload), &parsed); err != nil {
			t.Fatal(err)
		}
		if len(parsed.Texts) != 1 || parsed.Texts[0] != c.want {
			t.Fatal("single footer metadata incorrect")
		}
	}
}
