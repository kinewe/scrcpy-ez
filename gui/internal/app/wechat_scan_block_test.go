package app

import (
	"strings"
	"testing"
)

func TestBlockedWeChatScanActivity(t *testing.T) {
	blocked := `Display #0 (activities from top to bottom):
    topResumedActivity=ActivityRecord{111 u0 android/com.android.internal.app.BlockedAppStreamingActivity t3608}
    * Hist #0: ActivityRecord{222 u0 com.tencent.mm/.plugin.scanner.ui.BaseScanUI t3606}
Display #31 (activities from top to bottom):
    topResumedActivity=ActivityRecord{333 u0 com.tencent.mm/.plugin.scanner.ui.BaseScanUI t3453}
    * Hist #1: ActivityRecord{444 u0 android/com.android.internal.app.BlockedAppStreamingActivity t3452}
Display #32 (activities from top to bottom):
    topResumedActivity=ActivityRecord{555 u0 android/com.android.internal.app.BlockedAppStreamingActivity t3454}
`
	cases := []struct {
		name    string
		out     string
		display int
		want    bool
	}{
		{"blocked on current display", blocked, 32, true},
		{"ignore other displays and historical task", blocked, 31, false},
		{"still starting", blocked, 0, false},
		{"display removed", blocked, 30, false},
		{"empty", "", 31, false},
	}
	for _, c := range cases {
		if got := blockedWeChatScanActivity(c.out, c.display); got != c.want {
			t.Fatalf("%s: blockedWeChatScanActivity = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWeChatScanFrameLine(t *testing.T) {
	frame := "[out:118] INFO: NATIVE_ALPHA device: NATIVE_ALPHA_FRAME count=66 fps=32.2 readMs=18.7 captureMs=8.8 size=3196x2136"
	if !WeChatScanFrameLine(frame) {
		t.Fatal("NATIVE_ALPHA_FRAME must count as a successful scan capability proof")
	}
	cases := []string{
		"[out:53] [server] INFO: SCRCPY_EZ_APP_LAUNCH=started",
		"INFO: Perspective camera ready: 1920x1080 / 30fps maximum",
		"NATIVE_ALPHA device: NATIVE_ALPHA_BOUND uid=10290 display=8 activity=BaseScanUI",
		"",
	}
	for _, line := range cases {
		if WeChatScanFrameLine(line) {
			t.Fatalf("non-frame line must not be treated as success: %q", line)
		}
	}
}

func TestWeChatScanBlockedNoticeIsUserFacing(t *testing.T) {
	// 失败只提示重试（不隐藏入口），文案需说明原因与动作。
	for _, want := range []string{"系统", "扫一扫", "再试"} {
		if !strings.Contains(weChatScanBlockedNotice, want) {
			t.Fatalf("notice %q missing %q", weChatScanBlockedNotice, want)
		}
	}
	if strings.Contains(weChatScanBlockedNotice, "隐藏") {
		t.Fatalf("blocked notice must not promise hiding the entry anymore: %q", weChatScanBlockedNotice)
	}
}
