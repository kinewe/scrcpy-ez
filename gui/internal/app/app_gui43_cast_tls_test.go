package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui43：投屏中「TLS加密」实时判定（会话实际 transport 事实） ---

func TestSelectSessionTransport(t *testing.T) {
	usb := adb.RawDevice{Serial: "H9RNW18604002288", State: "device", ConnType: "usb"}
	usbOff := adb.RawDevice{Serial: "H9RNW18604002288", State: "offline", ConnType: "usb"}
	main := adb.RawDevice{Serial: "192.168.31.183:40273", State: "device", ConnType: "wifi"}
	alt := adb.RawDevice{Serial: "192.168.31.183:5555", State: "device", ConnType: "wifi"}

	cases := []struct {
		name       string
		usbSerial  string
		addr       string
		addr2      string
		transports []adb.RawDevice
		want       string
	}{
		{
			name:       "USB 在线优先（即使主地址也在线）",
			usbSerial:  "H9RNW18604002288",
			addr:       "192.168.31.183:40273",
			transports: []adb.RawDevice{main, usb},
			want:       "H9RNW18604002288",
		},
		{
			name:       "主地址在线",
			addr:       "192.168.31.183:40273",
			transports: []adb.RawDevice{main},
			want:       "192.168.31.183:40273",
		},
		{
			name:       "主地址不在线 + 备地址在线（降级）",
			addr:       "192.168.31.183:40273",
			addr2:      "192.168.31.183:5555",
			transports: []adb.RawDevice{alt},
			want:       "192.168.31.183:5555",
		},
		{
			name:       "USB offline 不认",
			usbSerial:  "H9RNW18604002288",
			addr:       "192.168.31.183:40273",
			transports: []adb.RawDevice{usbOff},
			want:       "",
		},
		{
			name:       "全不在线",
			addr:       "192.168.31.183:40273",
			addr2:      "192.168.31.183:5555",
			transports: nil,
			want:       "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := selectSessionTransport(c.usbSerial, c.addr, c.addr2, c.transports); got != c.want {
				t.Fatalf("selectSessionTransport = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTlsActiveOf(t *testing.T) {
	known := func(s string) bool { return s == "192.168.31.183:43105" }
	cases := []struct {
		name   string
		serial string
		known  func(string) bool
		want   bool
	}{
		{name: "5555 false", serial: "192.168.31.183:5555", known: known, want: false},
		{name: "40273 true", serial: "192.168.31.183:40273", known: known, want: true},
		{name: "mDNS known true", serial: "192.168.31.183:43105", known: known, want: true},
		{name: "USB false", serial: "H9RNW18604002288", known: known, want: false},
		{name: "空串 false", serial: "", known: known, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tlsActiveOf(c.serial, c.known); got != c.want {
				t.Fatalf("tlsActiveOf(%q) = %v, want %v", c.serial, got, c.want)
			}
		})
	}
}
