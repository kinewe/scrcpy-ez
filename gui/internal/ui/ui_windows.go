//go:build windows

package ui

import (
	"context"
	"log"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"github.com/webview/webview_go"
	"golang.org/x/sys/windows"

	"scrcpy-ez/gui/internal/app"
	"scrcpy-ez/gui/internal/bridge"
)

// setWindowIcon gui53：把 exe 资源里嵌入的图标（ID=1，像素风）设为窗口图标，
// 任务栏 / Alt+Tab 即显示它（WebView2 默认不设窗口图标，任务栏显示通用蓝窗）。
// 说明：x/sys/windows 未导出 LoadIcon/SendMessage，用 LazyDLL 直调 user32。
func setWindowIcon(hwnd uintptr) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	procLoadIcon := user32.NewProc("LoadIconW")
	procSendMessage := user32.NewProc("SendMessageW")

	// MAKEINTRESOURCEW(1)：资源名=ID 1（go-winres 生成的 ICON 资源 id=1）
	hIcon, _, _ := procLoadIcon.Call(0, 1)
	if hIcon == 0 {
		return
	}
	// WM_SETICON == 0x0080；ICON_BIG=1（Alt+Tab/窗口大图标）、ICON_SMALL=0（标题栏/任务栏小图标）
	const wmSetIcon, iconBig, iconSmall = 0x0080, 1, 0
	procSendMessage.Call(hwnd, wmSetIcon, iconBig, hIcon)
	procSendMessage.Call(hwnd, wmSetIcon, iconSmall, hIcon)
}

// Run 创建 WebView 主窗口并阻塞运行主循环（必须由主 goroutine 调用）。
// 窗口关闭后返回；退出前会调用 app.Close 释放资源。
func Run(a *app.App, html string) error {
	w := webview.New(false)
	defer w.Destroy()

	w.SetTitle("scrcpy-ez")
	w.SetSize(520, 760, webview.HintNone)

	// gui53：主窗口图标（任务栏 / Alt+Tab 显示）——WebView2 默认不设置窗口图标，
	// 任务栏会显示通用蓝窗图标；这里把 exe 资源里嵌入的像素图标（ID=1）发给窗口。
	if hwnd := uintptr(w.Window()); hwnd != 0 {
		setWindowIcon(hwnd)
	}

	bindings := []struct {
		name string
		fn   interface{}
	}{
		// 读轮询接口（GetState 700ms / RefreshNow 手动刷新）不记日志：高频刷屏无来源价值。
		{"GetState", func() (interface{}, error) { return a.Snapshot(), nil }},
		{"RefreshNow", func() (interface{}, error) { a.RefreshNow(); return a.Snapshot(), nil }},
		{"ForceDiscover", func() (interface{}, error) { a.ForceDiscover(); return a.Snapshot(), nil }},
		// 全部变更类入口加 [js] 来源日志（StartCast 全链路来源追踪：90 秒自动重投
		// 现场——前端谁在调一目了然；下一行 [app] 层另有 caller 栈帧留痕）。
		{"StartCast", func(serial string) error {
			bridge.DebugLog("[js] StartCast serial=%q", serial)
			return a.StartCast(serial)
		}},
		// 轮 B 多会话：并行会话入口（新设备弹窗【开始投屏】）+ 会话级操作（全部带 serial）
		{"StartCastParallel", func(serial string) error {
			bridge.DebugLog("[js] StartCastParallel serial=%q", serial)
			return a.StartCastParallel(serial)
		}},
		{"StopCast", func(serial string) error {
			bridge.DebugLog("[js] StopCast serial=%q", serial)
			return a.StopCast(serial)
		}},
		{"RestartCast", func(serial string) error {
			bridge.DebugLog("[js] RestartCast serial=%q", serial)
			return a.RestartCast(serial)
		}},
		{"ResetCast", func() error {
			bridge.DebugLog("[js] ResetCast")
			a.ResetCast()
			return nil
		}},
		{"DismissNewDevice", func(serial string) error {
			bridge.DebugLog("[js] DismissNewDevice serial=%q", serial)
			a.DismissNewDevice(serial)
			return nil
		}},
		{"ForgetSession", func(serial string) error {
			bridge.DebugLog("[js] ForgetSession serial=%q", serial)
			a.ForgetSession(serial)
			return nil
		}},
		// 标签点击 → 对应投屏窗口浮前（不夺 ez 焦点；失败不阻塞前端标签切换）
		{"BringCastToFront", func(serial string) error {
			bridge.DebugLog("[js] BringCastToFront serial=%q", serial)
			return a.BringCastToFront(serial)
		}},
		{"GetProfile", func(serial string) (interface{}, error) {
			bridge.DebugLog("[js] GetProfile serial=%q", serial)
			return a.GetProfile(serial), nil
		}},
		{"SaveProfileAndRestart", func(serial, mode string, res, fps, bitrate int, custom bool) error {
			bridge.DebugLog("[js] SaveProfileAndRestart serial=%q mode=%s res=%d fps=%d bitrate=%d custom=%v",
				serial, mode, res, fps, bitrate, custom)
			return a.SaveProfileAndRestart(serial, mode, res, fps, bitrate, custom)
		}},
		// 设备参数管理页：仅保存 profiles.json（不重投）
		{"SaveProfile", func(serial, mode string, res, fps, bitrate int, custom bool) error {
			bridge.DebugLog("[js] SaveProfile serial=%q mode=%s res=%d fps=%d bitrate=%d custom=%v",
				serial, mode, res, fps, bitrate, custom)
			return a.SaveProfile(serial, mode, res, fps, bitrate, custom)
		}},
		// 无线调试配对向导（gui12）：受理即返回，过程/结果经快照 pairStatus 轮询。
		// devKey=待配对卡键（自动发现场景）；手动场景 ip/pairPort/connPort 直接给值。
		{"PairConnect", func(devKey, ip, pairPort, connPort, code string) error {
			bridge.DebugLog("[js] PairConnect devKey=%q ip=%q pairPort=%q connPort=%q", devKey, ip, pairPort, connPort)
			return a.PairConnect(devKey, ip, pairPort, connPort, code)
		}},
		{"PairReset", func() error {
			bridge.DebugLog("[js] PairReset")
			a.PairReset()
			return nil
		}},
		{"ExitApp", func() {
			bridge.DebugLog("[js] ExitApp")
			// gui53：先停全部投屏会话（杀树），再杀 adb server——顺序保证
			// kill-server 不断正在投屏的 bat（与 main 退出路径同口径）。
			a.Close()
			if adbPath, ok := a.ShouldKillServerOnExit(); ok && adbPath != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cleaner := exec.CommandContext(ctx, adbPath, "kill-server")
				cleaner.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
				_ = cleaner.Run()
			}
			go requestExit(w)
		}},
		// 设备卡顺序写回（gui45：后端持久化；前端两处触发、后端幂等）
		{"SetDeviceOrder", func(order []string) error {
			bridge.DebugLog("[js] SetDeviceOrder n=%d", len(order))
			return a.SetDeviceOrder(order)
		}},
		// gui51 设备管理：右键/批量删除、批量改名（改名以 JSON 字符串传入）。
		{"DeleteDevices", func(keys []string) error {
			bridge.DebugLog("[js] DeleteDevices n=%d", len(keys))
			return a.DeleteDevices(keys)
		}},
		{"RenameDevices", func(raw string) error {
			bridge.DebugLog("[js] RenameDevices raw=%q", raw)
			return a.RenameDevicesJSON(raw)
		}},
		// UiReady 由页面 load 事件调用：此时主循环已起，可取 HWND
		{"UiReady", func() {
			bridge.DebugLog("[js] UiReady")
			if hwnd := w.Window(); hwnd != nil {
				SetHWND(hwnd)
			}
		}},
	}
	for _, b := range bindings {
		if err := w.Bind(b.name, b.fn); err != nil {
			log.Printf("[ui] 绑定 %s 失败: %v", b.name, err)
		}
	}

	w.SetHtml(html)
	w.Run()
	return nil
}

func requestExit(w webview.WebView) {
	go w.Terminate()
}

// --- 主窗口 HWND 注册表（供 systray"显示主窗口"使用） ---

var (
	hwndOnce unsafe.Pointer
	hwndChan = make(chan unsafe.Pointer, 1)
)

// SetHWND 由 UiReady 回调设置（UI 线程上调用）。
// 同时把宿主窗口句柄注入 bridge（标签点击二段式浮前的④"ez 顶回"用）。
func SetHWND(p unsafe.Pointer) {
	select {
	case hwndChan <- p:
	default:
	}
	hwndOnce = p
	bridge.SetFrontEzHwnd(uintptr(p))
}

// WaitHWND 阻塞至页面就绪并返回主窗口句柄。
func WaitHWND() unsafe.Pointer {
	return <-hwndChan
}
