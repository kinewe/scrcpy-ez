package app

import (
	"context"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/bridge"
)

const (
	// Android 用这个系统 Activity 顶替"被应用串流策略拒绝启动"的应用界面。
	blockedAppStreamingActivity = "android/com.android.internal.app.BlockedAppStreamingActivity"
	// 系统拦截只提示重试，不据此隐藏入口——实测同一设备换时机/清掉平板工作台
	// 自由窗口任务后可成功，能力本身是存在的。
	weChatScanBlockedNotice = "系统这次拦截了扫一扫窗口（多与平板工作台/微信自由窗口有关），请关窗后再试一次"
	// 成功实证标记：设备端原生 Alpha 出帧 = 扫码预览链路真的有画面
	// （只"启动成功"不够——黑屏也曾经出现过）。
	weChatScanFrameMarker = "NATIVE_ALPHA device: NATIVE_ALPHA_FRAME"
)

// 只检查本次扫一扫的虚拟屏前台，忽略主屏及其它任务留下的拦截页。
func blockedWeChatScanActivity(dumpsys string, display int) bool {
	if display <= 0 {
		return false
	}
	return scannerTopActivity(dumpsys, display) == blockedAppStreamingActivity
}

// WeChatScanFrameLine 判断应用窗口输出行是否为"扫码预览真的出画面"的成功实证。
// 出现即视为该设备具备直接打开微信扫一扫的能力：入档并跟随设备。
func WeChatScanFrameLine(line string) bool {
	return strings.Contains(line, weChatScanFrameMarker)
}

// watchWeChatScanOutcome 观察一次微信扫一扫窗口的运行结果（约 30 秒）：
//
//   - 看到设备端 NATIVE_ALPHA 出帧 → 成功实证，按 identity 写入设备档案
//     （之后即使偶发系统拦截也不再隐藏入口）；
//   - 早期发现系统用 BlockedAppStreamingActivity 顶替扫码页 → 收起占位窗口并
//     提示"再试一次"，不判定设备无能力、不隐藏入口；
//   - 窗口被用户关闭/退出 → 直接结束观察。
//
// 轮询只读 dumpsys / 内存日志，无副作用。
func (a *App) watchWeChatScanOutcome(st *appWinState) {
	key := appWinKey(st.serial, st.pkg)
	seen := 0
	deadline := time.Now().Add(30 * time.Second)
	var watchedDisplay int
	var displayDeadline, nextProbe time.Time
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		a.mu.RLock()
		current := a.appWins[key]
		if current != st || st.closing || st.restarting {
			a.mu.RUnlock()
			return
		}
		if len(st.log) < seen {
			seen = 0
		}
		var fresh []string
		if len(st.log) > seen {
			fresh = append([]string(nil), st.log[seen:]...)
			seen = len(st.log)
		}
		identity, serial, display := st.identity, st.serial, st.displayID
		a.mu.RUnlock()

		for _, line := range fresh {
			if !WeChatScanFrameLine(line) {
				continue
			}
			added, err := a.profiles.MarkWeChatScanVerified(identity)
			if err != nil {
				bridge.DebugLog("[appwin] 微信扫一扫成功实证入档失败 identity=%s: %v", identity, err)
				return
			}
			if added {
				bridge.DebugLog("[appwin] 微信扫一扫成功实证入档 identity=%s serial=%s（以后保持显示入口）", identity, serial)
			}
			return
		}
		if display <= 0 {
			watchedDisplay = 0
			continue
		}
		// 从本次虚拟屏创建起检查 6 秒，给相机初始化和上传留出时间。
		now := time.Now()
		if display != watchedDisplay {
			watchedDisplay = display
			displayDeadline = now.Add(6 * time.Second)
			nextProbe = now
		}
		if now.Before(nextProbe) || now.After(displayDeadline) {
			continue
		}
		nextProbe = now.Add(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		out, err := a.adb.Shell(ctx, serial, "dumpsys", "activity", "activities")
		cancel()
		if err != nil || !blockedWeChatScanActivity(out, display) {
			continue
		}
		a.mu.Lock()
		if a.appWins[key] != st || st.closing || st.restarting || st.displayID != display {
			a.mu.Unlock()
			continue
		}
		a.weChatScanNotice = weChatScanBlockedNotice
		a.mu.Unlock()
		bridge.DebugLog("[appwin] 微信扫一扫被系统拦截 identity=%s serial=%s display=%d（应用串流策略），提示重试不隐藏入口", identity, serial, display)
		_ = a.StopAppWin(serial, st.pkg)
		return
	}
}
