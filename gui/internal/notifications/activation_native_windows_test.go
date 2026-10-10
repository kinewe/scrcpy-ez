//go:build windows && cgo

package notifications

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeToastAndCOMActivationExecuteOnlyOnce(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in isolated native notification identity")
	}
	sink := testNativeSink(t)
	var opened, copied atomic.Int32
	card := Card{Group: "event-routing", Tag: "detail", Title: "合成事件测试", Silent: true, Open: func(context.Context) error { opened.Add(1); return nil }}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	token := sink.call(nativeRequest{op: "openToken", group: card.Group, tag: card.Tag}).token
	if token == "" {
		t.Fatal("open action missing")
	}
	for _, request := range []nativeRequest{
		{op: "toastActivate", group: card.Group, tag: card.Tag, value: "open:" + token},
		{op: "openActivate", value: token},
		{op: "toastActivate", group: card.Group, tag: card.Tag, value: "open:" + token},
	} {
		if err := sink.call(request).err; err != nil {
			t.Fatal(err)
		}
	}
	await(t, func() bool { return opened.Load() == 1 && len(sink.activations) == 0 })
	_ = sink.call(nativeRequest{op: "count"})
	if status := sink.call(nativeRequest{op: "contains", value: "通知已失效"}); status.err != nil || status.count != 0 {
		t.Fatal("duplicate callbacks reported false expiration")
	}
	card.Tag = "second-detail"
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	second := sink.call(nativeRequest{op: "openToken", group: card.Group, tag: card.Tag}).token
	_ = sink.call(nativeRequest{op: "openActivate", value: second})
	_ = sink.call(nativeRequest{op: "toastActivate", group: card.Group, tag: card.Tag, value: "open:" + second})
	await(t, func() bool { return opened.Load() == 2 && len(sink.activations) == 0 })
	_ = sink.call(nativeRequest{op: "count"})
	card.Tag, card.CopyCode = "otp", "123456"
	_ = sink.call(nativeRequest{op: "copyWriter", copier: func(code string, _ uint32) error {
		if code != "123456" {
			t.Error("wrong synthetic code")
		}
		copied.Add(1)
		return nil
	}})
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	copyToken := sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
	if copyToken == "" || sink.call(nativeRequest{op: "openToken", group: card.Group, tag: card.Tag}).token != "" {
		t.Fatal("OTP acquired detail action")
	}
	_ = sink.call(nativeRequest{op: "toastActivate", group: card.Group, tag: card.Tag, value: "copy:" + copyToken})
	_ = sink.call(nativeRequest{op: "activate", value: copyToken})
	await(t, func() bool { return copied.Load() == 1 && len(sink.activations) == 0 })
	_ = sink.call(nativeRequest{op: "count"})
	if opened.Load() != 2 || copied.Load() != 1 {
		t.Fatal("duplicate activation or OTP detail launch")
	}
}

func TestActivationDedupOnlySuppressesRecentlyConsumedTokens(t *testing.T) {
	var d activationDeduplicator
	now := time.Now()
	first, second := strings.Repeat("a", 32), strings.Repeat("b", 32)
	if d.Contains(first, now) {
		t.Fatal("unconsumed click discarded")
	}
	d.Record(first, now)
	if !d.Contains(first, now) || d.Contains(second, now) {
		t.Fatal("different notifications shared activation")
	}
	if d.Contains(first, now.Add(31*time.Second)) {
		t.Fatal("dedup suppressed later failure feedback")
	}
}
