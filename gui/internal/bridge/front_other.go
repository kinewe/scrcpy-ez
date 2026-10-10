//go:build !windows

package bridge

// BringCastToFront 非 Windows 空实现：投屏窗口浮前仅 Windows 形态
// （webview + scrcpy 均为 Win32 专属）；Linux 下单测可编译。
func BringCastToFront(serials []string) error { return nil }

// BringAppWinToFront 非 Windows 空实现（v2.1.46：点击应用卡片浮前应用窗口）。
func BringAppWinToFront(serials []string, pkg string) error { return nil }

func BringClientToFront(pid int) error { return nil }
