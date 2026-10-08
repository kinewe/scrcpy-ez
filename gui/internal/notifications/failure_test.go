package notifications

import (
	"context"
	"strings"
	"testing"
)

func TestFixedSourceReasonsAndIdentityCorrection(t *testing.T) {
	for _, code := range []string{"identity", "busy", "user", "permission", "unsupported", "startup"} {
		data := []byte(`{"v":2,"session":"test","seq":1,"type":"error","code":"` + code + `"}`)
		frame, err := ParseFrame(data, "test")
		if err != nil || frame.Code != code || sourceStatus(&sourceFailure{code: code, err: ErrUnavailable}) == "" {
			t.Fatal("fixed reason unavailable")
		}
	}
	if _, err := ParseFrame([]byte(`{"v":2,"session":"test","seq":1,"type":"error","code":"private 123456"}`), "test"); err == nil || strings.Contains(err.Error(), "123456") {
		t.Fatal("foreign reason leaked")
	}
	source := &unavailableSource{err: &sourceFailure{code: "identity", err: ErrUnavailable}}
	m := NewManager(context.Background(), source, func() (Sink, error) { return &testSink{}, nil })
	defer m.Close()
	target := Target{Identity: "tablet", Serial: "192.0.2.1:5555", DeviceSerial: "192.0.2.1:5555"}
	m.Reconcile([]Target{target}, Options{})
	await(t, func() bool { return len(m.Status()) == 1 && m.Status()[0].State == "unavailable" })
	if !strings.Contains(m.Status()[0].Text, "身份") {
		t.Fatal("identity failure hidden by a generic message")
	}
	target.DeviceSerial = "PHYSICAL_TABLET"
	m.Reconcile([]Target{target}, Options{})
	await(t, func() bool { return source.attempts.Load() == 2 })
}
