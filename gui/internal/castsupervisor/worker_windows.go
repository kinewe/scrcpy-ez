//go:build windows

package castsupervisor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/simplifiedchinese"
	"scrcpy-ez/gui/internal/clientlog"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/rootrepair"
	"scrcpy-ez/gui/internal/sessioncontrol"
)

type identityResult struct{ key, id string }
type learningResult struct{ serial, addr string }
type repairResult struct {
	key     string
	outcome rootrepair.Outcome
}
type childMessage struct {
	line string
	key  string
	done bool
	code int
}
type child struct {
	cmd              *exec.Cmd
	route            deviceevents.Transport
	key              string
	switchEvent      *sessioncontrol.Event
	ready, switching bool
	uploadDenied     bool
	frameSeen        bool
	readyAt          time.Time
}

var safeSerial = regexp.MustCompile(`^[A-Za-z0-9_.:\[\]-]+$`)

func say(format string, args ...any) {
	s := fmt.Sprintf(format, args...) + "\r\n"
	b, err := simplifiedchinese.GBK.NewEncoder().String(s)
	if err != nil {
		b = s
	}
	fmt.Fprint(os.Stdout, b)
}

// RunIfRequested is dispatched before creating any GUI or tray resources.
func RunIfRequested(args []string) bool {
	if len(args) < 2 || args[0] != "--event-cast" {
		return false
	}
	code := run(args[1], args[2:])
	os.Exit(code)
	return true
}

func hide(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

func query(ctx context.Context, adb string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, adb, args...)
	hide(c)
	b, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func identify(ctx context.Context, adb string, t deviceevents.Transport) string {
	id := query(ctx, adb, "-s", t.Serial, "shell", "getprop", "ro.serialno")
	if id == "" {
		id = query(ctx, adb, "-s", t.Serial, "shell", "getprop", "ro.boot.serialno")
	}
	if id == "" && t.Kind == "usb" {
		id = t.Serial
	}
	if !safeSerial.MatchString(id) {
		return ""
	}
	return id
}

func watchStop(ctx context.Context, cancel context.CancelFunc, stop *sessioncontrol.Event) {
	done, err := sessioncontrol.New("")
	if err != nil {
		cancel()
		return
	}
	defer done.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			done.Signal()
		case <-finished:
		}
	}()
	handles := []windows.Handle{done.Handle(), stop.Handle()}
	pid, _ := strconv.Atoi(os.Getenv("SCEZ_EVENT_PARENT_PID"))
	if pid == 0 {
		pid = os.Getppid()
	}
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == nil {
		defer windows.CloseHandle(parent)
		handles = append(handles, parent)
	} else {
		cancel()
		return
	}
	_, _ = windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
	cancel()
}

func startChild(bat string, args []string, route deviceevents.Transport, key, identity, stopName, tag string, generation int, messages chan<- childMessage) (*child, error) {
	event, err := sessioncontrol.New(fmt.Sprintf(`Local\SCEZ_ROUTE_%s_%d`, tag, generation))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("cmd.exe", "/d", "/c", bat)
	var tail strings.Builder
	for _, arg := range args {
		if strings.ContainsAny(arg, "\r\n\"&|<>^%!") {
			event.Close()
			return nil, fmt.Errorf("unsupported shell argument %q", arg)
		}
		tail.WriteString(` "` + arg + `"`)
	}
	hide(cmd)
	cmd.SysProcAttr.CmdLine = `cmd.exe /d /s /c ""` + bat + `"` + tail.String() + `"`
	cmd.Dir = filepath.Dir(bat)
	cmd.Env = append(os.Environ(), "SCEZ_EVENT_CHILD=1", "SCEZ_EVENT_ROUTE="+route.Serial,
		"SCEZ_EVENT_KIND="+route.Kind,
		"SCEZ_EXPECT_SERIAL="+identity,
		"SCEZ_NO_ADB_RESET=1", "SCEZ_NO_WATCH=1", "SCEZ_WATCH_TAG="+tag,
		"SCEZ_EVENT_STOP_NAME="+stopName, "SCEZ_EVENT_SWITCH_NAME="+event.Name,
		fmt.Sprintf("SCEZ_EVENT_SUPERVISOR_PID=%d", os.Getpid()))
	if generation > 1 {
		cmd.Env = append(cmd.Env, "FIRST_CAST_DONE=1")
	}
	cmd.Stdin = os.Stdin
	out, err := cmd.StdoutPipe()
	if err != nil {
		event.Close()
		return nil, err
	}
	errout, err := cmd.StderrPipe()
	if err != nil {
		event.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		event.Close()
		return nil, err
	}
	p := &child{cmd: cmd, route: route, key: key, switchEvent: event}
	var readers sync.WaitGroup
	read := func(r io.Reader) {
		defer readers.Done()
		br := bufio.NewReader(r)
		for {
			line, e := br.ReadString('\n')
			if line != "" {
				messages <- childMessage{line: line, key: key}
			}
			if e != nil {
				return
			}
		}
	}
	readers.Add(2)
	go read(out)
	go read(errout)
	go func() {
		readers.Wait()
		err := cmd.Wait()
		code := 0
		if err != nil {
			code = 1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		messages <- childMessage{done: true, code: code, key: key}
	}()
	return p, nil
}

func killTree(p *child) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "taskkill", "/F", "/T", "/PID", strconv.Itoa(p.cmd.Process.Pid))
	hide(c)
	_ = c.Run()
	_ = p.cmd.Process.Kill()
}

func addresses(dir string) []string {
	var out []string
	for _, s := range []string{os.Getenv("SCEZ_ADDR"), os.Getenv("SCEZ_ADDR2"), os.Getenv("SCEZ_SERIAL")} {
		if _, _, err := net.SplitHostPort(s); err == nil {
			out = append(out, s)
		}
	}
	// Free standalone sessions retain the single remembered wireless address.
	if len(out) == 0 && os.Getenv("SCEZ_EXPECT_SERIAL") == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "config.txt")); err == nil {
			s := strings.TrimSpace(string(b))
			if _, _, err := net.SplitHostPort(s); err == nil {
				out = append(out, s)
			}
		}
	}
	return out
}

// Standalone BAT has no GUI learner. Learn once per USB arrival and verify
// the wireless candidate before remembering it; all retries share a deadline.
func learnUSB(ctx context.Context, adb, serial, target string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := query(ctx, adb, "-s", serial, "shell", "ip", "-o", "-4", "addr", "show", "wlan0")
	var addr string
	f := strings.Fields(out)
	for i, v := range f {
		if v == "inet" && i+1 < len(f) {
			ip := strings.Split(f[i+1], "/")[0]
			if net.ParseIP(ip) != nil {
				addr = net.JoinHostPort(ip, "5555")
			}
		}
	}
	if addr == "" {
		return ""
	}
	if query(ctx, adb, "-s", serial, "shell", "getprop", "service.adb.tcp.port") != "5555" {
		query(ctx, adb, "-s", serial, "tcpip", "5555")
	}
	for n := 0; n < 3; n++ {
		if n > 0 {
			timer := time.NewTimer(time.Duration(n) * 500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ""
			case <-timer.C:
			}
		}
		query(ctx, adb, "connect", addr)
		if identify(ctx, adb, deviceevents.Transport{Serial: addr, Kind: "wifi"}) == target {
			return addr
		}
	}
	return ""
}

func run(bat string, args []string) int {
	bat, err := filepath.Abs(bat)
	if err != nil {
		return 1
	}
	dir := filepath.Dir(bat)
	adb := filepath.Join(dir, "adb.exe")
	if _, err := os.Stat(adb); err != nil {
		adb = "adb"
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctxDone := ctx.Done()
	tag := os.Getenv("SCEZ_WATCH_TAG")
	if tag == "" {
		tag, err = sessioncontrol.NewTag("SCEZ")
		if err != nil {
			return 1
		}
	}
	stop, err := sessioncontrol.New(sessioncontrol.StopName(tag))
	if err != nil {
		say("[错误] 无法建立会话停止事件：%v", err)
		return 1
	}
	defer stop.Close()
	if _, err := os.Stat(filepath.Join(os.TempDir(), "scrcpy_closed_"+tag+".flag")); err == nil {
		return 0
	}
	go watchStop(ctx, cancel, stop)
	hub := deviceevents.NewHub()
	shared := os.Getenv("SCEZ_EVENT_ENDPOINT") != ""
	if shared {
		go func() {
			if err := deviceevents.Follow(ctx, os.Getenv("SCEZ_EVENT_ENDPOINT"), os.Getenv("SCEZ_EVENT_TOKEN"), hub); err != nil && ctx.Err() == nil {
				say("[错误] GUI 事件连接已断开，结束会话")
				cancel()
			}
		}()
	} else {
		go deviceevents.Track(ctx, adb, hub)
	}
	events := hub.Subscribe(ctx)
	identities := map[string]string{}
	resolving := map[string]bool{}
	blocked := map[string]bool{}
	failures := map[string]int{}
	identityFailures := map[string]int{}
	results := make(chan identityResult, 64)
	learningDone := make(chan learningResult, 1)
	learned := map[string]uint64{}
	learningSerial := ""
	messages := make(chan childMessage, 128)
	target := strings.TrimSpace(os.Getenv("SCEZ_EXPECT_SERIAL"))
	lockedUSB := os.Getenv("SCEZ_SERIAL")
	if target == "" && lockedUSB != "" && !strings.Contains(lockedUSB, ":") {
		target = lockedUSB
	}
	if !shared && target == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "config.identity.txt")); err == nil && safeSerial.MatchString(strings.TrimSpace(string(b))) {
			target = strings.TrimSpace(string(b))
		}
	}
	known := addresses(dir)
	var snapshot deviceevents.Snapshot
	var observedEpoch uint64
	var p *child
	var clipboardState windows.Handle
	defer func() {
		if clipboardState != 0 {
			windows.CloseHandle(clipboardState)
		}
	}()
	generation := 0
	repairAttempted, preparing := false, false
	repairPaused := false
	pausedKey := ""
	preparingKey := ""
	var repairTicker *time.Ticker
	var repairProgress <-chan time.Time
	var repairDeadline time.Time
	var cancelRepair context.CancelFunc
	defer func() {
		if repairTicker != nil {
			repairTicker.Stop()
		}
		if cancelRepair != nil {
			cancelRepair()
		}
	}()
	repairs := make(chan repairResult, 1)
	var deadline, transition, retry <-chan time.Time
	var deadlineTimer, transitionTimer, retryTimer *time.Timer
	var absentAt time.Time
	stopping, userClosed := false, false
	recovering := false
	recoveryDone := make(chan struct{}, 1)
	recover := func() {
		if recovering || len(known) == 0 {
			return
		}
		recovering = true
		candidates := append([]string(nil), known...)
		go func() {
			defer func() { recoveryDone <- struct{}{} }()
			for round := 0; round < 3; round++ {
				if round > 0 {
					timer := time.NewTimer(time.Duration(round) * 500 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
				for _, addr := range candidates {
					if ctx.Err() != nil {
						return
					}
					query(ctx, adb, "connect", addr)
				}
			}
		}()
	}
	defer func() {
		for _, t := range []*time.Timer{deadlineTimer, transitionTimer, retryTimer} {
			if t != nil {
				t.Stop()
			}
		}
		if p != nil {
			stop.Signal()
			killTree(p)
			p.switchEvent.Close()
		}
	}()
	say("[自动切换] 会话已启用 ADB 事件驱动（USB 优先，同设备无线恢复）")
	// Recovery is finite and triggered by startup, disconnect, or observer restart.
	recover()
	for {
		snapshot = hub.Current() // queued notifications never authorize an older snapshot
		if repairPaused && !current(snapshot, pausedKey) {
			repairPaused = false
		}
		if preparing && !current(snapshot, preparingKey) && cancelRepair != nil {
			cancelRepair()
		}
		if snapshot.Available && snapshot.Epoch != observedEpoch {
			observedEpoch = snapshot.Epoch
			if p == nil {
				recover()
			}
		}
		if !stopping && ctx.Err() == nil && snapshot.Available {
			for _, t := range snapshot.Transports {
				if retry != nil {
					break
				}
				if t.State != "device" || (t.Kind != "usb" && t.Kind != "wifi") {
					continue
				}
				key := transportKey(snapshot, t)
				if identities[key] != "" || resolving[key] || identityFailures[key] >= 3 {
					continue
				}
				// Locked sessions do not query unrelated USB devices.
				if t.Kind == "usb" && lockedUSB != "" && !strings.Contains(lockedUSB, ":") && t.Serial != lockedUSB && t.Serial != target {
					continue
				}
				resolving[key] = true
				go func(t deviceevents.Transport, key string) {
					id := identify(ctx, adb, t)
					select {
					case results <- identityResult{key, id}:
					case <-ctx.Done():
					}
				}(t, key)
			}
			want := choose(snapshot, target, identities, blocked)
			if p == nil && want.Kind == "wifi" && transition != nil {
				want = deviceevents.Transport{}
			}
			if !shared && want.Kind == "usb" && learningSerial == "" && learned[want.Serial] != want.Generation {
				if target == "" {
					target = identities[transportKey(snapshot, want)]
				}
				if !shared {
					_ = os.WriteFile(filepath.Join(dir, "config.identity.txt"), []byte(target+"\r\n"), 0600)
				}
				learningSerial = want.Serial
				learned[want.Serial] = want.Generation
				hub.SetLearning(want.Serial, true)
				serial, id := want.Serial, target
				go func() {
					addr := learnUSB(ctx, adb, serial, id)
					select {
					case learningDone <- learningResult{serial, addr}:
					case <-ctx.Done():
					}
				}()
				want = deviceevents.Transport{}
			}
			if want.Serial == learningSerial {
				want = deviceevents.Transport{}
			}
			if p == nil && generation == 0 && want.Kind == "wifi" && (learningSerial != "" || startupWaitsForUSB(snapshot, target, lockedUSB, identities, blocked, identityFailures)) {
				want = deviceevents.Transport{}
			}
			if p == nil && want.Serial != "" && retry == nil && !preparing && !repairPaused {
				if target == "" {
					target = identities[transportKey(snapshot, want)]
				}
				generation++
				key := transportKey(snapshot, want)
				if clipboardState == 0 && target != "" {
					clipboardState, err = retainClipboardState(target)
					if err != nil {
						say("[剪贴板] 无法保留恢复状态：%v", err)
					}
				}
				p, err = startChild(bat, args, want, key, target, stop.Name, tag, generation, messages)
				if err != nil {
					say("[错误] 无法启动投屏：%v", err)
					return 1
				}
				say("[自动切换] 启动 %s：%s", want.Kind, want.Serial)
				deadlineTimer = time.NewTimer(30 * time.Second)
				deadline = deadlineTimer.C
				absentAt = time.Time{}
			} else if p != nil && !p.switching {
				present := false
				for _, t := range snapshot.Transports {
					if t.Serial == p.route.Serial && t.State == "device" {
						present = true
					}
				}
				// Grace is a single timer tied to an observed removal, not sampling.
				if present {
					absentAt = time.Time{}
				} else if absentAt.IsZero() {
					absentAt = time.Now()
					if transitionTimer != nil {
						transitionTimer.Stop()
					}
					transitionTimer = time.NewTimer(1500 * time.Millisecond)
					transition = transitionTimer.C
				}
				shouldSwitch := want.Serial != "" && want.Serial != p.route.Serial && (want.Kind == "usb" || (!absentAt.IsZero() && time.Since(absentAt) >= 1500*time.Millisecond))
				if shouldSwitch {
					p.switching = true
					p.switchEvent.Signal()
					say("[自动切换] 切换至 %s：%s", want.Kind, want.Serial)
					if deadlineTimer != nil {
						deadlineTimer.Stop()
					}
					deadlineTimer = time.NewTimer(5 * time.Second)
					deadline = deadlineTimer.C
				}
			}
		}
		select {
		case <-ctxDone:
			ctxDone = nil
			if !stopping {
				stopping = true
				stop.Signal()
				if p == nil && !preparing {
					return 0
				}
				if deadlineTimer != nil {
					deadlineTimer.Stop()
				}
				deadlineTimer = time.NewTimer(5 * time.Second)
				deadline = deadlineTimer.C
			}
		case repaired := <-repairs:
			preparing = false
			if repairTicker != nil {
				repairTicker.Stop()
			}
			repairProgress = nil
			if stopping || ctx.Err() != nil {
				return 0
			}
			if !current(hub.Current(), repaired.key) {
				continue
			}
			if repaired.outcome.Accepted() {
				delete(blocked, repaired.key)
				say("[root 修复] 上传复检通过，重新启动投屏；等待真实画面就绪")
			} else {
				blocked[repaired.key] = true
				repairPaused, pausedKey = true, repaired.key
				say("SCRCPY_EZ_RETRY_WAIT")
				say("[root 修复待处理] %s；仅在此手机已有 root 时启用或再次尝试修复", repaired.outcome.String())
			}
		case <-repairProgress:
			remaining := max(0, int(time.Until(repairDeadline).Seconds()))
			say("[root 修复] 等待诊断或修复：本轮最多还剩 %d 秒；请留意手机授权（授权最多 3 分钟，可停止）", remaining)
		case _, ok := <-events:
			if !ok {
				events = nil
				cancel()
				continue
			}
			snapshot = hub.Current()
		case r := <-results:
			delete(resolving, r.key)
			if !current(hub.Current(), r.key) {
				continue
			}
			// Descriptor fallback is insufficient for a different confirmed serial.
			if target != "" && r.id != target {
				for _, t := range hub.Current().Transports {
					if transportKey(hub.Current(), t) == r.key && t.Kind == "usb" && r.id == t.Serial {
						r.id = ""
					}
				}
			}
			if r.id != "" {
				identities[r.key] = r.id
				if r.id == target {
					for _, t := range snapshot.Transports {
						if transportKey(snapshot, t) == r.key && t.Kind == "wifi" {
							if _, _, err := net.SplitHostPort(t.Serial); err == nil {
								found := false
								for _, v := range known {
									if v == t.Serial {
										found = true
									}
								}
								if !found {
									known = append(known, t.Serial)
								}
							}
						}
					}
				}
			} else {
				identityFailures[r.key]++
				if retryTimer != nil {
					retryTimer.Stop()
				}
				retryTimer = time.NewTimer(500 * time.Millisecond)
				retry = retryTimer.C
			}
		case <-recoveryDone:
			recovering = false
		case learnedResult := <-learningDone:
			learningSerial = ""
			for _, t := range snapshot.Transports {
				if t.Serial == learnedResult.serial {
					learned[t.Serial] = t.Generation
				}
			}
			if learnedResult.addr != "" {
				known = append(known, learnedResult.addr)
				_ = os.WriteFile(filepath.Join(dir, "config.txt"), []byte(learnedResult.addr+"\r\n"), 0600)
			}
			hub.SetLearning(learnedResult.serial, false)
		case <-transition:
			transition = nil
			if p == nil {
				recover()
			}
		case <-retry:
			retry = nil
		case <-deadline:
			deadline = nil
			if p != nil {
				say("[提示] 本次启动或退出超时，清理本会话进程")
				killTree(p)
			}
			if stopping {
				return 0
			}
		case m := <-messages:
			if p == nil || m.key != p.key {
				continue
			}
			if m.line != "" {
				if clientlog.TextureSize(m.line) != "" {
					p.frameSeen = true
				}
				if strings.TrimSpace(m.line) == "SCRCPY_EZ_SERVER_UPLOAD_PERMISSION" && !p.ready {
					p.uploadDenied = true
				}
				fmt.Fprint(os.Stdout, m.line)
				if strings.Contains(m.line, "SCRCPY_EZ_USER_CLOSE") {
					userClosed = true
					cancel()
				}
				if strings.Contains(m.line, "SCRCPY_EZ_READY") && p != nil {
					p.ready = true
					p.readyAt = time.Now()
					if !p.switching && deadlineTimer != nil {
						deadlineTimer.Stop()
						deadline = nil
					}
				}
			}
			if !m.done {
				continue
			}
			if p == nil {
				continue
			}
			key := p.key
			denied, wasReady, route := p.uploadDenied, p.ready, p.route
			switched := p.switching
			say("[会话] 子进程退出：tag=%s route=%s key=%s bat_pid=%d code=%d (0x%08X) ready=%t switching=%t", tag, p.route.Serial, p.key, p.cmd.Process.Pid, m.code, uint32(m.code), p.ready, switched)
			if p.ready && time.Since(p.readyAt) >= 5*time.Second {
				failures[key] = 0
				if p.frameSeen {
					repairAttempted = false
				}
			}
			p.switchEvent.Close()
			p = nil
			if deadlineTimer != nil {
				deadlineTimer.Stop()
			}
			deadline = nil
			if stopping || userClosed || m.code == 0 {
				return 0
			}
			if denied && !wasReady && !switched && current(hub.Current(), key) {
				blocked[key] = true
				if repairAttempted {
					repairPaused, pausedKey = true, key
					say("SCRCPY_EZ_RETRY_WAIT")
					say("[root 修复待处理] 本次已尝试修复，真实 server 上传仍失败；请查看错误并手动处理")
					continue
				}
				repairAttempted = true
				preparing, preparingKey = true, key
				repairCtx, repairCancel := context.WithTimeout(ctx, rootrepair.Budget)
				repairDeadline, _ = repairCtx.Deadline()
				repairTicker = time.NewTicker(time.Second)
				repairProgress = repairTicker.C
				cancelRepair = repairCancel
				req := rootrepair.Request{Serial: route.Serial, Identity: target, Key: key}
				say("[root 修复] server 上传权限拒绝，检查此设备是否已启用修复（可停止）")
				go func() {
					defer repairCancel()
					var outcome rootrepair.Outcome
					if endpoint := os.Getenv("SCEZ_ROOT_ENDPOINT"); endpoint != "" {
						outcome = rootrepair.Call(repairCtx, endpoint, os.Getenv("SCEZ_ROOT_TOKEN"), req)
					} else {
						c := rootrepair.NewCoordinator(filepath.Join(dir, "root-repair.json"), func(cctx context.Context, r rootrepair.Request) (rootrepair.Report, error) {
							return rootrepair.PrepareLocked(cctx, rootrepair.Options{ADB: adb, Serial: r.Serial, Identity: r.Identity, LogDir: filepath.Join(dir, "root-repair-logs"), Execute: rootrepair.CommandExecutor(adb), Say: func(text string) { say("[root 修复] %s", text) }})
						})
						outcome = c.Repair(repairCtx, req)
					}
					repairs <- repairResult{key: key, outcome: outcome}
				}()
				continue
			}
			if m.code != 3 || !switched {
				failures[key]++
				if failures[key] >= 3 {
					blocked[key] = true
					say("SCRCPY_EZ_RETRY_WAIT")
					say("[提示] 本次连接连续失败，等待设备状态变化或手动重投")
				}
				retryTimer = time.NewTimer(time.Duration(failures[key]) * 500 * time.Millisecond)
				retry = retryTimer.C
				// adbd restart/removal has a bounded grace before wireless fallback.
				if transitionTimer != nil {
					transitionTimer.Stop()
				}
				transitionTimer = time.NewTimer(1500 * time.Millisecond)
				transition = transitionTimer.C
				recover()
			}
		}
	}
}
