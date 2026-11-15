//go:build windows

package nativealpha

import (
	"os/exec"
	"syscall"
)

func hide(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
