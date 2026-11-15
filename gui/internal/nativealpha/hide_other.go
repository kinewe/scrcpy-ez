//go:build !windows

package nativealpha

import "os/exec"

func hide(c *exec.Cmd) {}
