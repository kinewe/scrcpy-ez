package bridge

import (
	"sort"
	"strings"
)

// scrcpyProc is a typed process inventory row. Creation time guards PID reuse.
type scrcpyProc struct {
	pid           int
	ppid          int
	cmdline       string
	createdMicros int64
}

// scrcpyCmdlineMatches 判定 scrcpy 命令行是否属于本会话：命令行含
// "--serial <serial>"（bat 启动 scrcpy 的固定参数形式，serial 后必须是空格或
// 行尾——防前缀误匹配，如本会话 "TEST0001" 不得命中别台 "--serial TEST00011"）。
// serial 为本会话的候选目标（会话键 serial / SCEZ_SERIAL / SCEZ_ADDR 并集，
// 均属同一设备身份）。
//
// ⚠ 仅凭 serial 不足以判定"本会话"：同一设备可并行多路会话（主投屏 + 应用窗口
// 虚拟屏），其 scrcpy --serial 完全相同。需要判定归属时必须用 scrcpySessionMatch
// （serial + 形态特征），本函数只作 serial 粗筛。
func scrcpyCmdlineMatches(cmdline string, serials []string) bool {
	for _, s := range serials {
		if s == "" {
			continue
		}
		tok := "--serial " + s
		if strings.HasSuffix(cmdline, tok) {
			return true
		}
		if strings.Contains(cmdline, tok+" ") {
			return true
		}
	}
	return false
}

// scrcpySessionMatch 判定 scrcpy 命令行是否属于本会话（serial + 会话形态特征）：
//
//	serial 命中 且
//	  主投屏（vdSize==""）  → 命令行不含 --new-display；
//	  虚拟屏（vdSize!=""）  → 命令行含 --new-display，且 startApp 非空时
//	                          --start-app=<startApp> 精确命中（同设备多应用窗口互不误伤）。
//
// 为什么必须带形态特征（v2.1.30 修复）：主投屏 Stop 曾按纯 serial 匹配残余 scrcpy
// → 命中同设备的虚拟屏（--serial 相同）→ 被当成"本会话残余"优雅关窗 → 停止主投屏
// 把虚拟屏一起杀了（穿透）。主投屏无 --new-display、虚拟屏有，是两者的本质分界。
func scrcpySessionMatch(cmdline string, serials []string, vdSize, startApp string) bool {
	if !scrcpyCmdlineMatches(cmdline, serials) {
		return false
	}
	hasVD := strings.Contains(cmdline, "--new-display")
	if vdSize == "" {
		return !hasVD // 本会话=主投屏：虚拟屏 scrcpy 不算本会话
	}
	if !hasVD {
		return false // 本会话=虚拟屏：主投屏 scrcpy 不算本会话
	}
	if startApp != "" && !scrcpyAppMatches(cmdline, startApp) {
		return false // 同设备其他应用窗口（不同 --start-app）不算本会话
	}
	return true
}

// The explicit restart prefix is consumed after the first attempt. Both forms
// identify the same session, while package-name prefixes must never match.
func scrcpyAppMatches(cmdline, pkg string) bool {
	pkg = strings.TrimPrefix(pkg, "+")
	if pkg == "" {
		return false
	}
	args := strings.Fields(cmdline)
	for i, arg := range args {
		arg = strings.Trim(arg, `"`)
		value, ok := strings.CutPrefix(arg, "--start-app=")
		if !ok && arg == "--start-app" && i+1 < len(args) {
			value, ok = strings.Trim(args[i+1], `"`), true
		}
		if ok && strings.TrimPrefix(value, "+") == pkg {
			return true
		}
	}
	return false
}

// residualScrcpyCandidates 从枚举结果中挑出本会话的残余 scrcpy：
// 父进程链命中（ppid == 本会话 cmd pid，含"父死后未重挂"的孤儿——ppid 数值
// 在父进程死亡后不变，判据依然成立）或命令行命中（serial + 会话形态特征，
// 见 scrcpySessionMatch）。结果按 pid 升序（确定性）。
// 其他会话的 scrcpy 一律不碰（多会话安全：主投屏与虚拟屏互不误杀）。
func residualScrcpyCandidates(procs []scrcpyProc, cmdPid int, serials []string, vdSize, startApp string) []scrcpyProc {
	seen := map[int]bool{}
	var out []scrcpyProc
	for _, p := range procs {
		if p.pid <= 0 || seen[p.pid] {
			continue
		}
		if p.ppid == cmdPid || scrcpySessionMatch(p.cmdline, serials, vdSize, startApp) {
			seen[p.pid] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pid < out[j].pid })
	return out
}

// serialCandidates 生成本会话的 scrcpy --serial 匹配候选（去重、去空）：
// 会话键 serial + SCEZ_SERIAL + SCEZ_ADDR——bat 实际传给 scrcpy 的是其中
// 在线/命中的那个；三个都属同一设备身份，匹配任一都不会误伤别的会话。
func serialCandidates(serial string, params CastParams) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range []string{serial, params.Serial, params.Addr} {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// bringToFrontCandidates 从 scrcpy 进程枚举中挑出本会话的投屏窗口候选 pid
// （标签点击浮前用）：按命令行 --serial 匹配 + **排除虚拟屏**（v2.1.30）。
//
// 调用场景只有主投屏标签点击（虚拟屏/蓝灯标签不走 BringCastToFront）——同设备
// 并存虚拟屏时，纯 serial 匹配会把虚拟屏窗口一起提到前面（误浮前）；虚拟屏
// scrcpy 命令行必含 --new-display，据此排除。父链不参与（会话可能已重启，
// 父 pid 早已变化；且浮前只许命中本会话，多会话安全）。结果按 pid 升序。
func bringToFrontCandidates(procs []scrcpyProc, serials []string) []scrcpyProc {
	seen := map[int]bool{}
	var out []scrcpyProc
	for _, p := range procs {
		if p.pid <= 0 || seen[p.pid] {
			continue
		}
		if !scrcpyCmdlineMatches(p.cmdline, serials) {
			continue
		}
		if strings.Contains(p.cmdline, "--new-display") {
			continue // 虚拟屏（应用窗口）：不是主投屏标签的目标
		}
		seen[p.pid] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pid < out[j].pid })
	return out
}

// bringAppWinCandidates 从 scrcpy 进程枚举中挑出"指定应用窗口"的进程
// （点击应用卡片浮前用，v2.1.46）：serial 命中 + 含 --new-display（虚拟屏）+
// --start-app=<pkg> 或 +<pkg> 精确命中（同设备多个应用窗口互不误伤；不吃主投屏——它无
// --new-display）。结果按 pid 升序（确定性）。
func bringAppWinCandidates(procs []scrcpyProc, serials []string, pkg string) []scrcpyProc {
	seen := map[int]bool{}
	var out []scrcpyProc
	for _, p := range procs {
		if p.pid <= 0 || seen[p.pid] {
			continue
		}
		if !scrcpyCmdlineMatches(p.cmdline, serials) {
			continue
		}
		if !strings.Contains(p.cmdline, "--new-display") {
			continue // 主投屏：不是应用卡片的目标
		}
		if pkg != "" && !scrcpyAppMatches(p.cmdline, pkg) {
			continue // 同设备其他应用窗口
		}
		seen[p.pid] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pid < out[j].pid })
	return out
}
