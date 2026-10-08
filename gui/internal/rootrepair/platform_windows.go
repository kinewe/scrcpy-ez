//go:build windows

package rootrepair

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
)

func hide(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// Windows mutex ownership is tied to the OS thread. Across GUI, standalone BAT,
// USB and WiFi, the same physical identity serializes all repair attempts.
func PrepareLocked(ctx context.Context, o Options) (Report, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	digest := sha256.Sum256([]byte(o.Identity))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Local\SCEZ_ROOT_REPAIR_%x`, digest))
	if err != nil {
		return Report{}, err
	}
	m, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return Report{}, err
	}
	defer windows.CloseHandle(m)
	for {
		if ctx.Err() != nil {
			return Report{}, ctx.Err()
		}
		result, e := windows.WaitForSingleObject(m, 100)
		if e != nil {
			return Report{}, e
		}
		if result == windows.WAIT_OBJECT_0 || result == windows.WAIT_ABANDONED {
			break
		}
		if result != uint32(windows.WAIT_TIMEOUT) {
			return Report{}, fmt.Errorf("root mutex wait: %d", result)
		}
	}
	defer windows.ReleaseMutex(m)
	return Prepare(ctx, o)
}
