//go:build windows && cgo

package notifications

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
)

func TestNativeDeviceDetailSwitchLeavesOTPCopyActive(t *testing.T) {
	if os.Getenv("SCEZ_NOTIFICATION_NATIVE_TEST") != "1" {
		t.Skip("opt-in isolated native notification identity")
	}
	sink := testNativeSink(t)
	source := &policyTestSource{streams: map[string]chan Record{"tablet": make(chan Record), "phone": make(chan Record)}}
	m := NewManager(context.Background(), source, func() (Sink, error) { return &testSink{}, nil })
	defer m.Close()
	var opened, copied, dismissed atomic.Int32
	m.SetOpener(func(context.Context, OpenRequest) error { opened.Add(1); return nil })
	targets := []Target{{Identity: "tablet", Serial: "wifi-tablet"}, {Identity: "phone", Serial: "usb-phone"}}
	m.Reconcile(targets, Options{})
	card := Card{Group: "device-details", Tag: "tablet", Title: "合成设备开关测试", Silent: true, Open: func(ctx context.Context) error {
		err := m.open(ctx, OpenRequest{Identity: "tablet", OwnerPackage: "com.example.mail"})
		dismissed.Add(1)
		return err
	}}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	old := sink.call(nativeRequest{op: "openToken", group: card.Group, tag: card.Tag}).token
	card.Tag, card.CopyCode = "otp", "123456"
	_ = sink.call(nativeRequest{op: "copyWriter", copier: func(string, uint32) error { copied.Add(1); return nil }})
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	copyToken := sink.call(nativeRequest{op: "copyToken", group: card.Group, tag: card.Tag}).token
	if old == "" || copyToken == "" || sink.call(nativeRequest{op: "openToken", group: card.Group, tag: card.Tag}).token != "" {
		t.Fatal("native action routing failed")
	}
	enabled := false
	m.Reconcile(targets, Options{Policies: map[string]Policy{"tablet": {Mode: ModeAll, OpenEnabled: &enabled}}})
	_ = sink.call(nativeRequest{op: "toastActivate", group: "device-details", tag: "tablet", value: "open:" + old})
	await(t, func() bool { return dismissed.Load() == 1 })
	if opened.Load() != 0 {
		t.Fatal("previously shown native card bypassed disabled rule")
	}
	_ = sink.call(nativeRequest{op: "toastActivate", group: card.Group, tag: card.Tag, value: "copy:" + copyToken})
	await(t, func() bool { return copied.Load() == 1 })
	card.Tag, card.CopyCode = "phone", ""
	card.Open = func(ctx context.Context) error {
		return m.open(ctx, OpenRequest{Identity: "phone", OwnerPackage: "com.example.mail"})
	}
	if err := sink.Show(card); err != nil {
		t.Fatal(err)
	}
	phone := sink.call(nativeRequest{op: "openToken", group: card.Group, tag: card.Tag}).token
	_ = sink.call(nativeRequest{op: "openActivate", value: phone})
	await(t, func() bool { return opened.Load() == 1 })
	if status := sink.call(nativeRequest{op: "contains", value: "未能在应用窗口打开详情"}); status.err != nil || status.count != 0 {
		t.Fatal("disabled click showed a failure notification")
	}
}
