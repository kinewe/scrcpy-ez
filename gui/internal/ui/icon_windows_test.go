//go:build windows

package ui

import (
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Run against the release exe so this checks its real embedded icon resource.
// The test creates only an invisible, temporary native window.
func TestEZWindowIconFromBuiltExecutable(t *testing.T) {
	path := os.Getenv("SCEZ_TEST_ICON_EXE")
	if path == "" {
		t.Skip("set SCEZ_TEST_ICON_EXE to the freshly built GUI")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	module, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_AS_DATAFILE|windows.LOAD_LIBRARY_AS_IMAGE_RESOURCE)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.FreeLibrary(module)
	user32 := windows.NewLazySystemDLL("user32.dll")
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, callErr := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 10, 10, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatal(callErr)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	if !setWindowIconFromModule(hwnd, uintptr(module)) {
		t.Fatal("failed to load icon ID 1 from the actual GUI executable")
	}
	for _, size := range []uintptr{0, 1} {
		icon, _, _ := user32.NewProc("SendMessageW").Call(hwnd, 0x007f, size, 0) // WM_GETICON
		if icon == 0 {
			t.Fatalf("window icon missing for size=%d", size)
		}
	}
}
