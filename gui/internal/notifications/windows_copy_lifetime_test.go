//go:build windows && cgo

package notifications

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNativeCopyDeadlineAndBoundedBurst(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in native notification test")
	}
	sink := testNativeSink(t)
	copied := make(chan string, 4)
	_ = sink.call(nativeRequest{op: "copyWriter", copier: func(code string, _ uint32) error { copied <- code; return nil }})
	card := Card{Group: "copy-lifetime", Tag: "expired", Device: "合成平板", Body: "验证码850329，30秒内有效。", CopyCode: "850329", CopyIssuedAt: time.Now().Add(-time.Minute), Silent: true}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	if sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token != "" {
		t.Fatal("expired message issued a native action")
	}
	card.Tag = "long-fallback"
	card.Body = "验证码850329。"
	card.CopyIssuedAt = time.Now().Add(-6 * time.Hour)
	card.CopyFallback = 24 * time.Hour
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	long := sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
	if long == "" {
		t.Fatal("long fallback rejected a still eligible action")
	}
	_ = sink.call(nativeRequest{op: "activate", value: long})
	select {
	case code := <-copied:
		if code != card.CopyCode {
			t.Fatal("wrong code")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long action did not activate")
	}
	card.Body = "验证码850329。"
	card.CopyIssuedAt = time.Now()
	first, last := "", ""
	for i := 0; i < 160; i++ {
		card.Tag = fmt.Sprint(i)
		if err := sink.Show(card); err != nil {
			t.Fatal(err)
		}
		token := sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
		if token == "" {
			t.Fatal("native burst lost newest copy action")
		}
		if i == 0 {
			first = token
		}
		last = token
	}
	_ = sink.call(nativeRequest{op: "activate", value: first})
	select {
	case <-copied:
		t.Fatal("evicted native action wrote again")
	case <-time.After(100 * time.Millisecond):
	}
	_ = sink.call(nativeRequest{op: "activate", value: last})
	select {
	case <-copied:
	case <-time.After(3 * time.Second):
		t.Fatal("latest native burst action did not activate")
	}
	if failure := sink.call(nativeRequest{op: "failures"}); failure.count != 0 || failure.err != nil {
		t.Fatalf("async native failure: count=%d error=%v", failure.count, failure.err)
	}
	_ = sink.Clear(card.Group)
}
