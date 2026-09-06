//go:build windows

package adb

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// syscallProcAttr 缩短 syscall.SysProcAttr 的引用（Windows 专属字段）。
type syscallProcAttr = syscall.SysProcAttr

// HideConsole 隐藏控制台程序子进程的窗口（Windows）。
// adb.exe 是 console 程序：GUI（无控制台）进程直接启动它时，Windows 会默认
// 新建一个控制台窗口造成闪烁/弹窗。CREATE_NO_WINDOW 禁止创建新控制台；
// HideWindow(SW_HIDE) 再兜底隐藏。
// 任何直接启动 adb.exe 的 exec 都必须调用（adb 包与 discovery 包共用）。
func HideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscallProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}
