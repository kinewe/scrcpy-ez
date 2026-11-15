//go:build windows

package bridge

import (
	"os"

	"golang.org/x/sys/windows"
)

func openBridgeLogFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Allow log viewers but only one writer to this file.
	handle, err := windows.CreateFile(name, windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
