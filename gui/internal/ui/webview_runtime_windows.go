//go:build windows

package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CheckWebViewRuntime diagnoses missing dependencies before webview_go creates a window.
func CheckWebViewRuntime(appDir string) error {
	loader, err := windows.LoadLibraryEx(filepath.Join(appDir, "WebView2Loader.dll"), 0,
		windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return fmt.Errorf("无法加载 WebView2Loader.dll，请重新完整解压 EZ 安装包\n\n%w", err)
	}
	defer windows.FreeLibrary(loader)
	getVersion, err := windows.GetProcAddress(loader, "GetAvailableCoreWebView2BrowserVersionString")
	if err != nil {
		return fmt.Errorf("WebView2Loader.dll 缺少版本检测入口，请重新完整解压 EZ 安装包\n\n%w", err)
	}
	var folder *uint16
	if path := os.Getenv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER"); path != "" {
		folder, err = windows.UTF16PtrFromString(path)
		if err != nil {
			return fmt.Errorf("WebView2 Runtime 路径无效：%w", err)
		}
	}
	var version *uint16
	hr, _, _ := syscall.SyscallN(getVersion, uintptr(unsafe.Pointer(folder)), uintptr(unsafe.Pointer(&version)))
	if version != nil {
		windows.NewLazySystemDLL("ole32.dll").NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(version)))
	}
	if int32(hr) < 0 || version == nil {
		installer := filepath.Join(appDir, "runtime", "MicrosoftEdgeWebView2RuntimeInstallerX64.exe")
		if info, err := os.Stat(installer); err == nil && !info.IsDir() {
			return fmt.Errorf("未找到可用的 Microsoft Edge WebView2 Runtime（0x%08X）\n请运行离线安装器，然后重新打开 EZ\n\n%s", uint32(hr), installer)
		}
		return fmt.Errorf("未找到可用的 Microsoft Edge WebView2 Runtime（0x%08X）\n请从微软官网下载并安装 x64 Evergreen Runtime，然后重新打开 EZ\n\nhttps://developer.microsoft.com/microsoft-edge/webview2/", uint32(hr))
	}
	return nil
}

func ShowStartupError(err error) {
	message, _ := windows.UTF16PtrFromString(err.Error())
	title := windows.StringToUTF16Ptr("scrcpy-ez 启动失败")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(
		0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
