package bridge

import (
	"strings"
	"testing"
)

// StopCast 补杀 watcher 的命令核对（fake 测试）：
//   - 必须按会话 tag（SCRCPY_LAUNCH_USB_WATCH[_<nano>]，与 bat 一致）精确匹配命令行；
//   - 枚举全部 Win32_Process、排除自身 PID、逐进程强杀；
//   - 结构 = bat :stop_usb_watch 的 GUI 侧对应（bat 被强杀时其清理无机会执行）。
func TestStopWatchPSCmd(t *testing.T) {
	cmd := stopWatchPSCmd("")
	if !strings.Contains(cmd, WatchTag) {
		t.Fatalf("watcher 清理命令缺少 WATCH_TAG: %s", cmd)
	}
	for _, want := range []string{
		"Get-CimInstance Win32_Process",
		"CommandLine -match",
		"-ne $PID",
		"Stop-Process",
		"-Force",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("watcher 清理命令缺少 %q: %s", want, cmd)
		}
	}
	if WatchTag != "SCRCPY_LAUNCH_USB_WATCH" {
		t.Fatalf("WATCH_TAG 与 bat 不一致: %s", WatchTag)
	}
	// 会话 tag：按完整 tag 匹配（不是基标前缀误杀其他会话）
	sess := stopWatchPSCmd("SCRCPY_LAUNCH_USB_WATCH_1756600000000000000")
	if !strings.Contains(sess, "SCRCPY_LAUNCH_USB_WATCH_1756600000000000000") {
		t.Fatalf("会话 tag 应进入清理命令: %s", sess)
	}
	// 正则元字符转义（tag 若含 . 不得按任意字符匹配）
	quoted := stopWatchPSCmd("TAG.1")
	if !strings.Contains(quoted, `TAG\.1`) {
		t.Fatalf("tag 元字符应转义: %s", quoted)
	}
}
