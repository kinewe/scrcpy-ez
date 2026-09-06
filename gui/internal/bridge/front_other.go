//go:build !windows

package bridge

// BringCastToFront 非 Windows 空实现：投屏窗口浮前仅 Windows 形态
// （webview + scrcpy 均为 Win32 专属）；Linux 下单测可编译。
func BringCastToFront(serials []string) error { return nil }
