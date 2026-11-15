//go:build windows

package castsupervisor

import "testing"

// 本次会话的虚拟相机 id 来自服务端启动日志；退出清理靠它等待相机真正注销。
func TestPerspectiveCameraIDFromLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"server event line", "[out:28] [server] INFO: Perspective camera events: id=v39_1038", "v39_1038"},
		{"plain line", "Perspective camera events: id=v57_1056", "v57_1056"},
		{"unrelated stream", "Perspective stream: id=0 output=1920x1080 format=1", ""},
		{"native alpha", "NATIVE_ALPHA_BOUND uid=10290 display=7 activity=BaseScanUI camera=TextureView", ""},
		{"malformed", "Perspective camera events: id=", ""},
		{"uppercase not an id", "Perspective camera events: id=V57_1056", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := perspectiveCameraIDFromLine(c.line); got != c.want {
			t.Fatalf("%s: perspectiveCameraIDFromLine(%q) = %q, want %q", c.name, c.line, got, c.want)
		}
	}
}

// 释放判定只能看"当前设备映射"，历史事件里的 CONNECT/REMOVE 行不能当作仍占用。
func TestVirtualCameraMapped(t *testing.T) {
	dump := `
Number of camera devices: 9
    Device 0 maps to "0"
    Device 1 maps to "1"
    Device 2 maps to "v3_1073"
== Camera service events log (most recent at top): ==
  11-11 21:56:44 : DISCONNECT device v3_1073 client for package com.tencent.mm (PID 30950)
  11-11 21:56:40 : CONNECT device v3_1073 client for package com.tencent.mm (PID 30950)
  11-11 21:56:30 : ADD device v3_1073, reason: (Device added)
`
	if !virtualCameraMapped(dump, "v3_1073") {
		t.Fatal("mapped virtual camera must be detected")
	}
	clean := `
Number of camera devices: 8
    Device 0 maps to "0"
    Device 1 maps to "1"
    Device 2 maps to "5"
== Camera service events log (most recent at top): ==
  11-11 21:56:44 : DISCONNECT device v3_1073 client for package com.tencent.mm (PID 30950)
  11-11 21:56:30 : ADD device v3_1073, reason: (Device added)
`
	if virtualCameraMapped(clean, "v3_1073") {
		t.Fatal("historical event lines must not count as a current mapping")
	}
	if virtualCameraMapped(clean, "") {
		t.Fatal("empty camera id must never match")
	}
}
