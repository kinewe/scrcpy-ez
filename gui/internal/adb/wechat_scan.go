package adb

import (
	"context"
	"strconv"
	"strings"
)

const (
	// 微信包名 / 动态快捷方式分发 action。
	// RC29 的「扫一扫直达」发送的正是微信动态快捷方式所用的分发 Intent：
	//   action = com.tencent.mm.ui.ShortCutDispatchAction
	//   extra  = LauncherUI.Shortcut.LaunchType = launch_type_scan_qrcode
	// 由 com.tencent.mm/.ui.ShortCutDispatchActivity 接收（见 server Device.startWeChatScanner）。
	// 因此「手机有没有这个快速调用扫一扫的接口」= 包管理器能否解析到该 action 的接收组件。
	weChatPackage            = "com.tencent.mm"
	weChatScanDispatchAction = "com.tencent.mm.ui.ShortCutDispatchAction"
	// 扫一扫直达运行在「应用窗口」虚拟屏上；scrcpy 服务端明确不支持 Android 10 之前
	// 的新建虚拟屏（真机日志 "New virtual display is not supported before Android 10"）。
	weChatScanMinSDK = 29
)

// hasWeChatScan 在投屏列表富化阶段静态探测设备是否具备微信「扫一扫」直达接口。
//
// 判定分两层（任一不满足 → false，前端隐藏「微信扫一扫」按钮）：
//  1. 系统版本：Android 10+（应用窗口虚拟屏的最低要求；Android 8 真机实测
//     server 直接报 "New virtual display is not supported before Android 10"）；
//  2. 接口存在：PackageManager.resolveActivity(action=com.tencent.mm.ui.ShortCutDispatchAction,
//     package=com.tencent.mm) 命中组件（未装微信 / 版本无 ShortCutDispatchActivity → 未命中）。
//
// 该查询不需要启动微信、不产生可见副作用；Android 8 及更早没有
// `cmd package resolve-activity` 的设备回退 `pm resolve-activity`。
func (m *Manager) hasWeChatScan(ctx context.Context, serial string) bool {
	if !m.weChatScanAndroidSupported(ctx, serial) {
		return false
	}
	out, err := m.run(ctx, "-s", serial, "shell", "cmd", "package", "resolve-activity", "--brief", "-a", weChatScanDispatchAction, "-p", weChatPackage)
	if err != nil || strings.TrimSpace(out) == "" {
		// 老 Android 没有 cmd package 子命令（或 shell 直接报错）时回退 pm。
		out, err = m.run(ctx, "-s", serial, "shell", "pm", "resolve-activity", "--brief", "-a", weChatScanDispatchAction, "-p", weChatPackage)
	}
	if err != nil {
		return false
	}
	return ParseWeChatScanResolution(out)
}

// weChatScanAndroidSupported 读 ro.build.version.sdk 判定是否 Android 10+。
func (m *Manager) weChatScanAndroidSupported(ctx context.Context, serial string) bool {
	out, err := m.Getprop(ctx, serial, "ro.build.version.sdk")
	if err != nil {
		return false
	}
	sdk, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return false
	}
	return sdk >= weChatScanMinSDK
}

// ParseWeChatScanResolution 解析 `(cmd|pm) package resolve-activity --brief` 的输出：
//   - 命中：输出含 "com.tencent.mm/<activity>"（如 com.tencent.mm/.ui.ShortCutDispatchActivity）；
//   - 未命中：输出为 "No activity found" 或空。
func ParseWeChatScanResolution(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "priority=") {
			continue
		}
		if strings.HasPrefix(line, "No activity found") {
			return false
		}
		if strings.Contains(line, weChatPackage+"/") {
			return true
		}
	}
	return false
}
