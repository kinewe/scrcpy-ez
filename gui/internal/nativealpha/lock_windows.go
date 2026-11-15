//go:build windows

package nativealpha

import (
	"crypto/sha256"
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
	"runtime"
	"strings"
)

func Lock(path string) (func(), error) {
	runtime.LockOSThread() // Win32 mutex ownership is per OS thread.
	full, err := filepath.Abs(path)
	if err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(full))))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Local\SCEZ_ALPHA_PROFILE_%x`, sum[:16]))
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		runtime.UnlockOSThread()
		return nil, err
	}
	result, err := windows.WaitForSingleObject(h, 1500)
	if err != nil || (result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED) {
		windows.CloseHandle(h)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("profile lock unavailable: %d %v", result, err)
	}
	return func() { windows.ReleaseMutex(h); windows.CloseHandle(h); runtime.UnlockOSThread() }, nil
}
