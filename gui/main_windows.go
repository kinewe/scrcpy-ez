//go:build windows

package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/getlantern/systray"

	"scrcpy-ez/gui/internal/app"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/ui"
)

// killAdbServerOnExit gui53：GUI 退出统一清理——投屏会话已由 a.Close() 全停，
// 这里把共享 adb server 一并杀掉（无残留）。顺序必须 kill 在 Close 之后：
// 杀 server 会断正在投屏的 bat 连接，先停会话再杀才安全。
func killAdbServerOnExit(a *app.App) {
	adbPath, ok := a.ShouldKillServerOnExit()
	if !ok || adbPath == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cleaner := exec.CommandContext(ctx, adbPath, "kill-server")
	cleaner.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := cleaner.Run(); err != nil {
		log.Printf("[main] 退出清理 kill-server 失败: %v", err)
	}
}

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

//go:embed web/index.html
var indexHTML string

//go:embed web/style.css
var styleCSS string

//go:embed web/app.js
var appJS string

//go:embed web/param_state.js
var paramStateJS string

//go:embed web/session_map.js
var sessionMapJS string

//go:embed web/drag_order.js
var dragOrderJS string

//go:embed web/pair_ui.js
var pairUIJS string

//go:embed assets/icon.ico
var iconICO []byte

const (
	version = "v2.0"
)

// appDir gui53 产品级修复：返回 exe 所在目录（发行包内 bat 与 GUI 同级解压）。
// 原 defaultBatDir 硬编码开发机路径会随包泄露个人信息且用户机器上不存在。
func appDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// dirWritable gui53：目录可写检测（用于档案落盘位置选择）。
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// migrateLegacyProfile gui53：老版本档案在 %APPDATA%\scrcpy-ez\，新逻辑迁到
// 软件目录后，首次启动把旧档案复制过去（软件目录优先；AppData 那份保留兜底）。
func migrateLegacyProfile(newPath string) {
	if newPath == "" || filepath.Dir(newPath) == "" {
		return
	}
	dir := filepath.Dir(newPath)
	if base, err := os.UserConfigDir(); err == nil {
		legacy := filepath.Join(base, "scrcpy-ez", "profiles.json")
		if _, err := os.Stat(legacy); err != nil {
			return // 没有旧档案，无需迁移
		}
		if _, err := os.Stat(newPath); err == nil {
			return // 软件目录已有档案，以它为准
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return
		}
		b, err := os.ReadFile(legacy)
		if err != nil {
			return
		}
		if err := os.WriteFile(newPath, b, 0644); err != nil {
			return
		}
		log.Printf("配置档案已迁移: %s -> %s", legacy, newPath)
	}
}

func main() {
	log.SetFlags(log.Ltime)

	// gui53：bat/adb/config 默认取 exe 同目录（发行包结构）；环境变量可显式覆盖。
	batPath := envOr("SCEZ_BAT_PATH", filepath.Join(appDir(), "投屏支持.bat"))
	adbPath := envOr("SCEZ_ADB_PATH", filepath.Join(filepath.Dir(batPath), "adb.exe"))
	// 无线记忆 config.txt：默认 bat 同目录（bat 的 CONFIG_FILE 常量），
	// 与 bat 行为一致：本目录优先，缺失回退 ..\..\config.txt（共享记忆）。
	configPath := envOr("SCEZ_CONFIG_PATH", filepath.Join(filepath.Dir(batPath), "config.txt"))

	// 崩溃日志目录：默认 exe 同目录（crash-YYYYMMDD.log），SCEZ_CRASH_DIR 可覆盖
	crashDir := envOr("SCEZ_CRASH_DIR", func() string {
		if exe, err := os.Executable(); err == nil {
			return filepath.Dir(exe)
		}
		return ""
	}())

	// 设备参数记忆：与 bat 的 config.txt 同理念——默认软件目录（整个文件夹搬家
	// 即带走记忆）；目录不可写时（Program Files 等受限位）回退 %APPDATA%\scrcpy-ez\；
	// SCEZ_PROFILES_PATH 可显式覆盖。
	profilesPath := envOr("SCEZ_PROFILES_PATH", func() string {
		dir := appDir()
		if dir != "" && dirWritable(dir) {
			return filepath.Join(dir, "profiles.json")
		}
		if base, err := os.UserConfigDir(); err == nil {
			return filepath.Join(base, "scrcpy-ez", "profiles.json")
		}
		return ""
	}())
	// 迁移：老版本（AppData 全局档案）首次在新逻辑下启动时，
	// 若软件目录还没有档案而 AppData 有 → 复制过去，之后以软件目录为准。
	migrateLegacyProfile(profilesPath)

	cfg := app.Config{BatPath: batPath, AdbPath: adbPath, ConfigPath: configPath,
		CrashDir: crashDir, ProfilesPath: profilesPath, Version: version}
	a := app.New(cfg)
	// 轮 B 多会话：工厂按 serial 创建独立 bat 实例；onLine/onExit 由 App 侧
	// 绑定到对应会话（每个会话一个 bridge 读 goroutine 对，N≤5 无压力）。
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (app.Runner, error) {
		return bridge.NewBatRunner(cfg.BatPath, cfg.AdbPath, onLine, onExit), nil
	})
	a.StartDevicePolling(context.Background())

	html, err := ui.BuildIndex(indexHTML, styleCSS, sessionMapJS+"\n"+paramStateJS+"\n"+dragOrderJS+"\n"+pairUIJS+"\n"+appJS)
	if err != nil {
		log.Fatalf("界面资源组装失败: %v", err)
	}

	// systray 在自己的 goroutine（Windows 下纯 syscall 实现）
	go systray.Run(func() {
		systray.SetIcon(iconICO)
		systray.SetTitle("scrcpy-ez")
		systray.SetTooltip("scrcpy-ez · 轻松易用不折腾")
		show := systray.AddMenuItem("显示主窗口", "打开 scrcpy-ez 窗口")
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出", "退出 scrcpy-ez")
		go func() {
			for {
				select {
				case <-show.ClickedCh:
					if hwnd := ui.WaitHWND(); hwnd != nil {
						procShowWindow.Call(uintptr(hwnd), 9 /* SW_RESTORE */)
						procSetForegroundWindow.Call(uintptr(hwnd))
					}
				case <-quit.ClickedCh:
					a.Close()          // 先停全部投屏会话（杀树）
					killAdbServerOnExit(a) // gui53：再杀 adb server（无残留）
					systray.Quit()
					return
				}
			}
		}()
	}, func() {
		// 托盘退出：收尾由 main 完成
	})

	// 主线程跑 WebView 主循环（阻塞至窗口关闭）
	if err := ui.Run(a, html); err != nil {
		log.Printf("[main] 界面退出: %v", err)
	}
	a.Close()              // 先停全部投屏会话（杀树）
	killAdbServerOnExit(a) // gui53：再杀 adb server（无残留）
	systray.Quit()
}
