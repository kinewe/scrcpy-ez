package app

// 应用窗口（二期）· Step 3 v2：走 bat 的虚拟屏会话（完整 ez 特性继承）。
//
// 【背景 v2.1.27】v2.1.21 版直接 spawn scrcpy.exe（裸参数），实测缺 ez 的完整能力：
//   - 无 --keyboard=uhid（scrcpy 默认 auto→sdk，中文打字不继承）；
//   - 无渲染低延迟参数（--render-driver=direct3d --video-buffer=0）与码率档；
//   - 无拔插自动切换 / 异常自动重连（bat 自愈链路）。
//
// 现改为主投屏同构——每应用一个 bat 会话（Runner），参数经 SCEZ_VD_* /
// SCEZ_START_APP / SCEZ_WIN_TITLE 注入，bat 侧组装 VD_ARGS（投屏支持.bat
// 「gui56 虚拟屏参数」段）→ uhid 键盘/渲染参数/自愈/插拔切换天然继承（主人拍板）。
//
// 【生命周期】
//   - 点应用=开窗（bat 启动 → 设备检测/连接 → scrcpy 虚拟屏窗口出现）；
//   - 停止=标记 closing（快照透传 → 前端"正在关闭…"遮罩）→ 异步 runner.Stop()
//     （防重连标记/优雅关窗/整树杀/watcher 清理，与主投屏同款）→ bat 退出 → 摘除；
//   - 用户手关窗口 → scrcpy 码 0 → bat 写 closed flag 退出 → 摘除（前端卡片淡出）；
//   - 拔线/异常 → bat 自动重连（重建虚拟屏），会话保持（卡片保持）。
//   - 状态经快照下发（Snapshot.AppWins）：后端为真相源，前端据此校准卡片。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

// AppWinItem 是快照里的一条应用窗口（前端卡片数据源）。
type AppWinItem struct {
	Serial   string `json:"serial"`
	Identity string `json:"identity,omitempty"`
	Pkg      string `json:"pkg"`
	Name     string `json:"name"`
	// Mode = 启动时连接形态（usb/wifi）——设置面板标题「有线/无线参数」与档案
	// 套选择用（v2.1.47）。
	Mode string `json:"mode"`
	// Closing 停止已受理、进程清理中（前端在停止按钮位置显示"正在关闭…"遮罩；
	// 真关完（会话摘除）后条目消失 → 卡片淡出）。
	Closing bool `json:"closing"`
	// Phase/PhaseText 转换/重连中的动态状态（v2.1.54）：插拔切换、断线重连等
	// 阶段显示与主投屏同款的阶段文字（如"检测到 USB 插线，切换有线投屏…"）；
	// 投屏继续类事件清空 → 前端回默认"正在窗口"。
	Phase     string `json:"phase,omitempty"`
	PhaseText string `json:"phaseText,omitempty"`
	// Log 最近 60 行原始输出（v2.1.81 排查用日志区：前端渲染「应用名」输出（虚拟屏））。
	Log []string `json:"log,omitempty"`
}

// appWinState 是一路应用窗口会话的运行时状态。
// closing 由 a.mu 保护（快照读出给前端）。
type appWinState struct {
	serial        string
	identity      string
	identityCheck *wirelessIdentityCheck
	pkg           string
	name          string
	mode          string // 启动时连接形态（usb/wifi）
	phase         string // 转换/重连中的动态状态（bridge.Kind 字符串；v2.1.54）
	phaseText     string // 卡片状态文字（""=默认"正在窗口"）
	runner        Runner
	startedAt     time.Time
	closing       bool
	restarting    bool // 参数重启间隙（v2.1.70）：条目原地保留（卡片不消失），runner 释放后原地替换
	// log 是最近 60 行原始输出（v2.1.81：排查用日志区——与主投屏 CastState.Log 同模式，
	// 快照随 AppWinItem.Log 带出；前端渲染「应用名」输出（虚拟屏）模块）。
	log []string
}

// devPhys 是设备物理参数（虚拟屏 dpi 等比公式的输入）。
type devPhys struct {
	longSide int       // 主屏长边（px，wm size）
	dpi      int       // 主屏物理密度（wm density）
	at       time.Time // 查询时间（长 TTL：机型参数运行期不变）
}

const (
	appWinVdW         = 1280 // 默认虚拟屏宽（总纲默认档 1280x720）
	appWinVdH         = 720
	appWinVdFPS       = 60 // 默认刷新率上限（bat 无值默认同值）
	appWinVdBIT       = 8  // 默认码率上限 Mbps（bat 无值默认同值）
	appWinPhysTTL     = time.Hour
	appWinPhysTimeout = 5 * time.Second
	// appWinStopFallback 停止兜底：runner.Stop 返回后等 onExit 摘除的上限
	// （Stop 内含同步清理，正常远早于此；onExit 极端丢失时才兜底强摘）。
	appWinStopFallback = 10 * time.Second
)

func appWinKey(serial, pkg string) string { return serial + "#" + pkg }

// StartAppWin 启动应用窗口（幂等：同 serial#pkg 已在运行 → nil）。
// 走 bat（Runner）：参数=虚拟屏默认档（1280x720 + 等比 dpi + flex + IME=local
// + --start-app=+pkg）+ 设备锁定注入；bat 侧组装完整投屏参数（uhid/渲染/自愈）。
func (a *App) StartAppWin(serial, pkg, name string) error {
	unlockUpdate, updateErr := a.guardUpdateStart()
	if updateErr != nil {
		return updateErr
	}
	defer unlockUpdate()
	if serial == "" || pkg == "" {
		return fmt.Errorf("参数不完整（serial/pkg 为空）")
	}
	key := appWinKey(serial, pkg)
	lookupKey := serial
	a.mu.Lock()
	if old := a.appWins[key]; old != nil && old.restarting && old.identity != "" {
		lookupKey = old.identity
	}
	// v2.1.70：restarting 条目（参数重启间隙）放行——下方原地替换（照主投屏重启模式）。
	if old := a.appWins[key]; old != nil && !old.restarting {
		a.mu.Unlock()
		bridge.DebugLog("[appwin] 已在运行 serial=%s pkg=%s（幂等）", serial, pkg)
		return nil
	}
	a.mu.Unlock()

	if a.newR == nil {
		return errors.New("bat 桥接未初始化")
	}

	// 设备锁定注入（USB 优先 / 无线地址+备用 / 身份防抢——与 StartCast 同链）。
	params := a.appWinLockParams(lookupKey)
	identity := a.appListKeyFor(lookupKey)
	if params.Serial == "" && params.Addr == "" {
		return errors.New("设备当前没有可用连接地址")
	}
	// v2.1.91：编码格式随档案两套注入（独立于 Set——应用窗口与主投屏同源）。
	// v2.1.95：应用级优先，空则继承设备档案（应用没设置过 → 跟随设备档案）。
	lp := a.profiles.Get(identity)
	ap, hasArchive := a.appWinParamsFor(identity, pkg)
	pickV := func(appV, devV string) string {
		if strings.TrimSpace(appV) != "" {
			return NormalizeVCodec(appV)
		}
		return NormalizeVCodec(devV)
	}
	pickA := func(appV, devV string) string {
		if strings.TrimSpace(appV) != "" {
			return NormalizeACodec(appV)
		}
		return NormalizeACodec(devV)
	}
	params.Usb.VCodec = pickV(ap.Usb.VCodec, lp.Usb.VCodec)
	params.Usb.ACodec = pickA(ap.Usb.ACodec, lp.Usb.ACodec)
	params.Wifi.VCodec = pickV(ap.Wifi.VCodec, lp.Wifi.VCodec)
	params.Wifi.ACodec = pickA(ap.Wifi.ACodec, lp.Wifi.ACodec)

	// v2.1.47：连接形态（档案套选择/面板标题用；启动时判定——USB transport 在线
	// 时 bat 首轮走 USB）。
	mode := "wifi"
	if params.Serial != "" {
		mode = "usb"
	}

	// 虚拟屏参数（二期 Step 4/5）：档案（无条目=默认档）→ 两套注入（_USB/_WIFI）。
	// 等比 dpi 公式 §1.8 = 主屏dpi × 虚拟屏长边 ÷ 主屏长边（按各套尺寸现算）。
	// v2.1.32：只读缓存（命中即注入 dpi）；未命中不阻塞——后台预热（下次开窗命中），
	// 本次不注入（scrcpy flex 默认 160）。StartAppWin 跑在 GUI 消息循环线程上，
	// 同步 adb 查询会冻结整个界面（实测"启动虚拟屏时 GUI 卡一下、无法操作"）。
	// （ap/hasArchive 已在编码注入处提前取得。）
	phys := a.devicePhysCached(identity)
	if phys.dpi == 0 {
		bridge.DebugLog("[appwin] 物理参数未缓存 serial=%s（本次不注入 dpi，已触发后台预热）", serial)
		if a.appListKeyFor(serial) == identity {
			go a.guard("appwin-phys-warm", func() { a.devicePhys(serial) })
		}
	}
	// v2.1.74：档案 Size=长边 → 按设备宽高比换算 WxH 注入（native=内存读，无慢 IO）。
	native := a.nativeRes(identity)
	params.VdUsb = vdParamsToBridge(ap.Usb, phys, native)
	params.VdWifi = vdParamsToBridge(ap.Wifi, phys, native)
	// 旧单套字段同步注入（启动形态那套）：新旧 bat 组合兼容（旧 bat 读 SCEZ_VD_SIZE 等）。
	cur := params.VdUsb
	if mode == "wifi" {
		cur = params.VdWifi
	}
	params.VdSize = cur.Size
	params.VdDpi = cur.Dpi
	params.VdFlex = cur.Flex
	params.VdAudio = cur.Audio
	params.VdIme = "local"
	params.VdNoDecor = a.appWinNoSystemDecorations(identity)
	params.StartApp = "+" + pkg // "+"=先强停再启动（保完整形态 + 干净，总纲 §1.4）
	params.WinTitle = sanitizeWinTitle(name)

	// 参数控件启动可见性（v2.1.56）：跟随全局设置（与主投屏 StartCast 同链）。
	// 此前虚拟屏未注入 SCEZ_PARAM_OVERLAY → scrcpy 客户端回退历史默认=可见，
	// 表现为"GUI 设置里关了参数控件，主投屏不显示、应用窗口却默认开着"。
	// 显式注入（Set=true）才能覆盖客户端默认；投屏中 Ctrl+F 手动切换不受影响。
	settings := a.settings.Get()
	params.OverlayVisible = settings.ShowParamOverlay
	params.OverlayVisibleSet = true
	// App windows work independently of the phone's lock screen.
	params.KeepDeviceAwake = false
	params.KeepDeviceAwakeSet = true

	// 会话级 runner（bat 实例）：回调按 serial#pkg 绑定，App 侧按 runner 身份防串
	// （陈旧 runner 的迟到回调不污染替换后的新会话）。
	var r Runner
	r, err := a.newR(serial,
		func(line string) { a.onAppWinLine(serial, pkg, line) },
		func(code int) { a.onAppWinExit(serial, pkg, r, code) })
	if err != nil {
		return fmt.Errorf("创建 bat 会话失败：%w", err)
	}
	// 创建 runner 期间也可能收到新地址，启动前再读取同一档案入口。
	if a.appListKeyFor(lookupKey) != identity {
		return errors.New("设备身份已变化，请重新选择设备")
	}
	locked := a.appWinLockParams(lookupKey)
	params.Serial, params.Addr, params.Addr2 = locked.Serial, locked.Addr, locked.Addr2
	params.ExpectedSerial = locked.ExpectedSerial
	params.Market, params.Model = locked.Market, locked.Model
	mode = "wifi"
	if params.Serial != "" {
		mode = "usb"
	}
	if err := r.Start(serial, params); err != nil {
		return fmt.Errorf("启动 bat 失败：%w", err)
	}

	st := &appWinState{serial: serial, identity: identity, pkg: pkg, name: name, mode: mode, runner: r, startedAt: time.Now()}
	a.mu.Lock()
	st.identityCheck = a.wirelessIdentityChecks[params.Addr]
	// v2.1.70：重启间隙原地替换——沿用 startedAt 保持卡片位置/顺序（照主投屏 restarting 模式）。
	if old := a.appWins[key]; old != nil && old.restarting {
		st.startedAt = old.startedAt
	}
	a.appWins[key] = st
	a.mu.Unlock()
	bridge.DebugLog("[appwin] 启动 serial=%s pkg=%s mode=%s 档案=%v usb=[%s %dfps %dM flex=%v audio=%s dpi=%d %s/%s] wifi=[%s %dfps %dM flex=%v audio=%s dpi=%d %s/%s]（主屏 %dpx@%ddpi，走 bat）",
		serial, pkg, mode, hasArchive,
		ap.Usb.Size, ap.Usb.FPS, ap.Usb.Bitrate, ap.Usb.Flex, ap.Usb.EffectiveAppWinAudio(), params.VdUsb.Dpi, params.Usb.VCodec, params.Usb.ACodec,
		ap.Wifi.Size, ap.Wifi.FPS, ap.Wifi.Bitrate, ap.Wifi.Flex, ap.Wifi.EffectiveAppWinAudio(), params.VdWifi.Dpi, params.Wifi.VCodec, params.Wifi.ACodec,
		phys.longSide, phys.dpi)
	bridge.DebugLog("[appwin] 虚拟屏系统界面 identity=%s enabled=%v", identity, !params.VdNoDecor)
	// v2.1.50（主人拍板）：本次以默认档开虚拟屏（无自定义档案）→ 首次把推导默认
	// 规格播种入档（该形态 baseline）——之后各处直接读档，不再每次从头推导。
	// 收益：设备离线/富化数据过期时默认值稳定不漂移；档案可见可查。
	if !hasArchive {
		cur := ap.Usb
		if mode == "wifi" {
			cur = ap.Wifi
		}
		a.seedAppWinBaseline(identity, mode, cur)
	}
	return nil
}

// onAppWinLine 解析应用窗口 bat 输出行（v2.1.51 起改用统一分类器 bridge.ClassifyLine，
// 语义对照主投屏 applyEventLocked——"closing 置位后必须能被"投屏继续"信号解除"）：
//   - KindUserClose（SCRCPY_EZ_USER_CLOSE 哨兵）/ KindDone（"已检测到窗口关闭"）：
//     置 closing —— 用户手关窗口 → 卡片"正在关闭…"遮罩，bat 退出后摘除淡出；
//   - KindSpec（[高清]/[流畅]/[custom] 新规格行）：**解除 closing** —— 切换完成的新
//     分支=投屏继续不是关闭（自动切有线后"正在关闭…"立即复位，不再永久卡住——与
//     主投屏同注释）；同时更新会话形态（usb/wifi → 面板标题/档案套跟随）+ 规格入档
//     （非 custom：虚拟屏投屏也算"实测一次"，与主屏共享同一 baseline）；
//   - KindSwitchUSB/KindReconnect/KindADBReset/KindDetect（切换/重连类）：**解除 closing**
//     （同一会话进入新阶段）。
//
// 真正摘除只由 onAppWinExit（bat 退出）执行。
func (a *App) onAppWinLine(serial, pkg, line string) {
	bridge.DebugLog("[appwin:%s] %s", pkg, line)
	ev := bridge.ClassifyLine(line)
	key := appWinKey(serial, pkg)
	a.mu.Lock()
	st := a.appWins[key]
	if st == nil {
		a.mu.Unlock()
		return
	}
	// v2.1.81：原始输出缓冲（任何行，含未分类行——与主投屏 applyEventLocked 同模式）
	st.log = append(st.log, ev.Text)
	if len(st.log) > 60 {
		st.log = st.log[len(st.log)-60:]
	}
	switch ev.Kind {
	case bridge.KindUserClose:
		// v2.1.72（照抄主投屏卡片设计）：参数重启间隙的停止输出不算"用户关窗"——
		// 重启期间客户端退出/窗口关闭是流程的一部分，卡片保持「重新连接中…」。
		if !st.closing && !st.restarting {
			st.closing = true
			bridge.DebugLog("[appwin] 用户关窗哨兵 → closing serial=%s pkg=%s", serial, pkg)
		}
	case bridge.KindDone:
		if strings.Contains(line, "已检测到窗口关闭") && !st.closing && !st.restarting {
			st.closing = true
			bridge.DebugLog("[appwin] 窗口关闭确认行 → closing serial=%s pkg=%s", serial, pkg)
		}
	case bridge.KindSpec:
		if st.closing {
			st.closing = false
			bridge.DebugLog("[appwin] 新规格行 → 解除 closing serial=%s pkg=%s（切换/重连=投屏继续）", serial, pkg)
		}
		if ev.Spec != nil {
			m := "wifi"
			if ev.Spec.Wired {
				m = "usb"
			}
			if st.mode != m {
				st.mode = m
				bridge.DebugLog("[appwin] 会话形态更新 → %s serial=%s pkg=%s", m, serial, pkg)
			}
		}
	case bridge.KindSwitchUSB, bridge.KindReconnect, bridge.KindADBReset, bridge.KindDetect:
		if st.closing {
			st.closing = false
			bridge.DebugLog("[appwin] 切换/重连 → 解除 closing serial=%s pkg=%s", serial, pkg)
		}
	}
	// 卡片动态状态（v2.1.54）：转换/重连期间副行显示阶段文字（与主投屏 phaseText
	// 同款措辞——主人拍板"用主投屏插拔转换时的文字"）；投屏继续类事件清空回
	// 默认"正在窗口"（新规格/开始投屏/监测开启/纹理就绪 = 本轮转换已走完）。
	if txt, ok := appWinPhaseText(ev.Kind); ok {
		txt = bridge.RootPhaseText(ev, txt)
		if st.phaseText != txt {
			st.phase, st.phaseText = ev.Kind.String(), txt
		}
	} else if appWinPhaseClear(ev.Kind) {
		if st.phaseText != "" {
			st.phase, st.phaseText = "", ""
		}
	}
	if ev.Kind == bridge.KindWifiOK || ev.Kind == bridge.KindTexture {
		st.identity = a.bindPendingCastIdentityLocked(st.identity, &st.identityCheck, st.serial)
		a.retryWirelessIdentityLocked(st.identityCheck)
	}
	identity := st.identity
	a.mu.Unlock()
	// 规格入档（锁外；updateBaseline 自行取锁）：虚拟屏投屏也算一次"实测"——
	// 与主屏投屏共享同一 baseline（主人："主屏虚拟屏都行"）。[custom] 回显不更新。
	// serial 用归一键（无线 IP:port 形态落进设备主档案，不产生孤儿档）。
	if ev.Kind == bridge.KindSpec && ev.Spec != nil && !ev.Spec.Custom {
		m := "wifi"
		if ev.Spec.Wired {
			m = "usb"
		}
		if identity == "" {
			identity = a.appListKeyFor(serial)
		}
		a.updateBaseline(identity, m, Baseline{Res: ev.Spec.MaxSize, FPS: ev.Spec.FPS, Bitrate: ev.Spec.Mbps})
	}
}

// appWinPhaseText 是应用窗口版的事件 → 卡片状态文字映射（v2.1.54）。
// 措辞与主投屏 phaseText 同款（"主投屏插拔转换时的文字"——主人拍板）；
// 差异：①投屏继续/常态类事件不在此列（KindCasting/KindSpec/KindWatchOn 由
// appWinPhaseClear 清空回默认"正在窗口"）；②KindVDCreating 是应用窗口特有的
// 虚拟屏启动步骤（server push → 建虚拟屏 → 拉起应用的可见化）。
// ok=false = 该事件不设置状态文字。
func appWinPhaseText(k bridge.Kind) (string, bool) {
	switch k {
	case bridge.KindRootRequired:
		return "上传权限受阻；已有 root 的设备可启用修复", true
	case bridge.KindRootPrepare:
		return "root 诊断与修复中（授权最多 3 分钟，可停止）", true
	case bridge.KindADBReset:
		return "正在准备 adb…", true
	case bridge.KindDetect:
		return "正在扫描设备…", true
	case bridge.KindUSBFound:
		return "已找到 USB 设备", true
	case bridge.KindUSBHint:
		return "检测到 USB 设备未授权，请解锁手机点击允许", true
	case bridge.KindNoUSB:
		return "未检测到 USB 设备，尝试无线连接", true
	case bridge.KindWifiTry:
		return "尝试连接上次的无线设备…", true
	case bridge.KindWifiOK:
		return "无线设备已连接", true
	case bridge.KindWifiFail:
		return "无线连接失败，正在扫描其他设备…", true
	case bridge.KindScanWifi:
		return "扫描已有无线设备…", true
	case bridge.KindGuide:
		return "首次连接向导", true
	case bridge.KindLearning:
		return "正在学习无线信息（约 15 秒）…", true
	case bridge.KindSwitchUSB:
		return "检测到 USB 插线，切换有线投屏…", true
	case bridge.KindReconnect:
		return "连接断开，自动重连中…", true
	case bridge.KindVDCreating:
		return "正在启动虚拟屏…", true
	case bridge.KindRetryWait:
		return "连续投屏失败，可重新投屏", true
	}
	return "", false
}

// appWinPhaseClear 判断该事件是否把卡片状态清回默认"正在窗口"：
// 投屏继续/已就绪类信号（开始投屏、新规格行、插线监测开启、纹理就绪）。
func appWinPhaseClear(k bridge.Kind) bool {
	switch k {
	case bridge.KindCasting, bridge.KindSpec, bridge.KindWatchOn, bridge.KindTexture:
		return true
	}
	return false
}

// onAppWinExit 是应用窗口 bat 会话的退出回调（bridge 层 waitLoop 触发）：
// 从活动列表摘除 → 快照少一条 → 前端卡片淡出（含"用户手关窗口"与"停止完成"两场景）。
// r 身份防串：陈旧 runner 的迟到回调不误删替换后的新会话。
func (a *App) onAppWinExit(serial, pkg string, r Runner, code int) {
	key := appWinKey(serial, pkg)
	bridge.DebugLog("[diag] onAppWinExit 进入（等锁）serial=%s pkg=%s code=%d", serial, pkg, code)
	a.mu.Lock()
	bridge.DebugLog("[diag] onAppWinExit 已持锁")
	st := a.appWins[key]
	if st != nil && (r == nil || st.runner == r) {
		// v2.1.70（主人拍板）：参数重启间隙——条目原地保留（卡片不消失），只释放
		// runner；新会话由 RestartAppWin 的守卫 goroutine 原地替换。用户关闭/异常
		// 退出等其他路径照旧摘除。
		if st.restarting {
			st.runner = nil
			// v2.1.72：停止流程的输出行可能把 closing 误标（"已检测到窗口关闭"）——
			// bat 退出即清掉，卡片回「重新连接中…」（重启继续，不是关闭）。
			st.closing = false
			st.phase = "restarting"
			st.phaseText = "重新连接中…"
		} else {
			delete(a.appWins, key)
		}
	}
	a.mu.Unlock()
	bridge.DebugLog("[appwin] 会话退出 serial=%s pkg=%s code=%d", serial, pkg, code)
}

// StopAppWin 停止应用窗口（与主投屏 StopCast 同构的"受理→清理→摘除"）：
//   - 同步段只置 closing（快照透传 → 前端"正在关闭…"遮罩；重复点击幂等）；
//   - 异步段 runner.Stop()（防重连标记 + 优雅关窗 + 整树杀 + watcher 清理）；
//   - bat 退出 → onAppWinExit 摘除（前端淡出）；Stop 失败复位 closing（可重试）；
//   - 兜底：Stop 成功但 onExit 极端丢失 → 超时强摘（防卡片卡死）。
func (a *App) StopAppWin(serial, pkg string) error {
	key := appWinKey(serial, pkg)
	a.mu.Lock()
	st := a.appWins[key]
	if st == nil {
		a.mu.Unlock()
		bridge.DebugLog("[appwin] 停止：无此会话（幂等）serial=%s pkg=%s", serial, pkg)
		return nil
	}
	if st.closing {
		a.mu.Unlock()
		return nil // 停止已受理：重复点击幂等（前端同步禁用）
	}
	st.closing = true
	st.restarting = false // v2.1.70：显式停止覆盖重启间隙（onExit 按关闭路径摘除；重启守卫不重开）
	st.phase = ""
	st.phaseText = ""
	r := st.runner
	// v2.1.75：设备级单通知——本设备还有其它会话时跳过"等通知撤下"（通知不会被撤）。
	others := a.otherSessionIDsLocked("", key)
	a.mu.Unlock()
	if r != nil {
		r.SetSkipNotifWait(a.anyOnSameDevice(serial, others))
	}
	bridge.DebugLog("[appwin] 停止受理 serial=%s pkg=%s（closing）", serial, pkg)

	go a.guard("appwin-stop", func() {
		// v2.1.70：重启间隙 runner 已释放（条目保留）——无进程可停，直接摘除收尾。
		if st.runner == nil {
			a.mu.Lock()
			if cur := a.appWins[key]; cur == st {
				delete(a.appWins, key)
			}
			a.mu.Unlock()
			bridge.DebugLog("[appwin] 停止：重启间隙无 runner，直接摘除 serial=%s pkg=%s", serial, pkg)
			return
		}
		if err := st.runner.Stop(); err != nil {
			// Stop 失败（如 bat 未在运行）：复位按钮可重试；会话状态不变。
			a.mu.Lock()
			if cur := a.appWins[key]; cur == st {
				cur.closing = false
			}
			a.mu.Unlock()
			bridge.DebugLog("[appwin] 停止失败 serial=%s pkg=%s: %v（closing 复位，可重试）", serial, pkg, err)
			return
		}
		// Stop 返回 → bat 进程已清（onExit 摘除）；兜底超时强摘（一次性定时）。
		go a.guard("appwin-stop-timeout", func() {
			time.Sleep(appWinStopFallback)
			a.mu.Lock()
			if cur := a.appWins[key]; cur == st {
				delete(a.appWins, key)
				bridge.DebugLog("[appwin] 停止兜底：onExit 未达，强制摘除 serial=%s pkg=%s", serial, pkg)
			}
			a.mu.Unlock()
		})
	})
	return nil
}

// appWinLockParams 组装应用窗口的设备锁定注入（与 StartCast 同链，单设备视角）：
//   - USB transport 在线 → SCEZ_SERIAL（bat 首轮即走 USB——USB 物理连接必通）；
//   - 无线地址（广播活跃 → 档案顺序兜底 → 无线上目标卡地址）→ SCEZ_ADDR + 异形态备用 SCEZ_ADDR2；
//   - 档案身份 → SCEZ_MARKET/SCEZ_MODEL（防抢锁定，wireless 回退时拒绝异身份设备）。
//
// 设备不在线（极少：卡片刚消失）→ 按 serial 形态直注，交给 bat 自愈。
func (a *App) appWinLockParams(serial string) bridge.CastParams {
	a.mu.RLock()
	devices := append([]adb.Device(nil), a.devices...)
	a.mu.RUnlock()
	return a.deviceLockParams(serial, devices)
}

// appWinsListLocked 列出全部应用窗口（按启动时间排序；调用方须持 a.mu）。
func (a *App) appWinsListLocked() []AppWinItem {
	states := make([]*appWinState, 0, len(a.appWins))
	for _, st := range a.appWins {
		states = append(states, st)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].startedAt.Before(states[j].startedAt) })
	out := make([]AppWinItem, 0, len(states))
	for _, st := range states {
		out = append(out, AppWinItem{Serial: st.serial, Identity: st.identity, Pkg: st.pkg, Name: st.name, Mode: st.mode, Closing: st.closing, Phase: st.phase, PhaseText: st.phaseText,
			Log: append([]string{}, st.log...)})
	}
	return out
}

// ---------- 设备物理参数（等比 dpi 输入）----------

var (
	rePhysSize = regexp.MustCompile(`(\d+)x(\d+)`)
	rePhysDpi  = regexp.MustCompile(`(?m)^Physical density:\s*(\d+)`)
)

// devicePhys 查询并缓存设备物理参数（wm size 长边 + wm density；长 TTL）。
func (a *App) devicePhys(serial string) devPhys {
	identity := a.appListKeyFor(serial)
	a.physMu.Lock()
	if p, ok := a.physCache[identity]; ok && time.Since(p.at) < appWinPhysTTL {
		a.physMu.Unlock()
		return p
	}
	a.physMu.Unlock()

	// v2.1.84：等 adb 服务就绪（缓存路径已排除——真正的 adb 查询不落在抢庄窗口里）
	a.waitSrvReady(context.Background())
	transport := a.bestEnumSerial(identity, serial)

	p := devPhys{at: time.Now()}
	// wm size 偶发失败（无线 adb 慢/设备忙——实测"长边=0"→ dpi 未注入跑 160）：
	// 失败重试一次（短间隔）；仍失败则不缓存 longSide（下次启动重查）。
	for attempt := 0; attempt < 2 && p.longSide == 0; attempt++ {
		if attempt > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		if out, err := a.adbShell(transport, "wm", "size"); err == nil {
			if m := rePhysSize.FindStringSubmatch(out); m != nil {
				w, _ := strconv.Atoi(m[1])
				h, _ := strconv.Atoi(m[2])
				if w > h {
					p.longSide = w
				} else {
					p.longSide = h
				}
			}
		}
	}
	// wm density 查询（同上：偶发失败重试一次；仍失败不缓存，下次启动重查）。
	for attempt := 0; attempt < 2 && p.dpi == 0; attempt++ {
		if attempt > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		if out, err := a.adbShell(transport, "wm", "density"); err == nil {
			if m := rePhysDpi.FindStringSubmatch(out); m != nil {
				p.dpi, _ = strconv.Atoi(m[1])
			}
		}
	}
	if p.longSide > 0 && p.dpi > 0 && identity == a.appListKeyFor(transport) {
		a.physMu.Lock()
		a.physCache[identity] = p
		a.physMu.Unlock()
	}
	bridge.DebugLog("[appwin] 设备物理参数 serial=%s 长边=%d dpi=%d", serial, p.longSide, p.dpi)
	return p
}

// adbShell 执行 adb -s <serial> shell <args...>（5s 超时）。
// v2.1.32：补 HideConsole——GUI 无控制台，直接启动 adb.exe 会新建控制台窗口
// 造成闪窗（"两个命令行窗口未隐藏"实锤：wm size + wm density 各一个）。
func (a *App) adbShell(serial string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), appWinPhysTimeout)
	defer cancel()
	full := append([]string{"-s", serial, "shell"}, args...)
	cmd := exec.CommandContext(ctx, a.cfg.AdbPath, full...)
	adb.HideConsole(cmd) // Windows: CREATE_NO_WINDOW + HideWindow；其他平台空实现
	out, err := cmd.Output()
	return string(out), err
}

// devicePhysCached 纯读设备物理参数缓存（不触发查询；v2.1.32）。
// 用途：StartAppWin 等"必须在 GUI 消息循环线程上快速返回"的路径——慢查询一律
// 交给 devicePhys（后台预热）跑，绝不在此阻塞。
func (a *App) devicePhysCached(serial string) devPhys {
	identity := a.appListKeyFor(serial)
	a.physMu.Lock()
	defer a.physMu.Unlock()
	if p, ok := a.physCache[identity]; ok && time.Since(p.at) < appWinPhysTTL {
		return p
	}
	return devPhys{}
}

// calcVdDpi 等比 dpi 公式（总纲 §1.8）：主屏dpi × 虚拟屏长边 ÷ 主屏长边。
// 返回 0=无法计算（调用方不注入 dpi 参数）。
func calcVdDpi(p devPhys, w, h int) int {
	vdLong := w
	if h > vdLong {
		vdLong = h
	}
	if p.dpi <= 0 || p.longSide <= 0 || vdLong <= 0 {
		return 0
	}
	return int(math.Round(float64(p.dpi) * float64(vdLong) / float64(p.longSide)))
}

// sanitizeWinTitle 窗口标题安全化（总纲 §1.11）：去引号/百分号、空白→下划线。
func sanitizeWinTitle(s string) string {
	if s == "" {
		return "scrcpy-ez"
	}
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, "%", "")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// ---------- 应用窗口参数（二期 Step 4/5：面板保存 + 档案记忆 + 重启生效） ----------

var reVdSize = regexp.MustCompile(`^(\d{2,5})x(\d{2,5})$`)

// parseVdSize 解析 "WxH" 尺寸（面板/档案校验用）；失败返回 0,0。
func parseVdSize(s string) (int, int) {
	m := reVdSize.FindStringSubmatch(s)
	if m == nil {
		return 0, 0
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	return w, h
}

// DefaultAppWinParams 应用窗口回退默认档（无设备上下文时的硬编码兜底）：
// 长边 1280 / 60fps / 8M / flex 开 / 不静音。正常路径走 appWinDefaults（共享主投屏）。
// v2.1.74：Size=长边数字（短边由 mdns10ProfileRes 按设备宽高比换算 WxH 注入）。
func DefaultAppWinParams() AppWinParams {
	m := AppWinModeParams{Size: strconv.Itoa(appWinVdW), FPS: appWinVdFPS, Bitrate: appWinVdBIT, Flex: true}
	return AppWinParams{Usb: m, Wifi: m}
}

// seedAppWinBaseline v2.1.50（主人拍板「算一次入档，之后直接取」）：首次以默认档
// 打开虚拟屏时，把推导出的默认规格播种进档案 baseline——此后 appWinDefaults 直接
// 读档（不再依赖设备富化数据现算；离线/过期待机时默认值稳定不漂移）。
//   - 仅当该形态 baseline 为空（从未记录）时写一次（幂等；已有值=实测或更权威，不覆盖）；
//   - key 用 appListKeyFor 归一（无线 IP:port 形态落进设备主档案，不产生孤儿档）；
//   - 主屏投屏的实测规格行（updateBaseline）更权威，后到自然覆盖。
func (a *App) seedAppWinBaseline(serial, mode string, m AppWinModeParams) {
	if m.Size == "" {
		return
	}
	le := ParseLongEdge(m.Size)
	if le <= 0 {
		return
	}
	key := a.appListKeyFor(serial)
	p := a.profiles.Get(key)
	changed := false
	if mode == "usb" {
		if p.Usb.Baseline.Res == 0 {
			p.Usb.Baseline = Baseline{Res: le, FPS: m.FPS, Bitrate: m.Bitrate}
			changed = true
		}
	} else {
		if p.Wifi.Baseline.Res == 0 {
			p.Wifi.Baseline = Baseline{Res: le, FPS: m.FPS, Bitrate: m.Bitrate}
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := a.profiles.Save(key, p); err != nil {
		bridge.DebugLog("[appwin] 默认规格播种失败 key=%s mode=%s: %v", key, mode, err)
		return
	}
	bridge.DebugLog("[appwin] 默认规格已入档 key=%s mode=%s 长边=%d %dfps %dM（首次以默认档开虚拟屏）",
		key, mode, le, m.FPS, m.Bitrate)
}

// appWinDefaults 应用窗口默认档（v2.1.48 主人拍板：**共享主投屏默认规格**）。
// 来源 = effectiveProfile 的动态 baseline（已投屏=bat 规格行实测值；未投屏=
// adb 推导：有线=设备长边/刷新率、无线=1920/60/15）——与主投屏参数浮窗同一份
// 权威数据；尺寸=长边数字（v2.1.74；WxH 注入时按设备宽高比现算）。
// baseline 全缺时由 vdModeFromBaseline 回退硬编码档。
func (a *App) appWinDefaults(serial string) AppWinParams {
	prof := a.effectiveProfile(serial)
	return AppWinParams{
		Usb:  vdModeFromBaseline(prof.Usb.Baseline),
		Wifi: vdModeFromBaseline(prof.Wifi.Baseline),
	}
}

// nativeRes 设备原生分辨率（宽≥高 "WxH"）：档案持久化优先 → 设备流富化兜底 →
// 空（空则 mdns10ProfileRes 按 16:9 兜底）。与 mdns10DecorateSpecs 同链。
func (a *App) nativeRes(serial string) string {
	key := a.appListKeyFor(serial)
	if e, ok := a.profiles.Entry(key); ok && e.Res != "" {
		return e.Res
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for i := range a.devices {
		d := &a.devices[i]
		if a.identityOf(d) == key {
			if d.Res != "" {
				return d.Res
			}
			if d.WirelessRes != "" {
				return d.WirelessRes
			}
		}
	}
	return ""
}

// vdModeFromBaseline 把主投屏单模式基线（长边/fps/码率）转成应用窗口单套默认档。
// v2.1.74：Size=长边数字（与主投屏参数面板同口径；WxH 在注入时按设备宽高比现算）。
func vdModeFromBaseline(b Baseline) AppWinModeParams {
	le := b.Res
	if le <= 0 {
		le = appWinVdW
	}
	fps := b.FPS
	if fps <= 0 {
		fps = appWinVdFPS
	}
	bit := b.Bitrate
	if bit <= 0 {
		bit = appWinVdBIT
	}
	return AppWinModeParams{Size: strconv.Itoa(le), FPS: fps, Bitrate: bit, Flex: true, Audio: "phone"}
}

// normalizeAppWinParams 把档案条目里的"未设置套"（Size 空）填为默认档（def=
// 设备默认档，调用方由 appWinDefaults 取得）；同时把 Size 归一化为长边数字格式
// （v2.1.74：旧档案存 "WxH"——读取即迁移，前端/回写全程只见新格式）。
func normalizeAppWinParams(p AppWinParams, def AppWinParams) AppWinParams {
	p.Usb = normalizeAppWinMode(p.Usb, def.Usb)
	p.Wifi = normalizeAppWinMode(p.Wifi, def.Wifi)
	return p
}

// normalizeAppWinMode 单套归一：空=默认档；Size 用 ParseLongEdge 取长边 → 数字字符串
// （解析失败保持原样——防御损坏数据，不在此处丢字段）；v2.1.78 声音档位输出最终值
// （Empty 套走 def 后也需归一，故不再直接 return def）。
func normalizeAppWinMode(m, def AppWinModeParams) AppWinModeParams {
	if m.Empty() {
		m = def
	} else if le := ParseLongEdge(m.Size); le > 0 {
		m.Size = strconv.Itoa(le)
	}
	m.Audio = m.EffectiveAppWinAudio()
	return m
}

// compactAppWinMode 把"等于默认档"的套清为零值（未设置）——落盘更干净；
// 行为等价（读取时零值套回默认档）。def=设备默认档（共享主投屏规格）。
func compactAppWinMode(p AppWinModeParams, def AppWinModeParams) AppWinModeParams {
	if p.Empty() {
		return p
	}
	if p == def {
		return AppWinModeParams{}
	}
	return p
}

// appWinParamsFor 读单应用窗口参数（设备身份归一；无条目=默认档）。
// 归一用 appListKeyFor（设备列表→identity，覆盖无线地址/TLS 端口等形态）；
// 返回 hasArchive=档案里有该应用条目（诊断日志用）。
func (a *App) appWinParamsFor(serial, pkg string) (AppWinParams, bool) {
	def := a.appWinDefaults(serial)
	if e, ok := a.profiles.Entry(a.appListKeyFor(serial)); ok {
		if p, ok2 := e.AppParams[pkg]; ok2 {
			return normalizeAppWinParams(p, def), true
		}
	}
	return def, false
}

// vdParamsToBridge 把档案套转为注入参数：
//   - v2.1.74：档案 Size=长边数字 → 按设备宽高比换算完整 "WxH"（虚拟屏注入仍要 WxH；
//     换算照抄 mdns10ProfileRes——与卡片规格/baseline 同一函数，短边向下取偶）；
//   - dpi：档案自定义值优先；未自定义=按换算后尺寸等比现算；phys 未缓存且未自定义=0 不注入。
func vdParamsToBridge(p AppWinModeParams, phys devPhys, native string) bridge.VdModeParams {
	le := ParseLongEdge(p.Size)
	if le <= 0 {
		le = appWinVdW // 数据损坏兜底（normalize 后不可达）
	}
	size := appWinInitialSize(native, le, p.RatioW, p.RatioH)
	w, h := parseVdSize(size)
	dpi := p.Dpi
	if dpi <= 0 {
		dpi = calcVdDpi(phys, w, h)
	}
	return bridge.VdModeParams{
		Size:        size,
		Dpi:         dpi,
		FPS:         p.FPS,
		Bitrate:     p.Bitrate,
		Flex:        p.Flex,
		Audio:       p.EffectiveAppWinAudio(),
		LockFps:     p.LockFps,
		LockBitrate: p.LockBitrate,
	}
}

// appWinInitialSize 以现有长边像素数为基准生成虚拟屏尺寸。比例未设置时走原有
// 设备比例算法，保证旧档案和恢复默认的行为完全一致；自定义比例保留 W:H 方向。
func appWinInitialSize(native string, longEdge, ratioW, ratioH int) string {
	if longEdge <= 0 || ratioW <= 0 || ratioH <= 0 {
		return mdns10ProfileRes(native, longEdge)
	}
	short := longEdge * min(ratioW, ratioH) / max(ratioW, ratioH)
	if short > 2 {
		short &^= 1 // 与现有虚拟屏编码尺寸一致，短边向下取偶
	} else if short < 2 {
		short = 2
	}
	if ratioW >= ratioH {
		return strconv.Itoa(longEdge) + "x" + strconv.Itoa(short)
	}
	return strconv.Itoa(short) + "x" + strconv.Itoa(longEdge)
}

// AppWinParamsView 是 GetAppWinParams 返回（前端面板初始化：两套 + 当前形态）。
type AppWinParamsView struct {
	Usb  AppWinModeParams `json:"usb"`
	Wifi AppWinModeParams `json:"wifi"`
	// UsbDef/WifiDef = 设备默认档（v2.1.48 共享主投屏 baseline 换算）——前端
	// 「重置/恢复默认」目标值 + 档位序列构建（buildTiers）用。
	UsbDef  AppWinModeParams `json:"usbDef"`
	WifiDef AppWinModeParams `json:"wifiDef"`
	// UsbSet/WifiSet = 该套是否有自定义档案（normalize 前原始判据）——前端
	// 「恢复默认」显隐与打开推断（infer）用；两套数据本身已归一为默认档。
	UsbSet  bool   `json:"usbSet"`
	WifiSet bool   `json:"wifiSet"`
	Mode    string `json:"mode"` // 当前连接形态（usb/wifi；无会话=usb 兜底）
	// PhysDpi/PhysLong = 主屏物理参数（0=未缓存）——前端算「自动」档换算值
	// （界面密度显示「自动 ≈NNN」）用；未缓存时前端只显示「自动」（本 RPC 不阻塞，
	// 触发后台预热，下次打开即有值）。
	PhysDpi  int `json:"physDpi"`
	PhysLong int `json:"physLong"`
	// v2.1.95：编码生效值（应用级 ?? 设备级 ?? 默认）——窗口设置 chips 显示用。
	// 与 Usb/Wifi 内的 VCodec 字段（应用级原值，空=未设置，前端 own 判据）区分。
	UsbVCodec  string `json:"usbVCodec"`
	UsbACodec  string `json:"usbACodec"`
	WifiVCodec string `json:"wifiVCodec"`
	WifiACodec string `json:"wifiACodec"`
}

// GetAppWinParams 读应用窗口参数（档案或默认档）+ 当前形态（面板标题用）。
func (a *App) GetAppWinParams(serial, pkg string) (AppWinParamsView, error) {
	if serial == "" || pkg == "" {
		return AppWinParamsView{}, errors.New("参数不完整（serial/pkg 为空）")
	}
	identity := a.appWinProfileKey(serial, pkg)
	var raw AppWinParams
	if e, ok := a.profiles.Entry(identity); ok {
		if pr, ok2 := e.AppParams[pkg]; ok2 {
			raw = pr
		}
	}
	def := a.appWinDefaults(identity)
	ap := normalizeAppWinParams(raw, def)
	// 主屏物理参数（只读缓存——本 RPC 跑 GUI 消息循环线程，禁慢 IO）：未缓存时
	// 返回 0（前端只显示「自动」）+ 后台预热（下次打开面板即可显示换算值）。
	phys := a.devicePhysCached(identity)
	if phys.dpi == 0 && a.appListKeyFor(serial) == identity {
		go a.guard("appwin-phys-warm", func() { a.devicePhys(serial) })
	}
	// v2.1.95：编码生效值（应用级 ?? 设备级 ?? 默认）——chips 显示用。
	lp := a.profiles.Get(identity)
	effVC := func(appV, devV string) string {
		if strings.TrimSpace(appV) != "" {
			return NormalizeVCodec(appV)
		}
		return NormalizeVCodec(devV)
	}
	effAC := func(appV, devV string) string {
		if strings.TrimSpace(appV) != "" {
			return NormalizeACodec(appV)
		}
		return NormalizeACodec(devV)
	}
	v := AppWinParamsView{
		Usb: ap.Usb, Wifi: ap.Wifi,
		UsbDef: def.Usb, WifiDef: def.Wifi,
		UsbSet: !raw.Usb.Empty(), WifiSet: !raw.Wifi.Empty(),
		Mode:       "usb",
		PhysDpi:    phys.dpi,
		PhysLong:   phys.longSide,
		UsbVCodec:  effVC(raw.Usb.VCodec, lp.Usb.VCodec),
		UsbACodec:  effAC(raw.Usb.ACodec, lp.Usb.ACodec),
		WifiVCodec: effVC(raw.Wifi.VCodec, lp.Wifi.VCodec),
		WifiACodec: effAC(raw.Wifi.ACodec, lp.Wifi.ACodec),
	}
	a.mu.RLock()
	if st := a.appWins[appWinKey(serial, pkg)]; st != nil && st.mode != "" {
		v.Mode = st.mode
	}
	a.mu.RUnlock()
	return v, nil
}

// SaveAppWinParams 保存应用窗口参数（payload=JSON：{"mode":"usb|wifi","size":"2560",
// "fps":n,"bitrate":n,"dpi":n,"flex":bool,"mute":bool}；size=长边像素数，兼容旧 "WxH"
// （取长边；落档统一长边格式）；size 空=恢复默认该套；dpi 0=自动等比（不自定义））+ 重启会话。
// 只改当前形态那一套（另一套保留档案原值）；两套都等于默认档 → 条目删除（落盘干净）。
func (a *App) SaveAppWinParams(serial, pkg, payload string) error {
	if serial == "" || pkg == "" {
		return errors.New("参数不完整（serial/pkg 为空）")
	}
	var in struct {
		Mode    string `json:"mode"`
		Size    string `json:"size"`
		RatioW  int    `json:"ratioW"`
		RatioH  int    `json:"ratioH"`
		FPS     int    `json:"fps"`
		Bitrate int    `json:"bitrate"`
		Dpi     int    `json:"dpi"`
		Flex    bool   `json:"flex"`
		Audio   string `json:"audio"`
		// v2.1.80：ABR 锁定（窗口设置「锁定」按钮）
		LockFps     bool `json:"lockFps"`
		LockBitrate bool `json:"lockBitrate"`
		// v2.1.95：编码（空=未设置→启动时继承设备档案；非空=归一固化）
		VCodec string `json:"vcodec"`
		ACodec string `json:"acodec"`
	}
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		return fmt.Errorf("参数解析失败：%w", err)
	}
	if in.Mode != "usb" && in.Mode != "wifi" {
		return fmt.Errorf("模式无效：%q", in.Mode)
	}
	if (in.RatioW == 0) != (in.RatioH == 0) || in.RatioW < 0 || in.RatioH < 0 || in.RatioW > 10000 || in.RatioH > 10000 {
		return fmt.Errorf("初始比例无效：宽高必须同时留空，或均为 1-10000 的整数")
	}
	var np AppWinModeParams
	if s := strings.TrimSpace(in.Size); s != "" {
		// v2.1.74：size=长边像素数（如 "2560"）；兼容旧 "WxH"（取长边）。落档统一长边格式。
		le := ParseLongEdge(s)
		if le <= 0 {
			return fmt.Errorf("尺寸无效：%q（应为长边像素数，如 2560）", s)
		}
		if in.FPS <= 0 || in.FPS > 240 {
			return fmt.Errorf("刷新率超出范围：%d（1-240）", in.FPS)
		}
		if in.Bitrate <= 0 || in.Bitrate > 500 {
			return fmt.Errorf("码率超出范围：%d（1-500 Mbps）", in.Bitrate)
		}
		if in.Dpi < 0 || in.Dpi > 1000 {
			return fmt.Errorf("界面密度超出范围：%d（1-1000；0=自动）", in.Dpi)
		}
		// v2.1.95：编码——空保持空（未设置，启动时继承设备档案）；非空归一（非法→默认）。
		vc := strings.TrimSpace(in.VCodec)
		if vc != "" {
			vc = NormalizeVCodec(vc)
		}
		ac := strings.TrimSpace(in.ACodec)
		if ac != "" {
			ac = NormalizeACodec(ac)
		}
		np = AppWinModeParams{Size: strconv.Itoa(le), RatioW: in.RatioW, RatioH: in.RatioH, FPS: in.FPS, Bitrate: in.Bitrate, Dpi: in.Dpi, Flex: in.Flex, Audio: NormalizeAudioMode(in.Audio, "phone"),
			LockFps: in.LockFps, LockBitrate: in.LockBitrate, VCodec: vc, ACodec: ac}
	}
	identity := a.appWinProfileKey(serial, pkg)
	def := a.appWinDefaults(identity)
	ap, _ := a.appWinParamsFor(identity, pkg)
	if in.Mode == "usb" {
		ap.Usb = np
	} else {
		ap.Wifi = np
	}
	// 落盘前把"等于默认档"的套清为零值（未设置）——条目更干净；读取语义不变
	// （零值套读时回默认档）。两套都过一遍：保存一套时另一套的默认值也不落盘。
	// v2.1.48：比较口径 = 设备默认档（共享主投屏 baseline 换算）——「恢复默认」
	// 之后保存即删条目。
	ap.Usb = compactAppWinMode(ap.Usb, def.Usb)
	ap.Wifi = compactAppWinMode(ap.Wifi, def.Wifi)
	if err := a.profiles.SetAppParams(identity, pkg, ap); err != nil {
		return err
	}
	bridge.DebugLog("[appwin] 参数已保存 serial=%s pkg=%s mode=%s size=%q fps=%d bitrate=%d dpi=%d flex=%v audio=%s vcodec=%q acodec=%q",
		serial, pkg, in.Mode, np.Size, np.FPS, np.Bitrate, np.Dpi, np.Flex, np.Audio, np.VCodec, np.ACodec)
	return a.RestartAppWin(serial, pkg)
}

// RestartAppWin 重启应用窗口会话（保存参数后生效；无会话/已在关闭/已在重启=no-op）。
// v2.1.70（主人拍板）：重启间隙**条目原地保留**（卡片不消失）——照抄主投屏 restarting
// 闩锁模式：置 restarting（卡片显示「重新连接中…」、无按钮）→ Stop → 等 runner 释放
// （onExit 置 nil，条目保留）→ 新会话原地替换（沿用 startedAt 保持卡片位置）。
// 竞态防护：①停止流程受理时清 restarting（onExit 按关闭路径摘除，且下方 proceed
// 检查失败不再重开）；②Start 前在锁内确认条目仍是本 st 且仍处 restarting。
// 异常兜底：15s 未退出 → 强摘（等同旧行为）；Start 失败 → 摘除。
func (a *App) RestartAppWin(serial, pkg string) error {
	key := appWinKey(serial, pkg)
	a.mu.Lock()
	st := a.appWins[key]
	if st == nil || st.closing || st.restarting {
		a.mu.Unlock()
		return nil
	}
	st.restarting = true
	st.phase = "restarting"
	st.phaseText = "重新连接中…"
	name := st.name
	runner := st.runner
	// v2.1.75：重启同样按"是否本设备最后一个会话"设置跳过等待。
	others := a.otherSessionIDsLocked("", key)
	a.mu.Unlock()
	if runner != nil {
		runner.SetSkipNotifWait(a.anyOnSameDevice(serial, others))
	}
	bridge.DebugLog("[appwin] 重启受理 serial=%s pkg=%s（新参数）", serial, pkg)

	go a.guard("appwin-restart", func() {
		bridge.DebugLog("[appwin] 重启守卫进入 serial=%s pkg=%s", serial, pkg)
		if runner != nil {
			// v2.1.72（主人拍板：照抄主投屏 RestartCast 的 `_ = r.Stop()`）——Stop 的
			// 错误（如"bat 未在运行"：会话已在退出中）不放弃重启：继续走"等 runner
			// 释放 → 原地重开"。此前"报错即复位返回"会把会话卡死在「正在关闭」。
			_ = runner.Stop()
		}
		bridge.DebugLog("[appwin] 重启守卫：Stop 返回，等 runner 释放 serial=%s pkg=%s", serial, pkg)
		// 等 runner 释放（onAppWinExit 置 nil；条目保留不再消失）。
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			a.mu.Lock()
			cur := a.appWins[key]
			released := cur == nil || cur != st || cur.runner == nil
			a.mu.Unlock()
			if released {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		bridge.DebugLog("[appwin] 重启守卫：等待结束，进入兜底检查 serial=%s pkg=%s", serial, pkg)
		// 兜底强摘（onExit 极端丢失：本 st 仍在且 runner 未释放）。
		a.mu.Lock()
		if cur := a.appWins[key]; cur == st && cur.runner != nil {
			delete(a.appWins, key)
			bridge.DebugLog("[appwin] 重启兜底：bat 15s 未退出，强制摘除 serial=%s pkg=%s", serial, pkg)
		}
		a.mu.Unlock() // ★ v2.1.73 修复：此处曾丢 Unlock（v2.1.72 手误）→ 下方与后续所有 Lock 自锁死锁
		// 重开前确认条目仍是本 st 且仍处 restarting（期间可能被「停止」流程摘除/替换）。
		a.mu.Lock()
		cur := a.appWins[key]
		proceed := cur == st && cur.restarting
		a.mu.Unlock()
		bridge.DebugLog("[appwin] 重启守卫：proceed=%v serial=%s pkg=%s", proceed, serial, pkg)
		if !proceed {
			bridge.DebugLog("[appwin] 重启中止：条目已被停止/替换 serial=%s pkg=%s", serial, pkg)
			return
		}
		// 新参数重开（StartAppWin 内部读档案 → 两套注入；检测 restarting 条目 → 原地替换）。
		if err := a.StartAppWin(serial, pkg, name); err != nil {
			a.mu.Lock()
			if cur := a.appWins[key]; cur == st {
				delete(a.appWins, key)
			}
			a.mu.Unlock()
			bridge.DebugLog("[appwin] 重启失败（Start）serial=%s pkg=%s: %v（条目摘除）", serial, pkg, err)
		}
	})
	return nil
}
