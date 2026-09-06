//go:build !windows

package adb

import "os/exec"

// HideConsole 在非 Windows 平台为空实现：控制台窗口是 Windows 特有行为。
// 真实实现见 adb_windows.go（CREATE_NO_WINDOW + HideWindow）。
func HideConsole(c *exec.Cmd) {}
