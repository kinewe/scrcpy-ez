package notifications

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMaskedSmsUsesExplicitCodeWithoutRewritingText(t *testing.T) {
	record := Record{Key: "sms", Package: "com.android.mms", PostTime: 2, Title: "新短信", Body: "【合成服务】验证码******，5分钟内有效。", VerificationCode: "039571"}
	for _, preview := range []bool{true, false} {
		state, sink := NewState("test", "合成手机", preview), &testSink{}
		for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: record}} {
			if err := state.Apply(frame, sink); err != nil {
				t.Fatal(err)
			}
		}
		if len(sink.shown) != 1 {
			t.Fatal("masked SMS was not delivered")
		}
		card := sink.shown[0]
		if preview {
			if card.CopyCode != record.VerificationCode || card.Body != record.Body || !strings.Contains(ToastXML(card), "验证码：039571") {
				t.Fatal("explicit SMS code did not reach existing OTP presentation")
			}
		} else if card.CopyCode != "" || card.Body == record.Body || strings.Contains(ToastXML(card), record.VerificationCode) {
			t.Fatal("preview-off setting exposed the SMS code")
		}
		if state.records[record.Key].value.VerificationCode != "" || state.records[record.Key].value.Body != "" {
			t.Fatal("notification cache retained text/code after delivery")
		}
	}
}

func TestVerificationMetadataBoundaryAndConflicts(t *testing.T) {
	for _, record := range []Record{
		{Package: "com.synthetic.app", DisplayPackage: "com.android.mms", VerificationCode: "039571"},
		{Package: "com.android.mms", VerificationCode: "123"},
		{Package: "com.android.mms", VerificationCode: "123456789"},
		{Package: "com.android.mms", VerificationCode: "039571\n"},
		{Package: "com.android.mms", VerificationCode: "０３９５７１"},
		{Package: "com.android.mms", Body: "验证码850329", VerificationCode: "039571"},
	} {
		if recordOTP(record) != "" {
			t.Fatal("invalid owner, invalid metadata or conflicting code offered a copy action")
		}
	}
	if recordOTP(Record{Package: "com.synthetic.app", Body: "验证码850329", VerificationCode: "039571"}) != "850329" {
		t.Fatal("ordinary text extraction changed")
	}
	if recordOTP(Record{Package: "com.android.mms", Body: "验证码039571", VerificationCode: "039571"}) != "039571" {
		t.Fatal("matching code was rejected")
	}
}

func TestMaskedSmsSameKeyAndTextCanReceiveAnotherCode(t *testing.T) {
	state, sink := NewState("test", "合成手机", true), &testSink{}
	record := Record{Key: "reused", Package: "com.android.mms", PostTime: 2, Body: "验证码******，5分钟内有效。", VerificationCode: "039571"}
	for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: record}} {
		if err := state.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	record.VerificationCode, record.PostTime = "850329", 3
	if err := state.Apply(Frame{Sequence: 4, Type: "post", Record: record}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.shown) != 2 || sink.shown[1].CopyCode != "850329" || sink.shown[1].Silent || sink.shown[0].Tag != sink.shown[1].Tag {
		t.Fatal("changed code was deduplicated or did not replace its old copy action")
	}
}

func TestVerificationMetadataIsOptionalInProtocol(t *testing.T) {
	session := strings.Repeat("a", 32)
	for _, code := range []string{"", "039571", "invalid", strings.Repeat("1", 100)} {
		frame := Frame{Version: ProtocolVersion, Session: session, Sequence: 1, Type: "post", Record: Record{Key: "sms", Package: "com.android.mms", PostTime: 1, VerificationCode: code}}
		data, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseFrame(data, session)
		want := ""
		if code == "039571" {
			want = code
		}
		if err != nil || parsed.Record.VerificationCode != want {
			t.Fatal("optional field caused a protocol failure or retained invalid metadata")
		}
	}
}

func TestOTPOnlyPolicyRecognizesMaskedSmsMetadata(t *testing.T) {
	state, sink := NewState("test", "合成手机", true), &testSink{}
	if err := state.SetPolicy(Policy{Mode: ModeOTP}, true, sink); err != nil {
		t.Fatal(err)
	}
	record := Record{Key: "sms", Package: "com.android.mms", PostTime: 2, Body: "验证码******，5分钟内有效。", VerificationCode: "039571"}
	for _, frame := range []Frame{{Sequence: 1, Type: "hello", Cutoff: 1}, {Sequence: 2, Type: "ready"}, {Sequence: 3, Type: "post", Record: record}} {
		if err := state.Apply(frame, sink); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.shown) != 1 || sink.shown[0].CopyCode != record.VerificationCode {
		t.Fatal("OTP-only mode filtered out the masked SMS code")
	}
	record.Package, record.Key, record.PostTime = "com.synthetic.app", "spoofed", 3
	if err := state.Apply(Frame{Sequence: 4, Type: "post", Record: record}, sink); err != nil || len(sink.shown) != 1 {
		t.Fatal("OTP-only mode trusted another application's metadata")
	}
	record.Package, record.Key, record.PostTime = "com.android.mms", "conflict", 4
	record.Body = "验证码850329"
	if err := state.Apply(Frame{Sequence: 5, Type: "post", Record: record}, sink); err != nil || len(sink.shown) != 1 {
		t.Fatal("OTP-only mode accepted conflicting code fields")
	}
}
