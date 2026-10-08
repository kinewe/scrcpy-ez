//go:build windows && cgo

package notifications

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeCopySuccessConfirmationLifecycle(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("isolated native confirmation test")
	}
	sink := testNativeSink(t)
	var writes atomic.Int32
	var busy atomic.Bool
	_ = sink.call(nativeRequest{op: "copyWriter", copier: func(_ string, _ uint32) error {
		if busy.Load() {
			return errors.New("synthetic writer busy")
		}
		writes.Add(1)
		return nil
	}})
	card := Card{Group: "feedback-test", Tag: "original", App: "Synthetic", Body: "OTP: 850329, valid for 5 minutes", CopyCode: "850329", CopyIssuedAt: time.Now(), Silent: true}
	show := func() string {
		t.Helper()
		if err := sink.Show(card); err != nil {
			t.Fatal(err)
		}
		token := sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
		if len(token) != 32 {
			t.Fatal("copy token missing")
		}
		return token
	}
	contains := func(text string) int {
		t.Helper()
		r := sink.call(nativeRequest{op: "contains", value: text})
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.count
	}
	activate := func(token string) {
		t.Helper()
		if r := sink.call(nativeRequest{op: "activate", value: token}); r.err != nil {
			t.Fatal(r.err)
		}
	}
	feedback := func() int {
		t.Helper()
		return sink.call(nativeRequest{op: "feedbackState"}).count
	}
	token := show()
	busy.Store(true)
	activate(token)
	// Activation and request queues are independent; wait briefly for failed
	// delivery, then verify that it did not produce a success confirmation.
	time.Sleep(100 * time.Millisecond)
	if writes.Load() != 0 || contains("✓ 复制成功") != 0 || feedback() != 0 {
		t.Fatal("failed copy reported success")
	}
	busy.Store(false)
	activate(token)
	await(t, func() bool { return writes.Load() == 1 && contains("✓ 复制成功") == 1 })
	// Visible, topmost tool window, NOACTIVATE, and not the foreground window.
	if feedback() != 15 {
		t.Fatal("copy feedback missing or stealing focus")
	}
	await(t, func() bool { return contains("850329") == 0 })
	if contains(`duration="short"`) != 1 || contains(`launch="copy:`) != 0 || contains(`<actions>`) != 0 {
		t.Fatal("confirmation inherited original action or long duration")
	}
	activate(token)
	time.Sleep(100 * time.Millisecond)
	if writes.Load() != 1 {
		t.Fatal("repeated activation copied again")
	}
	if r := sink.call(nativeRequest{op: "count"}); r.err != nil || r.count != 1 {
		t.Fatal("duplicate confirmation history")
	}
	// A different device's cleanup must not hide this confirmation.
	if err := sink.Clear("other-device"); err != nil || feedback() != 15 {
		t.Fatal("unrelated cleanup hid copy feedback")
	}
	// The visible confirmation times out independently of long OTP toasts;
	// the quiet native history entry remains available.
	deadline := time.Now().Add(4 * time.Second)
	for feedback() != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if feedback() != 0 || contains("✓ 复制成功") != 1 {
		t.Fatal("copy feedback lifetime or history incorrect")
	}
	// A second successful message replaces the same device's confirmation.
	card.Tag = "second"
	token = show()
	activate(token)
	await(t, func() bool { return writes.Load() == 2 && contains("850329") == 0 })
	if feedback() != 15 {
		t.Fatal("second copy did not show feedback")
	}
	if r := sink.call(nativeRequest{op: "count"}); r.err != nil || r.count != 1 {
		t.Fatal("confirmations accumulated")
	}
	// Clear/withdraw and expiration must not report a later success.
	if err := sink.Clear(card.Group); err != nil {
		t.Fatal(err)
	}
	if feedback() != 0 {
		t.Fatal("device clear left visible feedback")
	}
	token = show()
	if err := sink.Remove(card.Group, card.Tag); err != nil {
		t.Fatal(err)
	}
	activate(token)
	time.Sleep(100 * time.Millisecond)
	if writes.Load() != 2 || contains("✓ 复制成功") != 0 || feedback() != 0 {
		t.Fatal("withdrawn action reported success")
	}
	card.CopyCode = "039571"
	card.Body = "OTP: 039571"
	card.CopyFallback = 100 * time.Millisecond
	card.CopyIssuedAt = time.Now()
	token = show()
	time.Sleep(200 * time.Millisecond)
	activate(token)
	time.Sleep(100 * time.Millisecond)
	if writes.Load() != 2 || contains("✓ 复制成功") != 0 || feedback() != 0 {
		t.Fatal("expired action reported success")
	}
	if r := sink.call(nativeRequest{op: "failures"}); r.err != nil || r.count != 0 {
		t.Fatal("native confirmation failure")
	}
}
