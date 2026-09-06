package bridge

import (
	"sort"
	"strconv"
	"strings"
)

// stopSteps 是 BatRunner.Stop 的固定执行顺序（修复"双投屏"后契约）：
//  1. treekill —— 先 taskkill /F /T <cmd pid>（cmd 存活时整树杀：cmd+scrcpy+watcher，
//     scrcpy 不会因父进程先死而逃逸成孤儿）；
//  2. killcmd —— 再 Process.Kill 兜底（taskkill 失败/未完全退出时确保 cmd 主进程死）；
//  3. watchtag —— 按本会话 WATCH_TAG 补杀残留 watcher powershell；
//  4. residual —— 等 500ms 后复查本会话 scrcpy（父链=cmd pid 或命令行含本会话 serial）
//     仍存活则 taskkill /F 补杀（防 taskkill /T 128 失败后的孤儿窗口）；
//  5. adbserver —— 杀服门（仅当无其他活动会话才兜底 kill-server）。
//
// 顺序不可乱：先 Kill 后 taskkill 是历史 bug（cmd 已死 → taskkill 128 → scrcpy 逃逸）。
var stopSteps = []string{"treekill", "killcmd", "watchtag", "residual", "adbserver"}

// scrcpyProc 是残余 scrcpy 进程枚举结果（powershell 输出解析）。
type scrcpyProc struct {
	pid     int
	ppid    int
	cmdline string
}

// parseScrcpyProcs 解析 scrcpy 进程枚举输出（每行 "pid|ppid|cmdline"，由
// bat_windows.go 的 listScrcpyProcs 生成；纯函数便于跨平台单测）。
func parseScrcpyProcs(output string) []scrcpyProc {
	var out []scrcpyProc
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		pidS, rest, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		ppidS, cmdline, ok := strings.Cut(rest, "|")
		if !ok {
			continue
		}
		pid, err1 := strconv.Atoi(strings.TrimSpace(pidS))
		ppid, err2 := strconv.Atoi(strings.TrimSpace(ppidS))
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, scrcpyProc{pid: pid, ppid: ppid, cmdline: cmdline})
	}
	return out
}

// scrcpyCmdlineMatches 判定 scrcpy 命令行是否属于本会话：命令行含
// "--serial <serial>"（bat 启动 scrcpy 的固定参数形式，serial 后必须是空格或
// 行尾——防前缀误匹配，如本会话 "601c9f08" 不得命中别台 "--serial 601c9f081"）。
// serial 为本会话的候选目标（会话键 serial / SCEZ_SERIAL / SCEZ_ADDR 并集，
// 均属同一设备身份）。
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

// residualScrcpyCandidates 从枚举结果中挑出本会话的残余 scrcpy：
// 父进程链命中（ppid == 本会话 cmd pid，含"父死后未重挂"的孤儿）或命令行
// 命中本会话 serial。结果按 pid 升序（确定性）。其他会话的 scrcpy 一律不碰
// （多会话安全：只按会话记录判定，不按进程名全局误杀）。
func residualScrcpyCandidates(procs []scrcpyProc, cmdPid int, serials []string) []scrcpyProc {
	seen := map[int]bool{}
	var out []scrcpyProc
	for _, p := range procs {
		if p.pid <= 0 || seen[p.pid] {
			continue
		}
		if p.ppid == cmdPid || scrcpyCmdlineMatches(p.cmdline, serials) {
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
// （标签点击浮前用）：仅按命令行 --serial 匹配（父链信息在浮前场景不可靠——
// 会话可能已重启，父 pid 早已变化；且浮前只许命中本会话，多会话安全）。
// 结果按 pid 升序（确定性）。
func bringToFrontCandidates(procs []scrcpyProc, serials []string) []scrcpyProc {
	seen := map[int]bool{}
	var out []scrcpyProc
	for _, p := range procs {
		if p.pid <= 0 || seen[p.pid] {
			continue
		}
		if scrcpyCmdlineMatches(p.cmdline, serials) {
			seen[p.pid] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pid < out[j].pid })
	return out
}
