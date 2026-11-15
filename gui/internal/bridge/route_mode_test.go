package bridge

import "testing"

func TestClassifySupervisorRouteMode(t *testing.T) {
	for _, tc := range []struct{ line, mode string }{
		{"[自动切换] 启动 wifi：192.0.2.159:5555", "wifi"},
		{"[自动切换] 切换至 usb：PHONE", "usb"},
		{"[自动切换] 切换至 wifi：adb-PHONE._adb-tls-connect._tcp", "wifi"},
		{"[自动切换] 会话已启用 ADB 事件驱动（USB 优先，同设备无线恢复）", ""},
		{"[学习] 保存无线地址 192.0.2.159:5555", ""},
		{"[server] INFO: [自动切换] 启动 usb：PHONE", ""},
		{"其他消息 [自动切换] 启动 usb：PHONE", ""},
	} {
		if ev := ClassifyLine(tc.line); ev.Mode != tc.mode {
			t.Fatalf("%q mode=%q, want=%q", tc.line, ev.Mode, tc.mode)
		}
	}
}
