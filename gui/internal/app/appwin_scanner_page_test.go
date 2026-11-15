package app

import "testing"

// MIUI 的 dumpsys 输出按 Display #N 分段；只取目标 display 的
// topResumedActivity，不能误取物理屏或其他虚拟屏。
func TestScannerTopActivity(t *testing.T) {
	dump := `ACTIVITY MANAGER ACTIVITIES (dumpsys activity activities)
Display #0 (activities from top to bottom):
  * Task{aaa #1 type=standard A=10290:com.tencent.mm}
    topResumedActivity=ActivityRecord{111 u0 com.tencent.mm/.ui.LauncherUI t1}
Display #31 (activities from top to bottom):
  * Task{bbb #2 type=standard A=10290:com.tencent.mm}
    topResumedActivity=ActivityRecord{222 u0 com.tencent.mm/.plugin.webview.ui.tools.WebViewUI t2}
    ResumedActivity: ActivityRecord{222 u0 com.tencent.mm/.plugin.webview.ui.tools.WebViewUI t2}
Display #32 (activities from top to bottom):
    topResumedActivity=ActivityRecord{333 u0 com.tencent.mm/.plugin.scanner.ui.BaseScanUI t3}
`
	if got := scannerTopActivity(dump, 0); got != "com.tencent.mm/.ui.LauncherUI" {
		t.Fatalf("display 0 = %q", got)
	}
	if got := scannerTopActivity(dump, 31); got != "com.tencent.mm/.plugin.webview.ui.tools.WebViewUI" {
		t.Fatalf("display 31 = %q", got)
	}
	if got := scannerTopActivity(dump, 32); got != "com.tencent.mm/.plugin.scanner.ui.BaseScanUI" {
		t.Fatalf("display 32 = %q", got)
	}
	if got := scannerTopActivity(dump, 99); got != "" {
		t.Fatalf("unknown display must be empty, got %q", got)
	}
}
