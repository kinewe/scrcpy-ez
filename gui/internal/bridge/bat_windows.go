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
	"scrcpy-ez/gui/internal/sessioncontrol"
)

// syscallProcAttr 缩短 syscall.SysProcAttr 的引用（Windows 专属字段）。
type syscallProcAttr = syscall.SysProcAttr

// BatRunner 隐藏启动 投屏支持.bat 并桥接其 stdin/stdout/stderr。
// 窗口隐藏：CreationFlags=CREATE_NO_WINDOW|CREATE_NEW_PROCESS_GROUP + HideWindow(SW_HIDE)。
// 输出：stdout/stderr 管道逐行读取 → GBK 解码 → onLine 回调。
// 输入：仅当 bat 出现 choice/pause 提示（app 层判定）才写 stdin。
type BatRunner struct {
	batPath string
	onLine  func(string)
	onExit  func(code int)

	// Unique event namespace; Stop never matches clients by serial or model.
	watchTag      string
	CanKillServer func() bool // API compatibility; server ownership is external.
	skipNotifWait bool        // API compatibility; native cleanup is always awaited.

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex
	code     int
	exited   bool
	waitDone chan struct{}
}

func NewBatRunner(batPath, adbPath string, onLine func(string), onExit func(int)) *BatRunner {
	return &BatRunner{batPath: batPath, onLine: onLine, onExit: onExit, code: -1}
}

// Start 启动隐藏的 cmd.exe /c bat 子进程。
// 参数按模式分离注入：Usb.Set=true 注入 SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB，
// Wifi.Set=true 注入 SCEZ_RES_WIFI/SCEZ_FPS_WIFI/SCEZ_BITRATE_WIFI；
// Serial/Addr 非空注入 SCEZ_SERIAL/SCEZ_ADDR（锁定目标设备），
// NoWatch=true 注入 SCEZ_NO_WATCH=1（已废弃：GUI 不再生成——watcher 会话化后
// 并行会话各自 watcher 互不干扰；bat 侧门保留仅用于手工/旧调用兼容）；
// OverlayVisibleSet=true 注入 SCEZ_PARAM_OVERLAY=1/0（参数控件启动可见性）。
// 未自定义/未设置的字段不注入（bat 原逻辑）。
func (r *BatRunner) Start(serial string, params CastParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return errors.New("bat 已在运行")
	}
	DebugLog("[start] cmd.exe /c %s (usb set=%v res=%d fps=%d bitrate=%d | wifi set=%v res=%d fps=%d bitrate=%d | serial=%q addr=%q addr2=%q nowatch=%v | overlay set=%v visible=%v)",
		r.batPath, params.Usb.Set, params.Usb.Res, params.Usb.FPS, params.Usb.Bitrate,
		params.Wifi.Set, params.Wifi.Res, params.Wifi.FPS, params.Wifi.Bitrate,
		params.Serial, params.Addr, params.Addr2, params.NoWatch,
		params.OverlayVisibleSet, params.OverlayVisible)
	cmd := exec.Command("cmd.exe", "/c", r.batPath)
	// Batch starts need independent namespaces even with identical clock readings.
	var err error
	r.watchTag, err = sessioncontrol.NewTag(WatchTag)
	if err != nil {
		return fmt.Errorf("无法生成会话标识：%w", err)
	}
	DebugLog("[start] session tag=%s serial=%s", r.watchTag, serial)
	// 注入环境变量（含 SCEZ_PARAM_OVERLAY 等）——组装逻辑在 params.go 的 castEnv，
	// 平台无关、可单测（见 params_test.go）
	env := castEnv(params, r.watchTag)
	env = append(env, fmt.Sprintf("SCEZ_EVENT_PARENT_PID=%d", os.Getpid()))
	if exe, err := os.Executable(); err == nil {
		env = append(env, "SCEZ_EVENT_HELPER="+exe)
	}
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
	r.waitDone = make(chan struct{})

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
			decoded := DecodeGBK([]byte(line))
			DebugLog("[%s:%d] %s", tag, n, strings.TrimRight(decoded, "\r\n"))
			r.onLine(decoded)
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
	_ = os.Remove(closedFlagPath(r.watchTag))
	close(r.waitDone)
	r.mu.Unlock()
	r.onExit(code)
}

// 只读展示架构（主人决策）：GUI 不向 bat 写 stdin。stdin 管道仍创建并保持打开
// （choice 有有效句柄时按 /t 超时 /d 默认自愈；死等菜单由用户在 GUI 点会话级按钮，
// 经 app.RestartCast/StopCast 杀树处理），stdin 写端仅 Stop 时关闭。

// Stop cancels the event supervisor, waits for graceful client/server cleanup,
// then uses only this owned process tree as a bounded last resort.
func (r *BatRunner) Stop() error {
	r.mu.Lock()
	cmd, tag, done := r.cmd, r.watchTag, r.waitDone
	if cmd == nil || cmd.Process == nil {
		r.mu.Unlock()
		return errors.New("bat 未在运行")
	}
	if r.exited {
		r.mu.Unlock()
		return nil
	}
	markSessionClosed(tag) // Covers Stop before the worker has created its event.
	if r.stdin != nil {
		_ = r.stdin.Close()
		r.stdin = nil
	}
	r.mu.Unlock()
	_ = sessioncontrol.SignalStop(tag)
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		DebugLog("[stop] event supervisor timeout; terminating owned tree pid=%d", cmd.Process.Pid)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, "taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
		c.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
		_ = c.Run()
		_ = cmd.Process.Kill()
		return nil
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

// SetSkipNotifWait retains the Runner API. Native client/server cleanup is awaited.
func (r *BatRunner) SetSkipNotifWait(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skipNotifWait = v
}

// ---------------------------------------------------------------- gui54：通知滞留修复

func markSessionClosed(tag string) {
	p := closedFlagPath(tag)
	if p == "" {
		return
	}
	if err := os.WriteFile(p, []byte("1"), 0o644); err != nil {
		DebugLog("[stop] 写关闭标记失败: %v", err)
		return
	}
	DebugLog("[stop] 已写关闭标记 %s（防 bat 自动重连）", filepath.Base(p))
}

func gracefulClosePID(pid int) {
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := cmd.Run(); err != nil {
		DebugLog("[stop] taskkill /PID %d（关窗）: %v", pid, err)
	}
}

// forceKillPID 强杀单个进程（不连带子进程：adb.exe 必须活着，server 才能优雅退出）。
func forceKillPID(pid int) {
	cmd := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscallProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := cmd.Run(); err != nil {
		DebugLog("[stop] taskkill /F /PID %d: %v", pid, err)
	}
}

// GracefulClosePID 导出包装（应用窗口会话停止用）：taskkill 不带 /F → WM_CLOSE。
func GracefulClosePID(pid int) { gracefulClosePID(pid) }

// ForceKillPID 导出包装：taskkill /F 强杀单个进程（不连带子进程，保留 adb.exe）。
func ForceKillPID(pid int) { forceKillPID(pid) }
