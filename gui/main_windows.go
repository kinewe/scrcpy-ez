//go:build windows

package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/getlantern/systray"

	"scrcpy-ez/gui/internal/app"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/castsupervisor"
	"scrcpy-ez/gui/internal/rootrepair"
	"scrcpy-ez/gui/internal/ui"
)

// gui54 退出体验优化：「假关」+ 并行清理 + 15s 全局兜底。
//
// 现状问题：托盘「退出」/窗口关闭时窗口一直挂到全部会话串行 Stop 跑完（实测双会话 ~9.4s），
// 用户盯着等。改为：
//  1. 触发即「假关」——主窗口与托盘图标立即消失（ui.Quit + systray.Quit），无任何提示；
//  2. 后台并行清理——App.BeginClose() 对每个会话一个 goroutine（会话内 ⓪~⑤ 不变），
//     总耗时≈最慢的那个会话；
//  3. 全部停完后按原语义执行一次 kill adb server，然后真正退出进程；
//  4. 全局兜底：自触发起 15s 未清理完 → 记日志强制退出（进程任何情况下不悬挂）。
//     无会话时 BeginClose 立即完成 → 立即退出，不等兜底。
const exitCleanupWatchdog = 15 * time.Second

var (
	shutdownOnce      sync.Once
	shutdownStartedAt time.Time
	shutdownDone      <-chan struct{}
)

// beginShutdown 触发一次全局退出（幂等）。调用方立即返回，「假关」先发生。
func beginShutdown(a *app.App) {
	shutdownOnce.Do(func() {
		shutdownStartedAt = time.Now()
		bridge.DebugLog("[main] 退出触发：假关（主窗口 + 托盘图标立即消失），后台并行清理开始")
		ui.Quit()      // 主窗口立即消失（forceExit → 窗口过程直接销毁，见 ui_windows.go）
		systray.Quit() // 托盘图标立即消失
		shutdownDone = a.BeginClose()
	})
}

// waitShutdownAndExit 由 main goroutine 调用：等待并行清理完成（或 15s 兜底），
// 再按既有语义 kill adb server，最后真正退出进程。
func waitShutdownAndExit(a *app.App) {
	beginShutdown(a) // 幂等；同时保证 shutdownDone/shutdownStartedAt 已建立（sync.Once 同步）
	left := exitCleanupWatchdog - time.Since(shutdownStartedAt)
	if left < 0 {
		left = 0
	}
	select {
	case <-shutdownDone:
		bridge.DebugLog("[main] 全部会话清理完成（自触发 %dms），准备退出进程",
			time.Since(shutdownStartedAt).Milliseconds())
	case <-time.After(left):
		bridge.DebugLog("[main] 退出清理超时 %v（全局兜底），强制退出进程", exitCleanupWatchdog)
		bridge.FlushDebugLog() // 异步日志：退出前刷盘，保证最后几行证据落盘
		os.Exit(0)
	}
	killAdbServerOnExit(a) // 全部会话停完后执行一次（语义不变）
	bridge.DebugLog("[main] adb server 清理完成，进程退出（自触发 %dms）",
		time.Since(shutdownStartedAt).Milliseconds())
	bridge.FlushDebugLog() // 同上：退出前把"完成"证据刷盘
	os.Exit(0)
}

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

//go:embed web/appsetting_state.js
var appsettingStateJS string

//go:embed web/session_map.js
var sessionMapJS string

//go:embed web/drag_order.js
var dragOrderJS string

//go:embed web/pair_ui.js
var pairUIJS string

//go:embed web/pinyin_pro.js
var pinyinProJS string

//go:embed web/app_icon_cache.js
var appIconCacheJS string

//go:embed web/app_search.js
var appSearchJS string

//go:embed web/appwin_text.js
var appWinTextJS string

//go:embed web/appwin_bar.js
var appWinBarJS string

//go:embed web/update_ui.js
var updateUIJS string

//go:embed assets/icon.ico
var iconICO []byte

//go:embed assets/github-mark-white.png
var githubMarkPNG []byte

//go:embed assets/gitee.svg
var giteeMarkSVG []byte

const (
	version = rootrepair.Version
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
		legacy := filepath.Join(base, "yinmo-root-2.2.2", "profiles.json")
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
	if castsupervisor.RunIfRequested(os.Args[1:]) {
		return
	}
	// Experimental root packages have no stable updater helper or recovery path.
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
			return filepath.Join(base, "yinmo-root-2.2.2", "profiles.json")
		}
		return ""
	}())
	// 迁移：老版本（AppData 全局档案）首次在新逻辑下启动时，
	// 若软件目录还没有档案而 AppData 有 → 复制过去，之后以软件目录为准。
	// No import from an official installation into this experimental package.

	// 全局设置（设置面板两个开关）：独立 settings.json，不混入设备档案 profiles.json。
	// 位置与 profiles.json 同目录（软件目录优先、受限位回退 %APPDATA%\scrcpy-ez\），
	// 整个文件夹搬家即带走设置；SCEZ_SETTINGS_PATH 可显式覆盖。
	settingsPath := envOr("SCEZ_SETTINGS_PATH", func() string {
		if profilesPath != "" {
			return filepath.Join(filepath.Dir(profilesPath), "settings.json")
		}
		if base, err := os.UserConfigDir(); err == nil {
			return filepath.Join(base, "yinmo-root-2.2.2", "settings.json")
		}
		return ""
	}())

	cfg := app.Config{BatPath: batPath, AdbPath: adbPath, ConfigPath: configPath,
		CrashDir: crashDir, ProfilesPath: profilesPath, SettingsPath: settingsPath,
		Version: version}
	a := app.New(cfg)
	// 轮 B 多会话：工厂按 serial 创建独立 bat 实例；onLine/onExit 由 App 侧
	// 绑定到对应会话（每个会话一个 bridge 读 goroutine 对，N≤5 无压力）。
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (app.Runner, error) {
		return bridge.NewBatRunner(cfg.BatPath, cfg.AdbPath, onLine, onExit), nil
	})
	a.StartDevicePolling(context.Background())
	// v2.1.77：5037 抢庄——启动检测归属，旧版 adb server 坐庄时夺回（异步不阻塞首屏）。
	a.StartServerOwnershipCheck()

	// v2.1.18：pinyin_pro（拼音库，全局 pinyinPro）+ app_search（搜索过滤模块）
	// 在业务脚本前内联（app.js 依赖 SCEZAppSearch）。
	pageTemplate := strings.NewReplacer("/*__GITHUB_ICON__*/", base64.StdEncoding.EncodeToString(githubMarkPNG), "/*__GITEE_ICON__*/", base64.StdEncoding.EncodeToString(giteeMarkSVG)).Replace(indexHTML)
	html, err := ui.BuildIndex(pageTemplate, styleCSS, updateUIJS+"\n"+pinyinProJS+"\n"+appSearchJS+"\n"+appIconCacheJS+"\n"+appWinTextJS+"\n"+appWinBarJS+"\n"+sessionMapJS+"\n"+paramStateJS+"\n"+appsettingStateJS+"\n"+dragOrderJS+"\n"+pairUIJS+"\n"+appJS)
	if err != nil {
		log.Fatalf("界面资源组装失败: %v", err)
	}

	// systray 在自己的 goroutine（Windows 下纯 syscall 实现）。
	//
	// LockOSThread 不可省略：systray 的窗口 + GetMessage 消息循环必须始终位于同一个
	// OS 线程。Go runtime 会把未锁定线程的 goroutine 在系统调用返回后调度到别的线程，
	// 而窗口消息队列仍留在原线程 —— 一旦发生迁移，窗口消息再也无人处理，托盘图标
	// 从此对所有点击（左键/右键/拖到任务栏）无响应，且无法自愈，永久假死
	// （IsHungAppWindow=True、SendMessageTimeout 超时）。
	// 实测（tray_test bad/good 对照）：不加锁在负载下约 51s 必假死；加锁后同等压力
	// 75s+ 稳定，且运行期线程号始终不变。
	go func() {
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(iconICO)
			systray.SetTitle("音墨 root 尝试版")
			systray.SetTooltip("音墨 " + version + " · 未实机验证")
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
						// gui54：「假关」——窗口与托盘图标立即消失，清理转后台并行执行，
						// 等待与真正退出进程由 main goroutine 的 waitShutdownAndExit 收口。
						// （设置开关 B 打开时窗口关闭只是隐藏、主循环仍在跑：ui.Quit 置
						//   forceExit 后仍能保证真正终止界面主循环。）
						beginShutdown(a)
						return
					}
				}
			}()
		}, func() {
			// 托盘退出：收尾由 main 完成
		})
	}()

	// 主线程跑 WebView 主循环（阻塞至窗口关闭）
	if err := ui.Run(a, html); err != nil {
		log.Printf("[main] 界面退出: %v", err)
	}
	// 走到这里说明窗口已被销毁（用户关窗=退出，或托盘退出/ExitApp 的 ui.Quit）：
	// 先「假关」收掉托盘图标，再等后台并行清理完成、kill adb server、真正退出进程。
	waitShutdownAndExit(a)
}
