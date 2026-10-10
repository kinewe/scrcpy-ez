//go:build windows && cgo

package notifications

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNativeDeferredRemovalInvalidatesActionsImmediately(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in isolated native notification history")
	}
	for _, action := range []string{"open", "copy"} {
		t.Run(action, func(t *testing.T) {
			sink := testNativeSink(t)
			card := Card{Package: "com.tencent.mm", Group: "synthetic-grace", Tag: action, App: "synthetic grace " + action, Title: "synthetic grace " + action, Silent: true}
			if action == "open" {
				card.Open = func(context.Context) error { t.Error("withdrawn detail launched"); return nil }
			} else {
				card.CopyCode = "601934"
			}
			if err := sink.Show(card); err != nil {
				t.Fatal(err)
			}
			if token := sink.call(nativeRequest{op: action + "Token", group: card.Group, tag: card.Tag}).token; token == "" {
				t.Fatal("test action not available")
			}
			// OTP cards intentionally replace the title with a code summary;
			// the synthetic app footer is present in both presentations.
			await(t, func() bool { return sink.call(nativeRequest{op: "contains", value: card.App}).count == 1 })
			if err := sink.Remove(card.Group, card.Tag); err != nil {
				t.Fatal(err)
			}
			if token := sink.call(nativeRequest{op: action + "Token", group: card.Group, tag: card.Tag}).token; token != "" {
				t.Fatal("withdrawal retained a usable action during visual grace")
			}
			if sink.call(nativeRequest{op: "contains", value: card.App}).count != 1 {
				t.Fatal("visual withdrawal was not coalesced")
			}
			deadline := time.Now().Add(wechatRemovalGrace + time.Second)
			for sink.call(nativeRequest{op: "contains", value: card.App}).count != 0 {
				if time.Now().After(deadline) {
					t.Fatal("unreplaced notification remained beyond the grace period")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestNativeReplacementSurvivesPendingRemovalAndClearIsImmediate(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in isolated native notification history")
	}
	sink := testNativeSink(t)
	card := Card{Package: "com.tencent.mm", Group: "synthetic-replacement", Tag: "same-chat", Title: "synthetic first message", Silent: true}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	if err := sink.Remove(card.Group, card.Tag); err != nil {
		t.Fatal(err)
	}
	card.Title = "synthetic replacement message"
	card.Open = func(context.Context) error { return nil }
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	// Exercise the native event loop beyond the old timer's deadline: a unit
	// test of the map alone would miss a timer that incorrectly stayed armed.
	time.Sleep(wechatRemovalGrace + 100*time.Millisecond)
	if result := sink.call(nativeRequest{op: "contains", value: card.Title}); result.err != nil || result.count != 1 {
		t.Fatal("old withdrawal removed the new message")
	}
	if token := sink.call(nativeRequest{op: "openToken", group: card.Group, tag: card.Tag}).token; token == "" {
		t.Fatal("replacement action missing")
	}
	if err := sink.Remove(card.Group, card.Tag); err != nil {
		t.Fatal(err)
	}
	if err := sink.Clear(card.Group); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return sink.call(nativeRequest{op: "contains", value: card.Title}).count == 0 })
}

func TestNativeFailedReplacementStillExpires(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in isolated native notification history")
	}
	sink := testNativeSink(t)
	card := Card{Package: "com.tencent.mm", Group: "synthetic-failed", Tag: "same-chat", Title: "synthetic failed replacement", Silent: true}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	if err := sink.Remove(card.Group, card.Tag); err != nil {
		t.Fatal(err)
	}
	if err := sink.call(nativeRequest{op: "show", card: card, group: card.Group, tag: card.Tag, xml: "invalid XML"}).err; err == nil {
		t.Fatal("malformed replacement unexpectedly succeeded")
	}
	deadline := time.Now().Add(wechatRemovalGrace + time.Second)
	for sink.call(nativeRequest{op: "contains", value: card.Title}).count != 0 {
		if time.Now().After(deadline) {
			t.Fatal("failed Show canceled a required removal")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
