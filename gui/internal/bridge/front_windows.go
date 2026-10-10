//go:build windows

package bridge

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// --- 标签点击 → 投屏窗口浮前（问题 3，浏览器范式） ---
// v5 二段式浮前（用户定稿，实机验证 21:3x：单纯 SetWindowPos+NOACTIVATE
// 置顶不激活 → 未最小化窗口浮不出来）：
//   ① SetWindowPos(scrcpyWin, HWND_TOP, 0,0,0,0, NOSIZE|NOMOVE|SHOWWINDOW)
//      —— 无 NOACTIVATE（z-order 前置兜底）；
//   ② SetForegroundWindow(scrcpyWin) —— 激活拉前（点击窗口级效果）；
//   ③ 延时 50ms（置顶动画完成/确保前置）；
//   ④ SetForegroundWindow(ez) —— ez 顶回（最终 ez 最前、焦点留 ez）；
//   ⑤ 兜底：②失败（前台锁限制）→ AttachThreadInput(scrcpy 线程) 后重试
//      SetForegroundWindow，完成即 DetachThreadInput。
// v6 稳健化（"窗口已就绪仍不弹"深挖修复，证据：bridge-20260824.log 22:47:01/
// 22:47:02/22:48:50 ②SetForegroundWindow OK=false 且 attach=false）：
//   ① 修复 getWindowThreadID：GetWindowThreadProcessId 返回值才是线程 ID，
//      旧实现误把第二参数（进程 ID）当线程 ID → AttachThreadInput 必失败
//      （日志"scrcpy线程=36080"实为 scrcpy pid）；
//   ② 有界重试（点击触发，600ms×3 轮）：进程未出现→重新枚举进程（后轮强制
//      新鲜，缓存可能停留在"进程未启动"快照）；进程在但窗口不可见（SDL 启动
//      期先隐藏后显示）→只重枚举窗口；找到可见主窗
//      才执行浮前，且每次点击只浮前一次（重试只补侦测，不重复刷 z-order）；
//   ③ 全部窗口不可见时不再回退置顶隐藏窗（v4 兜底会置顶隐藏窗制造 z-order
//      乱序），改为等待下一轮重试；
//   ④ 进程枚举缓存 TTL 3s，共享原生 WMI 查询，6s 超时；失败不缓存。
//      窗口匹配及浮前前均校验进程创建时间，防退出后 PID 被复用；
//   ⑤ 同会话连点幂等：新一轮点击立即取消上一轮查询等待和重试。
// 主窗选择沿用 v4（可见性过滤/无属主优先/面积最大——selectFrontMainWin 纯函数，
// 只置顶主窗一个，7 窗口乱序根因）。全程独立 goroutine：50ms 延时不得阻塞
// webview UI 线程（前端已 fire-and-forget，标签切换不被拖住）。
// 未找到进程/窗口=不算错误（DebugLog）。

const (
	swpNoSize        = 0x0001 // 保持窗口尺寸（SetWindowPos 默认按 cx/cy 重设大小，必须带）
	swpNoMove        = 0x0002 // 保持窗口位置
	swpNoActivate    = 0x0010 // v5：置顶步不再携带（用户定稿）；常量保留供参考
	swpShowWindow    = 0x0040 // 显示窗口
	hwndTop          = 0      // HWND_TOP：z-order 置顶
	swRestore        = 9      // SW_RESTORE：最小化恢复显示
	swShowNoActivate = 4      // SW_SHOWNOACTIVATE：显示但不激活（置顶失败重试前置）
	gwOwner          = 4      // GW_OWNER：取窗口属主（辅助窗判定）
	attachInputTrue  = 1
	attachInputFalse = 0
)

// frontDelay：②激活后、④ez 顶回前的置顶动画延时（v5 用户定稿 25ms：
// 浮前闪感与置顶完成度的折中——GUI 闪一下是 WebView2 occlusion/焦点切换
// 恢复渲染空档，延时减半让闪感减半）。
const frontDelay = 25 * time.Millisecond

// v6 有界重试（点击触发，非后台轮询）：最多 frontMaxAttempts 轮、间隔
// frontRetryInterval——覆盖"进程启动到窗口可见"的 1-3s 时序（实证：
// 0825 日志 00:38:17 进程在而窗口 0 命中、00:15:11 进程在而窗口已销毁）。
const (
	frontMaxAttempts    = 3
	frontRetryInterval  = 600 * time.Millisecond
	frontRequestTimeout = 8 * time.Second
)

// scrcpyProcsCache 缓存最近一次 scrcpy 进程枚举（浮前点击按需刷新，无后台轮询）：
// 快速连点多个标签共享一次原生 WMI 查询；TTL 短（3s），
// 重试循环后续轮强制新鲜。失败不缓存，进程身份另作实时校验。
var scrcpyProcsCache struct {
	mu    sync.Mutex
	at    time.Time
	procs []scrcpyProc
}

const scrcpyProcsCacheTTL = 3 * time.Second

// frontRequests 合并同一会话的重复浮前请求（幂等）：新一轮点击取消上一轮
// 未完成的查询等待和重试循环（防 z-order 轰炸）。
var frontRequests struct {
	mu sync.Mutex
	m  map[string]*frontRequest
}

type frontRequest struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func init() { frontRequests.m = map[string]*frontRequest{} }

// frontRequestKey 浮前请求的幂等键：serials + 应用包名（v2.1.46——
// 同设备不同应用窗口的请求互不取消；appPkg 空=主投屏标签请求）。
func frontRequestKey(serials []string, appPkg string) string {
	return strings.Join(serials, "\x00") + "\x01" + appPkg
}

// listScrcpyProcsCached 返回缓存/新鲜的 scrcpy 进程枚举（浮前点击按需，无后台轮询）。
// fresh=false 且缓存未过期（TTL 内）→ 直接复用；
// fresh=true 或缓存过期 → 重新枚举并写缓存。只在读写缓存时持锁；
// 等待由查询层合并，可取消，避免超时查询把后续点击排成长队。
func listScrcpyProcsCached(ctx context.Context, fresh bool) ([]scrcpyProc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scrcpyProcsCache.mu.Lock()
	if !fresh && time.Since(scrcpyProcsCache.at) < scrcpyProcsCacheTTL {
		procs := append([]scrcpyProc{}, scrcpyProcsCache.procs...)
		scrcpyProcsCache.mu.Unlock()
		return procs, nil
	}
	scrcpyProcsCache.mu.Unlock()
	procs, err := nativeScrcpyInventory.load(ctx)
	if err != nil {
		return nil, err
	}
	scrcpyProcsCache.mu.Lock()
	scrcpyProcsCache.at = time.Now()
	scrcpyProcsCache.procs = append([]scrcpyProc(nil), procs...)
	scrcpyProcsCache.mu.Unlock()
	return procs, nil
}

var (
	user32Front              = syscall.NewLazyDLL("user32.dll")
	kernel32Front            = syscall.NewLazyDLL("kernel32.dll")
	procEnumWindowsFront     = user32Front.NewProc("EnumWindows")
	procGetWindowThreadPID   = user32Front.NewProc("GetWindowThreadProcessId")
	procSetWindowPosFront    = user32Front.NewProc("SetWindowPos")
	procShowWindowFront      = user32Front.NewProc("ShowWindow")
	procIsIconicFront        = user32Front.NewProc("IsIconic")
	procIsWindowVisibleFront = user32Front.NewProc("IsWindowVisible")
	procGetWindowFront       = user32Front.NewProc("GetWindow")
	procGetWindowRectFront   = user32Front.NewProc("GetWindowRect")
	procSetForegroundWin     = user32Front.NewProc("SetForegroundWindow")
	procGetForegroundWin     = user32Front.NewProc("GetForegroundWindow")
	procAttachThreadInput    = user32Front.NewProc("AttachThreadInput")
	procGetCurrentThreadID   = kernel32Front.NewProc("GetCurrentThreadId")
	procGetCurrentProcessID  = kernel32Front.NewProc("GetCurrentProcessId")
)

// frontEzHwnd 是 ez GUI 宿主窗口句柄（UiReady 时由 ui 层捕获注入）；
// 0=未捕获（④兜底走本进程可见窗口枚举）。
var frontEzHwnd atomic.Uintptr

// SetFrontEzHwnd 记录 ez GUI 宿主窗口句柄（ui.SetHWND 调用，UI 线程）。
func SetFrontEzHwnd(hwnd uintptr) {
	frontEzHwnd.Store(hwnd)
	DebugLog("[front] ezHwnd 已捕获=%d（浮前④顶回用）", hwnd)
}

// frontWin 是枚举命中的一个投屏窗口（hwnd + 所属 pid）。
type frontWin struct {
	hwnd syscall.Handle
	pid  int
}

// rect 是 GetWindowRect 的 RECT 结构（left/top/right/bottom）。
type rect struct {
	left, top, right, bottom int32
}

// winInfo 给枚举窗口补属性快照（可见性/属主/矩形）→ 主窗选择纯函数。
func winInfo(w frontWin) frontWinInfo {
	info := frontWinInfo{hwnd: uintptr(w.hwnd), pid: w.pid}
	if r, _, _ := procIsWindowVisibleFront.Call(uintptr(w.hwnd)); r != 0 {
		info.visible = true
	}
	if r, _, _ := procGetWindowFront.Call(uintptr(w.hwnd), gwOwner); r != 0 {
		info.owned = true
	}
	var rc rect
	if r, _, _ := procGetWindowRectFront.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&rc))); r != 0 {
		info.w = int(rc.right - rc.left)
		info.h = int(rc.bottom - rc.top)
	}
	return info
}

// setForegroundWindow 激活指定窗口（返回是否成功）。
func setForegroundWindow(hwnd uintptr) bool {
	r, _, _ := procSetForegroundWin.Call(hwnd)
	return r != 0
}

// getWindowThreadID 取窗口所属线程 ID（⑤ AttachThreadInput 兜底用）。
// GetWindowThreadProcessId 的**返回值**才是线程 ID（第二个参数写进程 ID）——
// 旧实现误把第二参数（进程 ID）当线程 ID 返回，导致 AttachThreadInput 必失败
// （实证 0824 日志 22:47:01/22:48:50："scrcpy线程=36080" 实为 scrcpy pid，
// attach=false 重试=false → SetForegroundWindow 失败无兜底）。
func getWindowThreadID(hwnd uintptr) uintptr {
	r, _, _ := procGetWindowThreadPID.Call(hwnd, 0, 0)
	return r
}

// attachThreadInput 挂接/断开线程输入队列（前台锁限制兜底）。
func attachThreadInput(idAttach, idAttachTo uintptr, attach bool) bool {
	f := uintptr(attachInputFalse)
	if attach {
		f = attachInputTrue
	}
	r, _, _ := procAttachThreadInput.Call(idAttach, idAttachTo, f)
	return r != 0
}

// ezHwndNow 返回 ez GUI 宿主窗口句柄与来源：优先 UiReady 捕获值；
// 未捕获（0）→ 枚举本进程可见顶层窗口取面积最大（兜底：极端情况下
// 标签点击早于 UiReady 回调）。
func ezHwndNow() (uintptr, string) {
	if h := frontEzHwnd.Load(); h != 0 {
		return h, "捕获"
	}
	ownPid, _, _ := procGetCurrentProcessID.Call()
	var wins []frontWinInfo
	procEnumWindowsFront.Call(syscall.NewCallback(func(hwnd syscall.Handle, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadPID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
		if uintptr(pid) != ownPid {
			return 1
		}
		info := frontWinInfo{hwnd: uintptr(hwnd), pid: int(pid)}
		if r, _, _ := procIsWindowVisibleFront.Call(uintptr(hwnd)); r != 0 {
			info.visible = true
		}
		var rc rect
		if r, _, _ := procGetWindowRectFront.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc))); r != 0 {
			info.w = int(rc.right - rc.left)
			info.h = int(rc.bottom - rc.top)
		}
		wins = append(wins, info)
		return 1
	}), 0)
	if h := pickVisibleLargestHwnd(wins); h != 0 {
		return h, "枚举"
	}
	return 0, ""
}

// BringCastToFront 二段式浮前（v6 稳健版）：scrcpy 主窗激活拉前 + ez 顶回。
// 立即返回（内部 goroutine）：延时/重试不阻塞 webview UI 线程；
// 找不到进程/窗口不算错误（只记日志）；同会话新一轮点击取消上一轮重试（幂等）。
func BringCastToFront(serials []string) error {
	return bringFrontRequest(serials, "")
}

// BringAppWinToFront：点击应用卡片 → 把对应应用窗口（虚拟屏）提到前面
// （v2.1.46）——浮前序列与"点标签置顶主投屏"完全同款（①置顶②激活③延时
// ④ez 顶回最前），只是候选选择换成"指定包名的虚拟屏进程"（同设备多应用窗口
// 互不误伤；主投屏不吃）。
func BringAppWinToFront(serials []string, pkg string) error {
	return bringFrontRequest(serials, pkg)
}

// bringFrontRequest 浮前请求统一入口（appPkg 为空=主投屏会话；非空=指定应用窗口）：
// 幂等合并（同 key 连点取消上一轮）+ 独立 goroutine（不阻塞 UI 线程）。
func bringFrontRequest(serials []string, appPkg string) error {
	key := frontRequestKey(serials, appPkg)
	ctx, cancel := context.WithTimeout(context.Background(), frontRequestTimeout)
	req := &frontRequest{ctx: ctx, cancel: cancel}
	frontRequests.mu.Lock()
	if old, ok := frontRequests.m[key]; ok {
		old.cancel() // 连点：立即取消上一轮查询等待及重试。
	}
	frontRequests.m[key] = req
	frontRequests.mu.Unlock()

	go func() {
		defer req.cancel()
		err := bringToFrontSync(serials, appPkg, req)
		frontRequests.mu.Lock()
		if frontRequests.m[key] == req {
			delete(frontRequests.m, key)
		}
		frontRequests.mu.Unlock()
		if err != nil && req.ctx.Err() == nil {
			DebugLog("[front] bring-to-front serials=%v pkg=%q 最终失败: %v", serials, appPkg, err)
		}
	}()
	return nil
}

// bringToFrontSync 有界重试主循环（独立 goroutine，可取消，整体 8s 时限）：
// 进程未出现→重枚举进程（后续轮强制新鲜）；进程在但窗口不可见→只重枚举窗口；
// 找到可见主窗才执行一次浮前（raiseFrontWindow），随后立即返回——重试只补侦测，
// 不重复刷 z-order。前端 fire-and-forget：窗口未就绪时的点击不丢请求（自动重试），
// 重试有上限（frontMaxAttempts×frontRetryInterval）。
// v2.1.46：appPkg 空=主投屏标签（候选=会话 scrcpy，排除虚拟屏）；非空=应用卡片
// （候选=指定包名的虚拟屏 scrcpy，主投屏与其他应用窗口不吃）。
func bringToFrontSync(serials []string, appPkg string, req *frontRequest) error {
	var procs []scrcpyProc
	haveProcs := false
	for attempt := 0; attempt < frontMaxAttempts; attempt++ {
		if req.ctx.Err() != nil {
			DebugLog("[front] bring-to-front serials=%v pkg=%q 被新一轮点击取消", serials, appPkg)
			return nil
		}
		// 第 1 轮走缓存（快速连点共享枚举）；后续轮强制新鲜——缓存可能停留在
		// "进程未启动"的旧快照，沿用会掩盖刚启动的进程
		if !haveProcs {
			var err error
			procs, err = listScrcpyProcsCached(req.ctx, attempt > 0)
			if err != nil {
				if req.ctx.Err() != nil {
					return req.ctx.Err()
				}
				DebugLog("[front] scrcpy 进程枚举失败（第 %d/%d 轮）: %v", attempt+1, frontMaxAttempts, err)
			} else {
				haveProcs = true
			}
		}
		if haveProcs {
			var cands []scrcpyProc
			if appPkg == "" {
				cands = bringToFrontCandidates(procs, serials) // 主投屏标签：排除虚拟屏
			} else {
				cands = bringAppWinCandidates(procs, serials, appPkg) // 应用卡片：指定包名
			}
			if len(cands) == 0 {
				DebugLog("[front] bring-to-front 未找到目标 scrcpy 进程（第 %d/%d 轮，candidates=%v pkg=%q）",
					attempt+1, frontMaxAttempts, serials, appPkg)
				haveProcs = false // 下轮重新枚举（进程可能尚未启动/刚退出）
			} else {
				pids := make(map[int]bool, len(cands))
				for _, p := range cands {
					if h, err := openInventoryProcess(p); err == nil {
						pids[p.pid] = true
						windows.CloseHandle(h)
					}
				}
				DebugLog("[front] bring-to-front 候选 scrcpy pid=%d 个 %v（第 %d/%d 轮）",
					len(cands), cands, attempt+1, frontMaxAttempts)

				// EnumWindows → 收集 pid 命中的窗口（回调保持最小：只做 syscall + 收集）
				var wins []frontWin
				procEnumWindowsFront.Call(syscall.NewCallback(func(hwnd syscall.Handle, _ uintptr) uintptr {
					var pid uint32
					procGetWindowThreadPID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
					if pids[int(pid)] {
						wins = append(wins, frontWin{hwnd: hwnd, pid: int(pid)})
					}
					return 1 // 继续枚举
				}), 0)
				DebugLog("[front] EnumWindows 命中窗口数=%d（scrcpy pid 候选=%d）", len(wins), len(pids))

				// 可见性过滤 + 主窗选择（v4 纯函数；隐藏窗/落选可见窗全跳过）
				infos := make([]frontWinInfo, 0, len(wins))
				for _, w := range wins {
					infos = append(infos, winInfo(w))
				}
				sel := selectFrontMainWin(infos)
				if sel.ok && !sel.fallback {
					DebugLog("[front] 可见窗口数=%d 隐藏跳过=%d | 主窗 hwnd=%d pid=%d 面积=%dx%d owner=%v | 落选可见窗=%d（不再置顶）",
						len(wins)-sel.skippedHidden, sel.skippedHidden,
						sel.main.hwnd, sel.main.pid, sel.main.w, sel.main.h, sel.main.owned,
						sel.skippedVisible)
					if raiseInventoryWindow(sel.main, cands, req.ctx) {
						return nil
					}
					haveProcs = false // Exited/replaced while windows were enumerated.
				}
				if len(wins) == 0 {
					// pid 已失效（进程刚退出/重启）：下轮重新枚举进程
					DebugLog("[front] 未枚举到投屏窗口（第 %d/%d 轮，进程存在但窗口未创建/已销毁）",
						attempt+1, frontMaxAttempts)
					haveProcs = false
				} else {
					// 窗口已创建但全部不可见（SDL 启动期先隐藏后显示）：
					// 不回退置顶隐藏窗（v4 兜底会置顶隐藏窗制造 z-order 乱序），
					// 保留进程快照，下轮便宜地只重枚举窗口
					DebugLog("[front] 命中窗口全部不可见（第 %d/%d 轮，隐藏跳过=%d），等待窗口显示后重试",
						attempt+1, frontMaxAttempts, sel.skippedHidden)
				}
			}
		}
		if attempt < frontMaxAttempts-1 {
			timer := time.NewTimer(frontRetryInterval)
			select {
			case <-req.ctx.Done():
				timer.Stop()
				return req.ctx.Err()
			case <-timer.C:
			}
		}
	}
	DebugLog("[front] bring-to-front 重试 %d 轮仍未就绪（candidates=%v），本击放弃", frontMaxAttempts, serials)
	return nil
}

func raiseInventoryWindow(sel frontWinInfo, candidates []scrcpyProc, ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	for _, p := range candidates {
		if p.pid != sel.pid {
			continue
		}
		h, err := openInventoryProcess(p)
		if err != nil {
			return false
		}
		defer windows.CloseHandle(h)
		var actual uint32
		procGetWindowThreadPID.Call(sel.hwnd, uintptr(unsafe.Pointer(&actual)))
		visible, _, _ := procIsWindowVisibleFront.Call(sel.hwnd)
		if int(actual) != p.pid || visible == 0 {
			return false
		}
		if ctx.Err() == nil {
			raiseFrontWindow(sel)
		}
		return true
	}
	return false
}

// raiseFrontWindow 对已选中的可见主窗执行一次二段式浮前（v5 序列 + ⑤修复）：
// ①置顶 ②激活拉前（失败 AttachThreadInput 兜底重试）③延时 ④ez 顶回。
func raiseFrontWindow(sel frontWinInfo) {
	raiseFrontWindowWithFocus(sel, true)
}

// The PID comes from this owned client's ready signal, without shell inventory.
func BringClientToFront(pid int) error {
	if pid <= 0 {
		return nil
	}
	var infos []frontWinInfo
	procEnumWindowsFront.Call(syscall.NewCallback(func(hwnd syscall.Handle, _ uintptr) uintptr {
		var actual uint32
		procGetWindowThreadPID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&actual)))
		if int(actual) == pid {
			infos = append(infos, winInfo(frontWin{hwnd: hwnd, pid: pid}))
		}
		return 1
	}), 0)
	selected := selectFrontMainWin(infos)
	if selected.ok && !selected.fallback {
		raiseFrontWindowWithFocus(selected.main, false)
	}
	return nil
}

func raiseFrontWindowWithFocus(sel frontWinInfo, restoreEZ bool) {
	scrcpyWin, scrcpyPid := sel.hwnd, sel.pid

	// 最小化主窗先恢复显示（SW_RESTORE）
	if r, _, _ := procIsIconicFront.Call(scrcpyWin); r != 0 {
		procShowWindowFront.Call(scrcpyWin, swRestore)
		DebugLog("[front] 主窗最小化 → SW_RESTORE：hwnd=%d", scrcpyWin)
	}

	// ① z-order 置顶（无 NOACTIVATE——用户定稿：允许激活语义）
	r1, _, err1 := procSetWindowPosFront.Call(scrcpyWin, hwndTop, 0, 0, 0, 0,
		swpNoSize|swpNoMove|swpShowWindow)
	if r1 == 0 {
		DebugLog("[front] ① SetWindowPos 失败（hwnd=%d pid=%d err=%v），ShowWindow(SW_SHOWNOACTIVATE) 后重试",
			scrcpyWin, scrcpyPid, err1)
		procShowWindowFront.Call(scrcpyWin, swShowNoActivate)
		r1, _, err1 = procSetWindowPosFront.Call(scrcpyWin, hwndTop, 0, 0, 0, 0,
			swpNoSize|swpNoMove|swpShowWindow)
	}
	DebugLog("[front] ① SetWindowPos 置顶：hwnd=%d pid=%d 结果=%d（0=失败 err=%v）", scrcpyWin, scrcpyPid, r1, err1)

	// ② 激活拉前（点击窗口级效果）；失败 → ⑤ AttachThreadInput 兜底重试
	// （v6 修复：getWindowThreadID 返回真实线程 ID，attach 不再必败；挂接目标
	// 优先取当前前台窗口所属线程——Windows 只允许前台输入队列所有者切前台，
	// 这是 SetForegroundWindow 前台锁的标准解除方式）
	fgOK := setForegroundWindow(scrcpyWin)
	if !fgOK {
		scrcpyTid := getWindowThreadID(scrcpyWin)
		ourTid, _, _ := procGetCurrentThreadID.Call()
		attachTo, attachSrc := scrcpyTid, "scrcpy线程"
		if fg, _, _ := procGetForegroundWin.Call(); fg != 0 {
			if t := getWindowThreadID(fg); t != 0 {
				attachTo, attachSrc = t, "前台线程"
			}
		}
		attached := attachThreadInput(ourTid, attachTo, true)
		retry := setForegroundWindow(scrcpyWin)
		detached := false
		if attached {
			detached = attachThreadInput(ourTid, attachTo, false)
		}
		DebugLog("[front] ② SetForegroundWindow(scrcpy) 失败 → ⑤ AttachThreadInput 兜底：%s=%d 本线程=%d attach=%v 重试=%v detach=%v",
			attachSrc, attachTo, ourTid, attached, retry, detached)
		fgOK = retry
	}
	DebugLog("[front] ② SetForegroundWindow(scrcpy)：hwnd=%d OK=%v", scrcpyWin, fgOK)

	// ③ 延时（置顶动画完成/确保前置）
	if !restoreEZ {
		return
	}
	time.Sleep(frontDelay)
	DebugLog("[front] ③ 延时 %dms 完成", frontDelay.Milliseconds())

	// ④ ez 顶回（最终 ez 最前、焦点留 ez）
	ezHwnd, ezSrc := ezHwndNow()
	if ezHwnd == 0 {
		DebugLog("[front] ④ ezHwnd=0（UiReady 未回调且本进程枚举失败）→ 跳过顶回（焦点暂留 scrcpy）")
		return
	}
	ezOK := setForegroundWindow(ezHwnd)
	DebugLog("[front] ④ SetForegroundWindow(ez)：ezHwnd=%d（来源=%s）OK=%v —— ez 顶回最前、焦点留 ez",
		ezHwnd, ezSrc, ezOK)
}
