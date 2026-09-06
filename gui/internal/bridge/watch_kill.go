package bridge

import "regexp"

// WatchTag 是 bat 的 USB 插线监测 watcher 进程命令行标记
// （与 投屏启动.bat 的 WATCH_TAG=SCRCPY_LAUNCH_USB_WATCH 一致）。
// 多会话隔离：每个会话的 BatRunner 生成唯一 tag（WatchTag_<nano>）经
// SCEZ_WATCH_TAG 注入 bat，bat 的 :stop_usb_watch 与切换 flag 文件都按
// tag 区分——Stop 时只补杀本会话的 watcher，不误杀其他会话。
// watcher 由 bat 用 `start /b` 独立拉起（不在 cmd 进程树内），
// bat 被强杀（taskkill /F）时其 :stop_usb_watch 无机会执行，
// 因此 GUI StopCast 杀树后必须按此标记补杀残留 watcher
// （否则残留 watcher 每 2s 检测到任意 USB → 写 flag 关 scrcpy →
// 造成"停止后仍在重启投屏"）。
const WatchTag = "SCRCPY_LAUNCH_USB_WATCH"

// stopWatchPSCmd 返回按指定 tag 精确清理残留 watcher 的 powershell 命令：
// 枚举全部 Win32_Process → CommandLine 命中 tag（正则转义，防元字符误匹配）
// → 逐进程强杀（排除自身 PID，与 bat 的 :stop_usb_watch 逻辑对应）。
// tag 为空时退回基标 WatchTag（未 Start 的会话/兼容旧 watcher）。
func stopWatchPSCmd(tag string) string {
	if tag == "" {
		tag = WatchTag
	}
	return "Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -match '" + regexp.QuoteMeta(tag) +
		"' -and $_.ProcessId -ne $PID } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }"
}
