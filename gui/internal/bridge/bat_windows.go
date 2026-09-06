//go:build windows

package bridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"syscall"

	"golang.org/x/sys/windows"
)

// syscallProcAttr 缩短 syscall.SysProcAttr 的引用（Windows 专属字段）。
type syscallProcAttr = syscall.SysProcAttr

// BatRunner 隐藏启动 投屏支持.bat 并桥接其 stdin/stdout/stderr。
// 窗口隐藏：CreationFlags=CREATE_NO_WINDOW|CREATE_NEW_PROCESS_GROUP + HideWindow(SW_HIDE)。
// 输出：stdout/stderr 管道逐行读取 → GBK 解码 → onLine 回调。
// 输入：仅当 bat 出现 choice/pause 提示（app 层判定）才写 stdin。
type BatRunner struct {
	batPath string
	adbPath string
	onLine  func(string)
	onExit  func(code int)

	// watchTag 是本会话 watcher 的唯一标记（Start 时生成，SCEZ_WATCH_TAG 注入 bat，
	// bat 切换 flag 与 :stop_usb_watch 都按它区分——多会话互不误杀/互不误读）。
	// serialCandidates 是本会话 scrcpy 的 --serial 匹配候选（会话键/SCEZ_SERIAL/
	// SCEZ_ADDR 并集，Start 时定格）——Stop 残余 scrcpy 兜底复查按它判定"本会话的"，
	// 不按进程名全局误杀其他会话投屏。
	// CanKillServer 保留兼容占位（gui46 起 Stop 不再兜底 kill-server，此回调不再使用；
	// 字段/SetCanKillServer 仍保留以兼容外部调用方，不影响新行为）。
	watchTag         string
	serialCandidates []string
	CanKillServer    func() bool

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex
	code   int
	exited bool
}

func NewBatRunner(batPath, adbPath string, onLine func(string), onExit func(int)) *BatRunner {
	return &BatRunner{batPath: batPath, adbPath: adbPath, onLine: onLine, onExit: onExit, code: -1}
}

// Start 启动隐藏的 cmd.exe /c bat 子进程。
// 参数按模式分离注入：Usb.Set=true 注入 SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB，
// Wifi.Set=true 注入 SCEZ_RES_WIFI/SCEZ_FPS_WIFI/SCEZ_BITRATE_WIFI；
// Serial/Addr 非空注入 SCEZ_SERIAL/SCEZ_ADDR（锁定目标设备），
// NoWatch=true 注入 SCEZ_NO_WATCH=1（已废弃：GUI 不再生成——watcher 会话化后
// 并行会话各自 watcher 互不干扰；bat 侧门保留仅用于手工/旧调用兼容）。
// 未自定义/未设置的字段不注入（bat 原逻辑）。
func (r *BatRunner) Start(serial string, params CastParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return errors.New("bat 已在运行")
	}
	// 本会话 scrcpy --serial 匹配候选（Stop 残余兜底复查用，Start 时定格）
	r.serialCandidates = serialCandidates(serial, params)

	DebugLog("[start] cmd.exe /c %s (usb set=%v res=%d fps=%d bitrate=%d | wifi set=%v res=%d fps=%d bitrate=%d | serial=%q addr=%q addr2=%q nowatch=%v)",
		r.batPath, params.Usb.Set, params.Usb.Res, params.Usb.FPS, params.Usb.Bitrate,
		params.Wifi.Set, params.Wifi.Res, params.Wifi.FPS, params.Wifi.Bitrate,
		params.Serial, params.Addr, params.Addr2, params.NoWatch)
	cmd := exec.Command("cmd.exe", "/c", r.batPath)
	var env []string
	// GUI 已接管 adb 生命周期（track 长连/设备档案/插线学习）：GUI 启动的一切
	// 投屏 bat 一律跳过 adb kill-server 重置与清理，避免插线设备被清掉、
	// 双长连被断。bat 独立运行时无此变量，保留原自愈逻辑。
	env = append(env, "SCEZ_NO_ADB_RESET=1")
	if params.Usb.Set {
		env = append(env,
			fmt.Sprintf("SCEZ_RES_USB=%d", params.Usb.Res),
			fmt.Sprintf("SCEZ_FPS_USB=%d", params.Usb.FPS),
			fmt.Sprintf("SCEZ_BITRATE_USB=%d", params.Usb.Bitrate))
	}
	if params.Wifi.Set {
		env = append(env,
			fmt.Sprintf("SCEZ_RES_WIFI=%d", params.Wifi.Res),
			fmt.Sprintf("SCEZ_FPS_WIFI=%d", params.Wifi.FPS),
			fmt.Sprintf("SCEZ_BITRATE_WIFI=%d", params.Wifi.Bitrate))
	}
	if params.Serial != "" {
		env = append(env, "SCEZ_SERIAL="+params.Serial)
	}
	if params.Addr != "" {
		env = append(env, "SCEZ_ADDR="+params.Addr)
	}
	if params.Addr2 != "" {
		env = append(env, "SCEZ_ADDR2="+params.Addr2)
	}
	if params.NoWatch {
		env = append(env, "SCEZ_NO_WATCH=1")
	}
	if params.Market != "" {
		env = append(env, "SCEZ_MARKET="+params.Market)
	}
	if params.Model != "" {
		env = append(env, "SCEZ_MODEL="+params.Model)
	}
	// 多会话 watcher 隔离：每会话唯一 tag（bat 未注入时用基标）
	r.watchTag = fmt.Sprintf("%s_%d", WatchTag, time.Now().UnixNano())
	env = append(env, "SCEZ_WATCH_TAG="+r.watchTag)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Dir = filepath.Dir(r.batPath)
	cmd.SysProcAttr = &syscallProcAttr{
		CmdLine:       `cmd.exe /c "` + r.batPath + `"`,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		DebugLog("[start] StdinPipe 失败: %v", err)
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		DebugLog("[start] StdoutPipe 失败: %v", err)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		DebugLog("[start] StderrPipe 失败: %v", err)
		return err
	}

	if err := cmd.Start(); err != nil {
		DebugLog("[start] 启动失败: %v", err)
		return err
	}
	DebugLog("[start] 进程已启动 pid=%d", cmd.Process.Pid)
	r.cmd = cmd
	r.stdin = stdin
	r.code = -1
	r.exited = false

	go r.readLoop(stdout, "out")
	go r.readLoop(stderr, "err")
	go r.waitLoop()
	return nil
}

func (r *BatRunner) readLoop(rc io.ReadCloser, tag string) {
	defer rc.Close()
	br := bufio.NewReader(rc)
	n := 0
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			n++
			// 原始 GBK 解码后的行（调试日志含行号+时间，供卡死取证）
			DebugLog("[%s:%d] %s", tag, n, strings.TrimRight(line, "\r\n"))
			r.onLine(DecodeGBK([]byte(line)))
		}
		if err != nil {
			DebugLog("[%s] 管道 EOF（err=%v，共 %d 行）", tag, err, n)
			return
		}
	}
}

func (r *BatRunner) waitLoop() {
	err := r.cmd.Wait()
	code := -1
	if err == nil {
		code = 0
	} else if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	DebugLog("[exit] bat 进程退出 code=%d (wait err=%v)", code, err)
	r.mu.Lock()
	r.code = code
	r.exited = true
	r.mu.Unlock()
	r.onExit(code)
}

// 只读展示架构（主人决策）：GUI 不向 bat 写 stdin。stdin 管道仍创建并保持打开
// （choice 有有效句柄时按 /t 超时 /d 默认自愈；死等菜单由用户在 GUI 点会话级按钮，
// 经 app.RestartCast/StopCast 杀树处理），stdin 写端仅 Stop 时关闭。

// Stop 按固定顺序停止会话（stopSteps 契约，修复"双投屏"）：
//
//	① 先 taskkill /F /T <cmd pid> —— cmd 存活时整树杀（cmd+scrcpy+watcher 全灭，
//	   scrcpy 不会因父进程先死而逃逸成孤儿——历史 bug 是先 Process.Kill 只杀 cmd、
//	   再 taskkill 时 cmd 已死 → exit 128 → scrcpy 逃逸 → 保存重投后双窗口）；
//	② 再 Process.Kill 兜底（taskkill 失败/未完全退出时确保 cmd 主进程死）；
//	③ 按 WATCH_TAG 补杀 bat `start /b` 独立拉起的 watcher powershell
//	   （bat 被强杀时 :stop_usb_watch 无机会执行——否则残留 watcher 会在停止后
//	   继续写 flag 关 scrcpy，导致"停止后仍在重启"）；
//	④ 等 500ms 后复查本会话 scrcpy：父链=本 cmd pid 或命令行含本会话 serial 的
//	   残余进程 → taskkill /F 补杀（防 taskkill /T 128 失败后的孤儿窗口；
//	   只按会话判定，绝不误杀其他会话的投屏）；
//	⑤ 杀服门：仅当无其他活动会话才兜底 kill-server（共享 adb server 多会话保护）。
func (r *BatRunner) Stop() error {
	r.mu.Lock()
	cmd := r.cmd
	if r.stdin != nil {
		_ = r.stdin.Close()
		r.stdin = nil
	}
	tag := r.watchTag
	serials := append([]string{}, r.serialCandidates...)
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return errors.New("bat 未在运行")
	}
	pid := cmd.Process.Pid
	DebugLog("[stop] 停止投屏 pid=%d（顺序：%s）", pid, strings.Join(stopSteps, "→"))

	// ① 整树杀优先：cmd 存活时 taskkill /T 把 cmd+scrcpy+watcher 一并清掉
	treeKiller := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	treeKiller.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := treeKiller.Run(); err != nil {
		DebugLog("[stop] taskkill /T 失败: %v（cmd 可能已退出，转 Kill+残余兜底）", err)
	}

	// ② Kill 兜底：无论 taskkill 是否成功，确保 cmd 主进程死亡（幂等；已死则报错忽略）
	if err := cmd.Process.Kill(); err != nil {
		DebugLog("[stop] Kill 兜底: %v（cmd 已退出=正常）", err)
	}

	// ③ 补杀独立 watcher（按本会话 tag 精确匹配，防串线死循环残留；
	// 未 Start 过/空 tag 退回基标兼容）
	DebugLog("[stop] 按 WATCH_TAG(%s) 清理残留 watcher", tag)
	watchKiller := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden",
		"-Command", stopWatchPSCmd(tag))
	watchKiller.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := watchKiller.Run(); err != nil {
		DebugLog("[stop] watcher 清理失败: %v", err)
	}

	// ④ 残余 scrcpy 兜底复查（500ms 后）：只杀本会话的（父链/命令行判定）
	time.Sleep(500 * time.Millisecond)
	r.killResidualScrcpy(pid, serials)

	// ⑤ gui46：不再兜底 kill-server（原继承 bat :adb_cleanup 语义，杀服会断全部
	// transport 引发离线卡窗口）——adb 清理由 GUI 退出时统一执行（见 App.ShouldKillServerOnExit）。
	// bat 独立运行场景由 bat 自己的 :adb_cleanup 负责（不变）。
	return nil
}

// listScrcpyProcs 枚举全部 scrcpy.exe 进程（pid/ppid/cmdline，powershell CIM）。
// 输出行格式 "pid|ppid|cmdline"（cmdline 含管道符的概率可忽略，SplitN 兜底）。
// 注意：powershell 是 console 程序，GUI（无控制台）直接启动会闪黑框——
// 必须带 SysProcAttr（CREATE_NO_WINDOW+HideWindow），-WindowStyle Hidden 单独不足以防弹窗。
// 命令带 6s 超时（实测正常 0.7-0.9s）：powershell 偶发卡死时不再让浮前点击无限挂起。
func listScrcpyProcs() ([]scrcpyProc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	psCmd := "Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'scrcpy.exe' } | " +
		"ForEach-Object { [string]$_.ProcessId + '|' + [string]$_.ParentProcessId + '|' + [string]$_.CommandLine }"
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", psCmd)
	cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseScrcpyProcs(string(out)), nil
}

// killResidualScrcpy 复查并补杀本会话残余 scrcpy（Stop ④）：
// 只按"父链==本 cmd pid 或 命令行含本会话 serial"判定本会话，多会话安全。
func (r *BatRunner) killResidualScrcpy(cmdPid int, serials []string) {
	procs, err := listScrcpyProcs()
	if err != nil {
		DebugLog("[stop] 残余 scrcpy 复查失败（跳过）: %v", err)
		return
	}
	for _, p := range residualScrcpyCandidates(procs, cmdPid, serials) {
		DebugLog("[stop] 兜底补杀本会话残余 scrcpy pid=%d (ppid=%d, cmdline=%.120s)", p.pid, p.ppid, p.cmdline)
		killer := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(p.pid))
		killer.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
		if err := killer.Run(); err != nil {
			DebugLog("[stop] 补杀 scrcpy pid=%d 失败: %v", p.pid, err)
		}
	}
}

// ExitCode 返回 bat 最终退出码；运行中返回 -1。
func (r *BatRunner) ExitCode() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.code
}

// SetCanKillServer 设置 Stop 兜底 kill-server 前的判定回调（App 注入）：
// 返回 true 才杀共享 adb server；false 跳过（其他会话仍在投屏）。
func (r *BatRunner) SetCanKillServer(f func() bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.CanKillServer = f
}
