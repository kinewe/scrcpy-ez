// Package app 组装设备轮询与投屏会话状态，是 UI 绑定层与底层（adb/bat 桥接）之间的中枢。
// 本包不依赖任何 Windows 专属 API，可在 WSL 下单测。
//
// 轮 B（多设备并行投屏）：App 从"单 runner"升级为 map[serial]*sessionState——
// 每个设备一个独立会话（独立 bat 实例/环境变量/事件分发/日志缓冲），
// 会话级操作（Stop/Restart/Start）一律带 serial 参数；全局只共享 adc 探测与设备档案。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/discovery"
)

// Runner 是单个会话的 bat 子进程桥接抽象：windows 下由 bridge.BatRunner 实现。
// 只读展示架构：GUI 不向 bat 写 stdin（bat 的 choice 自愈 /t /d 与自动重连全自理），
// 会话级操作只有 Stop（杀树）与 Start（重跑新会话）。
// Start 的 params 按模式注入：Usb.Set=true → SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB，
// Wifi.Set=true → SCEZ_RES_WIFI/SCEZ_FPS_WIFI/SCEZ_BITRATE_WIFI（参数浮窗覆盖）；
// 该模式 Set=false=自动档（不注入）。
type Runner interface {
	// Start 隐藏启动 投屏支持.bat（可指定优先连接的目标设备）。
	Start(serial string, params bridge.CastParams) error
	// Stop 终止 bat 进程树。
	Stop() error
	// ExitCode 返回 bat 最终退出码（未结束返回 -1）。
	ExitCode() int
}

// RunnerFactory 创建指定会话的 Runner。onLine/onExit 是**会话级**回调：
// serial 由 App 侧绑定（工厂不需要关心串号）；App 内部再按 runner 身份防串
// （陈旧 runner 的迟到回调不会污染替换后的新会话）。
// 由 ui 层注入真实实现（bridge.NewBatRunner），测试注入 fake。
type RunnerFactory func(serial string, onLine func(string), onExit func(int)) (Runner, error)

// CastState 是单个会话"投屏中"视图的完整快照。
type CastState struct {
	Active       bool         `json:"active"`
	Serial       string       `json:"serial"`
	DevName      string       `json:"devName"`
	Phase        string       `json:"phase"`     // 事件枚举值（bridge.Kind 字符串）
	PhaseText    string       `json:"phaseText"` // 用户可读状态
	Spec         *bridge.Spec `json:"spec"`
	KeyboardMode string       `json:"keyboardMode"` // uhid / sdk / 未知
	Mode         string       `json:"mode"`         // 当前投屏模式：usb/wifi（按 [高清]/[流畅]/[custom] 行判定）；空=未知
	Tls          bool         `json:"tls"`          // 当前实际连接走 TLS（gui43 实时判定；投屏中跟 transport 事实）
	// TransportSerial 当前会话实际连接的 transport serial（IP:port 或 USB serial；
	// gui43 每轮轮询按 adb 事实更新；空=未知（保留上次显示）。
	TransportSerial string            `json:"transportSerial,omitempty"`
	WaitingInput    bool              `json:"waitingInput"` // 仅展示：bat 在等待菜单选择
	Prompt          bridge.PromptKind `json:"prompt"`
	Log             []string          `json:"log"`      // 最近 60 行原始输出（每会话独立缓冲）
	ExitCode        int               `json:"exitCode"` // -1 = 运行中
	ExitText        string            `json:"exitText"`
	LastError       string            `json:"lastError"`
	Stalled         bool              `json:"stalled"`   // 无输出提示：可点击"重启投屏"（不自动杀树）
	StallSecs       int               `json:"stallSecs"` // 已停滞秒数（仅展示）
	// NativeRes 是启动时捕获的设备原生分辨率（宽≥高）：投屏会话期间保持，
	// 不随实时设备列表消失——无线恢复期设备暂时离线时，自定义长边徽标仍可按
	// 此比例换算 WxH（修复恢复期显示"长边 1920"）。
	NativeRes string `json:"nativeRes"`
}

// Session 是"投屏中"页标签栏的一个会话条目（轮 B：每会话完整内容）。
// Cast 内嵌该会话的完整快照——标签内容（状态卡/规格徽标/raw 输出）各自独立。
// Active=false 表示 bat 已退出（标签淡出前的最后快照）；Stopping=true 表示
// StopCast 已受理、bat 尚未退出（前端"停止投屏"按钮 → 禁用态"正在终止…"）；
// StartedAt 保证 Snapshot.Sessions 顺序稳定（按 StartCast 时间，同刻按 serial）。
type Session struct {
	Serial    string    `json:"serial"`
	DevName   string    `json:"devName"`
	Identity  string    `json:"identity,omitempty"` // 设备档案 identity（前端设备卡按身份绑定会话）
	Active    bool      `json:"active"`             // false=bat 已退出（标签淡出前的最后快照）
	Stopping  bool      `json:"stopping"`           // StopCast 已受理、bat 未退出（"正在终止…"）
	Closing   bool      `json:"closing"`            // 投屏窗口被点 X 关闭、bat 清理中未退出（"正在关闭…"）
	Mode      string    `json:"mode"`               // 当前投屏模式：usb/wifi（参数浮窗自动定位用）
	StartedAt int64     `json:"startedAt"`          // unix 毫秒（标签排序：按 StartCast 时间）
	Cast      CastState `json:"cast"`               // 会话内容（跟会话：状态/规格/日志）
}

// NewDeviceInfo 是"检测到新设备"弹窗的内容（轮 B 目标 1）：
// 设备轮询发现"在线 USB/无线且身份不在会话集"的设备 → 弹窗询问是否开始投屏。
type NewDeviceInfo struct {
	Serial   string `json:"serial"`
	Name     string `json:"name"`
	ConnType string `json:"connType"` // usb / wifi
	Identity string `json:"-"`        // 设备档案 identity（后端判重用，不进 JSON）
}

// DiscoveryStatus 是无线探测（mDNS + 并行 connect）的最近一次结果快照。
// 探测不阻塞 UI：设备轮询发现"档案中设备不在线且档案有 addrs"时后台触发
// （15s 节流），窗口内取首个成功；全失败 → "未找到"。
type DiscoveryStatus struct {
	Status  string   `json:"status"` // idle / searching / found / notfound
	Tried   []string `json:"tried,omitempty"`
	Found   string   `json:"found,omitempty"`
	LastTry int64    `json:"lastTry"` // 最近一次探测时间（unix 秒）
}

// ProfileItem 是设备参数管理页的档案条目（全部设备档案，含离线）。
// Key=identity（档案键）；Name=展示名（市场名 → 型号 → 首个 serial → 首个 addr → 键）；
// Serials/Addrs 供前端匹配在线设备与投屏会话（Addrs 按 active 优先排序）。
type ProfileItem struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Model   string   `json:"model,omitempty"`
	Serials []string `json:"serials,omitempty"`
	Addrs   []string `json:"addrs,omitempty"`
}

// PendingDevice 是无线调试接入向导的"待配对"设备卡（gui12）：
// mDNS 广播 _adb-tls-connect._tcp 且档案无此 identity 的新设备——
// 前端橙点卡片 + 橙色"配对"按钮；配对成功入档后消失（转正常设备卡）。
type PendingDevice struct {
	Key      string `json:"key"`      // 卡片键 = tls 地址 ip:port
	Name     string `json:"name"`     // 展示名：tls 服务名剥出的 serial；未知则"无线调试设备"
	IP       string `json:"ip"`       // 设备 IP
	Addr     string `json:"addr"`     // 连接地址 ip:tls端口（自动发现，无需填写）
	PairPort string `json:"pairPort"` // 配对端口（_adb-tls-pairing 同 GUID 服务；无则空=需手动）
	Guid     string `json:"guid"`     // tls 服务实例名（adb-<serial>-XXXXXX，配对端口自动匹配用）
	Serial   string `json:"serial"`   // 服务名剥出的真 serial（入档用）
}

// PairStep 是配对向导的单步状态（前端 ①②③ 步骤条）。
type PairStep struct {
	Name string `json:"name"` // pair / connect / done
	OK   bool   `json:"ok"`
	Text string `json:"text,omitempty"`
}

// PairStatus 是无线调试配对向导的后端状态机快照（前端轮询渲染步骤/成败）。
// 配对流程：pair（adb pair）→ connect（adb connect ip:tls端口）→ verify
// （getprop 验证 marketname）→ 成功入档（mode=tls）。失败带分类码（前端映射文案）。
type PairStatus struct {
	Phase   string      `json:"phase"`          // idle / pairing / connecting / verifying / success / failed
	Mode    string      `json:"mode,omitempty"` // gui50：auto / manual / qr（配对路线）
	Key     string      `json:"key,omitempty"`
	Name    string      `json:"name,omitempty"`
	Addr    string      `json:"addr,omitempty"`
	Steps   []PairStep  `json:"steps,omitempty"`
	ErrCode string      `json:"errCode,omitempty"` // pair-code / pair-port / conn-port / connect-failed / timeout / addr-invalid / internal
	ErrText string      `json:"errText,omitempty"`
	Device  *adb.Device `json:"device,omitempty"` // 成功后的设备卡数据
	// gui50 二维码路线：qrText=WIFI:T:ADB 文本；qrExpireAt=失效时刻（unix 秒）。
	QrText     string `json:"qrText,omitempty"`
	QrExpireAt int64  `json:"qrExpireAt,omitempty"`
}

// 配对失败分类码（前端按码映射用户文案）。
const (
	PairModeAuto     = "auto"
	PairModeManual   = "manual"
	PairModeQR       = "qr"
	PairErrCode      = "pair-code"
	PairErrPort      = "pair-port"
	PairErrConnPort  = "conn-port"
	PairErrConnect   = "connect-failed"
	PairErrTimeout   = "timeout"
	PairErrAddr      = "addr-invalid"
	PairErrInternal  = "internal"
	PairPhaseIdle    = "idle"
	PairPhasePairing = "pairing"
	PairPhaseConn    = "connecting"
	PairPhaseVerify  = "verifying"
	PairPhaseSuccess = "success"
	PairPhaseFailed  = "failed"
	PairStepPair     = "pair"
	PairStepConnect  = "connect"
	PairStepDone     = "done"
)

// 配对向导各步超时（var 供测试缩短；配对码 30s 过期，pair 单步 10s 足够）。
var (
	pairTimeout       = 10 * time.Second // adb pair 单步超时
	connectPairTimout = 8 * time.Second  // 配对后的 connect 超时
	verifyTimeout     = 4 * time.Second  // getprop 验证超时（单属性）
	pairQrTTL         = 2 * time.Minute  // gui50：二维码 2 分钟失效
	pairQrServiceName = "ADBQR-connectPhoneOverWifi"

	// gui30「插线即学习」：getprop 端口检查 / adb tcpip 各自的单次超时
	// （tcpip 会重启设备端 adbd，给足 5s；pollOnce 的 8s qctx 是总兜底）。
	teachGetpropTimeout = 4 * time.Second
	teachTcpipTimeout   = 5 * time.Second
)

// Snapshot 是 JS 每次轮询拿到的完整界面状态。
// Cast 兼容字段 = 活动会话（多会话下取最近启动的活动会话）的内容。
type Snapshot struct {
	Version    string          `json:"version"`
	BatPath    string          `json:"batPath"`
	AdbOK      bool            `json:"adbOK"`
	AdbFailing bool            `json:"adbFailing"` // 真空期去抖：adbOK 仍为 true 但当前有连续失败（前端展示"刷新中"软提示，不亮红条）
	Devices    []adb.Device    `json:"devices"`
	Pending    []PendingDevice `json:"pending"` // 待配对设备卡（mDNS tls 服务发现、档案无此 identity）
	Discovery  DiscoveryStatus `json:"discovery"`
	Cast       CastState       `json:"cast"`
	Sessions   []Session       `json:"sessions"`   // 标签栏会话列表（按 StartCast 时间排序）
	Profiles   []ProfileItem   `json:"profiles"`   // 设备参数管理页档案列表（全部设备，含离线）
	DevOrder   []string        `json:"devOrder"`   // 设备卡顺序（gui45 后端持久化；空=未初始化，前端 merge 后写回）
	NewDevice  *NewDeviceInfo  `json:"newDevice"`  // 新设备弹窗（非 nil = 需要弹）
	PairStatus *PairStatus     `json:"pairStatus"` // 无线调试配对向导状态（非 idle = 前端弹窗展示步骤/结果）
}

// sessionState 是一个投屏会话的全部后端状态（map[serial]*sessionState 的一个值）。
// 每个会话独立：runner/bat 环境、事件分发、规格解析、日志缓冲（60 行截断）、
// 无输出计时、重启闩锁、参数覆盖。
type sessionState struct {
	cast       CastState
	runner     Runner
	restarting bool      // 用户重启闩锁（幂等）；重启完成/失败后清除
	stopping   bool      // StopCast 已受理、bat 尚未退出（"正在终止…"；onExit/超时复位）
	closing    bool      // 投屏窗口 X 已关闭、bat 清理中未退出（"正在关闭…"；onExit 复位）
	lastLineAt time.Time // 最近一次 bat 输出行时间（无输出提示用）
	// gui43 会话连接参数存证：供轮询按 adb 事实选举实际 transport。
	castAddr  string    // 主连接地址（StartCast params.Addr；可能为空）
	castAddr2 string    // 备连接地址（params.Addr2；可能为空）
	usbSerial string    // USB transport serial（params.Serial；可能为空）
	startedAt time.Time // StartCast 时间（标签排序；重启沿用保持位置）
	endedAt   time.Time // bat 退出时间（结束态会话 GC 用）
	identity  string    // 设备档案 identity（弹窗判重/前端绑定/防同设备双开会话）
}

// popupState 是新设备弹窗的后端状态机（轮 B 目标 1）。
// 防重复：同设备（identity）30s 内不重复弹；【暂不】后整个"在线周期"不再弹
// （设备离线再上线 = 新出现周期，可再弹；拔线再插 = 新插线周期，可再弹）；
// 弹窗 30s 未处理自动消失。
type popupState struct {
	info      *NewDeviceInfo
	shownAt   time.Time
	lastShown map[string]time.Time // identity -> 上次弹出时间（30s 防重复）
	dismissed map[string]bool      // identity -> 本在线周期已"暂不"
	// expireTimer 是弹窗 30s 未处理自动消失的一次性 timer（a.mu 保护）。
	// 新弹窗/关闭/超时回调均需 Stop 旧 timer。
	expireTimer *time.Timer
}

type App struct {
	cfg Config

	adb  *adb.Manager
	newR RunnerFactory

	mu      sync.RWMutex
	devices []adb.Device
	adbOK   bool
	// applyTrackMu 串行化 applyTrackUpdateCtx（gui49-fix7）：track 读循环与
	// 60s 校准轮不得并发进入显示合成，保证「置位→提交」顺序无乱序帧。
	applyTrackMu sync.Mutex
	adbFail      int // 连续 adb 失败轮数（真空期去抖：未达阈值前保持 adbOK=true 与上次设备列表）
	// adbHealTried 是「半死保险」每段连续失败只触发一次 kill/start 自愈的闩锁
	// （gui49-fix3）；成功后清零。a.mu 保护。
	adbHealTried bool
	cancel       context.CancelFunc

	// 多会话核心（轮 B）：map[serial]*sessionState，App.mu 保护；
	// nextParams=参数浮窗保存后的下一次 StartCast 注入覆盖（按 serial）。
	sessions   map[string]*sessionState
	nextParams map[string]bridge.CastParams

	// 只读展示架构（主人决策）：GUI 不写 stdin、不自动干预。
	// profiles=设备档案（identity 唯一化，全局共享只读）；
	// 无线探测（mDNS + 并行 connect）：disc=adb 探测器；lastDisc=探测节流时间戳；
	// discStatus=最近一次探测结果（Snapshot.Discovery 输出）。
	profiles   *ProfileStore
	disc       *discovery.Connector
	lastDisc   time.Time
	discBusy   bool // 探测 in-flight 标记（gui22 防抖）：runDiscovery 期间 true，ForceDiscover 幂等
	discStatus DiscoveryStatus

	// mDNS 服务快照（gui48-mdns）：由自管 zeroconf 浏览事件维护（mdnsMu 保护），
	// 供待配对设备卡、TLS 标/副行 IP、配对端口自动发现使用。
	mdnsMu sync.Mutex
	mdns   []discovery.MdnsService
	// mdnsFirstDone 标记 mdns 流首块已到齐（启动基线对账用，mdnsMu 保护）。
	mdnsFirstDone bool
	// mdnsProbeSeq 是每地址的 TCP 探测发起序号（gui48-mdns8 后发者胜）：
	// Goodbye 探测与 90s 静默问询可能交错，结果写状态前校验自己仍是最新，
	// 过期结果直接丢弃（mdnsProbeMu 保护）。
	mdnsProbeMu  sync.Mutex
	mdnsProbeSeq map[string]int64

	// 待配对设备卡（mDNS tls 服务 → 档案无此 identity）与配对向导状态机。
	pending         []PendingDevice
	pair            *PairStatus
	pairQR          *pairQRState
	pairOps         pairOps
	pairFastConnect bool // fix6：生产开启 connect 先行快路径；测试默认关，新测试显式开

	// 新设备弹窗状态机（轮 B）
	popup *popupState

	// 「插线即学习」（gui30）：USB 在线设备自动补学 adb tcpip 5555。
	// teachOps=adb 操作注入点（测试注入 fake）；taughtTcpip=本插线周期已学的
	// serial 集（teachMu 保护；track 回调 goroutine 与校准/RefreshNow 可能并发
	// 调用——必须独立锁，不能用 a.mu 拿住整个 adb 调用段）。
	// 设备从列表消失=插线周期结束（拔线/adbd 重启瞬态），下一轮重建。
	teachOps    teachOps
	teachMu     sync.Mutex
	taughtTcpip map[string]bool
	// 「插线进行中」（gui47-fix F2'）：plugging[identity]=检测到插线的时刻。
	// 事件驱动状态机：USB added（offline/unauthorized/device 均算）置位；
	// device 事件或在线无线卡且无 USB 条目（拔线事实）或 15s 兜底超时清除。
	// 兜底超时由 plugTimers 的 time.AfterFunc 一次性 timer 执行（禁止轮询）。
	plugging         map[string]time.Time
	plugTimers       map[string]*time.Timer
	plugStableTimers map[string]*time.Timer
	// 「拔线遮罩」（gui49）：unplugging[identity]=检测到 USB removed 的时刻。
	// 与插线遮罩互斥：插线遮罩运行期内的 removed 信号被吸收，不启动拔线遮罩。
	// 清因：无线 device 恢复（主）/ 10s 兜底 unplugTimers；同样由 teachMu 保护。
	unplugging   map[string]time.Time
	unplugTimers map[string]*time.Timer
	// 「配对遮罩」（gui52-fix14）：pairing[identity]=配对成功时刻。
	// 无线情形 adbd 重启以「offline 波动」为主（非 removed 单一信号），
	// 因此清因=「该设备任一 transport 连续 device 满 2s」（pairStableTimers），
	// 波动→重置；10s 兜底（pairTimers）直接退出；
	// 退出时 pairProbeAllAddrs 并行 connect 档案两条地址刷 active/stale。
	pairing          map[string]time.Time
	pairTimers       map[string]*time.Timer
	pairStableTimers map[string]*time.Timer
	pairLastStable   map[string][]adb.Device
	// unplugConfirmFn 是拔线遮罩 connect 确认注入点（测试注入 fake；nil=真实
	// a.disc.Connect）。unplugRetries/unplugRetryTimers 是事件驱动重试状态。
	unplugConfirmFn   func(ctx context.Context, id, addr string, attempt int)
	unplugRetries     map[string]int
	unplugRetryTimers map[string]*time.Timer
	// removed 事件 → 2s 防抖 timer（key=identity）；防抖期间设备再现 → 取消。
	dropMu     sync.Mutex
	dropTimers map[string]*time.Timer
	// gui51 删除标记：设备被删除后，设备流/mDNS/显示层/弹窗全部过删除集过滤，
	// 防「删掉了又回来」（USB 线还插着 / adb server mdns auto-connect 秒回）。
	// gui52-fix17b：deletedUsbKeys 是删除集「按设备」索引（serial → 全键集）——
	// 清除不再靠帧字段凑键（added 帧常无 marketname，按字段清导致市场名键残留、
	// device 帧被持续过滤、档案永不建档），USB 任意状态帧重现即按索引全清。
	deleteMu   sync.Mutex
	deletedUsb map[string]bool
	// deletedUsbKeys: serial/短号 → 该设备的删除集全键（与 deletedUsb 同步维护）。
	deletedUsbKeys map[string][]string
	// track 事件流的上一份权威列表与首块基线标记（a.mu 保护）。
	lastTrack     []adb.Device
	trackBaseline bool
	// startupBaselineDone 保证「双首块基线 stale 对账」只执行一次（a.mu 保护）。
	// mdns 重连首块（First=true）不得重复跑基线对账，否则 kill-server 真空期
	// 会把临时双缺席误判成离线并闪离线卡。
	startupBaselineDone bool
	// coldSearch5555Done 保证冷启动 5555 搜索只执行一次（a.mu 保护）。
	coldSearch5555Done bool
}

// teachOps 是「插线即学习」的 adb 操作注入点（真实实现 → adb.Manager；测试注入 fake）。
// probeFn 是「5555 真就绪」的一次性 TCP 握手探测（纯握手零副作用）；生产实现
// 复用 discovery.Connector.TcpProbe，测试注入 fake。nil=旧测试路径不探测
// （gui48-teachfix：生产构造时按 Version 注入，见 New）。
type teachOps struct {
	getpropFn func(ctx context.Context, serial, prop string) (string, error)
	tcpipFn   func(ctx context.Context, serial, port string) error
	shellFn   func(ctx context.Context, serial string, args ...string) (string, error)
	probeFn   func(ctx context.Context, addr string) bool
}

// pairQRState 是 gui50 二维码路线的后端状态：电脑生成 6 位配对码与
// WIFI:T:ADB 文本；手机扫码后 _adb-tls-pairing 广播出现 → 自动发起配对。
type pairQRState struct {
	code        string
	serviceName string
	expireAt    time.Time
}

// pairOps 是配对向导的 adb 操作注入点（真实实现 → adb/connector；测试注入 fake）。
type pairOps struct {
	pairFn     func(ctx context.Context, ip, port, code string) (string, error)
	connectFn  func(ctx context.Context, addr string) (string, error)
	getpropFn  func(ctx context.Context, serial, prop string) (string, error)
	mdnsScanFn func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error)
	tcpipFn    func(ctx context.Context, serial, port string) error // gui52-fix8：无线配对开启 5555（幂等检测）
}

// 无输出提示：非投屏中阶段 40s 无任何 bat 新输出行 → 状态区显示
// "已 X 秒无输出，可点击【重启投屏】"（仅提示，不自动杀树；用户点击才重启会话）。
const stalledRestartWait = 40 * time.Second

// stopResetTimeoutNanos 是停止投屏的兜底复位时长（纳秒）：Stop 已受理
// （stopping=true）但 bat 长时间未退出（kill 未生效的罕见场景）→ 复位
// stopping，前端按钮恢复"停止投屏"可重试（会话状态不变——与既有行为一致，
// 仅按钮状态恢复）。atomic：单测会缩短时长，与后台兜底 goroutine 并发读写
// 必须原子（-race 验证）。
var stopResetTimeoutNanos int64 = int64(15 * time.Second)

// stopResetDuration 返回当前兜底复位时长（原子读）。
func stopResetDuration() time.Duration {
	return time.Duration(atomic.LoadInt64(&stopResetTimeoutNanos))
}

// setStopResetDuration 测试用：设置兜底复位时长（原子写）。
func setStopResetDuration(d time.Duration) {
	atomic.StoreInt64(&stopResetTimeoutNanos, int64(d))
}

// 新设备弹窗参数（轮 B）：30s 防重复窗口；未处理 30s 自动消失；结束态会话保留 10s
// （前端淡出动画后 ForgetSession 主动移除；超时兜底 GC 防泄漏）。
const (
	newDevicePopupThrottle = 30 * time.Second
	newDevicePopupExpire   = 30 * time.Second
	deadSessionKeep        = 10 * time.Second
)

// popupExpireTimeout 是弹窗未处理自动消失时长（生产=newDevicePopupExpire；
// 测试可注入更短值）。
var popupExpireTimeout = newDevicePopupExpire

// adbFailDebounce 是 adb 真空期去抖阈值（连续失败轮数）：投屏启动时 bat 会
// kill-server+start-server 造成 1-2s 真空（老设备重新枚举更久），期间 adb 调用
// 偶发失败属正常瞬态——连续失败未达阈值前保持 adbOK=true 且保留上次设备列表
// （前端只给"刷新中"软提示，不亮红条）。阈值 3 ≈ 6s（轮询 2s×3）：真实致命错误
// （adb.exe 缺失/无法启动、长时间持续失败）在约 6s 后转红条，与临时真空明确区分。
const adbFailDebounce = 3

// 无线探测参数（轮 A）：节流 15s；mDNS 截断 1.5s；单地址 connect ≤3s；窗口 6s 取首个成功。
const (
	discoveryThrottle = 15 * time.Second
	discoveryWindow   = 6 * time.Second
	connectTimeout    = 3 * time.Second
	mdnsTimeout       = 1500 * time.Millisecond
	// plugShieldTimeout 是插线遮罩的兜底超时（gui48-teachfix4：15s→10s）：
	// 覆盖 adbd 重启断档上限（实测 3.45s / 华为至 5s）+ 余量；真拔线/设备死机/
	// 事件链异常时 ≤10s 遮罩自动清 → 档案态呈现。getprop 判定是主清因，
	// 兜底是唯一第二清因。兜底由 plugTimers 的 time.AfterFunc 一次性 timer 执行。
	plugShieldTimeout = 10 * time.Second
	// unplugShieldTimeout 是拔线遮罩的兜底超时（gui49）：10s 后仍未出现无线
	// device → 遮罩清 → 显示真实档案态（无线未恢复=离线卡）。
	unplugShieldTimeout = 10 * time.Second
	// unplugConnectAttempts 是拔线遮罩 connect 确认的有限重试次数。
	unplugConnectAttempts = 3
	// dropDebounce 是 removed 事件的防抖窗口：2s 内设备再现 → 取消掉线处理。
	dropDebounce = 2 * time.Second
	// trackCalibrateInterval 是 60s 全量校准间隔：adb.List 与事件维护的列表
	// diff 对账（防 track 流静默失效后设备列表失真）。低频一次性动作，不是
	// 2s 采样轮询。
	trackCalibrateInterval = 60 * time.Second
)

// unplugConnectRetryDelay 是拔线遮罩 connect 确认失败后的重试间隔
// （事件驱动 timer；var 供测试注入缩短，生产 2s）。
var unplugConnectRetryDelay = 2 * time.Second

// plugStableDuration 是插线遮罩清因①的稳定确认窗口（gui49-fix6）：
// USB 条目连续 device 满此时长才清遮罩；非 device 波动/removed 重置计时。
// var 供测试注入缩短，生产 2s。
var plugStableDuration = 2 * time.Second

// pairShieldStableDuration 是配对遮罩稳定确认窗口（gui52-fix14）：
// 该设备任一 transport 连续 device 满 2s 才清遮罩（无线 adbd 波动期 offline
// 反复，必须连续稳定才放行——与插线遮罩同构）。var 供测试注入缩短。
var pairShieldStableDuration = 2 * time.Second

// pairShieldTimeout 是配对遮罩 10s 兜底（gui52-fix14）：超时直接退出，
// probe 刷状态显示真实情况（与插线遮罩 plugShieldTimeout 同值）。
var pairShieldTimeout = 10 * time.Second

// Config 是壳的路径配置（开发期常量，后续可读配置文件覆盖）。
type Config struct {
	BatPath      string // 默认 exe 同目录 投屏支持.bat（gui53 起以软件目录为准）
	AdbPath      string // 默认同目录 adb.exe
	ConfigPath   string // 无线记忆 config.txt；默认 bat 同目录 config.txt（缺失回退 ..\..\config.txt）
	CrashDir     string // panic 崩溃日志目录（exe 同目录）；空=不落盘
	ProfilesPath string // 参数记忆 profiles.json（默认 %APPDATA%\scrcpy-ez\profiles.json）；空=内存模式
	Version      string
}

func New(cfg Config) *App {
	a := &App{
		cfg:        cfg,
		adb:        adb.New(cfg.AdbPath, cfg.ConfigPath),
		adbOK:      true, // 真空期去抖：首轮轮询未回前视为"可用/刷新中"（不闪红条，前端显示扫描中空态）
		profiles:   NewProfileStore(cfg.ProfilesPath),
		disc:       discovery.New(cfg.AdbPath),
		sessions:   map[string]*sessionState{},
		nextParams: map[string]bridge.CastParams{},
		popup: &popupState{
			lastShown: map[string]time.Time{},
			dismissed: map[string]bool{},
		},
		taughtTcpip:       map[string]bool{},
		plugging:          map[string]time.Time{},
		plugTimers:        map[string]*time.Timer{},
		plugStableTimers:  map[string]*time.Timer{},
		unplugging:        map[string]time.Time{},
		unplugTimers:      map[string]*time.Timer{},
		pairing:           map[string]time.Time{},
		pairTimers:        map[string]*time.Timer{},
		pairStableTimers:  map[string]*time.Timer{},
		pairLastStable:    map[string][]adb.Device{},
		unplugRetries:     map[string]int{},
		unplugRetryTimers: map[string]*time.Timer{},
		dropTimers:        map[string]*time.Timer{},
		deletedUsb:        map[string]bool{},
		deletedUsbKeys:    map[string][]string{},
		mdnsProbeSeq:      map[string]int64{},
	}
	// 配对向导操作真实实现（测试可注入替换）
	a.pairOps = pairOps{
		pairFn:     a.adb.Pair,
		connectFn:  a.disc.ConnectOut,
		getpropFn:  a.adb.Getprop,
		mdnsScanFn: a.disc.MdnsScan,
	}
	// gui52-fix7：配对成功后的 tcpip 5555 开启/connect 学习已整体移除（mdns 权威）。
	// fix6：生产开启 connect 先行快路径；旧单测默认保持 pair-first 语义，
	// 新 fix6 单测显式开启验证快路径。
	a.pairFastConnect = cfg.Version != "test"
	// gui52-fix8：无线配对成功后开启设备 tcpip 5555（与插线学习一致）；
	// Version==\"test\" 不挂，避免旧配对单测触达真实 adb（新测试显式注入 fake）。
	if cfg.Version != "test" {
		a.pairOps.tcpipFn = a.adb.Tcpip
	}
	// 插线即学习操作真实实现（测试可注入替换）
	a.teachOps = teachOps{
		getpropFn: a.adb.Getprop,
			shellFn:   a.adb.Shell,
	}
	// gui48-teachfix：生产才挂真实 TCP 探测（5555 就绪验证）；测试 App 通过
	// Version=="test" 保持 probeFn=nil（旧单测不触达真实网络），新测试显式注入。
	if cfg.Version != "test" {
		a.teachOps.probeFn = a.disc.TcpProbe
	}
	_ = a.profiles.Load() // 文件缺失/损坏=空档（全部自动档），不阻断启动；
	// 旧结构 profiles.json 在 Load 内自动迁移（identity=serial 回退键）并落盘新结构
	return a
}

// SetRunnerFactory 注入 bat 桥接工厂（ui 层在启动时调用）。
func (a *App) SetRunnerFactory(f RunnerFactory) { a.newR = f }

// --- panic 防护与崩溃日志 ---
// GUI 静默死是最恶劣的失败模式：所有桥接回调/轮询 goroutine 都经 guard 包裹，
// panic 时恢复现场并落盘 crash-YYYYMMDD.log（含堆栈 + cast 状态 + 日志尾部 50 行）。

func (a *App) guard(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			a.writeCrashLog(name, r)
		}
	}()
	fn()
}

func (a *App) writeCrashLog(name string, r any) {
	defer func() { _ = recover() }() // 崩溃日志自身失败不得再次 panic
	if a.cfg.CrashDir == "" {
		return
	}
	path := filepath.Join(a.cfg.CrashDir, "crash-"+time.Now().Format("20060102")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	var b strings.Builder
	fmt.Fprintf(&b, "\n===== %s panic: %v (%s) =====\n", name, r, time.Now().Format("2006-01-02 15:04:05"))
	b.Write(debug.Stack())
	b.WriteString("----- cast 状态 -----\n")
	// 用未加 guard 的原始快照：崩溃日志内部不得再触发 guard → 防递归
	fmt.Fprintf(&b, "%+v\n", a.snapshotRaw().Cast)
	b.WriteString("----- 日志尾部 50 行 -----\n")
	for _, l := range a.tailLog(50) {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	_, _ = f.WriteString(b.String())
}

// tailLog 返回活动会话日志尾部 n 行（崩溃日志上下文用）。
func (a *App) tailLog(n int) []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	log := a.activeCastLocked().Log
	if len(log) > n {
		log = log[len(log)-n:]
	}
	return append([]string{}, log...)
}

// StartDevicePolling 每 2 秒轮询一次 adb devices 并刷新设备信息。
// 轮 B：多会话期间轮询**不暂停**——新设备弹窗依赖持续轮询发现
// "在线但未创建会话"的设备；bat 已参数化（SCEZ_SERIAL/SCEZ_ADDR），
// 不再需要暂停轮询规避 adb server 竞态。
func (a *App) StartDevicePolling(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()

	// track-devices 长连：设备列表变化由事件流驱动（替代 2s 轮询数据源）。
	go a.guard("device-track-loop", func() {
		err := a.adb.TrackDevices(ctx, func(ev adb.TrackEvents) {
			a.guard("track-event", func() {
				a.applyTrackUpdateCtx(ctx, ev.Devices)
			})
		})
		if err != nil && ctx.Err() == nil {
			bridge.DebugLog("[app] track-devices 长连退出: %v", err)
		}
	})

	// 启动立即拉一次全量作为首块基线（track 首块到达前 UI 也能尽快有数据）。
	go a.guard("device-poll-once", func() { a.pollOnce(ctx) })

	// 2s tick 只保留与设备状态无关的会话 UI tick（无输出提示/结束态 GC）。
	go a.guard("device-ui-tick", func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.promptTick()
			}
		}
	})

	// 60s 全量校准：adb.List 与事件维护的列表 diff 对账 + mdns 快照对账
	// （低频 timer，不是采样轮询）。
	go a.guard("device-calibrate-loop", func() {
		tick := time.NewTicker(trackCalibrateInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.pollOnce(ctx)
				a.reconcileMdnsSnapshot(ctx)
			}
		}
	})

	// 自建 mDNS 监听：广播变化由事件流驱动，不依赖 adb server。
	go a.guard("mdns-browse-loop", func() {
		err := a.adb.BrowseMdns(ctx, func(ev adb.MdnsTrackEvents) {
			a.guard("mdns-browse-event", func() {
				a.onMdnsTrackEvents(ctx, ev)
			})
		}, func(addr string) {
			a.guard("mdns-idle-signal", func() {
				a.onMdnsIdleSignal(addr)
			})
		})
		if err != nil && ctx.Err() == nil {
			bridge.DebugLog("[app] mdns browse 退出: %v", err)
		}
	})

	// 冷启动 5555 搜索（仅一次，与冷启动 mDNS 查询并行）：每台档案设备的
	// 5555 地址并行 connect（≈2s 级），成功入档 active，失败不动。
	go a.guard("mdns-cold-search-5555", func() {
		a.coldSearch5555(context.Background())
	})
}

// onMdnsTrackEvents 是 mdns 快照/事件回调：维护 mdns 快照、
// 应用广播 added/removed（档案 active/stale、待配对卡、TLS 标副行）。
func (a *App) onMdnsTrackEvents(ctx context.Context, ev adb.MdnsTrackEvents) {
	// fix3 自动配对需要“真正新增”的 pairing 服务，不能直接用 ev.Added：
	// QU 强制 emit 时 ev.Added=全量快照，稳定老 pairing 也会混入。
	// 这里在 applyMdnsSnapshot 更新前取上一份快照，用本地 diff 得到本次新增。
	var prev []discovery.MdnsService
	firstLike := ev.First
	a.mdnsMu.Lock()
	if !a.mdnsFirstDone {
		firstLike = true
	}
	prev = append([]discovery.MdnsService(nil), a.mdns...)
	a.mdnsMu.Unlock()

	added, removed := a.applyMdnsSnapshot(ev.Snapshot, ev.First, ev.Added, ev.Removed)
	for i := range added {
		a.applyMdnsServiceAdded(&added[i])
	}
	for i := range removed {
		// Goodbye/门铃确认已等价探测：立即按服务类型分形态打 stale。
		a.onMdnsDropped(mdnsServiceDropKey(removed[i]), removed[i])
	}
	if ev.First {
		a.maybeStartupBaselineStale()
	} else if !firstLike {
		autoAdded, _ := adb.DiffMdnsServices(prev, ev.Snapshot)
		a.maybeAutoPairQR(autoAdded)
	}
	a.reapplyDisplay("mdns事件")
}

// applyMdnsSnapshot 用一份完整 mdns 快照更新事件维护的 a.mdns（mdnsMu 保护）。
// 非首块优先采用 A 层已算好的 evAdded/evRemoved（含 QU 强制 emit 场景：
// 快照内容未变但 Added=全量——必须据此翻 active）；两者皆空时回退本地 diff。
func (a *App) applyMdnsSnapshot(snapshot []discovery.MdnsService, first bool, evAdded, evRemoved []discovery.MdnsService) (added, removed []discovery.MdnsService) {
	a.mdnsMu.Lock()
	prev := append([]discovery.MdnsService(nil), a.mdns...)
	if first || !a.mdnsFirstDone {
		a.mdns = append([]discovery.MdnsService(nil), snapshot...)
		a.mdnsFirstDone = true
		a.mdnsMu.Unlock()
		return append([]discovery.MdnsService(nil), snapshot...), nil
	}
	if evAdded != nil || evRemoved != nil {
		added, removed = evAdded, evRemoved
	} else {
		added, removed = adb.DiffMdnsServices(prev, snapshot)
	}
	a.mdns = append([]discovery.MdnsService(nil), snapshot...)
	a.mdnsMu.Unlock()
	return added, removed
}

// applyMdnsServiceAdded 广播 added → 档案 AddrSuccess 入档 active（已 stale 则翻 active）。
func (a *App) applyMdnsServiceAdded(s *discovery.MdnsService) {
	if s == nil || s.Addr == "" {
		return
	}
	// gui52-fix17：已删除设备的广播不写档不翻 active——删除后 adb server
	// mdns auto-connect 秒回时广播还在持续，不挡就会把档案写活（复活）。
	if a.deletedUsbMarked("ip:"+ipOfAddr(s.Addr)) || a.deletedUsbMarked(s.Name) {
		bridge.DebugLog("[app] 删除集拦截 mDNS 入档：%s %s", s.Name, s.Addr)
		return
	}
	id := a.mdnsServiceIdentity(s)
	if id == "" {
		return // 档案无此 identity：待配对卡由 buildPending 从快照构建
	}
	// 广播匹配负责：入档 active（AddrSuccess）、清 stale 翻 active、
	// 形态升级（wirelessForm/tlsGuid）——与旧 MatchMdnsModes 同口径。
	a.profiles.MatchMdnsModes([]MdnsMatch{{Name: s.Name, Addr: s.Addr, Mode: s.Mode}})
	// gui52fix3：广播建档后事件驱动合卡（mDNS 建立身份档的同时吸收过渡孤儿）。
	a.profiles.CleanOrphanIPPort()
}

// mdnsServiceIdentity 解析 mdns 服务所属档案 identity（服务名 serial 优先，其次 addr）。
func (a *App) mdnsServiceIdentity(s *discovery.MdnsService) string {
	if s == nil {
		return ""
	}
	if serial := TlsServiceIdentity(s.Name); serial != "" {
		if k := a.profiles.ResolveKey(serial); k != "" {
			return k
		}
	}
	if s.Addr != "" {
		if k := a.profiles.ResolveKey(s.Addr); k != "" {
			return k
		}
	}
	return ""
}

// profileHasActiveAddr 档案是否存在任一 state=active 的地址（gui52：判据只认
// state 二值；active=在线证据，stale=离线候选）。
func profileHasActiveAddr(e DeviceEntry) bool {
	for i := range e.Addrs {
		if e.Addrs[i].State == AddrStateActive {
			return true
		}
	}
	return false
}

// mdnsHasServiceForIdentity 当前 mdns 快照是否存在该 identity 的任一广播
// （仅启动基线一次性对账用；实时显示/投屏/离线判据只读档案）。
func (a *App) mdnsHasServiceForIdentity(id string) bool {
	if id == "" {
		return false
	}
	for _, s := range a.mdnsSnapshot() {
		if a.mdnsServiceIdentity(&s) == id {
			return true
		}
	}
	return false
}

// reapplyDisplay 用当前权威设备列表（lastTrack）重跑显示层合成（TLS 标/待配对
// 卡/离线卡/USB 遮罩均读取最新 mdns 快照与档案状态）。
// src=调用点来源（gui49-fix7 诊断日志）。
func (a *App) reapplyDisplay(src ...string) {
	source := "未知"
	if len(src) > 0 && src[0] != "" {
		source = src[0]
	}
	bridge.DebugLog("[app] reapplyDisplay：来源=%s（%s）", source, time.Now().Format("15:04:05.000"))
	a.mu.RLock()
	base := append([]adb.Device(nil), a.lastTrack...)
	a.mu.RUnlock()
	a.commitDisplay(base, "reapplyDisplay:"+source)
}

// mdnsProbeKind 区分 Goodbye 探测与 90s 静默问询（两种来源共用同一条
// 「后发者胜」并发线：同一地址只有最新发起的探测结果有权写状态）。
type mdnsProbeKind int

const (
	mdnsProbeKindGoodbye mdnsProbeKind = iota
	mdnsProbeKindIdle
)

// mdnsProbeNext 为地址登记一次新探测并返回递增序号。
func (a *App) mdnsProbeNext(addr string) int64 {
	a.mdnsProbeMu.Lock()
	defer a.mdnsProbeMu.Unlock()
	seq := a.mdnsProbeSeq[addr] + 1
	a.mdnsProbeSeq[addr] = seq
	return seq
}

// mdnsProbeLatest 判断 seq 是否仍是该地址最新一次探测；过期结果直接丢弃。
func (a *App) mdnsProbeLatest(addr string, seq int64) bool {
	a.mdnsProbeMu.Lock()
	defer a.mdnsProbeMu.Unlock()
	return a.mdnsProbeSeq[addr] == seq
}

// startMdnsProbe 异步发起「主人拍板」TCP 探测（2s 级，不阻塞 A 层事件链）。
// 结果即定论：通 → AddrSuccess(active，允许翻回)；不通 → MarkAddrStale。
// 写状态前校验发起序号仍是最新（后发者胜）；过期结果直接丢弃、不落库。
func (a *App) startMdnsProbe(id, addr string, kind mdnsProbeKind) {
	disc := a.disc
	seq := a.mdnsProbeNext(addr)
	go func() {
		ok := false
		if disc != nil {
			ok = disc.TcpProbe(context.Background(), addr)
		}
		if !a.mdnsProbeLatest(addr, seq) {
			return // 已有更新的探测发起：本结果过期，不写状态
		}
		switch kind {
		case mdnsProbeKindGoodbye:
			if ok {
				a.profiles.AddrSuccess(id, addr)
				bridge.DebugLog("[app] mdns Goodbye（TCP 探测通，active）：%s/%s", id, addr)
			} else if a.profiles.MarkAddrStale(id, addr) {
				bridge.DebugLog("[app] mdns Goodbye：%s/%s → 对应形态打 stale", id, addr)
			}
		case mdnsProbeKindIdle:
			if ok {
				a.profiles.AddrSuccess(id, addr)
				bridge.DebugLog("[app] mdns 静默问询（通，active）：%s/%s", id, addr)
			} else if a.profiles.MarkAddrStale(id, addr) {
				bridge.DebugLog("[app] mdns 静默问询（不通，stale）：%s/%s", id, addr)
			}
		}
		a.reapplyDisplay("mdns探测结果")
	}()
}

// onMdnsDropped 响应 Goodbye（TTL=0）事件（gui48-mdns8 主人拍板）：
// IP:port 地址先做一次纯 TCP 探测再定——通=active（硬事实，含翻回），
// 不通=stale；令牌/非 IP:port 条目保持原 stale 语义（不探测）。
func (a *App) onMdnsDropped(key string, s discovery.MdnsService) {
	_ = key
	id := a.mdnsServiceIdentity(&s)
	if id == "" || s.Addr == "" {
		return
	}
	if !IsIPPort(s.Addr) {
		if a.profiles.MarkAddrStale(id, s.Addr) {
			bridge.DebugLog("[app] mdns Goodbye：%s/%s → 对应形态打 stale", id, s.Addr)
		}
		return
	}
	a.startMdnsProbe(id, s.Addr, mdnsProbeKindGoodbye)
}

// onMdnsIdleSignal 响应 mDNS 90s 无信号事件（gui48-mdns8 主人拍板）：
// 静默≠判死——IP:port 地址异步 TCP 问一句：通=维持/翻回 active，
// 不通=stale（真断连）；非 IP 条目保持原 stale 语义。
func (a *App) onMdnsIdleSignal(addr string) {
	if addr == "" {
		return
	}
	id := a.profiles.ResolveKey(addr)
	if id == "" {
		return
	}
	if !IsIPPort(addr) {
		if a.profiles.MarkAddrStale(id, addr) {
			bridge.DebugLog("[app] mdns 90s 无信号：%s/%s → 对应形态打 stale", id, addr)
		}
		a.reapplyDisplay("mdns静默非IP")
		return
	}
	a.startMdnsProbe(id, addr, mdnsProbeKindIdle)
}

func mdnsServiceDropKey(s discovery.MdnsService) string {
	if s.Addr != "" {
		return "addr:" + s.Addr
	}
	return "name:" + s.Name
}

// reconcileMdnsSnapshot 执行一次 `adb mdns services` 快照对账（RefreshNow /
// 60s 校准触发；不是 15s 扫描轮询）。
func (a *App) reconcileMdnsSnapshot(ctx context.Context) {
	a.mu.RLock()
	adbOK := a.adbOK
	disc := a.disc
	a.mu.RUnlock()
	if !adbOK || disc == nil {
		return
	}
	svcs, err := disc.MdnsScan(ctx, mdnsTimeout)
	if err != nil {
		return
	}
	a.applyMdnsSnapshot(svcs, false, nil, nil)
	a.reapplyDisplay("mdns快照对账")
}

// maybeStartupBaselineStale 启动基线对账：设备流首块 + mdns 流首块均到齐后，
// 对「档案设备既不在设备流列表、也不在 mdns 广播集」的条目一次性打 stale
// （K80 假在线回归修复）。
// once 语义：startupBaselineDone（a.mu 保护）保证只在双首块首次同时到齐时
// 执行一次；mdns 重连首块（First=true）不会重复对账。
func (a *App) maybeStartupBaselineStale() {
	a.mu.RLock()
	done := a.startupBaselineDone
	deviceDone := a.trackBaseline
	track := append([]adb.Device(nil), a.lastTrack...)
	a.mu.RUnlock()
	if done || !deviceDone {
		return
	}
	a.mdnsMu.Lock()
	mdnsDone := a.mdnsFirstDone
	a.mdnsMu.Unlock()
	if !mdnsDone {
		return
	}
	for key, e := range a.profiles.Entries() {
		if deviceListHasEntry(a, key, e, track) || a.mdnsHasServiceForIdentity(key) {
			continue
		}
		if a.profiles.MarkAllAddrsStale(key) {
			bridge.DebugLog("[app] 启动基线对账：%s 不在设备流也不在 mdns 快照 → 打 stale（K80 假在线修复）", key)
		}
	}
	a.mu.Lock()
	a.startupBaselineDone = true
	a.mu.Unlock()
}

// deviceListHasEntry 判定权威设备列表中是否存在该档案条目（identity/serial/addrs 任一命中）。
func deviceListHasEntry(a *App, key string, e DeviceEntry, devs []adb.Device) bool {
	for i := range devs {
		d := &devs[i]
		if a.identityOf(d) == key {
			return true
		}
		if contains(e.Serials, d.Serial) || contains(e.Serials, d.Wireless) ||
			addrInList(e.Addrs, d.Serial) || addrInList(e.Addrs, d.Wireless) {
			return true
		}
	}
	return false
}

// hasOnlineWirelessLocked 是否有"mDNS 广播者"在线（需要持 a.mu；gui41b 修正）：
// mDNS 只广播无线调试 TLS 服务（_adb-tls-connect._tcp），tcpip 明文 5555 连接
// 不上广播——判据收窄为"在线且连接形态为 TLS"的无线设备。5555-only 设备在线
// + mdns 列表空 = 正常（不该计为静默挂）。形态判定与 isTlsFormAddr 同口径：
// ConnType==wifi 且 Serial/Wireless 端口 != 5555。
func (a *App) hasOnlineWirelessLocked() bool {
	for i := range a.devices {
		d := &a.devices[i]
		if d.State != "device" {
			continue
		}
		if d.ConnType == "wifi" && isTlsFormAddr(d.Serial) {
			return true
		}
		if d.Wireless != "" && isTlsFormAddr(d.Wireless) {
			return true
		}
	}
	return false
}

// anyActiveSessionLocked 判定会话集中是否存在 runner != nil 的活动投屏会话。
// exclude 非空时跳过该 serial：StartCast 的 SetCanKillServer 门排除会话自身
// （自身正在停止/重建，不应挡自己）；mDNS 自愈传 ""——任何活动会话都禁止
// kill-server（杀共享 adb server 会断掉正在进行的投屏）。
// 调用方必须持有 a.mu（读锁）。
func (a *App) anyActiveSessionLocked(exclude string) bool {
	for k, o := range a.sessions {
		if k == exclude {
			continue
		}
		if o.runner != nil {
			return true
		}
	}
	return false
}

// mdnsSnapshot 返回最近一次 mDNS 扫描快照副本。
func (a *App) mdnsSnapshot() []discovery.MdnsService {
	a.mdnsMu.Lock()
	defer a.mdnsMu.Unlock()
	return append([]discovery.MdnsService{}, a.mdns...)
}

// selectSessionTransport 按 bat 的切换/降级顺序选举会话当前 transport：
// ① USB 在线（usbSerial 匹配且 state=device）→ USB；
// ② 主地址（addr，PINNED_ADDR 语义）在线 → 主；
// ③ 备地址（addr2，ALT_ADDR 降级语义）在线 → 备；
// ④ 否则空串（找不到——adb 真空/重连窗口，调用方保持上次值）。
// 与 bat 行为一致：USB 插线优先、主地址优先于备地址（单次降级）。
func selectSessionTransport(usbSerial, addr, addr2 string, transports []adb.RawDevice) string {
	if usbSerial != "" {
		for _, t := range transports {
			if t.ConnType == "usb" && t.Serial == usbSerial && t.State == "device" {
				return t.Serial
			}
		}
	}
	if addr != "" {
		for _, t := range transports {
			if t.Serial == addr && t.State == "device" {
				return t.Serial
			}
		}
	}
	if addr2 != "" {
		for _, t := range transports {
			if t.Serial == addr2 && t.State == "device" {
				return t.Serial
			}
		}
	}
	return ""
}

// tlsActiveOf 判定 transport serial 是否为 TLS 形态：
// 无线 transport 只有 tcpip 5555 与 TLS 随机端口两种形态，port != 5555 即 TLS
// （isTlsFormAddr，gui12 环境事实启发式）；叠加 known（isKnownTlsAddr 语义）
// 双保险覆盖 mDNS 广播窗口；USB/空串恒 false。
func tlsActiveOf(serial string, known func(string) bool) bool {
	if serial == "" {
		return false
	}
	if isTlsFormAddr(serial) {
		return true
	}
	return known != nil && known(serial)
}

// wirelessStartAddr 无线投屏/显示首选地址（gui48-mdns4 档案化；gui52 二态）：
// 只认 state=active（TLS 优先 → 5555）——stale 只是离线候选，不得作为
// 显示主行/在线证据；stale 探测/投屏兜底走 OrderedAddrs 直取链。
// 无 active 候选返回 ""；USB 卡仍走 BestAddr 提示 + target.Serial 兜底。
func (a *App) wirelessStartAddr(serial string) string {
	for _, c := range a.profiles.OrderedAddrs(serial) {
		if c.State == AddrStateActive {
			return c.Addr
		}
	}
	return ""
}

// secondaryWirelessAddr 返回与主地址不同形态的备用无线地址（gui42）：
// 主地址优先取广播权威 wirelessStartAddr；无广播时回退档案 OrderedAddrs[0]
// （与 StartCast 主地址选择同口径）。gui52：备选只认对侧形态 state=active
// （档案顺序第一条 active），不读 Stale/LastOk；无对侧活性地址 → ""（单地址，无降级）。
func (a *App) secondaryWirelessAddr(serial string) string {
	main := a.wirelessStartAddr(serial)
	if main == "" {
		if cands := a.profiles.OrderedAddrs(serial); len(cands) > 0 {
			main = cands[0].Addr
		}
	}
	if main == "" {
		return ""
	}
	e, ok := a.profiles.Entry(serial)
	if !ok {
		return ""
	}
	mainClass := ModeTcpip
	if isTlsFormAddr(main) {
		mainClass = ModeTls
	}
	altClass := ModeTcpip
	if mainClass == ModeTcpip {
		altClass = ModeTls
	}
	for i := range e.Addrs {
		if addrEntryClass(e.Addrs[i]) == altClass && e.Addrs[i].State == AddrStateActive {
			return e.Addrs[i].Addr
		}
	}
	return ""
}

func (a *App) pollOnce(ctx context.Context) {
	a.guard("pollOnce", func() {
		a.promptTick() // 先做无输出提示判定与结束态会话 GC，避免被慢 adb 拖后

		qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		devs, err := a.adb.List(qctx)
		if err == nil && len(devs) == 0 {
			// 无线自恢复：bat 退出时 kill-server 会清掉无线设备，
			// 列表为空时按 config.txt 记忆地址 adb connect 一次（内部 30s 节流），
			// 结果由下一次全量校准/RefreshNow 验证。
			a.adb.RecoverIfEmpty(qctx)
		}
		if err == nil {
			// 全量列表经同一事件分发主函数处理：diff → 事件 → 动作 → 显示层。
			a.applyTrackUpdateCtx(qctx, devs)
			// gui43：投屏会话实时 transport/TLS 判定（全量校准时刷新；无活动会话跳过）
			a.refreshSessionTransports(qctx)
		} else {
			a.mu.Lock()
			// 真空期去抖：bat 重置 adb 服务（kill-server+start-server）期间失败属瞬态——
			// 连续失败未达 adbFailDebounce 前保持 adbOK=true 与上次设备列表（前端
			// 显示"刷新中"软提示），不亮红条；达到阈值才标记 adb 不可用（真实致命错误）。
			// 失败期间不额外跑任何 adb 命令（加重真空），也不执行弹窗/档案同步/探测
			// （它们都在 err==nil 分支内，真空期自然跳过，恢复后下一次校准自动触发）。
			a.adbFail++
			shouldHeal := false
			if a.adbFail >= adbFailDebounce {
				if a.adbOK {
					a.adbOK = false
					bridge.DebugLog("[app] adb 连续失败 %d 轮（%v）→ 标记 adb 不可用（红条）；真空期去抖阈值=%d 轮",
						a.adbFail, err, adbFailDebounce)
				}
				if !a.adbHealTried {
					// gui49-fix3 半死保险：连续失败达到阈值 → kill-server×2+start-server。
					// 每段连续失败只触发一次（成功后清零）。
					a.adbHealTried = true
					shouldHeal = true
					bridge.DebugLog("[app] adb 半死保险触发：连续失败 %d 轮（%v）→ kill-server×2+start-server",
						a.adbFail, err)
				}
			}
			a.mu.Unlock()
			if shouldHeal {
				a.healAdbServer(ctx)
			}
		}
	})
}

// healAdbServer 是 adb 半死保险（gui49-fix3）：复用 discovery.RestartServer
// （kill-server×2 + start-server）自愈。有活动投屏会话时跳过（杀共享 server
// 会断正在进行的投屏）；成功后清 adbFail 与半死闩锁。
func (a *App) healAdbServer(ctx context.Context) {
	a.mu.RLock()
	hasActive := false
	for _, st := range a.sessions {
		if st.cast.Active {
			hasActive = true
			break
		}
	}
	a.mu.RUnlock()
	if hasActive {
		bridge.DebugLog("[app] adb 半死保险：有活动投屏会话，跳过 kill-server（避免断投）")
		return
	}

	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.disc.RestartServer(hctx); err != nil {
		bridge.DebugLog("[app] adb 半死保险自愈失败：%v", err)
		return
	}
	bridge.DebugLog("[app] adb 半死保险自愈完成（kill-server×2+start-server），清零失败计数")
	a.mu.Lock()
	a.adbFail = 0
	a.adbHealTried = false
	a.mu.Unlock()
}

// refreshSessionTransports 刷新投屏会话的实际 transport/TLS 显示（gui43）。
func (a *App) refreshSessionTransports(ctx context.Context) {
	a.mu.RLock()
	hasActiveCast := false
	for _, st := range a.sessions {
		if st.cast.Active {
			hasActiveCast = true
			break
		}
	}
	a.mu.RUnlock()
	if !hasActiveCast {
		return
	}
	tr, err := a.adb.Transports(ctx)
	if err != nil {
		return
	}
	a.mu.Lock()
	for _, st := range a.sessions {
		if !st.cast.Active {
			continue
		}
		ts := selectSessionTransport(st.usbSerial, st.castAddr, st.castAddr2, tr)
		if ts == "" {
			continue // adb 真空/重连窗口：保持上次显示，不误判
		}
		st.cast.TransportSerial = ts
		// 只按环境事实（port!=5555）判 TLS；广播快照判据已退役（档案为唯一事实源）。
		st.cast.Tls = tlsActiveOf(ts, nil)
	}
	a.mu.Unlock()
}

// --- gui51：设备删除（右键/批量）+ 用户自定义名称（批量改名） ---

func (a *App) deletedUsbMarked(id string) bool {
	a.deleteMu.Lock()
	defer a.deleteMu.Unlock()
	return a.deletedUsb[id]
}

func (a *App) setDeletedUsb(id string) {
	a.deleteMu.Lock()
	a.deletedUsb[id] = true
	a.deleteMu.Unlock()
}

func (a *App) clearDeletedUsb(id string) {
	a.deleteMu.Lock()
	delete(a.deletedUsb, id)
	a.deleteMu.Unlock()
}

// clearDeletedUsbForAdded 新插线事件（USB added 任意状态帧）清删除标记 → 重新学习入档。
// gui52-fix17b：按设备索引（serial → 全键集）全清，不再按帧字段凑键（added 帧常无
// marketname，按字段清会残留市场名键 → device 帧被持续过滤 → 档案永不建档——
// 2026-09-02 23:56 华为插线不建档实锤）。只认 added：拔线再插=重插=重来；
// 删除后线还插着的 changed/offline 帧（同一周期状态更新）不得清集（否则删不掉）。
func (a *App) clearDeletedUsbForAdded(added []adb.Device) {
	for i := range added {
		d := &added[i]
		if d.ConnType != "usb" || d.Serial == "" {
			continue
		}
		if a.deletedUsbKeysIndexed(d.Serial) {
			a.clearDeletedAllForSerial(d.Serial)
			bridge.DebugLog("[app] 插线清删除集（索引全清）：%s", d.Serial)
			continue
		}
		// 兜底：无索引（旧标记/测试直设）时按字段清
		if id := a.identityOf(d); id != "" {
			a.clearDeletedUsb(id)
		}
		a.clearDeletedUsb(d.Serial)
		if d.Identity != "" {
			a.clearDeletedUsb(d.Identity)
		}
		if d.Marketname != "" {
			a.clearDeletedUsb(d.Marketname)
		}
	}
}

// deletedUsbKeysIndexed 该 serial 是否作为删除集设备索引存在。
func (a *App) deletedUsbKeysIndexed(serial string) bool {
	if serial == "" {
		return false
	}
	a.deleteMu.Lock()
	defer a.deleteMu.Unlock()
	return len(a.deletedUsbKeys[serial]) > 0
}

// clearDeletedAllForSerial 按设备索引清除删除集全键（gui52-fix17b）：
// serial 命中索引 → 清该设备全部键（档案键/市场名/身份/IP/TlsGuid/显示名…），
// 无论帧字段携带多少；索引未命中时兜底清 serial 本身。
func (a *App) clearDeletedAllForSerial(serial string) {
	if serial == "" {
		return
	}
	a.deleteMu.Lock()
	keys := a.deletedUsbKeys[serial]
	for _, k := range keys {
		if k != "" {
			delete(a.deletedUsb, k)
		}
	}
	delete(a.deletedUsbKeys, serial)
	a.deleteMu.Unlock()
	// serial 本身也是删除集条目（旧格式直设可能只有它）
	a.clearDeletedUsb(serial)
}

// devicesForSync 档案同步前过滤「本次插线周期已删除」的 USB 设备（防复活）。
func (a *App) devicesForSync(devs []adb.Device) []adb.Device {
	out := make([]adb.Device, 0, len(devs))
	for i := range devs {
		d := &devs[i]
		if a.deletedUsbMatch(d) {
			continue
		}
		out = append(out, *d)
	}
	return out
}

// DeleteDevices 删除档案设备（彻底，不复活）：
//   - profiles.RemoveDevice 立即写盘（devices + deviceOrder）；
//   - 无线地址执行 adb disconnect（防设备流重建档）；
//   - 无条件记录删除集全键（gui52-fix17：不限 USB 在线——无线设备删除后
//     adb server 的 mdns auto-connect（ADB_MDNS_AUTO_CONNECT 默认开启）会在
//     数秒内自动重连 transport，删除集是 GUI 侧唯一的防回归机制）。
func (a *App) DeleteDevices(keys []string) error {
	for _, key := range keys {
		if key == "" {
			continue
		}
		e, ok := a.profiles.RemoveDevice(key)
		if !ok {
			continue
		}
		for i := range e.Addrs {
			addr := e.Addrs[i].Addr
			if !IsIPPort(addr) {
				continue
			}
			a.adbDisconnectAddr(addr)
		}
		a.markDeletedAll(key, e)
		bridge.DebugLog("[app] 设备已删除：%s", key)
		// gui52-fix16 诊断：删除后档案残留检查（离线卡幽灵根因定位）
		if _, still := a.profiles.Entry(key); still {
			bridge.DebugLog("[app] !!! 删除后档案仍存在：%s", key)
		}
	}
	a.reapplyDisplay("设备删除")
	return nil
}

func (a *App) adbDisconnectAddr(addr string) {
	adbPath := ""
	if a.disc != nil {
		adbPath = a.disc.AdbPath
	}
	if adbPath == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, adbPath, "disconnect", addr)
	adb.HideConsole(cmd)
	if err := cmd.Run(); err != nil {
		bridge.DebugLog("[app] 删除设备 adb disconnect %s 失败：%v", addr, err)
		return
	}
	bridge.DebugLog("[app] 删除设备 adb disconnect %s", addr)
}

// markDeletedAll 删除时无条件记录「删除设备集」全键（gui52-fix17）：
// 不限 USB 在线——无线设备删除后 adb server 的 mdns auto-connect 会在数秒内
// 自动重连 transport（新端口/服务名形态），删除集是 GUI 侧唯一的防回归机制，
// 设备流 / mDNS 入档 / 待配对卡 / 新设备弹窗 / 显示层全部过该集过滤。
// 键集：档案键、市场名、厂商+型号、显示名、全部序列号（含无线短号）、
// TlsGuid（mDNS 服务名）、全部地址 IP（"ip:" 前缀，防与 serial 混淆）。
// gui52-fix17b：同时维护「按设备」索引（deletedUsbKeys：serial/短号 → 全键集），
// 清除走索引（clearDeletedAllForSerial），不再按帧字段凑键。
func (a *App) markDeletedAll(key string, e DeviceEntry) {
	keys := []string{key}
	if e.Marketname != "" {
		keys = append(keys, e.Marketname)
	}
	if e.Manufacturer != "" && e.Model != "" {
		keys = append(keys, e.Manufacturer+" "+e.Model)
	}
	if e.DisplayName != "" {
		keys = append(keys, e.DisplayName)
	}
	for _, s := range e.Serials {
		if s != "" {
			keys = append(keys, s)
		}
	}
	if e.TlsGuid != "" {
		keys = append(keys, e.TlsGuid)
	}
	for i := range e.Addrs {
		if ip := ipOfAddr(e.Addrs[i].Addr); ip != "" {
			keys = append(keys, "ip:"+ip)
		}
	}
	// 去重 + 写删除集
	uniq := make([]string, 0, len(keys))
	seen := map[string]bool{}
	a.deleteMu.Lock()
	for _, k := range keys {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		a.deletedUsb[k] = true
		uniq = append(uniq, k)
	}
	// 按设备索引：serial/短号（设备流出现即按此清全键）
	idxs := make(map[string]bool)
	for _, s := range e.Serials {
		if s != "" {
			idxs[s] = true
		}
	}
	for i := range e.Addrs {
		if ip := ipOfAddr(e.Addrs[i].Addr); ip != "" {
			idxs["ip:"+ip] = true
		}
	}
	if e.TlsGuid != "" {
		idxs[e.TlsGuid] = true
	}
	for idx := range idxs {
		a.deletedUsbKeys[idx] = appendUniqueStrings(a.deletedUsbKeys[idx], uniq...)
		// 兼容旧格式：索引键也可能是一个删除集键（如串行号直查）
	}
	a.deleteMu.Unlock()
	bridge.DebugLog("[app] 删除设备集（%s）键全集：%v 索引：%v", key, uniq, keysOfMap(idxs))
}

// appendUniqueStrings 向 dst 追加 src 中不重复的元素（保持 dst 顺序）。
func appendUniqueStrings(dst []string, src ...string) []string {
	have := map[string]bool{}
	for _, s := range dst {
		have[s] = true
	}
	for _, s := range src {
		if s == "" || have[s] {
			continue
		}
		have[s] = true
		dst = append(dst, s)
	}
	return dst
}

// keysOfMap 取 map 键集合（有序性不保证，仅日志用）。
func keysOfMap(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// deletedUsbMatch 设备是否命中「已删除」标记集（gui52-fix17 泛化：USB+无线全谱）。
// 匹配键：档案身份（identityOf）、Identity、Serial（原值 + IP 提取 + mDNS 短号提取）、
// Marketname、Wireless。无线 transport 的端口/形态会变（adb server mdns auto-connect
// 自动重连），IP 级与短号级匹配保证「删掉的设备换了端口也认得出」。
func (a *App) deletedUsbMatch(d *adb.Device) bool {
	if d == nil {
		return false
	}
	if id := a.identityOf(d); id != "" && a.deletedUsbMarked(id) {
		return true
	}
	if d.Identity != "" && a.deletedUsbMarked(d.Identity) {
		return true
	}
	if d.Serial != "" {
		if a.deletedUsbMarked(d.Serial) {
			return true
		}
		if IsIPPort(d.Serial) && a.deletedUsbMarked("ip:"+ipOfAddr(d.Serial)) {
			return true
		}
		// mDNS 令牌 transport（adb-<短号>-XXXX._adb-tls-connect._tcp）：短号级匹配
		if serial := TlsServiceIdentity(d.Serial); serial != "" && a.deletedUsbMarked(serial) {
			return true
		}
	}
	if d.Marketname != "" && a.deletedUsbMarked(d.Marketname) {
		return true
	}
	if d.Wireless != "" {
		if a.deletedUsbMarked(d.Wireless) {
			return true
		}
		if IsIPPort(d.Wireless) && a.deletedUsbMarked("ip:"+ipOfAddr(d.Wireless)) {
			return true
		}
	}
	return false
}

// filterDeletedUsb 显示层过滤「已删除」设备卡（gui52-fix17 泛化：USB+无线全谱）。
// 删除=删档案，但设备流还会持续推送——USB 线还插着（原 USB 场景）或 adb server
// mdns auto-connect 秒回无线 transport（新端口）——不隐藏就「删掉了又回来」。
// 用户主动重新配对（扫码/手动/插线学习）后清标记，设备恢复发现。
func (a *App) filterDeletedUsb(devs []adb.Device) []adb.Device {
	out := make([]adb.Device, 0, len(devs))
	for i := range devs {
		d := devs[i]
		if a.deletedUsbMatch(&d) {
			bridge.DebugLog("[app] filterDeletedUsb 过滤：%s|%s|%s", d.Serial, d.State, d.ConnType)
			continue
		}
		out = append(out, d)
	}
	return out
}

// RenameDevices 批量改名：非空 → DisplayNameSet=true；空 → 清用户名称（原算法链）。
func (a *App) RenameDevices(names map[string]string) error {
	for key, name := range names {
		if key == "" {
			continue
		}
		if a.profiles.SetDisplayName(key, name) {
			bridge.DebugLog("[app] 设备改名：%s -> %q", key, name)
		}
	}
	a.reapplyDisplay("设备改名")
	return nil
}

// RenameDevicesJSON 是前端 RPC 的 JSON 字符串入口（WebView 绑定 map 兼容性）。
func (a *App) RenameDevicesJSON(raw string) error {
	var names map[string]string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return err
	}
	return a.RenameDevices(names)
}

// applyTrackUpdate 是 track 回调/测试入口的主分发函数：
// diff + 事件 + 动作 + 显示层提交（等价于 a.applyTrackUpdateCtx 用 8s 兜底 ctx）。
func (a *App) applyTrackUpdate(devs []adb.Device) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	a.applyTrackUpdateCtx(ctx, devs)
}

// applyTrackUpdateCtx 处理一份完整设备列表快照（track 列表块或 adb.List 全量）：
// 折叠幽灵条目 → 与上一份权威列表 diff → 事件动作（插线学习/遮罩/弹窗/掉线防抖）
// → 档案同步 → 显示层合成（TLS 标注/离线卡/USB 遮罩）→ 提交 a.devices。
func (a *App) applyTrackUpdateCtx(ctx context.Context, devs []adb.Device) {
	// gui49-fix7：全流程串行化——track 读循环与 60s 校准轮（及 RefreshNow）
	// 不得并发进入；提交顺序恒为「事件动作（置位）→ 显示提交」。
	a.applyTrackMu.Lock()
	defer a.applyTrackMu.Unlock()

	// gui12/gui31 折叠（在 diff 之前：diff 基于折叠后的权威列表）
	devs = a.foldGhostWireless(devs)
	devs = a.foldGhostUsb(devs)

	a.mu.Lock()
	prev := append([]adb.Device(nil), a.lastTrack...)
	first := !a.trackBaseline
	a.lastTrack = append([]adb.Device(nil), devs...)
	a.trackBaseline = true
	a.mu.Unlock()

	// 设备流首块基线到达 → 尝试启动基线 stale 对账（once；mdns 首块未到则等）。
	if first {
		a.maybeStartupBaselineStale()
	}

	added, removed, changed := adb.DiffDevices(prev, devs)

	// gui52-fix9 诊断：track diff 明细（serial/state/connType）——
	// 重演开关无线调试时定位「removed 的是哪个 transport（TLS 还是 5555）」的硬事实。
	if len(added) > 0 || len(removed) > 0 || len(changed) > 0 {
		var sb strings.Builder
		for i := range added {
			fmt.Fprintf(&sb, " [+%s|%s|%s]", added[i].Serial, added[i].State, added[i].ConnType)
		}
		for i := range removed {
			fmt.Fprintf(&sb, " [-%s|%s|%s]", removed[i].Serial, removed[i].State, removed[i].ConnType)
		}
		for i := range changed {
			fmt.Fprintf(&sb, " [~%s|%s->%s|%s]", changed[i].Serial, changed[i].State, changed[i].State, changed[i].ConnType)
		}
		bridge.DebugLog("[app] track diff：%s", sb.String())
	}

	// 防抖取消：防抖期间设备再现 → 取消掉线 timer（不误报）。
	for i := range added {
		a.cancelDrop(&added[i])
	}
	for i := range changed {
		a.cancelDrop(&changed[i])
	}

	// 弹窗必须在 SyncDevices 之前：同步会把首见设备并入档案，误判“已建档”。
	a.popupOnEvents(devs, added, changed, first)

	// gui51：新插线事件清删除标记（fix17b：USB added 任意状态帧按索引全清；
	// 随后档案同步用过滤列表（已删除设备不重建档）。
	a.clearDeletedUsbForAdded(added)
	syncDevs := a.devicesForSync(devs)
	// 设备档案同步（identity 归并/重键、serials 累积、addrs 状态更新）。
	// gui49-fix6：插线遮罩活跃 identity 进入离线观察豁免（removed 折腾期不误标）。
	if a.profiles.SyncDevicesExempt(syncDevs, a.plugExemptIDs()) {
		bridge.DebugLog("[app] 设备档案已同步（%d 台在线）", len(syncDevs))
	}
	// gui52fix3：设备流新建档案后事件驱动合卡（瞬时 IP:port 过渡档并入同 IP 主档案）。
	a.profiles.CleanOrphanIPPort()

	// 拔线遮罩（gui49）：清因优先处理（无线 device 恢复 / USB 再现），
	// 再按 USB removed 启动拔线遮罩；与插线遮罩互斥由 unplugStart 内部吸收。
	a.handleUnplugEvents(devs, removed)

	// 遮罩状态机（plugging 唯一状态机，事件驱动）
	a.dispatchPlugEvents(devs, added, changed)

	// 插线学习（gui48-teachfix）：事件链必须覆盖完整插线状态曲线——
	// USB added（任意状态）与状态翻转 changed（offline/unauthorized→device）
	// 都进入学习触发；teachTcpipSerial 只在 State==device 时实际执行。
	var addedUSB, changedUSB []adb.Device
	for i := range added {
		if added[i].ConnType == "usb" {
			addedUSB = append(addedUSB, added[i])
		}
	}
	for i := range changed {
		if changed[i].ConnType == "usb" {
			changedUSB = append(changedUSB, changed[i])
		}
	}
	a.maybeTeachTcpipEvent(ctx, devs, addedUSB, changedUSB)

	// gui49-fix6：插线遮罩稳定确认——USB 连续 device 满 2s 清遮罩；
	// 非 device 波动/removed 重置计时。
	a.plugStabilityUpdate(devs)

	// gui52-fix14：配对遮罩稳定确认——任一 transport 连续 device 满 2s 清遮罩；
	// 无线 adbd 波动期 offline 反复（无单一 removed 信号）→ 状态判定+重置。
	a.pairStabilityUpdate(devs)

	// 掉线探测：removed 事件 → 2s 防抖 → justDropped（ForceDiscover + 离线打 stale）
	for i := range removed {
		a.scheduleDrop(&removed[i])
	}

	// 探测触发：档案中设备不在线且档案有 addrs → mDNS+并行 connect（15s 节流）
	// 显示层提交
	a.commitDisplay(devs, "applyTrackUpdate")
}

// commitDisplay 构建显示层列表并提交到 a.devices。
// gui48-mdns9：显示层统一——unifyProfileCards 是「一设备一卡 + 双源合一」的
// 唯一入口：先按原始 transport 归并（Serial 尚未被显示地址替换，副行保持真实
// 无线 transport），再统一从档案现算装饰，最后补齐唯一缺失卡。
// decorateTls 不再参与本提交路径（保留为直接单测的装饰入口）。
// src=提交源（gui49-fix7 诊断日志：applyTrackUpdate / reapplyDisplay / 遮罩刷新）。
func (a *App) commitDisplay(devs []adb.Device, src ...string) {
	source := "未知"
	if len(src) > 0 && src[0] != "" {
		source = src[0]
	}
	a.teachMu.Lock()
	plugN := len(a.plugging)
	a.teachMu.Unlock()
	// gui52-fix13：显示提交最前先清孤儿——applyProfileNames 与 unifyProfileCards
	// 都必须基于干净档案。否则「孤儿键还在档」时 applyProfileNames 会给该
	// transport 卡套上孤儿键的 IP 名（36475 一闪），孤儿随后删除但名字已设置。
	a.profiles.CleanOrphanIPPort()
	bridge.DebugLog("[app] 显示提交：%s（插线遮罩活跃=%d，%s）", source, plugN, time.Now().Format("15:04:05.000"))
	applyProfileNames(devs, a.profiles)
	a.buildPending(devs)
	devs = a.unifyProfileCards(devs)
	devs = a.shieldUsbLearning(devs)
	devs = a.shieldPairing(devs)
	devs = a.shieldUnplugDisconnect(devs)

	// gui49-fix11 诊断：每次显示提交后输出最终卡列表摘要（Serial|State|ConnType|
	// Connecting|Name），用于定位额外卡（如第 4 张卡）的产出路径。
	var sb strings.Builder
	fmt.Fprintf(&sb, "n=%d", len(devs))
	for i := range devs {
		d := &devs[i]
		fmt.Fprintf(&sb, " [%d:%s|%s|%s|connecting=%v|name=%s]", i, d.Serial, d.State, d.ConnType, d.Connecting, d.Name)
	}
	bridge.DebugLog("[app] 显示提交摘要：%s", sb.String())

	a.mu.Lock()
	a.adbOK = true
	a.adbFail = 0
	a.adbHealTried = false
	a.devices = devs
	a.mu.Unlock()
}

// dispatchPlugEvents 按事件序列推进 plugging 状态机（gui48-teachfix5）：
//   - 置位入口唯一 =「连线信号」：USB added（任意状态）。changed 跌回
//     （device→offline/unauthorized）不再置位——拔线/任何「有→无」不得启动进程；
//   - 进程运行期收到的重复置位信号由 plugStart 幂等吸收（不重计时/不重复日志）；
//   - 遮罩清因唯一双通道：getprop==5555 判定（主）/ 10s 兜底超时（保险丝）。
//     其余事件（removed/在线无线卡/设备流自然处理）不得清遮罩——
//     历史「终点二」「拔线确认」「changed 跌回置位」均已删除。
func (a *App) dispatchPlugEvents(devs []adb.Device, added, changed []adb.Device) {
	now := time.Now()
	// 起点：USB added（含 offline/unauthorized/device）
	for i := range added {
		d := &added[i]
		if d.ConnType != "usb" || d.Serial == "" {
			continue
		}
		id := a.identityOf(d)
		a.plugStart(id, now, "added(usb)")
	}
}

// --- gui49：拔线遮罩（「断开中…」卡，与插线遮罩对称） ---

// handleUnplugEvents 拔线遮罩事件处理：
//   - 主清因：当前设备流出现该 identity 的无线 device → 立即清拔线遮罩；
//   - USB 再现（快速重插）→ 清拔线遮罩（插线遮罩接管）；
//   - 置位唯一入口：USB removed 且当前设备流已无该 identity 任何条目 →
//     unplugStart（无防抖；插线遮罩运行期内由 unplugStart 幂等吸收）。
func (a *App) handleUnplugEvents(devs []adb.Device, removed []adb.Device) {
	// 清因优先：无线 device 恢复 / USB 再现。
	for i := range devs {
		d := &devs[i]
		if d.Serial == "" {
			continue
		}
		if (d.ConnType == "wifi" && d.State == "device") || d.ConnType == "usb" {
			if id := a.identityOf(d); id != "" {
				a.unplugClear(id, "设备恢复")
			}
		}
	}

	// 置位：USB removed，且当前设备流里该 identity 无 State==device 的条目。
	// 只认 device：真实拔线时序里 197:5555 先标 offline 但仍保留在列表——offline
	// 无线条目不是「无线恢复」，不得阻止拔线遮罩启动。
	present := map[string]bool{}
	for i := range devs {
		d := &devs[i]
		if d.State != "device" {
			continue
		}
		if id := a.identityOf(d); id != "" {
			present[id] = true
		}
	}
	for i := range removed {
		d := &removed[i]
		if d.ConnType != "usb" || d.Serial == "" {
			continue
		}
		// gui52-fix16c：已删除的设备拔线 = 正常收尾（用户主动删除后移除物理线），
		// 不启动拔线遮罩「断开中…」——否则删除后拔线会冒出幽灵卡。
		if a.deletedUsbMatch(d) {
			continue
		}
		id := a.identityOf(d)
		if id == "" {
			id = d.Serial
		}
		if present[id] {
			continue // 同一事件流里已恢复（无线 device）：不启动拔线遮罩
		}
		a.unplugStart(id, time.Now(), "removed(usb)")
	}
}

// unplugStart 置位拔线遮罩（identity → 开始时刻），启动 10s 兜底 timer。
// 幂等吸收重复信号；插线遮罩运行期内不启动（互斥）。
func (a *App) unplugStart(id string, now time.Time, src ...string) {
	if id == "" {
		return
	}
	source := "未知"
	if len(src) > 0 && src[0] != "" {
		source = src[0]
	}
	a.teachMu.Lock()
	// 互斥：插线遮罩盖着（adbd 重启窗口等）→ removed 吸收，不启动拔线遮罩。
	if _, plugActive := a.plugging[id]; plugActive {
		a.teachMu.Unlock()
		return
	}
	started := false
	if _, ok := a.unplugging[id]; !ok {
		a.unplugging[id] = now
		started = true
		bridge.DebugLog("[app] 拔线遮罩开始：%s（源=%s，%s）", id, source, now.Format("15:04:05.000"))
	}
	if _, ok := a.unplugTimers[id]; !ok {
		a.unplugTimers[id] = time.AfterFunc(unplugShieldTimeout, func() {
			a.guard("unplug-timeout", func() { a.unplugTimeout(id) })
		})
	}
	a.teachMu.Unlock()
	if started {
		a.refreshDisplayForPlug()        // 立即显示「断开中…」卡
		a.unplugLaunchConnectConfirm(id) // 异步 connect 确认：由 connect 定无线死活
	}
}

// unplugClear 清除拔线遮罩、停兜底/重试 timer；真实状态变化后立即刷新显示层。
func (a *App) unplugClear(id string, src ...string) {
	if id == "" {
		return
	}
	source := "未知"
	if len(src) > 0 && src[0] != "" {
		source = src[0]
	}
	a.teachMu.Lock()
	at, ok := a.unplugging[id]
	if !ok {
		a.teachMu.Unlock()
		return
	}
	delete(a.unplugging, id)
	if t := a.unplugTimers[id]; t != nil {
		t.Stop()
		delete(a.unplugTimers, id)
	}
	if t := a.unplugRetryTimers[id]; t != nil {
		t.Stop()
		delete(a.unplugRetryTimers, id)
	}
	delete(a.unplugRetries, id)
	bridge.DebugLog("[app] 拔线遮罩结束：%s（源=%s，持续=%s）", id, source, time.Since(at).Round(time.Millisecond))
	a.teachMu.Unlock()
	a.refreshDisplayForPlug()
}

// unplugTimeout 是拔线遮罩 10s 兜底回调：无线未恢复 → 清遮罩 → 真实档案态。
func (a *App) unplugTimeout(id string) {
	a.teachMu.Lock()
	at, ok := a.unplugging[id]
	if !ok {
		a.teachMu.Unlock()
		return
	}
	cleared := false
	if time.Since(at) >= unplugShieldTimeout {
		delete(a.unplugging, id)
		cleared = true
		bridge.DebugLog("[app] 拔线遮罩兜底超时：%s（起于 %s）", id, at.Format("15:04:05.000"))
	}
	if t := a.unplugTimers[id]; t != nil {
		t.Stop()
		delete(a.unplugTimers, id)
	}
	if t := a.unplugRetryTimers[id]; t != nil {
		t.Stop()
		delete(a.unplugRetryTimers, id)
	}
	delete(a.unplugRetries, id)
	a.teachMu.Unlock()
	if cleared {
		a.refreshDisplayForPlug()
	}
}

// unplugActiveID 判断拔线遮罩是否仍盖住（teachMu 保护）。
func (a *App) unplugActiveID(id string) bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	_, ok := a.unplugging[id]
	return ok
}

// unplugTargetAddr 拔线/插线遮罩 connect 目标（gui52）：取 OrderedAddrs[0]
// ——active 优先（TLS 优先 → 5555），无 active 时 stale 也是离线候选
// （遮罩 connect 确认正是用来把 stale 翻回 active 的）。节流已由
// OrderedAddrs 内存态 60s 判定执行。
func (a *App) unplugTargetAddr(id string) string {
	if cands := a.profiles.OrderedAddrs(id); len(cands) > 0 {
		return cands[0].Addr
	}
	return ""
}

// unplugLaunchConnectConfirm 拔线遮罩置位后异步补 connect 确认。
func (a *App) unplugLaunchConnectConfirm(id string) {
	addr := a.unplugTargetAddr(id)
	if addr == "" {
		bridge.DebugLog("[app] 拔线遮罩 connect 确认：%s 无档案无线地址，等待 10s 兜底", id)
		return
	}
	go a.guard("unplug-connect-confirm", func() {
		if a.unplugConfirmFn != nil {
			a.unplugConfirmFn(context.Background(), id, addr, 1)
			return
		}
		a.unplugConnectConfirm(context.Background(), id, addr, 1)
	})
}

// unplugConnectConfirm 执行一次 connect 确认（gui49-fix2）：成功 → 直写 active
// + 清遮罩；失败 → 直写 stale + 有限重试（事件驱动 timer，禁止阻塞轮询）。
func (a *App) unplugConnectConfirm(ctx context.Context, id, addr string, attempt int) {
	if !a.unplugActiveID(id) {
		return // 遮罩已被主清因/兜底清掉：迟到结果不动作
	}
	bridge.DebugLog("[app] 拔线遮罩 connect 确认：%s -> %s（第 %d 次）", id, addr, attempt)
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	err := a.disc.Connect(cctx, addr)
	cancel()

	if !a.unplugActiveID(id) {
		return
	}
	if err == nil {
		bridge.DebugLog("[app] 拔线遮罩 connect 成功：%s -> %s（active）", id, addr)
		a.profiles.AddrSuccess(id, addr)
		a.unplugClear(id, "connect成功")
		return
	}
	bridge.DebugLog("[app] 拔线遮罩 connect 失败：%s -> %s（%v）→ stale", id, addr, err)
	a.profiles.MarkAddrStale(id, addr)
	a.scheduleUnplugConnectRetry(id, addr, attempt)
}

// scheduleUnplugConnectRetry 失败后的事件驱动有限重试（timer，不阻塞）。
func (a *App) scheduleUnplugConnectRetry(id, addr string, attempt int) {
	if attempt >= unplugConnectAttempts {
		bridge.DebugLog("[app] 拔线遮罩 connect 确认：%s 已重试 %d 次，等待 10s 兜底", id, attempt)
		return
	}
	a.teachMu.Lock()
	if _, active := a.unplugging[id]; !active {
		a.teachMu.Unlock()
		return
	}
	if _, ok := a.unplugRetryTimers[id]; ok {
		a.teachMu.Unlock()
		return
	}
	next := attempt + 1
	a.unplugRetries[id] = next
	a.unplugRetryTimers[id] = time.AfterFunc(unplugConnectRetryDelay, func() {
		a.guard("unplug-connect-retry", func() {
			// 先清 timer 占位，允许本轮失败后继续排下一次重试。
			a.teachMu.Lock()
			delete(a.unplugRetryTimers, id)
			a.teachMu.Unlock()
			a.unplugConnectConfirm(context.Background(), id, addr, next)
		})
	})
	a.teachMu.Unlock()
}

// shieldUnplugDisconnect 显示层：拔线遮罩运行期把该 identity 所有设备卡
// （含投屏中的卡）替换为一张「断开中…」无线卡（按钮禁点）。Serial 使用
// 合成键避免命中活动会话（前端 cast 分支优先于 connecting，投屏中卡也必须
// 被遮罩盖住）；WirelessIP 用档案 active 地址（TLS 优先）。
func (a *App) shieldUnplugDisconnect(devs []adb.Device) []adb.Device {
	a.teachMu.Lock()
	active := map[string]time.Time{}
	for id, at := range a.unplugging {
		active[id] = at
	}
	a.teachMu.Unlock()
	if len(active) == 0 {
		return devs
	}

	ids := make([]string, 0, len(active))
	for id := range active {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	belongs := func(d *adb.Device, id string) bool {
		if d == nil {
			return false
		}
		if d.Identity == id || a.identityOf(d) == id {
			return true
		}
		if k := a.profiles.ResolveKey(d.Serial); k == id {
			return true
		}
		if d.Wireless != "" && a.profiles.ResolveKey(d.Wireless) == id {
			return true
		}
		return false
	}

	drop := map[int]bool{}
	for i := range devs {
		for _, id := range ids {
			if belongs(&devs[i], id) {
				drop[i] = true
				break
			}
		}
	}

	out := make([]adb.Device, 0, len(devs)+len(ids))
	for i := range devs {
		if !drop[i] {
			out = append(out, devs[i])
		}
	}
	for _, id := range ids {
		name := id
		addr := ""
		if e, ok := a.profiles.Entry(id); ok {
			name = profileCardName(e, id)
			addr = a.wirelessStartAddr(id)
			if addr == "" {
				addr = recentOkAddr(e)
			}
		}
		out = append(out, adb.Device{
			Serial:     "unplug-" + id, // 合成键：避免命中活动会话 serial/identity
			State:      "device",
			ConnType:   "wifi",
			Name:       name,
			WirelessIP: addr,
			Connecting: true, // 前端：wifi+connecting → 「断开中…」
		})
	}
	return out
}

// scheduleDrop 为 removed 事件启动 2s 防抖 timer（key=identity 优先，回退 serial）。
func (a *App) scheduleDrop(d *adb.Device) {
	if d == nil || d.Serial == "" {
		return
	}
	id := a.identityOf(d)
	if id == "" {
		id = d.Serial
	}
	a.dropMu.Lock()
	if _, ok := a.dropTimers[id]; ok {
		a.dropMu.Unlock()
		return
	}
	a.dropTimers[id] = time.AfterFunc(dropDebounce, func() {
		a.guard("drop-debounce", func() { a.onDropped(id) })
	})
	a.dropMu.Unlock()
}

// cancelDrop 取消指定设备的掉线防抖 timer（防抖期间再现 → 不误报）。
func (a *App) cancelDrop(d *adb.Device) {
	if d == nil || d.Serial == "" {
		return
	}
	id := a.identityOf(d)
	if id == "" {
		id = d.Serial
	}
	a.dropMu.Lock()
	if t := a.dropTimers[id]; t != nil {
		t.Stop()
		delete(a.dropTimers, id)
	}
	a.dropMu.Unlock()
}

// onDropped 是 removed 防抖后的掉线处理（gui48-mdns4）：设备流确认缺席即可；
// 不打 stale、不探测——各地址状态由 mDNS 层 gone 事件按服务类型分形态写入档案
// （Goodbye/门铃确认等价于已探测）。设备级离线卡由「全部地址 stale + 设备流无
// device 条目」的档案状态自然判定。
//
// gui48-teachfix4：removed 只保留设备流层面的自然处理，**不再干预遮罩**——
// 「拔线确认」清遮罩路径已删除（adbd 重启断档实测可达 3.45s~5s，2s 防抖无法
// 与真拔线区分，曾造成误杀+闪烁）。真拔线由 10s 兜底超时自然呈现档案态。
func (a *App) onDropped(id string) {
	a.dropMu.Lock()
	delete(a.dropTimers, id)
	a.dropMu.Unlock()

	a.mu.RLock()
	present := false
	var track []adb.Device
	track = append(track, a.lastTrack...)
	for i := range track {
		d := &track[i]
		if d.Serial == id || a.identityOf(d) == id {
			present = true
			break
		}
	}
	a.mu.RUnlock()
	if present {
		return // 防抖期间设备再现：取消掉线
	}
	bridge.DebugLog("[app] removed 防抖确认：%s 设备流缺席；地址 stale 交由 mDNS gone 事件分形态处理", id)
	// gui52fix4：不依赖组播——对档案 active 地址做一轮 connect 验尸
	// （硬事实：通=保持 active，不通=stale）。异步执行防阻塞防抖回调。
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		a.probeProfileActiveAddrs(ctx, id)
	}()
}

// clearPopupLocked 清除当前弹窗并停掉过期 timer（调用方持 a.mu）。
func (a *App) clearPopupLocked() {
	if a.popup.expireTimer != nil {
		a.popup.expireTimer.Stop()
		a.popup.expireTimer = nil
	}
	a.popup.info = nil
}

// armPopupExpireTimerLocked 为新弹窗启动 30s 未处理自动消失 timer（调用方持 a.mu）。
func (a *App) armPopupExpireTimerLocked() {
	if a.popup.info == nil {
		return
	}
	if a.popup.expireTimer != nil {
		a.popup.expireTimer.Stop()
	}
	a.popup.expireTimer = time.AfterFunc(popupExpireTimeout, func() {
		a.guard("popup-expire", func() { a.expirePopup() })
	})
}

// expirePopup 是弹窗超时回调：30s 未处理 → 自动消失（timer 回调内不持锁调外部函数）。
func (a *App) expirePopup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.popup.info == nil {
		return
	}
	if a.popup.expireTimer != nil {
		a.popup.expireTimer.Stop()
		a.popup.expireTimer = nil
	}
	a.popup.info = nil
	bridge.DebugLog("[app] 新设备弹窗 30s 未处理：自动消失")
}

// popupOnEvents 事件驱动弹窗：device 事件 + 无活动会话 + 非「暂不」+ 30s 防重复 → 弹。
// 首块基线只对“新设备（身份不在档案中）”弹；已建档设备需 USB device 事件（真实插拔）。
func (a *App) popupOnEvents(devs []adb.Device, added, changed []adb.Device, first bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()

	// ① 失效清理：设备下线或已创建会话 → 清除当前弹窗
	if a.popup.info != nil {
		online := false
		for i := range devs {
			d := &devs[i]
			if d.State == "device" && (d.Serial == a.popup.info.Serial ||
				(d.Wireless != "" && d.Wireless == a.popup.info.Serial)) {
				online = true
				break
			}
		}
		if !online || a.popupHandledLocked() {
			a.clearPopupLocked()
		}
	}
	// ② 未处理超时兜底检查（主路径由 expireTimer 回调负责，这里做事件时防御性清理）
	if a.popup.info != nil && now.Sub(a.popup.shownAt) >= popupExpireTimeout {
		a.clearPopupLocked()
	}
	// ③ “暂不”集合修剪：仅保留当前在线设备身份（离线再上线 = 新周期）
	onlineIDs := map[string]bool{}
	for i := range devs {
		d := &devs[i]
		if d.State == "device" && (d.ConnType == "usb" || d.ConnType == "wifi") {
			onlineIDs[a.identityOf(d)] = true
		}
	}
	for id := range a.popup.dismissed {
		if !onlineIDs[id] {
			delete(a.popup.dismissed, id)
			delete(a.popup.lastShown, id)
		}
	}
	// ④ 一次只展示一个弹窗
	if a.popup.info != nil {
		return
	}
	// ⑤ 候选：device 事件（added/changed）按序取第一个
	for i := range added {
		d := &added[i]
		if a.tryPopupDeviceLocked(d, now, first) {
			return
		}
	}
	for i := range changed {
		d := &changed[i]
		if a.tryPopupDeviceLocked(d, now, first) {
			return
		}
	}
}

// tryPopupDeviceLocked 单设备弹窗候选判定（调用方持 a.mu）。
func (a *App) tryPopupDeviceLocked(d *adb.Device, now time.Time, first bool) bool {
	if d.State != "device" || (d.ConnType != "usb" && d.ConnType != "wifi") {
		return false
	}
	// gui52-fix17：已删除设备（删档后 adb server mdns auto-connect 秒回的
	// transport/新端口）不得再弹「新设备」——否则删除后 2 秒弹窗 = 删不掉。
	if a.deletedUsbMatch(d) {
		return false
	}
	if a.hasSessionForDeviceLocked(d) {
		return false
	}
	id := a.identityOf(d)
	if id == "" {
		return false
	}
	if a.popup.dismissed[id] {
		return false
	}
	if t, ok := a.popup.lastShown[id]; ok && now.Sub(t) < newDevicePopupThrottle {
		return false
	}
	if !a.identityProfiledLocked(id, d) {
		// 新设备：无条件弹（USB/无线均可）
	} else if d.ConnType == "usb" && !first {
		// 已建档：USB device 事件（真实插拔）→ 弹；首块基线不弹
	} else {
		return false
	}
	name := a.displayNameOf(d)
	a.popup.info = &NewDeviceInfo{Serial: d.Serial, Name: name, ConnType: d.ConnType, Identity: id}
	a.popup.shownAt = now
	a.popup.lastShown[id] = now
	a.armPopupExpireTimerLocked()
	bridge.DebugLog("[app] 新设备弹窗（事件驱动）：%s（%s，%s）", name, d.Serial, d.ConnType)
	return true
}

// justDropped 掉线首拍判据（gui41c F4）：上一轮快照有该设备在线卡（State==device），
// 本轮 devs 完全无该设备（任意卡）→ 视为刚掉线，需要立即探测。
// 只做首拍触发，不引入任何恢复窗口状态机。
func (a *App) justDropped(prev, cur []adb.Device) bool {
	prevOnline := map[string]bool{}
	for i := range prev {
		d := &prev[i]
		if d.State != "device" {
			continue
		}
		if k := a.profiles.ResolveKey(d.Serial); k != "" {
			prevOnline[k] = true
		}
		if d.Wireless != "" && d.State == "device" {
			if k := a.profiles.ResolveKey(d.Wireless); k != "" {
				prevOnline[k] = true
			}
		}
	}
	curIDs := map[string]bool{}
	for i := range cur {
		d := &cur[i]
		if k := a.profiles.ResolveKey(d.Serial); k != "" {
			curIDs[k] = true
		}
		if d.Wireless != "" {
			if k := a.profiles.ResolveKey(d.Wireless); k != "" {
				curIDs[k] = true
			}
		}
	}
	for id := range prevOnline {
		if !curIDs[id] {
			return true
		}
	}
	return false
}

// maybeDiscover 无线探测触发点（设备轮询内）：
// "档案中设备不在线且档案有 addrs" → 触发一次探测（节流：每 15s 最多一次）。
// 探测在独立 goroutine 执行（6s 窗口），不阻塞 UI 轮询。
func (a *App) maybeDiscover(devs []adb.Device) {
	cands := a.profiles.OfflineCandidateAddrs(devs)
	if len(cands) == 0 {
		return
	}
	a.mu.Lock()
	if time.Since(a.lastDisc) < discoveryThrottle {
		a.mu.Unlock()
		return
	}
	a.lastDisc = time.Now()
	a.discBusy = true // gui22 防抖：探测起飞即标记，与 ForceDiscover 的 in-flight 判定同一锁原子
	a.mu.Unlock()
	bridge.DebugLog("[app] 触发无线探测：%d 个档案设备不在线", len(cands))
	go a.guard("discovery", func() { a.runDiscovery(context.Background(), cands) })
}

// maybeTeachTcpip「插线即学习」（gui30）：USB 在线设备的 adbd 重启后
// service.adb.tcp.port 重置（无线 5555 丢失）——bat 只在投屏流程里学习，
// GUI 插线不投屏时无线永远学不会、拔线即失联。这里对每台 USB 在线设备
// 检查端口，非 5555 → 自动 `adb -s <serial> tcpip 5555`，拔线后无线立即可用。
//
// 语义边界：
//   - 触发：devs 中 State=device 且 ConnType=usb（USB 插着=人在场=合规，
//     与 bat 同一原则）；纯无线/离线/未授权设备不执行。
//   - 幂等/节流：每设备每插线周期至多学习一次（taughtTcpip 记录；设备从
//     列表消失=周期结束——拔线或 adbd 重启瞬态，再出现即新周期重新学习，
//     覆盖华为 FLA-TL10 端口随机丢失场景）。端口已是 5555 → 只记不学
//     （本周期后续轮 0 开销，连 getprop 都跳过）。
//   - 竞态：`adb tcpip` 会重启设备端 adbd（USB 短暂断开 1-2s）——设备
//     暂时消失由 GUI 轮询自动恢复（本轮预占名额，重启窗口内不重复跑；
//     恢复后新周期查端口=5555 即停）。重启后第一次无线 connect 可能
//     Connection refused，探测重试窗口（gui23/25）已覆盖，无需特殊处理。
//   - getprop 失败（未授权/瞬态）→ 本轮放弃且本周期不再重试（防 adbd
//     重启竞态下每 2s 重复轰炸），下个插线周期自然再学。
//
// 并发：pollOnce 可能从多个 goroutine 调用（轮询循环/启动首轮/RefreshNow），
// taughtTcpip 由独立 teachMu 保护；adb 调用在锁外执行（不阻塞其它轮询）。
func (a *App) maybeTeachTcpip(ctx context.Context, devs []adb.Device) {
	// 本轮在场 USB serial 集合（周期边界：消失=周期结束）
	present := map[string]bool{}
	for _, d := range devs {
		if d.State == "device" && d.ConnType == "usb" && d.Serial != "" {
			present[d.Serial] = true
		}
	}
	a.teachMu.Lock()
	// 不在场的 serial 清除记忆：拔线/瞬态消失=周期结束，再出现重新学习
	for k := range a.taughtTcpip {
		if !present[k] {
			delete(a.taughtTcpip, k)
		}
	}
	// 本轮待检查/待学习的设备：在场且本周期尚未处理——先预占名额再执行
	// （无论 getprop/tcpip 成败，本周期至多试一次，防 adbd 重启竞态重复跑）
	var toCheck []string
	for _, d := range devs {
		if !present[d.Serial] || a.taughtTcpip[d.Serial] {
			continue
		}
		a.taughtTcpip[d.Serial] = true
		toCheck = append(toCheck, d.Serial)
	}
	a.teachMu.Unlock()

	for _, serial := range toCheck {
		a.teachTcpipSerial(ctx, serial)
	}
}

// maybeTeachTcpipEvent 是事件驱动的插线学习入口（gui48-teachfix）：
// 覆盖完整插线状态曲线——USB added（任意状态）与 USB changed 都进入触发判定；
// 只有当前 State==device 的 serial 才真正执行 teachTcpipSerial（device 就绪
// 才能读 IP/查端口）。added 时未就绪（offline/unauthorized）不占 taughtTcpip
// 名额，状态翻转为 device 的 changed 事件到达时补学；taughtTcpip 保证每个
// 插线周期至多学习一次。在场 USB device 集合仍用于 taughtTcpip 周期清理。
func (a *App) maybeTeachTcpipEvent(ctx context.Context, devs []adb.Device, added, changed []adb.Device) {
	present := map[string]bool{}
	for _, d := range devs {
		if d.State == "device" && d.ConnType == "usb" && d.Serial != "" {
			present[d.Serial] = true
		}
	}
	a.teachMu.Lock()
	for k := range a.taughtTcpip {
		if !present[k] {
			delete(a.taughtTcpip, k)
		}
	}
	ready := func(d adb.Device) bool {
		return d.State == "device" && d.ConnType == "usb" && d.Serial != ""
	}
	var toCheck []string
	for _, d := range added {
		if !ready(d) {
			if d.ConnType == "usb" && d.Serial != "" {
				bridge.DebugLog("[app] 插线学习待命：%s（%s）→ 等待 device 就绪", d.Serial, d.State)
			}
			continue
		}
		if a.taughtTcpip[d.Serial] {
			continue
		}
		a.taughtTcpip[d.Serial] = true
		toCheck = append(toCheck, d.Serial)
	}
	// gui48-teachfix 关键补学路径：offline/unauthorized → device 的状态翻转
	// 是 changed 事件（added 时被过滤后没有任何补学机会——00:38 复现根因）。
	for _, d := range changed {
		if !ready(d) {
			continue
		}
		if a.taughtTcpip[d.Serial] {
			continue
		}
		a.taughtTcpip[d.Serial] = true
		toCheck = append(toCheck, d.Serial)
	}
	// gui49-fix6：不再用 getprop 补查清遮罩——遮罩清因①由 plugStabilityUpdate
	// （USB 连续 device 满 2s）负责，学习与遮罩退出解耦。
	a.teachMu.Unlock()

	for _, serial := range toCheck {
		bridge.DebugLog("[app] 插线学习开始：%s", serial)
		a.teachTcpipSerial(ctx, serial)
	}
}

// teachTcpipSerial 对单台 USB device 就绪设备执行一次插线学习（gui48-teachfix2）：
// getprop 查端口（5555=遮罩退出判定）→ 先读 IP 候选（tcpip 会重启 adbd，
// 必须先读；物理接口过滤 + wlan>eth 优先级）→ 端口非 5555 则 tcpip 5555 →
// 候选并行 TCP 探测（第一个通者胜）→ 通了才写档案（不通不写）。
func (a *App) teachTcpipSerial(ctx context.Context, serial string) {
	gctx, cancel := context.WithTimeout(ctx, teachGetpropTimeout)
	port, err := a.teachOps.getpropFn(gctx, serial, "service.adb.tcp.port")
	cancel()
	if err != nil {
		bridge.DebugLog("[app] 插线学习 getprop 失败：%s（%v），本周期放弃", serial, err)
		return
	}
	port = strings.TrimSpace(port)
	bridge.DebugLog("[app] 插线学习端口：%s = %q", serial, port)
	// gui49-fix6：getprop 不再清插线遮罩——清因①改为「USB 连续 device 满 2s」
	// （plugStabilityUpdate）；学习与遮罩退出解耦。
	a.checkTlsSwitch(ctx, serial)
	// gui47-fix F1'：读 IP 必须发生在 tcpip 之前（adbd 正常时读得到；
	// tcpip 会重启设备端 adbd，之后读必失败）。候选=物理接口（排除蜂窝/隧道）。
	cands := a.learnWirelessIPCandidates(ctx, serial)
	if len(cands) == 0 {
		bridge.DebugLog("[app] 插线学习 IP：%s 未取得", serial)
	} else {
		bridge.DebugLog("[app] 插线学习 IP 候选：%s -> %s", serial, strings.Join(cands, ","))
	}

	if port == "5555" {
		// 端口本就 5555（幂等分支）：不再 tcpip；探测通过才对齐档案。
		if len(cands) == 0 {
			bridge.DebugLog("[app] 插线学习 IP：%s 未取得，无法验证 5555", serial)
			return
		}
		if chosen := a.probeWirelessCandidates(ctx, serial, cands); chosen != "" {
			a.alignWirelessIP(serial, chosen)
		}
		return
	}

	bridge.DebugLog("[app] 插线学习 tcpip：%s -> 5555", serial)
	tctx, cancel := context.WithTimeout(ctx, teachTcpipTimeout)
	err = a.teachOps.tcpipFn(tctx, serial, "5555")
	cancel()
	if err != nil {
		bridge.DebugLog("[app] 插线学习 tcpip 失败：%s（%v）", serial, err)
		return
	}
	// gui48-p1：学习成功即置位 plugging（幂等；盖住 adbd 重启窗口）。
	a.plugStartBySerial(serial, time.Now(), "tcpip成功")
	if len(cands) == 0 {
		bridge.DebugLog("[app] 插线学习 IP：%s 未取得，tcpip 后无法验证 5555", serial)
		return
	}
	if chosen := a.probeWirelessCandidates(ctx, serial, cands); chosen != "" {
		a.alignWirelessIP(serial, chosen)
	}
}

// checkTlsSwitch 插线学习时的 TLS 开关感知（gui47）：读取全局
// adb_wifi_enabled，若为 0（无线调试/TLS 通道关闭），把该设备档案中的 TLS
// 条目标记为 stale（不可用），避免后续无线探测继续用已关闭的 TLS 地址。
func (a *App) checkTlsSwitch(ctx context.Context, serial string) {
	sctx, cancel := context.WithTimeout(ctx, teachGetpropTimeout)
	val, err := a.teachOps.shellFn(sctx, serial, "settings", "get", "global", "adb_wifi_enabled")
	cancel()
	if err != nil || strings.TrimSpace(val) != "0" {
		return // 读取失败/未关闭/空：不动作
	}
	id := a.profiles.ResolveKey(serial)
	if id == "" {
		return
	}
	if a.profiles.StaleTls(id) {
		bridge.DebugLog("[app] 插线学习 TLS 开关=0：%s TLS 地址标记 stale（待广播重新验证）", id)
	}
}

// teachIPCandidate 是插线学习读到的无线 IP 候选（接口名 + 优先级）。
type teachIPCandidate struct {
	iface string
	ip    string
	prio  int
}

// teachIPIfaceExcluded 判定接口名是否为蜂窝/隧道/USB 共享/转发类接口
// （gui48-teachfix2 现象 A：rmnet_data2 蜂窝 IP 不可达，不得成为候选）。
func teachIPIfaceExcluded(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range []string{
		"rmnet",                                               // 蜂窝
		"tun", "tap", "ppp", "ifb", "sit", "ip6tnl", "gretap", // 隧道/转发
		"usb", "rndis", "ncm", // USB 共享
		"lo", "dummy", "veth", "docker", "virbr", "br-", "p2p", // 非物理
	} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// teachIPIfacePriority 物理接口优先级：wlan > eth > 其他物理接口。
func teachIPIfacePriority(name string) (int, bool) {
	lower := strings.ToLower(name)
	if teachIPIfaceExcluded(lower) {
		return 0, false
	}
	switch {
	case strings.HasPrefix(lower, "wlan"):
		return 0, true
	case strings.HasPrefix(lower, "eth"):
		return 1, true
	default:
		return 2, true
	}
}

// teachIPCandidatesFromAddr 解析 `ip addr`：按接口收集 inet IPv4，过滤非物理
// 接口与不可用 IP，按 wlan>eth>其他排序（稳定：同优先级保持输出顺序）。
func teachIPCandidatesFromAddr(out string) []teachIPCandidate {
	var outCands []teachIPCandidate
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasSuffix(fields[1], ":") {
			cur = strings.TrimSuffix(fields[1], ":")
			continue
		}
		if len(fields) < 2 || fields[0] != "inet" {
			continue
		}
		prio, ok := teachIPIfacePriority(cur)
		if !ok {
			continue
		}
		ip := fields[1]
		if i := strings.IndexByte(ip, '/'); i >= 0 {
			ip = ip[:i]
		}
		if isUsableIPv4(ip) {
			outCands = append(outCands, teachIPCandidate{iface: cur, ip: ip, prio: prio})
		}
	}
	sort.SliceStable(outCands, func(i, j int) bool { return outCands[i].prio < outCands[j].prio })
	return outCands
}

// teachCandidateIPs 候选去重（保序）取 IP。
func teachCandidateIPs(cands []teachIPCandidate) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		if seen[c.ip] {
			continue
		}
		seen[c.ip] = true
		out = append(out, c.ip)
	}
	return out
}

// firstPhysicalRouteSrcIP 从 `ip route` 取第一条非蜂窝/隧道等接口的 src IPv4；
// 无 dev 字段的行按可用 IP 接受（保持旧回退行为）。
func firstPhysicalRouteSrcIP(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		dev := ""
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "dev" {
				dev = fields[i+1]
				break
			}
		}
		if dev != "" && teachIPIfaceExcluded(dev) {
			continue
		}
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "src" && isUsableIPv4(fields[i+1]) {
				return fields[i+1]
			}
		}
	}
	return ""
}

// learnWirelessIPCandidates 插线学习读 IP 候选（gui48-teachfix2）：
// 优先 `ip addr` 的物理接口（wlan>eth>其他，排除 rmnet/tun/usb 等）；
// 无候选回退 `ip route`（同样按 dev 过滤非物理接口）。先于 tcpip 执行。
func (a *App) learnWirelessIPCandidates(ctx context.Context, serial string) []string {
	sctx, cancel := context.WithTimeout(ctx, teachGetpropTimeout)
	out, err := a.teachOps.shellFn(sctx, serial, "ip", "addr")
	cancel()
	if err == nil {
		if ips := teachCandidateIPs(teachIPCandidatesFromAddr(out)); len(ips) > 0 {
			return ips
		}
	}
	sctx, cancel = context.WithTimeout(ctx, teachGetpropTimeout)
	out, err = a.teachOps.shellFn(sctx, serial, "ip", "route")
	cancel()
	if err == nil {
		if ip := firstPhysicalRouteSrcIP(out); ip != "" {
			return []string{ip}
		}
	}
	return nil
}

// probeWirelessCandidates 对候选列表并行 TCP 探测（总耗时≈单次上限）：
// 第一个探测通的候选写入档案（后发顺序=真实返回顺序）；全部不通返回空。
// probeFn 为 nil 表示旧测试路径（不注入网络探测）→ 返回第一个候选。
func (a *App) probeWirelessCandidates(ctx context.Context, serial string, cands []string) string {
	if len(cands) == 0 {
		return ""
	}
	if a.teachOps.probeFn == nil {
		return cands[0]
	}
	type probeResult struct {
		ip string
		ok bool
	}
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan probeResult, len(cands))
	for _, ip := range cands {
		ip := ip
		go func() {
			ch <- probeResult{ip: ip, ok: a.teachOps.probeFn(pctx, ip+":5555")}
		}()
	}
	var first string
	for range cands {
		r := <-ch
		if r.ok {
			bridge.DebugLog("[app] 插线学习 TCP 探测通：%s -> %s", serial, r.ip+":5555")
			if first == "" {
				first = r.ip
			}
			continue
		}
		bridge.DebugLog("[app] 插线学习 TCP 探测不通，不写档案：%s -> %s", serial, r.ip+":5555")
	}
	return first
}

// clearDeletedUsbForSerial 清该 serial 设备的所有删除标记键（序列号/身份/市场名/档案键）。
// 学习完成=重来：added/offline 帧无 marketname 时 clearDeletedUsbForAdded 清不全，
// 残留身份键会在档案重建后被 identityOf 命中（插上再也找不回）。
// gui52-fix17b：优先按设备索引全清（serial → 全键集），不再依赖 lastTrack 帧字段。
func (a *App) clearDeletedUsbForSerial(serial string) {
	if a.deletedUsbKeysIndexed(serial) {
		a.clearDeletedAllForSerial(serial)
		a.mu.RLock()
		var mName, ident string
		for i := range a.lastTrack {
			d := &a.lastTrack[i]
			if d.Serial == serial {
				mName = d.Marketname
				ident = d.Identity
				break
			}
		}
		a.mu.RUnlock()
		if mName != "" {
			a.clearDeletedUsb(mName)
		}
		if ident != "" {
			a.clearDeletedUsb(ident)
		}
		return
	}
	a.mu.RLock()
	var mName, ident string
	for i := range a.lastTrack {
		d := &a.lastTrack[i]
		if d.Serial == serial {
			mName = d.Marketname
			ident = d.Identity
			break
		}
	}
	a.mu.RUnlock()
	a.clearDeletedUsb(serial)
	if mName != "" {
		a.clearDeletedUsb(mName)
	}
	if ident != "" {
		a.clearDeletedUsb(ident)
	}
	if id := a.profiles.ResolveKey(serial); id != "" {
		a.clearDeletedUsb(id)
		// gui52-fix17：顺带清该档案全部无线地址 IP 键（防学习回来后 mDNS
		// 广播按 IP 命中删除集被拦截，设备「又没了」）。
		if e, ok := a.profiles.Entry(id); ok {
			for i := range e.Addrs {
				if ip := ipOfAddr(e.Addrs[i].Addr); ip != "" {
					a.clearDeletedUsb("ip:" + ip)
				}
			}
		}
	}
}

// clearDeletedForProfile 按配对/IP 清该设备全部删除标记（gui52-fix17）：
// 主动配对成功（扫码/手动）= 重来，与 clearDeletedUsbForSerial 语义一致；
// 档案刚由配对流程重建（PairArchive），按 IP→档案键→全键清，残留任何
// 删除集键都会让设备「配对成功但不显示/又被过滤」。
// gui52-fix17b：serial 命中索引时直接按设备全清（确定性清空）。
func (a *App) clearDeletedForProfile(ip, serial, tlsGuid string) {
	if serial != "" && a.deletedUsbKeysIndexed(serial) {
		a.clearDeletedAllForSerial(serial)
	}
	if ip != "" {
		a.clearDeletedUsb("ip:" + ip)
	}
	if serial != "" {
		a.clearDeletedUsb(serial)
	}
	if tlsGuid != "" {
		a.clearDeletedUsb(tlsGuid)
	}
	key := ""
	if ip != "" {
		key = a.profiles.ResolveKeyByIP(ip)
	}
	if key == "" && serial != "" {
		key = a.profiles.ResolveKey(serial)
	}
	if key != "" {
		a.clearDeletedUsb(key)
		if e, ok := a.profiles.Entry(key); ok {
			if e.Marketname != "" {
				a.clearDeletedUsb(e.Marketname)
			}
			if e.Manufacturer != "" && e.Model != "" {
				a.clearDeletedUsb(e.Manufacturer + " " + e.Model)
			}
			if e.DisplayName != "" {
				a.clearDeletedUsb(e.DisplayName)
			}
			for i := range e.Serials {
				if e.Serials[i] != "" {
					a.clearDeletedUsb(e.Serials[i])
				}
			}
			if e.TlsGuid != "" {
				a.clearDeletedUsb(e.TlsGuid)
			}
			for i := range e.Addrs {
				if ip := ipOfAddr(e.Addrs[i].Addr); ip != "" {
					a.clearDeletedUsb("ip:" + ip)
				}
			}
		}
	}
	bridge.DebugLog("[app] 配对成功清删除标记：ip=%s serial=%s key=%s", ip, serial, key)
}

// alignWirelessIP 把读到的无线 IP 直接对齐进档案（gui47-fix F1'）：不再 connect
// 验证——拔线后 justDropped 兜底探测/投屏 bat 自行 connect；写档案成功即打日志。
func (a *App) alignWirelessIP(serial, ip string) {
	id := a.profiles.ResolveKey(serial)
	if id == "" {
		// gui52-fix16d：档案键未就绪无法对齐，但仍要清标记——学习完成=重来。
		a.clearDeletedUsbForSerial(serial)
		bridge.DebugLog("[app] fix16d 学习完成清标记（档案未就绪）：%s", serial)
		return
	}
	if a.profiles.AddrSuccess(id, ip+":5555", ModeTcpip) {
		bridge.DebugLog("[app] 插线学习 IP：%s -> %s:5555（档案已对齐）", serial, ip)
	}
	// gui52-fix16d：学习完成 = 重来——清该设备全部删除标记（全键：serial/身份/
	// 市场名/档案键），杜绝「插上再也找不回」的残留键幽灵过滤。
	a.clearDeletedUsbForSerial(serial)
	bridge.DebugLog("[app] fix16d 学习入档清标记：%s（键=%s）", serial, id)
}

// plugIDForSerial 把 USB serial 解析成遮罩状态机的 identity 键。
func (a *App) plugIDForSerial(serial string) string {
	if id := a.profiles.ResolveKey(serial); id != "" {
		return id
	}
	return serial
}

// plugActiveSerial 判断该 serial 的插线遮罩是否仍在盖住。
func (a *App) plugActiveSerial(serial string) bool {
	id := a.plugIDForSerial(serial)
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	_, ok := a.plugging[id]
	return ok
}

// plugStartBySerial 以 serial 触发遮罩置位（幂等，plugStart 内部记日志）。
func (a *App) plugStartBySerial(serial string, now time.Time, src string) {
	a.plugStart(a.plugIDForSerial(serial), now, src)
}

// plugClearBySerial 以 serial 触发遮罩清除（幂等，plugClear 内部记日志）。
func (a *App) plugClearBySerial(serial, src string) {
	a.plugClear(a.plugIDForSerial(serial), src)
}

// plugCheckTcpipReady 插线学习补查（gui49-fix6）：只读 getprop 记录 tcpip
// 就绪状态，不再清插线遮罩——遮罩清因①=「USB 连续 device 满 2s」。
func (a *App) plugCheckTcpipReady(ctx context.Context, serial string) {
	gctx, cancel := context.WithTimeout(ctx, teachGetpropTimeout)
	port, err := a.teachOps.getpropFn(gctx, serial, "service.adb.tcp.port")
	cancel()
	if err != nil {
		bridge.DebugLog("[app] 插线学习 getprop 补查失败：%s（%v）", serial, err)
		return
	}
	port = strings.TrimSpace(port)
	if port == "5555" {
		bridge.DebugLog("[app] 插线学习 tcpip 就绪：%s（遮罩清因=稳定 device 2s）", serial)
		return
	}
	bridge.DebugLog("[app] 插线学习等待 tcpip：%s 端口=%q", serial, port)
}

// firstRouteSrcIP 从 `ip route` 输出取第一个非回环/链路本地的 src IPv4。
func firstRouteSrcIP(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] != "src" {
				continue
			}
			if isUsableIPv4(fields[i+1]) {
				return fields[i+1]
			}
		}
	}
	return ""
}

// firstInetIPv4 从 `ip addr` 输出取第一个非回环/链路本地的 inet IPv4（去 CIDR 前缀）。
func firstInetIPv4(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "inet" {
			continue
		}
		ip := fields[1]
		if i := strings.IndexByte(ip, '/'); i >= 0 {
			ip = ip[:i]
		}
		if isUsableIPv4(ip) {
			return ip
		}
	}
	return ""
}

// isUsableIPv4 仅接受合法 IPv4，且排除回环与链路本地（这些不是无线连接受用地址）。
func isUsableIPv4(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if v4[0] == 127 || (v4[0] == 169 && v4[1] == 254) {
		return false
	}
	return true
}

// plugStart 置位插线遮罩状态机（identity → 插线开始时刻），并启动 10s 兜底
// 一次性 timer（time.AfterFunc；禁止轮询检查超时）。进程模型（gui48-teachfix5）：
// 已有插线状态时幂等吸收重复信号——不重新计时、不重复日志、不改变首次来源。
// src=触发源（实机诊断日志）：added(usb)/tcpip成功。
func (a *App) plugStart(id string, now time.Time, src ...string) {
	if id == "" {
		return
	}
	source := "未知"
	if len(src) > 0 && src[0] != "" {
		source = src[0]
	}
	a.teachMu.Lock()
	started := false
	if _, ok := a.plugging[id]; !ok {
		a.plugging[id] = now
		started = true
		bridge.DebugLog("[app] 插线遮罩开始：%s（源=%s，%s）", id, source, now.Format("15:04:05.000"))
	}
	if _, ok := a.plugTimers[id]; !ok {
		a.plugTimers[id] = time.AfterFunc(plugShieldTimeout, func() {
			a.guard("plug-timeout", func() { a.plugTimeout(id) })
		})
	}
	a.teachMu.Unlock()
	if started {
		a.refreshDisplayForPlug() // 状态变化立即提交显示层，不等 60s 校准
	}
}

// plugClear 清除插线遮罩状态并停止兜底/稳定 timer。src=触发源：
// 稳定device2s / 兜底connect完成。
func (a *App) plugClear(id string, src ...string) {
	if id == "" {
		return
	}
	source := "未知"
	if len(src) > 0 && src[0] != "" {
		source = src[0]
	}
	a.teachMu.Lock()
	at, ok := a.plugging[id]
	if !ok {
		a.teachMu.Unlock()
		return
	}
	delete(a.plugging, id)
	if t := a.plugTimers[id]; t != nil {
		t.Stop()
		delete(a.plugTimers, id)
	}
	if t := a.plugStableTimers[id]; t != nil {
		t.Stop()
		delete(a.plugStableTimers, id)
	}
	bridge.DebugLog("[app] 插线遮罩结束：%s（源=%s，持续=%s）", id, source, time.Since(at).Round(time.Millisecond))
	a.teachMu.Unlock()
	a.refreshDisplayForPlug() // 清因任一通道 → 前端 ≤1s 看到档案态
}

// plugTimeout 是插线遮罩 10s 兜底回调（gui49-fix6）：超时 → 执行一次 connect
// 确认（目标=档案无线记忆地址）→ 档案以 connect 结果为准 → 遮罩退出。
// 稳定 device 2s 是主清因；此兜底覆盖真拔线/设备死机/折腾期 >10s。
func (a *App) plugTimeout(id string) {
	a.teachMu.Lock()
	at, ok := a.plugging[id]
	if !ok {
		a.teachMu.Unlock()
		return
	}
	if time.Since(at) < plugShieldTimeout {
		a.teachMu.Unlock()
		return
	}
	// 到点：先停 timer 与稳定 timer，遮罩保持到 connect 确认结束后才退。
	if t := a.plugTimers[id]; t != nil {
		t.Stop()
		delete(a.plugTimers, id)
	}
	if t := a.plugStableTimers[id]; t != nil {
		t.Stop()
		delete(a.plugStableTimers, id)
	}
	bridge.DebugLog("[app] 插线遮罩兜底超时：%s（起于 %s）→ connect 确认", id, at.Format("15:04:05.000"))
	a.teachMu.Unlock()
	a.plugTimeoutConnectConfirm(id)
}

// plugExemptIDs 返回插线遮罩活跃 identity 集合（SyncDevices 离线观察豁免）。
func (a *App) plugExemptIDs() map[string]bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	out := make(map[string]bool, len(a.plugging))
	for id := range a.plugging {
		out[id] = true
	}
	return out
}

// plugUSBStable 判断该 identity 当前是否「USB 条目在列且全部 device」。
// USB 条目 removed 或任一非 device（offline/unauthorized）→ 不稳定。
func (a *App) plugUSBStable(devs []adb.Device, id string) bool {
	usbCount, deviceCount := 0, 0
	for i := range devs {
		d := &devs[i]
		if d.ConnType != "usb" || d.Serial == "" {
			continue
		}
		if d.Identity != id && a.identityOf(d) != id {
			continue
		}
		usbCount++
		if d.State == "device" {
			deviceCount++
		}
	}
	return usbCount > 0 && deviceCount == usbCount
}

// plugStabilityUpdate 插线遮罩清因①：USB 连续 device 满 plugStableDuration
// 才清；期间非 device 波动 / removed → 取消稳定 timer（遮罩保持）。
func (a *App) plugStabilityUpdate(devs []adb.Device) {
	a.teachMu.Lock()
	ids := make([]string, 0, len(a.plugging))
	for id := range a.plugging {
		ids = append(ids, id)
	}
	a.teachMu.Unlock()

	for _, id := range ids {
		stable := a.plugUSBStable(devs, id)
		a.teachMu.Lock()
		if !stable {
			if t := a.plugStableTimers[id]; t != nil {
				t.Stop()
				delete(a.plugStableTimers, id)
			}
			a.teachMu.Unlock()
			continue
		}
		if _, ok := a.plugStableTimers[id]; ok {
			a.teachMu.Unlock()
			continue
		}
		a.plugStableTimers[id] = time.AfterFunc(plugStableDuration, func() {
			a.guard("plug-stable", func() {
				a.plugStabilityCheck(id)
			})
		})
		a.teachMu.Unlock()
	}
}

// plugStabilityCheck 稳定 timer 到点复查：仍稳定才清遮罩。
func (a *App) plugStabilityCheck(id string) {
	a.teachMu.Lock()
	_, active := a.plugging[id]
	a.teachMu.Unlock()
	if !active {
		return
	}
	a.mu.RLock()
	base := append([]adb.Device(nil), a.lastTrack...)
	a.mu.RUnlock()
	if !a.plugUSBStable(base, id) {
		a.teachMu.Lock()
		if t := a.plugStableTimers[id]; t != nil {
			t.Stop()
			delete(a.plugStableTimers, id)
		}
		a.teachMu.Unlock()
		return
	}
	a.plugClear(id, "稳定device2s")
}

// plugTimeoutConnectConfirm 是 10s 兜底的 connect 确认（gui49-fix6）：与拔线
// 遮罩 fix2 同口径取档案无线记忆地址；结果直写档案后遮罩退出。
func (a *App) plugTimeoutConnectConfirm(id string) {
	addr := a.unplugTargetAddr(id)
	if addr == "" {
		bridge.DebugLog("[app] 插线遮罩兜底 connect：%s 无档案无线地址，直接退出遮罩", id)
		a.plugClear(id, "兜底connect(无地址)")
		return
	}
	bridge.DebugLog("[app] 插线遮罩兜底 connect 确认：%s -> %s", id, addr)
	cctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	err := a.disc.Connect(cctx, addr)
	cancel()
	if err == nil {
		bridge.DebugLog("[app] 插线遮罩兜底 connect 成功：%s -> %s（active）", id, addr)
		a.profiles.AddrSuccess(id, addr)
		a.plugClear(id, "兜底connect成功")
		return
	}
	bridge.DebugLog("[app] 插线遮罩兜底 connect 失败：%s -> %s（%v）→ stale", id, addr, err)
	a.profiles.AddrFail(id, addr)
	a.profiles.MarkAddrStale(id, addr)
	a.plugClear(id, "兜底connect失败")
}

// ==================== 配对遮罩（gui52-fix14） ====================
// 遮罩卡信息（包级；shieldUsbLearning 原有函数内同名结构保持不动，
// shieldPairing 复用本类型——语义相同：serial/name/wireless 三字段）。
type shieldPairInfo struct {
	serial   string
	name     string
	wireless string
}

// 无线配对成功 → 「连接中…」遮罩（与插线遮罩同构，但无线情形差异）：
//   - 有线插线以 removed 信号为主 → 清因=「USB 连续 device 满 2s」；
//   - 无线 adbd 重启/tcpip 重置以「offline 波动」为主（无单一 removed 信号，
//     日志实锤 [-41935|offline]/[+41935|offline]/[~offline->offline]）→
//     清因=「该设备任一 transport 连续 device 满 2s」（状态判定，波动重置）。
//   - 遮罩开始时先 async connect 一次 ip:5555（fix8 已开 tcpip；失败不阻断）；
//   - 遮罩去除时 pairProbeAllAddrs 并行 connect 档案两条地址刷 active/stale。

func (a *App) pairShieldStart(identity, ip string) {
	if identity == "" {
		return
	}
	a.teachMu.Lock()
	if _, ok := a.pairing[identity]; ok {
		a.teachMu.Unlock()
		return // 已在遮罩中（幂等）
	}
	at := time.Now()
	a.pairing[identity] = at
	a.pairTimers[identity] = time.AfterFunc(pairShieldTimeout, func() {
		a.guard("pair-shield-timeout", func() { a.pairTimeout(identity) })
	})
	a.teachMu.Unlock()
	bridge.DebugLog("[app] 配对遮罩开始：%s（连接中…）", identity)
	// 遮罩期 connect 注入：尽力建立 transport（5555 已由 fix8 开启；失败不阻断，
	// 稳定清因/兜底 probe 兜底）。走 pairOps.connectFn（测试注入 fake；生产=a.disc.ConnectOut）。
	if ip != "" && a.pairOps.connectFn != nil {
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			addr := ip + ":5555"
			if _, err := a.pairOps.connectFn(cctx, addr); err == nil {
				bridge.DebugLog("[app] 配对遮罩 connect 注入成功：%s", addr)
			} else {
				bridge.DebugLog("[app] 配对遮罩 connect 注入失败（不阻断）：%s（%v）", addr, err)
			}
		}()
	}
}

// pairWirelessStable 配对遮罩稳定判定：该 identity 下【任一】transport 当前
// 为 device（状态判定，不依赖 removed/added 事件——无线 adbd 波动期以 offline
// 反复为主，只有 device 才释放遮罩）。
func (a *App) pairWirelessStable(devs []adb.Device, id string) bool {
	for i := range devs {
		d := &devs[i]
		if d.State != "device" {
			continue
		}
		if d.Serial == id || a.identityOf(d) == id {
			return true
		}
	}
	return false
}

// pairStabilityUpdate 每轮 track 检查：稳定→启动 2s timer；波动（offline/
// removed/unauthorized）→ 停 timer（重置——遮罩持续到真的准备好）。
func (a *App) pairStabilityUpdate(devs []adb.Device) {
	a.teachMu.Lock()
	ids := make([]string, 0, len(a.pairing))
	for id := range a.pairing {
		ids = append(ids, id)
	}
	a.teachMu.Unlock()

	for _, id := range ids {
		stable := a.pairWirelessStable(devs, id)
		a.teachMu.Lock()
		if _, active := a.pairing[id]; !active {
			a.teachMu.Unlock()
			continue
		}
		if !stable {
			if t := a.pairStableTimers[id]; t != nil {
				t.Stop()
				delete(a.pairStableTimers, id)
			}
			a.teachMu.Unlock()
			continue
		}
		if _, ok := a.pairStableTimers[id]; ok {
			a.teachMu.Unlock()
			continue
		}
		// 记录本轮稳定快照（复查用——不比 lastTrack 时序，fast path 直接可用）
		a.pairLastStable[id] = append([]adb.Device(nil), devs...)
		a.pairStableTimers[id] = time.AfterFunc(pairShieldStableDuration, func() {
			a.guard("pair-shield-stable", func() { a.pairStabilityCheck(id) })
		})
		a.teachMu.Unlock()
	}
}

// pairStabilityCheck 2s 稳定 timer 到点复查：用启动时的快照判定仍稳定才清遮罩。
func (a *App) pairStabilityCheck(id string) {
	a.teachMu.Lock()
	_, active := a.pairing[id]
	snap := append([]adb.Device(nil), a.pairLastStable[id]...)
	a.teachMu.Unlock()
	if !active {
		return
	}
	if !a.pairWirelessStable(snap, id) {
		a.teachMu.Lock()
		if t := a.pairStableTimers[id]; t != nil {
			t.Stop()
			delete(a.pairStableTimers, id)
		}
		a.teachMu.Unlock()
		return
	}
	a.pairClear(id, "稳定device2s")
}

// pairTimeout 10s 兜底：遮罩直接退出（probe 刷状态显示真实状态）。
func (a *App) pairTimeout(id string) {
	a.teachMu.Lock()
	if _, ok := a.pairing[id]; !ok {
		a.teachMu.Unlock()
		return
	}
	a.teachMu.Unlock()
	a.pairClear(id, "兜底超时10s")
}

// pairClear 清除配对遮罩：停 timer → 异步 probe（并行 connect 档案全部地址
// 刷 active/stale——主人语义：行就 active，不行就 stale）→ 立即刷新显示。
func (a *App) pairClear(id string, src ...string) {
	source := "未知"
	if len(src) > 0 && src[0] != "" {
		source = src[0]
	}
	a.teachMu.Lock()
	at, ok := a.pairing[id]
	if !ok {
		a.teachMu.Unlock()
		return
	}
	delete(a.pairing, id)
	if t := a.pairTimers[id]; t != nil {
		t.Stop()
		delete(a.pairTimers, id)
	}
	if t := a.pairStableTimers[id]; t != nil {
		t.Stop()
		delete(a.pairStableTimers, id)
	}
	delete(a.pairLastStable, id)
	bridge.DebugLog("[app] 配对遮罩结束：%s（源=%s，持续=%s）", id, source, time.Since(at).Round(time.Millisecond))
	a.teachMu.Unlock()
	// 遮罩去除：并行 connect 该档案两条地址刷状态（普通并行，失败仅日志）。
	go a.pairProbeAllAddrs(id)
	a.refreshDisplayForPlug()
}

// pairProbeAllAddrs 并行 connect 档案全部地址（active+stale 都测）：
// 通 → 该地址 active；不通 → 该地址 stale。测试注入用 pairOps.connectFn。
func (a *App) pairProbeAllAddrs(key string) {
	e, ok := a.profiles.Entry(key)
	if !ok {
		return
	}
	type target struct {
		addr string
		mode string
	}
	var targets []target
	for i := range e.Addrs {
		ae := e.Addrs[i]
		if ae.Addr == "" {
			continue
		}
		targets = append(targets, target{addr: ae.Addr, mode: addrEntryClass(ae)})
	}
	if len(targets) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			if a.pairOps.connectFn == nil {
				return
			}
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := a.pairOps.connectFn(cctx, t.addr)
			cancel()
			if err == nil {
				a.profiles.AddrSuccessMode(key, t.addr, t.mode)
				bridge.DebugLog("[app] 配对遮罩 probe：%s/%s 通 → active", key, t.addr)
			} else {
				a.profiles.AddrFailMode(key, t.addr, t.mode)
				bridge.DebugLog("[app] 配对遮罩 probe：%s/%s 不通 → stale（%v）", key, t.addr, err)
			}
		}(t)
	}
	wg.Wait()
	a.reapplyDisplay("配对遮罩probe")
}

// shieldPairing 配对遮罩合成（与 shieldUsbLearning 同构）：遮罩活跃期内，
// 该身份的卡一律合成「无线连接中…」卡（Connecting=true，前端禁点），
// 隐藏 transport 波动期的一切中间态（空电量/离线/IP 名闪等）。
func (a *App) shieldPairing(devs []adb.Device) []adb.Device {
	a.teachMu.Lock()
	now := time.Now()
	pairing := map[string]bool{}
	for id, at := range a.pairing {
		if now.Sub(at) > pairShieldTimeout {
			delete(a.pairing, id)
			if t := a.pairTimers[id]; t != nil {
				t.Stop()
				delete(a.pairTimers, id)
			}
			continue
		}
		pairing[id] = true
	}
	a.teachMu.Unlock()
	if len(pairing) == 0 {
		return devs
	}

	info := map[string]shieldPairInfo{}
	for id := range pairing {
		k := a.profiles.ResolveKey(id)
		if k == "" {
			continue
		}
		if _, ok := info[k]; ok {
			continue
		}
		e, ok := a.profiles.Entry(id)
		if !ok {
			continue
		}
		serial := recentOkAddr(e)
		if serial == "" {
			serial = id
		}
		info[k] = shieldPairInfo{serial: serial, name: profileCardName(e, serial), wireless: serial}
	}

	drop := map[int]bool{}
	for i := range devs {
		key := a.profiles.ResolveKey(devs[i].Serial)
		if key == "" && devs[i].Wireless != "" {
			key = a.profiles.ResolveKey(devs[i].Wireless)
		}
		if key == "" || !pairing[key] {
			continue
		}
		drop[i] = true
	}

	serials := make([]string, 0, len(info))
	for k := range info {
		serials = append(serials, k)
	}
	sort.Strings(serials)
	synth := make([]adb.Device, 0, len(serials))
	for _, key := range serials {
		si, ok := info[key]
		if !ok {
			continue
		}
		if !pairing[key] {
			continue
		}
		synth = append(synth, adb.Device{
			Serial:     si.serial,
			State:      "device",
			ConnType:   "wifi",
			Name:       si.name,
			Identity:   key,
			Wireless:   si.wireless,
			Connecting: true,
			Pairing:    true, // gui52-fix14：配对遮罩标记——前端区分拔线遮罩「断开中…」
		})
	}
	if len(drop) == 0 && len(synth) == 0 {
		return devs
	}
	out := make([]adb.Device, 0, len(devs)+len(synth))
	for i := range devs {
		if drop[i] {
			continue
		}
		out = append(out, devs[i])
	}
	out = append(out, synth...)
	return out
}

// refreshDisplayForPlug 遮罩状态变化后的立即显示层提交（gui48-teachfix5）：
// 用当前权威设备流（lastTrack）重跑 commitDisplay 并推送到 a.devices——拔线后
// 设备流无事件也不等 60s 校准，前端下一轮 700ms 轮询即看到档案态。
func (a *App) refreshDisplayForPlug() {
	a.mu.RLock()
	base := append([]adb.Device(nil), a.lastTrack...)
	a.mu.RUnlock()
	a.commitDisplay(base, "遮罩刷新")
}

// inUsbFirstSeenWindow 判定该 identity 当前是否处于插线进行中（gui47-fix：
// 弹窗避让用）。只查 plugging（事件驱动唯一状态机）。
// 已持 a.mu 的调用方可安全获取 teachMu（无反向锁序）。
func (a *App) inUsbFirstSeenWindow(id string) bool {
	if id == "" {
		return false
	}
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	at, ok := a.plugging[id]
	return ok && time.Since(at) < plugShieldTimeout
}

// runDiscovery 一次完整探测：mDNS 扫描（截断 1.5s）→ 分层并行 connect
// （gui12 TLS 优先：tls 层成功即用，全败才回退 5555 层；窗口 6s）→ 档案状态更新。
// gui23 广播优先 + 档案兜底（gui19"广播权威"修正）：mDNS 广播是设备自报的
// "当前优先地址"——广播地址排在层内最前（优先尝试）；广播 connect 失败后，
// 同设备档案兜底地址（gui27：每类最新一条且不在 60s 失败节流期内）仍在层内
// 其后兜底（横跳场景：旧广播缓存 TTL 未过期时，新 IP 档案兜底成功，不再扑空一轮）。
// 无广播命中的设备（daemon 挂/设备哑巴）→ 档案经验兜底（原行为：tls 优先分层）。
// 成功 → status=found + 档案该地址 active+fail=0+lastFail=0（形态回填）；
// 回退成功（tls 层失败 5555 层成功）→ tls 地址不判失败（未配对/端口过期≠离线）；
// 全失败 → status=notfound（各地址 lastFail=now 节流 60s、fail++ 兼容计数，不删）。
// 成功的 connect 本身已把设备同步进 adb server，下一轮 adb devices 轮询即可见。
func (a *App) runDiscovery(ctx context.Context, cands map[string][]AddrEntry) {
	ctx, cancel := context.WithTimeout(ctx, discoveryWindow+mdnsTimeout+2*time.Second)
	defer cancel()
	// gui22 防抖：探测结束（含 panic 前的 defer 链）复位 in-flight 标记，
	// 下一轮 ForceDiscover/节流探测可再次触发。探测主体逻辑零改动。
	defer func() {
		a.mu.Lock()
		a.discBusy = false
		a.mu.Unlock()
	}()

	a.mu.Lock()
	a.discStatus = DiscoveryStatus{Status: "searching", LastTry: time.Now().Unix()}
	a.mu.Unlock()

	// 1. mDNS 扫描（辅助线索：新 IP 归并进档案；失败/超时忽略）。
	// gui16：daemon 挂死错误此处仅忽略（辅助线索）；自愈由 mdns 流静默看门狗
	// 低频循环统一观测处理。
	var services []discovery.MdnsService
	if a.disc != nil {
		services, _ = a.disc.MdnsScan(ctx, mdnsTimeout)
	}
	matches := make([]MdnsMatch, 0, len(services))
	for _, s := range services {
		matches = append(matches, MdnsMatch{Name: s.Name, Addr: s.Addr, Mode: s.Mode})
	}
	mdnsAddrs := a.profiles.MatchMdnsModes(matches)

	// 2. per-device 分组并行（gui40）：cands 已是 identity 分组，保持分组不要平铺。
	// 每个设备独立：广播+档案组成该设备自己的 tls 层 / 5555 层（组内 tls 优先、
	// 失败回退 5555）；组间并行，一台设备 tls 成功不再截胡其他设备的 5555 层。
	// gui25 归属过滤保留：只有候选设备自己的广播进对应组，在线设备广播不进任何组。
	identities := make([]string, 0, len(cands))
	for id := range cands {
		identities = append(identities, id)
	}
	sort.Strings(identities)

	now := time.Now()
	// 每候选设备的地址集合（候选列表 + 档案地址，用于广播归属/节流/Stale 过滤）
	groupAddrSet := map[string]map[string]bool{}
	groupThrottled := map[string]map[string]bool{}
	for _, id := range identities {
		set := map[string]bool{}
		throttled := map[string]bool{}
		for _, ae := range cands[id] {
			set[ae.Addr] = true
			if addrThrottled(ae.LastFail, now) {
				throttled[ae.Addr] = true
			}
		}
		if e, ok := a.profiles.Entry(id); ok {
			for _, ae := range e.Addrs {
				set[ae.Addr] = true
				if addrThrottled(ae.LastFail, now) {
					throttled[ae.Addr] = true
				}
			}
		}
		groupAddrSet[id] = set
		groupThrottled[id] = throttled
	}

	// 广播归属：mDNS 地址只进入拥有它的候选设备组（profile 同步后的新端口也含在内）
	groupMdns := map[string][]MdnsAddr{}
	for _, m := range mdnsAddrs {
		for _, id := range identities {
			if groupAddrSet[id][m.Addr] {
				groupMdns[id] = append(groupMdns[id], m)
				break
			}
		}
	}

	type groupPlan struct {
		identity string
		tiers    [][]string
		addrs    []string
	}
	plans := make([]*groupPlan, 0, len(identities))
	for _, id := range identities {
		tiers := [][]string{{}, {}}
		seen := map[string]bool{}
		// 广播优先（各层内排前）
		for _, m := range groupMdns[id] {
			if seen[m.Addr] {
				continue
			}
			seen[m.Addr] = true
			if m.Mode == ModeTls {
				tiers[0] = append(tiers[0], m.Addr)
			} else {
				tiers[1] = append(tiers[1], m.Addr)
			}
		}
		// 档案兜底（gui52：cands 已按二态构建——active 在线证据不出现在
		// 离线候选里；stale 条目节流过后就是离线候选，此处只按内存态节流兜底）
		for _, ae := range cands[id] {
			if seen[ae.Addr] || groupThrottled[id][ae.Addr] {
				continue
			}
			seen[ae.Addr] = true
			if ae.Mode == ModeTls {
				tiers[0] = append(tiers[0], ae.Addr)
			} else {
				tiers[1] = append(tiers[1], ae.Addr)
			}
		}
		addrs := make([]string, 0, len(tiers[0])+len(tiers[1]))
		addrs = append(addrs, tiers[0]...)
		addrs = append(addrs, tiers[1]...)
		if len(addrs) == 0 {
			continue
		}
		plans = append(plans, &groupPlan{identity: id, tiers: tiers, addrs: addrs})
	}

	// 汇总 Tried 用；若所有组都无可尝试地址 → notfound（原语义）
	var tried []string
	for _, p := range plans {
		tried = append(tried, p.addrs...)
	}
	if len(tried) == 0 {
		a.mu.Lock()
		a.discStatus = DiscoveryStatus{Status: "notfound", Tried: tried, LastTry: time.Now().Unix()}
		a.mu.Unlock()
		return
	}

	// 3. 组间并行 connect：每组内 ConnectTiers（discovery 包零改动），
	// 组间互不取消——每台设备都有哨兵。
	type groupResult struct {
		plan *groupPlan
		addr string
		ok   bool
	}
	results := make([]groupResult, len(plans))
	var wg sync.WaitGroup
	for i, p := range plans {
		wg.Add(1)
		go func(i int, p *groupPlan) {
			defer wg.Done()
			if a.disc == nil {
				results[i] = groupResult{plan: p}
				return
			}
			gctx, gcancel := context.WithTimeout(ctx, discoveryWindow)
			defer gcancel()
			addr, ok := a.disc.ConnectTiers(gctx, p.tiers, connectTimeout, discoveryWindow)
			results[i] = groupResult{plan: p, addr: addr, ok: ok}
		}(i, p)
	}
	wg.Wait()

	// 4. 结果汇总：每组独立成功/失败。成功组只写成功地址，失败组只记本组失败。
	st := DiscoveryStatus{Tried: tried, LastTry: time.Now().Unix()}
	anyOK := false
	for i := range results {
		r := &results[i]
		if r.ok {
			if !anyOK {
				anyOK = true
				st.Found = r.addr
			}
			mode := ""
			if modeOf := a.profiles.AddrMode(r.addr); modeOf == ModeTls {
				mode = ModeTls
			}
			if mode == "" {
				for _, t := range r.plan.tiers[0] {
					if t == r.addr {
						mode = ModeTls
						break
					}
				}
			}
			if mode == "" && isTlsFormAddr(r.addr) {
				mode = ModeTls
			}
			// gui41 单记忆：成功/失败按 identity 写档案（广播/归档可能已把
			// 同一形态旧地址删除，直接以 addr 为 key 会 no-op）。
			a.profiles.AddrSuccessWithMode(r.plan.identity, r.addr, mode)
			bridge.DebugLog("[app] 无线探测成功：%s（tls=%v）", r.addr, mode == ModeTls)
		} else {
			for _, ad := range r.plan.addrs {
				a.profiles.AddrFail(r.plan.identity, ad)
				// gui48-mdns4fix2：connect 失败 1 次即 stale（只打失败地址，分形态平等）。
				if a.profiles.MarkAddrStale(r.plan.identity, ad) {
					bridge.DebugLog("[app] 无线探测失败：%s → 打 stale（1 次即 stale）", ad)
				}
			}
			bridge.DebugLog("[app] 无线探测未找到：尝试 %d 个地址（tls %d / 其他 %d）",
				len(r.plan.addrs), len(r.plan.tiers[0]), len(r.plan.tiers[1]))
		}
	}
	if anyOK {
		st.Status = "found"
	} else {
		st.Status = "notfound"
	}
	a.mu.Lock()
	a.discStatus = st
	a.mu.Unlock()
}

// decorateTls 设备卡 TLS 形态标注 + 副行 IP 实时化（gui12 C，gui26 实时化）：
// TLS 标只由实时判据驱动——mDNS tls 服务在播（serial/tlsGuid 匹配）或当前在线
// 地址/当前无线连接形态 = TLS（gui14 启发式 port≠5555）→ d.Tls=true。
// gui26：删除档案记忆分支（HasTlsAddr / e.Addrs mode=tls 兜底循环）——档案的
// TLS 地址记录不再点亮"现在有没有无线调试"的状态标（档案职责=投屏选择/离线
// 找回）。无线调试开着 → 广播在播 → 标亮（≤15s）；关闭 → 广播消失 → 标熄
// decorateTls（gui48-mdns4 档案化）：TLS 标/副行 IP/WirelessForm 只读档案
// active 地址状态——active 地址形态==TLS → 标亮；否则不亮。mdnsSnapshot 广播
// 实时判据全部退役（快照只作「把事件写进档案」的输入）。
func (a *App) decorateTls(devs []adb.Device) {
	for i := range devs {
		d := &devs[i]
		key := a.identityOf(d)
		// 前端副行专用显示地址：档案 active 排序取（无 active 为空）。
		d.WirelessIP = a.wirelessStartAddr(key)
		e, ok := a.deviceEntry(d)
		if !ok {
			continue
		}
		a.mdns9DecorateCard(d, key, e)
	}
}

// mdns9DecorateCard 用档案条目现算一张卡的 TLS 标/无线形态/显示地址
// （gui48-mdns9 统一装饰入口）：decorateTls 的档案命中路径与 unifyProfileCards
// 的修复/补齐路径共用同一函数，杜绝「IP 有标无 / 标有 IP 无」双判据割裂。
func (a *App) mdns9DecorateCard(d *adb.Device, key string, e DeviceEntry) {
	// 前端副行专用显示地址：档案 active 排序取（TLS 优先 → 5555）；与 TLS 标
	// 同一档案判据现算。
	d.WirelessIP = a.wirelessStartAddr(key)
	tlsActive := false
	tcpActive := false
	for i := range e.Addrs {
		ae := e.Addrs[i]
		// gui52：TLS 标/无线形态只看 state 二值——active TLS 才亮 TLS；
		// stale 一律不亮（不再看 Stale 冗余布尔/Fail 统计）。
		if ae.State != AddrStateActive {
			continue
		}
		switch addrEntryClass(ae) {
		case ModeTls:
			tlsActive = true
		case ModeTcpip:
			tcpActive = true
		}
	}
	d.Tls = tlsActive
	switch {
	case tlsActive:
		d.WirelessForm = ModeTls
	case tcpActive:
		d.WirelessForm = ModeTcpip
	default:
		d.WirelessForm = ""
	}
	// 副行 IP：已建档在线无线卡按档案 active 排序取（TLS 优先 → 5555）。
	if d.State == "device" && d.ConnType == "wifi" {
		if disp := a.wirelessStartAddr(key); disp != "" && disp != d.Serial {
			d.Serial = disp
		}
	}
	// gui48-mdns10：投屏规格从档案档位现算（与 IP/TLS 同源原则）——
	// 档案 wifi/usb 档是权威语义，覆盖 adb 富化值，杜绝 60s 粘滞过期空白。
	mdns10DecorateSpecs(d, e)
}

// mdns10DecorateSpecs 用档案档位（profiles.wifi / profiles.usb，含自定义）现算
// 卡片规格字段：无线卡 → WirelessRes+FPS（长边按设备宽高比换算）；有线卡 →
// Res+FPS（usb 档）。原生宽高比优先取档案持久化的 e.Res，缺失时用 adb 富化
// d.Res / d.WirelessRes（含宽高比）兜底；全部缺失按 16:9 兜底。Battery 仍保持
// adb 实时富化语义（本次不持久化）。
func mdns10DecorateSpecs(d *adb.Device, e DeviceEntry) {
	// gui49-fix9：Connecting=true（插线遮罩卡/连接中卡）是「未就绪」状态，
	// 不填规格——否则档案规格会被算进遮罩卡，前端渲染成「全规格有线卡」。
	if d.State != "device" || d.Connecting {
		return
	}
	native := e.Res
	if native == "" {
		native = d.Res
	}
	if native == "" {
		native = d.WirelessRes
	}
	wifiRes, wifiFPS := e.Profiles.Wifi.Res, e.Profiles.Wifi.FPS
	if wifiRes <= 0 {
		wifiRes = DefaultProfile().Wifi.Res
	}
	if wifiFPS <= 0 {
		wifiFPS = DefaultProfile().Wifi.FPS
	}
	usbRes, usbFPS := e.Profiles.Usb.Res, e.Profiles.Usb.FPS
	if usbRes <= 0 {
		usbRes = DefaultProfile().Usb.Res
	}
	if usbFPS <= 0 {
		usbFPS = DefaultProfile().Usb.FPS
	}
	switch d.ConnType {
	case "wifi":
		d.WirelessRes = mdns10ProfileRes(native, wifiRes)
		d.FPS = wifiFPS
	case "usb":
		d.Res = mdns10ProfileRes(native, usbRes)
		d.FPS = usbFPS
	}
}

// mdns10ProfileRes 把档案档位长边换算成设备宽高比的分辨率字符串：
// native 是 "WxH"（宽≥高），输出 "档位长边 x 按同比例换算短边"（整数截断，
// 与 adb.ScaleForWireless 同口径）。native 不可用时按 16:9 兜底。
func mdns10ProfileRes(native string, longEdge int) string {
	if longEdge <= 0 {
		return ""
	}
	wStr, hStr, ok := strings.Cut(native, "x")
	if !ok {
		return fmt.Sprintf("%dx%d", longEdge, longEdge*9/16)
	}
	w, err1 := strconv.Atoi(wStr)
	h, err2 := strconv.Atoi(hStr)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return fmt.Sprintf("%dx%d", longEdge, longEdge*9/16)
	}
	if h > w {
		w, h = h, w
	}
	h = h * longEdge / w
	return strconv.Itoa(longEdge) + "x" + strconv.Itoa(h)
}

// buildPending 构建"待配对"设备卡（gui12 A）：mDNS _adb-tls-connect._tcp 服务
// 且档案无此 identity（addr/serial/tlsGuid 均不命中）且设备未在线 → 新设备卡。
// 配对端口取同 GUID 的 _adb-tls-pairing 服务（配对弹窗打开期间才广播；无则空）。
// 必须在 SyncDevices 之后调用（档案同步后的"已知设备"判定才准确）。
func (a *App) buildPending(devs []adb.Device) {
	svcs := a.mdnsSnapshot()
	online := map[string]bool{}
	for i := range devs {
		online[devs[i].Serial] = true
		if devs[i].Wireless != "" {
			online[devs[i].Wireless] = true
		}
	}
	pairPorts := map[string]string{} // 实例名（guid）-> 配对端口
	for _, s := range svcs {
		if s.Mode == discovery.MdnsModePairing && s.Addr != "" {
			if _, ok := pairPorts[s.Name]; !ok {
				port := addrPort(s.Addr)
				pairPorts[s.Name] = port
			}
		}
	}
	var out []PendingDevice
	seen := map[string]bool{}
	for _, s := range svcs {
		if s.Mode != discovery.MdnsModeTls || s.Addr == "" || seen[s.Addr] {
			continue
		}
		if online[s.Addr] {
			continue // 已连接（配对成功）：正常设备卡展示
		}
		serial := TlsServiceIdentity(s.Name)
		// gui52-fix17b：删除集的设备**不在这里过滤**——「删除=不自动复活」由
		// 设备流（filterDeletedUsb/devicesForSync）、mDNS 自动入档（applyMdnsServiceAdded）
		// 与新设备弹窗（tryPopupDeviceLocked）的删除集过滤保证；配对弹窗是用户
		// 主动配对意图的入口，已删除设备必须能在此被重新发现（点配对→成功后
		// clearDeletedForProfile 清标记并入档）。在 buildPending 过滤会让
		// 「删除后再重新配对」找不到设备（2026-09-02 00:12 实测回退）。
		known := false
		if _, ok := a.profiles.Entry(s.Addr); ok {
			known = true
		}
		if !known && serial != "" {
			if _, ok := a.profiles.Entry(serial); ok {
				known = true
			}
		}
		if !known {
			known = a.profiles.TlsGuidKnown(s.Name)
		}
		if known {
			continue
		}
		name := serial
		if name == "" {
			name = "无线调试设备"
		}
		ip := strings.TrimSuffix(s.Addr, ":"+addrPort(s.Addr))
		seen[s.Addr] = true
		out = append(out, PendingDevice{
			Key:      s.Addr,
			Name:     name,
			IP:       ip,
			Addr:     s.Addr,
			PairPort: pairPorts[s.Name],
			Guid:     s.Name,
			Serial:   serial,
		})
	}
	a.mu.Lock()
	a.pending = out
	a.mu.Unlock()
}

// foldGhostWireless 处理无 model 的无线幽灵条目（gui12 BUG-B 双卡修复 + gui14
// mDNS 令牌扩展）：幽灵条目在 adb.List 分组时因 getprop 拿不到 model（offline）
// 而独立成组，而"身份"只有 app 层（档案/mDNS 快照）能判定——这里对已建卡做两层防御：
//
//	① 归并优先：条目 ip 命中已知 mDNS _adb-tls-connect._tcp 服务或档案任一
//	   addrs 的 ip → 归入同身份设备卡（Wireless 副行意图），不单独成卡；
//	② 过滤兜底：档案解析失败（resolve identity 失败）且 mDNS ip 不中 →
//	   从设备列表过滤（不建卡）。
//
// gui14 mDNS 令牌（adb 37）：`adb devices` 会列出服务名条目（完整 FQN，如
// "adb-601c9f08-KWqpio._adb-tls-connect._tcp"，state 可能为 offline 或 device）——
// 令牌不是可投屏目标，无条件折叠：TlsServiceIdentity 解析出 serial 后按档案
// 身份归并（隐去令牌卡；副行只写 ip:port 形态，绝不写令牌 FQN）；
// 身份解析失败 → 直接过滤。判据 isMdnsToken 与 state/ConnType 无关
// （令牌 ConnType 被 adb 层归 "other"，device 态也不放过）。
//
// 不受影响：档案离线设备卡（addr 在档且无同身份卡时保留原样）、mDNS 待配对卡
// （buildPending 独立构建）、USB/在线/未授权条目。效果等同于分组时归并。
func (a *App) foldGhostWireless(devs []adb.Device) []adb.Device {
	if len(devs) == 0 {
		return devs
	}
	svcs := a.mdnsSnapshot()
	tlsIPs := map[string]bool{}               // mDNS tls 服务的 ip 集
	tlsIPKeys := map[string]map[string]bool{} // ip -> 档案 identity 键（服务名 serial 解析）
	for _, s := range svcs {
		if s.Mode != discovery.MdnsModeTls || s.Addr == "" {
			continue
		}
		ip := ipOfAddr(s.Addr)
		if ip == "" {
			continue
		}
		tlsIPs[ip] = true
		if k := a.profiles.ResolveKey(TlsServiceIdentity(s.Name)); k != "" {
			if tlsIPKeys[ip] == nil {
				tlsIPKeys[ip] = map[string]bool{}
			}
			tlsIPKeys[ip][k] = true
		}
	}
	// 档案线索：identity 键 -> 其 addrs 的 ip 集（同 ip 视为同设备）
	// gui52：判据收敛到 state 二值——只收 active 条目（能表明当前归属），
	// stale 条目不参与 IP→设备匹配（不读 Stale 冗余布尔/LastOk 统计）。
	keyIPs := map[string]map[string]bool{}
	for k, e := range a.profiles.Entries() {
		ips := map[string]bool{}
		for i := range e.Addrs {
			a := e.Addrs[i]
			if a.State != AddrStateActive {
				continue
			}
			if ip := ipOfAddr(a.Addr); ip != "" {
				ips[ip] = true
			}
		}
		if len(ips) > 0 {
			keyIPs[k] = ips
		}
	}

	// 两遍处理：先收集不参与折叠的卡（在线/USB/未授权），幽灵卡再对其归并——
	// 幽灵条目在 adb devices 输出中可能排在同设备在线卡之前，单遍顺序处理会漏归并。
	kept := make([]adb.Device, 0, len(devs))
	var ghosts []adb.Device
	for i := range devs {
		d := devs[i]
		// gui14：mDNS 令牌无条件折叠（令牌永远不是可投屏目标）；
		// 其余只折叠 offline 的无线 ip:port 幽灵条目，其它原样保留。
		if isMdnsToken(d.Serial) || (d.State == "offline" && d.ConnType == "wifi" && IsIPPort(d.Serial)) {
			ghosts = append(ghosts, d)
			continue
		}
		kept = append(kept, d)
	}
	for _, d := range ghosts {
		// gui14 令牌路径：FQN 剥后缀 → 实例名剥 serial → 档案身份。
		// ip 不需要（身份命中即可归并/过滤）——令牌不是投屏目标，
		// 找到同身份卡则隐去令牌卡，找不到则直接过滤（不保留成卡）。
		if isMdnsToken(d.Serial) {
			key := a.profiles.ResolveKey(TlsServiceIdentity(d.Serial))
			if key == "" {
				continue // ② 兜底：身份解析失败 → 直接过滤
			}
			for j := range kept {
				c := &kept[j]
				ck := a.profiles.ResolveKey(c.Serial)
				if ck == "" && c.Wireless != "" {
					ck = a.profiles.ResolveKey(c.Wireless)
				}
				if ck != key {
					continue
				}
				// 副行不写令牌 FQN：无 Wireless 且档案有 ip:port 地址时
				// 只补首条 ip:port（不写等于主卡自身的地址）。
				if c.Wireless == "" {
					if e, ok := a.profiles.Entry(key); ok {
						for i := range e.Addrs {
							if IsIPPort(e.Addrs[i].Addr) && e.Addrs[i].Addr != c.Serial {
								c.Wireless = e.Addrs[i].Addr
								break
							}
						}
					}
				}
				break
			}
			continue
		}
		ip := ipOfAddr(d.Serial)
		// 幽灵自身直解身份（gui41 Fix B）：只有 ResolveKey(d.Serial) 直接命中才算
		// ——历史 IP 集合（keyIPs）与 mDNS 广播 IP（tlsIPKeys）都是“IP 复用会被
		// 多设备共享”的弱证据，只能用于下方“无身份不建卡”过滤，绝不能用于
		// 跨设备归并（实测：K80 关机 197:5555 幽灵被并进平板 Wireless 副行 →
		// K80 离线卡消失 + OfflineCandidateAddrs 误判在线 → 永不找回）。
		ownKeys := map[string]bool{}
		if k := a.profiles.ResolveKey(d.Serial); k != "" {
			ownKeys[k] = true
		}
		keys := map[string]bool{}
		if k := a.profiles.ResolveKey(d.Serial); k != "" {
			keys[k] = true
		}
		for k, ips := range keyIPs {
			if ips[ip] {
				keys[k] = true
			}
		}
		for k := range tlsIPKeys[ip] {
			keys[k] = true
		}
		if len(keys) == 0 && !tlsIPs[ip] {
			continue // ② 过滤兜底：无任何身份的幽灵条目不建卡
		}
		// ① 归并优先：同身份设备卡已存在 → 作为其 Wireless 副行并入
		// gui41：归并只信 ownKeys（幽灵自身直解身份），不再用含历史 IP 的 keys。
		merged := false
		for j := range kept {
			c := &kept[j]
			ck := a.profiles.ResolveKey(c.Serial)
			if ck == "" && c.Wireless != "" {
				ck = a.profiles.ResolveKey(c.Wireless)
			}
			if ck == "" || !ownKeys[ck] {
				continue
			}
			if c.Wireless == "" && c.Serial != d.Serial {
				c.Wireless = d.Serial
			}
			merged = true
			break
		}
		if !merged {
			// 无同身份卡：档案离线设备卡（addr 在档）保持原样
			kept = append(kept, d)
		}
	}
	return kept
}

// foldGhostUsb 处理 model 未就绪的 USB 幽灵条目（gui31「插线不闪现无线」）：
// gui30「插线即学习」的 adb tcpip 会重启设备端 adbd——USB transport 断开 1-2s
// 后恢复的头几轮里，`adb devices -l` 的 model 字段未就绪，adb 层 GroupDevices
// 无法按 model 把 USB 与无线条目归并 → USB/无线分两张卡，而无线探测
// （5555 已可连）比 USB model 确认更快 → 无线卡先亮 1-2s（主人观察：
// 从离线到无线再很快到有线）。这里用档案 identity 归并：USB serial 命中档案
// （如 K80 601c9f08）且同身份无线在线卡在场 → USB 作主 transport 立即归并成
// 单卡（Serial/State/ConnType 取 USB，无线地址并入 Wireless 副行；名称/型号/
// 电量/规格等富化字段从无线卡补缺——无线 transport 已富化，不等 USB getprop）。
// 无同身份无线在线卡时 USB 幽灵卡原样保留（本身就是 USB 卡，不显示无线）；
// 名称停在裸 serial（getprop 未就绪）时按档案市场名回补。
//
// 不受影响：纯 WiFi 设备（无 USB 卡 → 不触发，无线卡原样）；同身份无线卡
// offline 的组合（离线无线幽灵已由 foldGhostWireless 归并入 USB 卡 Wireless，
// 本函数不再动它）；不同档案身份的两张卡（绝不跨设备归并——无身份不归并）。
func (a *App) foldGhostUsb(devs []adb.Device) []adb.Device {
	drop := map[int]bool{}
	for i := range devs {
		u := &devs[i]
		// USB transport 出现即归并（gui33「插线零闪现」）：offline=握手未完成
		// 也算 USB 在场（0.5-1s 窗口内无线卡仍在闪，第一轮即归并成 USB 卡）；
		// unauthorized 不归并（保持未授权卡语义）。
		if (u.State != "device" && u.State != "offline") || u.ConnType != "usb" {
			continue
		}
		key := a.profiles.ResolveKey(u.Serial)
		if key != "" {
			for j := range devs {
				if j == i || drop[j] {
					continue
				}
				w := &devs[j]
				if w.State != "device" || w.ConnType != "wifi" {
					continue
				}
				wk := a.profiles.ResolveKey(w.Serial)
				if wk == "" && w.Wireless != "" {
					wk = a.profiles.ResolveKey(w.Wireless)
				}
				if wk != key {
					continue
				}
				mergeUsbPrimary(u, *w) // USB 优先覆盖（与 buildDevice 同口径）
				drop[j] = true
				break
			}
		}
		if u.Name == "" || u.Name == u.Serial {
			// 名称回补：USB getprop 未就绪（adbd 重启窗口）→ 档案统一回退链。
			if e, ok := a.profiles.Entry(u.Serial); ok {
				u.Name = profileCardName(e, u.Serial)
			}
		}
	}
	if len(drop) == 0 {
		return devs
	}
	out := make([]adb.Device, 0, len(devs))
	for i := range devs {
		if drop[i] {
			continue
		}
		out = append(out, devs[i])
	}
	return out
}

// mergeUsbPrimary 把无线卡 b 归并入 USB 幽灵卡 a（a 原地覆盖，USB 优先，
// 与 adb 层 buildDevice 同口径）：Serial/ConnType 取 USB 侧 a；State 取优
// （USB offline 但无线 device → device，见函数内注释）；无线卡自身地址
// （IP:port）并入 Wireless 副行；富化字段 USB 侧空时从无线卡
// 补缺（名称/型号/市场名/厂商/identity/电量/分辨率/帧率/无线分辨率/TLS 标注）——
// adbd 重启窗口内 USB getprop 未就绪而无线 transport 已富化（5555 已连上），
// 归并后当轮即显示完整 USB 卡，不等下一轮 getprop。两侧都无名称时 Name 置空，
// 由 foldGhostUsb 按档案市场名回补（applyProfileNames 只覆盖离线卡）。
func mergeUsbPrimary(a *adb.Device, b adb.Device) {
	usbSerial := a.Serial
	// 状态取优（gui33）：USB 侧非 device（offline/握手期）但无线侧 device →
	// 卡状态用 device（设备确实在线，无线 transport 已连）——插线后第一轮即
	// 显示在线 USB 卡，不闪「离线」。Serial/ConnType 仍取 USB 侧（USB 优先形态）。
	st := a.State
	if st != "device" && b.State == "device" {
		st = "device"
	}
	wifiAddr := b.Serial
	name := a.Name
	if name == "" || name == usbSerial {
		if b.Name != "" && b.Name != b.Serial {
			name = b.Name
		} else {
			name = ""
		}
	}
	model := a.Model
	if model == "" {
		model = b.Model
	}
	market := a.Marketname
	if market == "" {
		market = b.Marketname
	}
	man := a.Manufacturer
	if man == "" {
		man = b.Manufacturer
	}
	identity := a.Identity
	if identity == "" || identity == usbSerial {
		identity = b.Identity
	}
	battery := a.Battery
	if battery == 0 {
		battery = b.Battery
	}
	res := a.Res
	if res == "" {
		res = b.Res
	}
	fps := a.FPS
	if fps == 0 {
		fps = b.FPS
	}
	wres := a.WirelessRes
	if wres == "" {
		wres = b.WirelessRes
	}
	tls := a.Tls || b.Tls
	wform := a.WirelessForm
	if wform == "" {
		wform = b.WirelessForm
	}
	*a = adb.Device{
		Serial:       usbSerial,
		State:        st,
		ConnType:     "usb",
		Name:         name,
		Model:        model,
		Marketname:   market,
		Manufacturer: man,
		Identity:     identity,
		Wireless:     wifiAddr,
		WirelessRes:  wres,
		Battery:      battery,
		Res:          res,
		FPS:          fps,
		Tls:          tls,
		WirelessForm: wform,
	}
}

// appendProfileOfflineCards 档案设备离线卡补齐（gui29）：foldGhostWireless 只
// 保留 adb devices 里现存的 offline 幽灵条目（保留机制），当设备从 adb devices
// 完全干净消失（拔 USB 且无无线连接/无 offline 残留）时 devs 中没有任何该设备
// 的卡——设备"凭空消失"。这里按档案逐台补齐：devs 已有该档案设备的卡（在线卡
// /离线残留卡/未授权卡，Serial 或 Wireless 解析到档案 identity 即视为有卡）
// → 跳过；无卡 → 追加一张离线卡（市场名 + 副行最近无线地址，无投屏按钮，
// 与现有离线卡交互一致）。
//
// 语义边界（与既有机制不冲突）：
//   - 调用位置必须在 pollOnce 设备列表构建末端（buildPending 之后）：新设备
//     弹窗判据（jude 判据 v2："档案中不存在"）在 append 之前已判定，档案设备
//     不弹；待配对卡（buildPending）只收 mDNS 发现的非档案设备，互不影响；
//   - maybeDiscover/SyncDevices 在 append 之前执行：离线卡不参与探测候选计算、
//     不触发无线地址失败记账（避免每轮自刷失败节流）；
//   - 下一轮 pollOnce 从 adb 列表重新构建，离线卡不累积（每档案至多一张）。
func (a *App) appendProfileOfflineCards(devs []adb.Device) []adb.Device {
	// gui46：无线 offline 残留条目（旧端口/已失效地址在 adb devices -l 的瞬时残留）
	// 不独立成卡、不参与 have 判定——同身份有 device 卡 → 并入其 Wireless 副行；
	// 无同身份 device 卡 → 直接移除（其显示职责交给下方补卡/合成在线卡）。
	// 只处理无线（IsIPPort），USB offline/unauthorized 原样保留（未授权卡语义不动）。
	cleaned := make([]adb.Device, 0, len(devs))
	for i := range devs {
		d := devs[i]
		if d.State != "device" && d.ConnType == "wifi" && IsIPPort(d.Serial) {
			continue // 先收集非残留卡，残留稍后统一处理
		}
		cleaned = append(cleaned, d)
	}
	for i := range devs {
		d := devs[i]
		if d.State == "device" || d.ConnType != "wifi" || !IsIPPort(d.Serial) {
			continue
		}
		merged := false
		for j := range cleaned {
			c := &cleaned[j]
			if c.State == "device" && c.ConnType == "wifi" {
				k1 := a.profiles.ResolveKey(c.Serial)
				k2 := a.profiles.ResolveKey(d.Serial)
				if k1 != "" && k1 == k2 {
					if c.Wireless == "" {
						c.Wireless = d.Serial
					}
					merged = true
					break
				}
			}
		}
		if !merged {
			continue // 移除：补卡/合成在线卡接管
		}
	}
	devs = cleaned

	entries := a.profiles.Entries()
	if len(entries) == 0 {
		return devs
	}
	// 已有卡身份集：Identity 直解优先（gui48-mdns8 双卡修复：设备流卡 Serial
	// 被档案单形态单条规则淘汰后 ResolveKey 失败，但卡自身 Identity 仍指向
	// 档案 identity）；Serial/Wireless 解析保留为原有口径。
	have := map[string]bool{}
	for i := range devs {
		if devs[i].Identity != "" {
			have[devs[i].Identity] = true
		}
		if k := a.profiles.ResolveKey(devs[i].Serial); k != "" {
			have[k] = true
		}
		if devs[i].Wireless != "" {
			if k := a.profiles.ResolveKey(devs[i].Wireless); k != "" {
				have[k] = true
			}
		}
	}
	// 键排序：map 迭代无序，卡片追加顺序必须确定（前端顺序敏感）
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if have[k] {
			continue
		}
		e := entries[k]
		addr := recentOkAddr(e)
		// gui52：在线证据只认 state=active。
		hasActive := profileHasActiveAddr(e)
		if !hasActive {
			// 档案全 stale / 无无线成功记录：补离线卡（原 gui29 语义）。
			serial := ""
			if len(e.Serials) > 0 {
				serial = e.Serials[0]
			} else {
				serial = k
			}
			name := profileCardName(e, serial)
			devs = append(devs, adb.Device{
				Serial:   serial,
				State:    "offline",
				ConnType: "usb",
				Name:     name,
				Identity: k,
			})
			continue
		}
		// gui41c F3：档案有 active（非 Stale 且最近 ping 通）= 设备还活着的证据
		// → 合成在线卡（State=device 前端在线样式；TLS 优先由 recentOkAddr 保证）。
		name := profileCardName(e, addr)
		devs = append(devs, adb.Device{
			Serial:   addr,
			State:    "device",
			ConnType: "wifi",
			Name:     name,
			Identity: k,
		})
	}
	return devs
}

// --- gui48-mdns9：显示层统一（一设备一卡，双源合一） ---
//
// unifyProfileCards 是 mdns9 的唯一归并入口：以 profile 档案为唯一清单，
// 对每个档案 identity 至多产出一张卡；卡片状态取「有线 > 无线 > 离线」，
// 展示字段（IP/TLS 标/无线形态）全部由档案条目现算（mdns9DecorateCard）。
// 旧的分来源函数（foldGhostWireless/foldGhostUsb/appendProfileOfflineCards/
// decorateTls）仍保留：前三者继续承担权威设备流（lastTrack）的 transport 级
// 折叠与档案同步前清洗，decorateTls 继续为直接单测提供装饰入口；commitDisplay
// 中显示层归并统一由本函数承接，appendProfileOfflineCards 不再参与显示提交。

type mdns9CardSlot struct {
	key   string
	cards []adb.Device
}

// mdns9ProfileKey 解析设备卡归属的档案 identity：Serial → Wireless → Identity
// 三条线全查档案（ResolveKey 对 Identity 直查即为「设备卡自身 Identity 直解」），
// 修复设备流卡 Serial 被档案单形态规则淘汰后解析失败导致的 have miss。
// gui52fix2：精确链全部失败后，Serial 为 IP:port 时按 IP 回退到已有档案——
// adb 自动连接的瞬时旧端口 transport 与主卡归并展示，不独立成卡。
func (a *App) mdns9ProfileKey(d *adb.Device) string {
	if d == nil {
		return ""
	}
	if k := a.profiles.ResolveKey(d.Serial); k != "" {
		return k
	}
	if d.Wireless != "" {
		if k := a.profiles.ResolveKey(d.Wireless); k != "" {
			return k
		}
	}
	if d.Identity != "" {
		if k := a.profiles.ResolveKey(d.Identity); k != "" {
			return k
		}
	}
	if IsIPPort(d.Serial) {
		return a.profiles.ResolveKeyByIP(ipOfAddr(d.Serial))
	}
	return ""
}

// unifyProfileCards 把设备流卡按档案 identity 归并成唯一卡，再为档案中缺失的
// identity 补唯一卡（无线状态/离线状态）。输出顺序：设备流原始顺序（归并卡取
// 首次出现位置），其后按档案键升序追加补齐卡（前端顺序敏感，排序确定）。
func (a *App) unifyProfileCards(devs []adb.Device) []adb.Device {
	// gui52-fix16：删除后设备流卡不显示（deletedUsb 标记）——先过滤再归并
	// （与 devicesForSync 防复活同口径；拔线再插 added 清标记后恢复）。
	devs = a.filterDeletedUsb(devs)
	// gui52fix5：无线（wifi）unauthorized transport 一律视同 offline——撤权后
	// adb server 残留的 unauthorized 无线条目不得显示为在线；探测层已如实打
	// stale，显示层对齐。USB unauthorized（手机在场等授权）保持 gui49 语义不动。
	for i := range devs {
		if devs[i].State == "unauthorized" && devs[i].ConnType == "wifi" {
			devs[i].State = "offline"
		}
	}
	// gui52fix3：显示提交前事件驱动合卡（防瞬态漏网；该路径由 track/mdns
	// 事件触发，不是周期轮询）。无孤儿时不写盘。
	a.profiles.CleanOrphanIPPort()
	entries := a.profiles.Entries()
	type seg struct {
		card   adb.Device
		slot   *mdns9CardSlot
		isCard bool
	}
	segs := make([]seg, 0, len(devs))
	slots := map[string]*mdns9CardSlot{}

	for i := range devs {
		d := devs[i]
		key := a.mdns9ProfileKey(&d)
		if key == "" {
			// 无线 offline 残留无档案归属 → 不独立成卡（gui46 同口径），
			// 显示职责由设备流在线卡/档案补齐卡承担。
			if d.State != "device" && d.ConnType == "wifi" && IsIPPort(d.Serial) {
				continue
			}
			segs = append(segs, seg{card: d, isCard: true})
			continue
		}

		// 无线 offline 残留条目不独立成卡（gui46 同口径）：同 identity 有
		// device 卡时并入副行（slot 归并完成），无同 identity 卡时由下方
		// 档案补齐卡接管显示。
		if d.State != "device" && d.ConnType == "wifi" && IsIPPort(d.Serial) {
			sl := slots[key]
			if sl == nil {
				continue // 无主卡可并入：丢弃，显示职责交给档案补齐
			}
			sl.cards = append(sl.cards, d)
			continue
		}
		sl := slots[key]
		if sl == nil {
			sl = &mdns9CardSlot{key: key}
			slots[key] = sl
			segs = append(segs, seg{slot: sl})
		}
		sl.cards = append(sl.cards, d)
	}

	out := make([]adb.Device, 0, len(devs)+len(entries))
	have := map[string]bool{}
	for _, s := range segs {
		if s.isCard {
			out = append(out, s.card)
			if k := a.mdns9ProfileKey(&s.card); k != "" {
				have[k] = true
			}
			continue
		}
		e, ok := entries[s.slot.key]
		if !ok { // 防御：档案在 Entries 快照后被并发改写
			out = append(out, s.slot.cards...)
			continue
		}
		out = append(out, a.mdns9UnifySlot(s.slot, e))
		have[s.slot.key] = true
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if have[k] {
			continue
		}
		out = append(out, a.mdns9ProfileFallbackCard(k, entries[k]))
	}
	return out
}

// mdns9UnifySlot 把一个档案 identity 的多来源卡片归并成唯一一张：
// 有线 > 无线 > 离线的 transport 优先级（gui49-fix4 形态锁定）——USB 条目
// 只要仍在设备流（device/offline/unauthorized 任一状态）就钉有线主卡，不因
// USB 状态波动降级为无线卡；无线地址并入副行，富化字段按需吸收。
func (a *App) mdns9UnifySlot(sl *mdns9CardSlot, e DeviceEntry) adb.Device {
	usbRank := func(st string) int {
		switch st {
		case "device":
			return 2
		case "offline":
			return 1
		default:
			return 0 // unauthorized/other：仍是 USB 在列硬事实
		}
	}
	var usbPrimary, wifiDev, other *adb.Device
	for i := range sl.cards {
		c := &sl.cards[i]
		switch {
		case c.ConnType == "usb":
			if usbPrimary == nil || usbRank(c.State) > usbRank(usbPrimary.State) {
				usbPrimary = c
			}
		case c.ConnType == "wifi" && c.State == "device":
			if wifiDev == nil {
				wifiDev = c
			}
		default:
			if other == nil {
				other = c
			}
		}
	}

	var out adb.Device
	switch {
	case usbPrimary != nil:
		out = *usbPrimary
		if wifiDev != nil {
			// 无线地址永远并入副行；USB 状态如实呈现（离线/未授权不吞状态）。
			if out.Wireless == "" && wifiDev.Serial != out.Serial {
				out.Wireless = wifiDev.Serial
			}
			if out.State != "unauthorized" {
				merged := out
				mergeUsbPrimary(&merged, *wifiDev)
				merged.State = out.State // 形态锁定：波动只改状态显示，不换主卡
				out = merged
			}
		}
	case wifiDev != nil:
		out = *wifiDev
	case other != nil:
		out = *other
	default:
		out = sl.cards[0]
	}
	// gui49-fix5 显示层：USB 条目在列（线插着）且 State=offline 的瞬态 →
	// 标记 Connecting（与插线遮罩视觉一致；前端 connecting 分支显示「连接中…」，
	// 不显示「离线」）。State 保持 offline：shieldUsbLearning 据此区分
	// 「真实 USB device」与「offline 瞬态」，插线遮罩语义不受影响。
	if out.ConnType == "usb" && out.State == "offline" {
		out.Connecting = true
	}
	a.mdns9DecorateCard(&out, sl.key, e)
	return out
}

// mdns9ProfileFallbackCard 为设备流中完全缺失的档案 identity 补唯一卡：
// 档案有 active（非 Stale）地址 → 无线状态卡（State=device/ConnType=wifi，
// 地址与 TLS 标/IP 装饰同一判据现算）；全 stale → 离线卡（无 IP、无投屏按钮，
// 与既有离线卡交互一致）。
func (a *App) mdns9ProfileFallbackCard(key string, e DeviceEntry) adb.Device {
	serial := ""
	if len(e.Serials) > 0 {
		serial = e.Serials[0]
	} else {
		serial = key
	}
	name := profileCardName(e, serial)

	disp := a.wirelessStartAddr(key)
	if disp == "" {
		disp = recentOkAddr(e)
	}
	if !profileHasActiveAddr(e) || disp == "" {
		return adb.Device{
			Serial:   serial,
			State:    "offline",
			ConnType: "usb",
			Name:     name,
			Identity: key,
		}
	}
	d := adb.Device{
		Serial:   disp,
		State:    "device",
		ConnType: "wifi",
		Name:     name,
		Identity: key,
	}
	a.mdns9DecorateCard(&d, key, e)
	if d.WirelessIP == "" {
		d.WirelessIP = d.Serial // 同判据兜底：有卡必有 IP（与 TLS 标同源）
	}
	return d
}

// shieldUsbLearning「USB 遮罩」（gui48-p1 事件驱动）：
// 插线进行中（plugging）的身份，无论 devs 呈现 USB 消失（adbd 重启窗口）、
// 离线卡、在线无线卡，一律合成/维持「USB 连接中」卡（State=device、ConnType=usb、
// Serial=档案 USB serial、Name=市场名、Connecting=true——前端按钮「连接中…」禁点）；
// 在线无线卡并入副行，离线卡让位。只有「USB 完全准备好（State=device）」或
// 「拔线事实（在线无线卡且无 USB 条目）」或 15s 兜底超时（plugTimers）才能结束遮罩。
//
// 并发：plugging/plugTimers 由 teachMu 保护；合成顺序按 key 排序
// （map 迭代无序，卡片顺序必须确定——前端顺序敏感）。
func (a *App) shieldUsbLearning(devs []adb.Device) []adb.Device {
	a.teachMu.Lock()
	now := time.Now()
	plugging := map[string]bool{}
	for id, at := range a.plugging {
		if now.Sub(at) > plugShieldTimeout {
			delete(a.plugging, id) // 兜底超时（timer 之外的防御性清理）
			if t := a.plugTimers[id]; t != nil {
				t.Stop()
				delete(a.plugTimers, id)
			}
			continue
		}
		plugging[id] = true
	}
	a.teachMu.Unlock()
	if len(plugging) == 0 {
		return devs
	}

	// 档案身份 → 遮罩卡片信息（Serial=档案 USB serial 首条、Name=市场名、
	// Wireless=最近成功无线地址副行指示）。
	type shieldInfo struct {
		serial   string
		name     string
		wireless string
	}
	info := map[string]shieldInfo{}
	for id := range plugging {
		k := a.profiles.ResolveKey(id)
		if k == "" {
			continue
		}
		if _, ok := info[k]; ok {
			continue
		}
		e, ok := a.profiles.Entry(id)
		if !ok {
			continue
		}
		usbSerial := id
		if len(e.Serials) > 0 && e.Serials[0] != "" {
			usbSerial = e.Serials[0]
		}
		name := profileCardName(e, usbSerial)
		info[k] = shieldInfo{serial: usbSerial, name: name, wireless: recentOkAddr(e)}
	}

	pluggingKey := map[string]bool{}
	for id := range plugging {
		if k := a.profiles.ResolveKey(id); k != "" {
			pluggingKey[k] = true
		}
	}

	// 身份分类：liveWifi（在线无线地址并入遮罩卡副行）。
	liveWifi := map[string]string{}
	for i := range devs {
		key := a.profiles.ResolveKey(devs[i].Serial)
		if key == "" && devs[i].Wireless != "" {
			key = a.profiles.ResolveKey(devs[i].Wireless)
		}
		if key == "" {
			continue
		}
		if _, ok := info[key]; !ok {
			continue // 该身份未在窗口内 → 不动（回归零影响）
		}
		if devs[i].ConnType != "usb" && devs[i].State == "device" && devs[i].Serial != "" {
			liveWifi[key] = devs[i].Serial // 在线无线卡地址优先作副行指示
		}
	}

	// 遮罩（drop）：plugging 活跃期内无条件盖遮罩卡（gui49-fix10 删除
	// realUsbDevice 豁免——插线后的第一个 device 是瞬态，0.5-1s 后 adbd
	// 重启必掉 device，真卡露出只允许经「USB 连续 device 满 2s」清因判定）。
	drop := map[int]bool{}
	for i := range devs {
		key := a.profiles.ResolveKey(devs[i].Serial)
		if key == "" && devs[i].Wireless != "" {
			key = a.profiles.ResolveKey(devs[i].Wireless)
		}
		if key == "" || info[key].serial == "" {
			continue
		}
		if !pluggingKey[key] {
			continue
		}
		drop[i] = true
	}

	// 合成「USB 连接中」卡（每身份至多一张；key 排序保序）。
	serials := make([]string, 0, len(info))
	for k := range info {
		serials = append(serials, k)
	}
	sort.Strings(serials)
	synth := make([]adb.Device, 0, len(serials))
	appended := map[string]bool{}
	for _, key := range serials {
		si, ok := info[key]
		if !ok || appended[key] {
			continue
		}
		if !pluggingKey[key] {
			continue
		}
		appended[key] = true
		if w := liveWifi[key]; w != "" {
			si.wireless = w
		}
		synth = append(synth, adb.Device{
			Serial:     si.serial,
			State:      "device",
			ConnType:   "usb",
			Name:       si.name,
			Identity:   key,
			Wireless:   si.wireless,
			Connecting: true,
		})
	}
	if len(drop) == 0 && len(synth) == 0 {
		return devs
	}
	out := make([]adb.Device, 0, len(devs)+len(synth))
	for i := range devs {
		if drop[i] {
			continue
		}
		out = append(out, devs[i])
	}
	out = append(out, synth...)
	return out
}

// recentOkAddr 返回档案中当前 active 的无线地址（gui52：判据收敛到 state 二值）：
// TLS 类 active 优先 → tcpip 类 active 兜底；同形态取档案顺序中第一条 active
// （addrs 由写入路径排序：active 在前）。全部 stale → ""（离线卡回退 USB serial，
// 原逻辑）。不读 Stale 布尔/LastOk/Fail——是否可用只看 state。
func recentOkAddr(e DeviceEntry) string {
	for _, class := range []string{ModeTls, ModeTcpip} {
		for i := range e.Addrs {
			if e.Addrs[i].State == AddrStateActive && addrEntryClass(e.Addrs[i]) == class {
				return e.Addrs[i].Addr
			}
		}
	}
	return ""
}

// isMdnsToken 判定 serial 是否为 adb 37 在 `adb devices` 里列出的 mDNS 服务
// 令牌（实例名._服务类型._tcp 完整 FQN，如
// "adb-601c9f08-KWqpio._adb-tls-connect._tcp"）。判据宽松：
// adb- 前缀 + "._adb" 后缀段即视为令牌（IsIPPort 优先于本判定——
// ip:port 形式不会误中；裸实例名 "adb-xxx" 无 "._adb" 段也不中）。
func isMdnsToken(serial string) bool {
	return strings.HasPrefix(serial, "adb-") && strings.Contains(serial, "._adb")
}

// ipOfAddr 提取 "ip:port" 的 ip 部分（非 ip:port 返回整体）。
func ipOfAddr(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i > 0 {
		return addr[:i]
	}
	return addr
}

// addrPort 提取 "ip:port" 的端口（非 IP:port 返回整体）。
func addrPort(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// findPending 按卡片键查待配对设备。
func (a *App) findPending(key string) (PendingDevice, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.pending {
		if p.Key == key {
			return p, true
		}
	}
	return PendingDevice{}, false
}

// findTargetDevice 在设备列表中定位目标卡：serial 可能已过期（设备在 USB/无线
// transport 切换时卡片 Serial 会重键，而会话 cast.Serial 是启动时的旧值）——
// 同时匹配 Serial 与 Wireless 字段，修复"保存重投查不到目标 → 漏注 SCEZ_SERIAL"。
func findTargetDevice(serial string, devices []adb.Device) *adb.Device {
	for i := range devices {
		if devices[i].Serial == serial || devices[i].Wireless == serial {
			return &devices[i]
		}
	}
	return nil
}

// findTargetByIdentity 按档案 identity（serials 集合 + addrs 集合）定位目标卡
// （旧 serial 与当前卡片都不匹配时的第二层兜底）。
func findTargetByIdentity(e DeviceEntry, devices []adb.Device) *adb.Device {
	for i := range devices {
		d := &devices[i]
		if contains(e.Serials, d.Serial) || contains(e.Serials, d.Wireless) ||
			addrInList(e.Addrs, d.Serial) || addrInList(e.Addrs, d.Wireless) {
			return d
		}
	}
	return nil
}

func addrInList(addrs []AddrEntry, v string) bool {
	for i := range addrs {
		if addrs[i].Addr == v {
			return true
		}
	}
	return false
}

// deviceIdentity 取卡片 identity（优先 adb 轮询富化值，缺失按 IdentityKey 规则回退）。
func deviceIdentity(d *adb.Device) string {
	if d.Identity != "" {
		return d.Identity
	}
	return adb.IdentityKey(d.Marketname, d.Manufacturer, d.Model, d.Serial)
}

// deviceEntry 解析设备卡到档案条目（Serial/Wireless 双重精确匹配 + IP 级回退）。
// 档案是 identity 唯一化后的稳定来源：多会话 adb 竞态下 marketname 可能本轮
// 读不到，档案里存有市场名时仍能拿到"本尊"身份（防弹窗误判/档案分裂）。
// gui52fix2：adb server 自动连接产生的 IP:port transport 端口是瞬时旧端口
// （不在档案 addrs 里）——精确失败后按 IP 回退到已有档案（active 优先），
// 任意端口不再判新设备。精确匹配严格优先于 IP 回退。
func (a *App) deviceEntry(d *adb.Device) (DeviceEntry, bool) {
	if d == nil {
		return DeviceEntry{}, false
	}
	if e, ok := a.profiles.Entry(d.Serial); ok {
		return e, true
	}
	if d.Wireless != "" {
		if e, ok := a.profiles.Entry(d.Wireless); ok {
			return e, true
		}
	}
	if IsIPPort(d.Serial) {
		if k := a.profiles.ResolveKeyByIP(ipOfAddr(d.Serial)); k != "" {
			if e, ok := a.profiles.Entry(k); ok {
				return e, true
			}
		}
	}
	return DeviceEntry{}, false
}

// identityOf 取设备 identity：档案 marketname 优先（稳定）→ adb 富化回退链。
// 弹窗"新设备"判定与会话绑定的判据统一走这里（同设备任一 transport 同 identity）。
func (a *App) identityOf(d *adb.Device) string {
	if e, ok := a.deviceEntry(d); ok {
		if e.Marketname != "" {
			return e.Marketname
		}
	}
	return deviceIdentity(d)
}

// displayNameOf 设备展示名（市场名优先）：档案 marketname → adb marketname →
// Name（富化名，marketname 优先、man+model 兜底）→ serial。
// 弹窗文本必须用市场名：档案有 "REDMI K80" 而本轮 getprop 失败时，
// Name 会是 "Xiaomi 24117RK2CC"（厂商+型号），禁止拿它当弹窗文本。
func (a *App) displayNameOf(d *adb.Device) string {
	if e, ok := a.deviceEntry(d); ok {
		if n := profileCardName(e, ""); n != "" {
			return n
		}
	}
	if d.Marketname != "" {
		return d.Marketname
	}
	if d.Name != "" {
		return d.Name
	}
	return d.Serial
}

// usbSerialByIdentity 找同 identity 的在线 USB transport 的 serial
// （目标卡显示无线但同一设备的 USB transport 实际在线时——双卡/分卡场景——
// 仍应注入 SCEZ_SERIAL=USB serial；跳过目标自身）。identity 判据统一走
// a.identityOf（档案市场名优先，防 marketname 抖动导致漏注/错注）。
func (a *App) usbSerialByIdentity(identity, selfSerial string, devices []adb.Device) string {
	for i := range devices {
		d := &devices[i]
		if d.ConnType != "usb" || d.State != "device" || d.Serial == selfSerial {
			continue
		}
		if a.identityOf(d) == identity {
			return d.Serial
		}
	}
	return ""
}

// --- 会话查找（serial 直查 + 卡片重键 identity 兜底） ---

// findSessionLocked 按 serial 找会话；直查未命中时按设备档案 identity 兜底
// （设备 USB/无线切换导致卡片重键后，旧 serial 仍能定位到会话）。
// 调用方必须持 a.mu 写锁或读锁。
func (a *App) findSessionLocked(serial string) *sessionState {
	if st, ok := a.sessions[serial]; ok {
		return st
	}
	if d := findTargetDevice(serial, a.devices); d != nil {
		id := a.identityOf(d)
		if id == "" {
			return nil
		}
		for k, st := range a.sessions {
			if k != serial && st.identity != "" && st.identity == id {
				return st
			}
		}
	}
	return nil
}

// hasSessionForDeviceLocked 该设备（卡片）是否已存在会话条目
// （serial/无线地址/identity 三重匹配；含刚结束未 GC 的会话——
// 防止"停止投屏后立刻又弹新设备弹窗"）。
func (a *App) hasSessionForDeviceLocked(d *adb.Device) bool {
	id := a.identityOf(d)
	for _, st := range a.sessions {
		if st.cast.Serial == d.Serial || (d.Wireless != "" && st.cast.Serial == d.Wireless) {
			return true
		}
		if id != "" && st.identity != "" && st.identity == id {
			return true
		}
	}
	return false
}

// --- 新设备弹窗状态机（轮 B 目标 1，事件驱动） ---

// identityProfiledLocked 判定设备身份是否已在档案中（"新设备"判据）：
// identity 键 / serial / 无线地址 任一命中即视为已建档。
// 注意调用时机：弹窗判定必须在 SyncDevices 之前执行——若用同步后的
// 档案，首见设备会被 SyncDevices 并入档案而误判"已建档"、永不弹窗。
func (a *App) identityProfiledLocked(id string, d *adb.Device) bool {
	if id == "" {
		return false
	}
	if _, ok := a.profiles.Entry(id); ok {
		return true
	}
	if _, ok := a.profiles.Entry(d.Serial); ok {
		return true
	}
	if d.Wireless != "" {
		if _, ok := a.profiles.Entry(d.Wireless); ok {
			return true
		}
	}
	return false
}

// popupHandledLocked 弹窗设备是否已创建会话（【开始投屏】成功路径自动清弹窗）。
func (a *App) popupHandledLocked() bool {
	p := a.popup.info
	if p == nil {
		return false
	}
	for _, st := range a.sessions {
		if st.cast.Serial == p.Serial {
			return true
		}
		if p.Identity != "" && st.identity == p.Identity {
			return true
		}
	}
	return false
}

// DismissNewDevice 弹窗【暂不】：会话周期（本在线周期）内不再弹该设备；
// 用户可后续手动点设备卡"投屏"按钮。设备离线再上线后重新允许弹窗。
func (a *App) DismissNewDevice(serial string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := ""
	if a.popup.info != nil && a.popup.info.Serial == serial {
		id = a.popup.info.Identity
		a.clearPopupLocked()
	}
	if id == "" {
		if d := findTargetDevice(serial, a.devices); d != nil {
			id = a.identityOf(d)
		}
	}
	if id == "" {
		id = serial
	}
	a.popup.dismissed[id] = true
	bridge.DebugLog("[app] 新设备弹窗已暂不：%s", serial)
}

// listProfiles 返回全部设备档案条目（参数管理页列表；含离线设备）。
// 顺序稳定：按展示名升序，同名按 key。Addrs 沿用档案排序（active 优先）。
func (a *App) listProfiles() []ProfileItem {
	entries := a.profiles.Entries()
	out := make([]ProfileItem, 0, len(entries))
	for key, e := range entries {
		item := ProfileItem{
			Key:     key,
			Model:   e.Model,
			Serials: append([]string{}, e.Serials...),
			Addrs:   make([]string, 0, len(e.Addrs)),
		}
		for i := range e.Addrs {
			item.Addrs = append(item.Addrs, e.Addrs[i].Addr)
		}
		switch {
		case e.Marketname != "":
			item.Name = e.Marketname
		case e.Model != "":
			item.Name = e.Model
		case len(item.Serials) > 0:
			item.Name = item.Serials[0]
		case len(item.Addrs) > 0:
			item.Name = item.Addrs[0]
		default:
			item.Name = key
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// --- 快照 ---

// Snapshot 返回当前状态（JS 轮询入口，线程安全；panic 由 guard 兜底留痕）。
func (a *App) Snapshot() Snapshot {
	var s Snapshot
	a.guard("Snapshot", func() { s = a.snapshotRaw() })
	return s
}

// SetDeviceOrder 前端写回设备卡顺序（gui45；转 ProfileStore 持久化）。
func (a *App) SetDeviceOrder(order []string) error {
	return a.profiles.SetDeviceOrder(order)
}

func (a *App) snapshotRaw() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	sessions := make([]Session, 0, len(a.sessions))
	for serial, st := range a.sessions {
		c := st.cast
		c.Log = append([]string{}, st.cast.Log...)
		sessions = append(sessions, Session{
			Serial:    serial,
			DevName:   c.DevName,
			Identity:  st.identity,
			Active:    c.Active || st.restarting, // 重启间隙（已退出、待重跑）标签不淡出
			Stopping:  st.stopping,               // StopCast 已受理 → 前端"正在终止…"（onExit/超时复位）
			Closing:   st.closing,                // 投屏窗口 X 已关闭、bat 清理中（前端"正在关闭…"；onExit 复位）
			Mode:      c.Mode,
			StartedAt: st.startedAt.UnixMilli(),
			Cast:      c,
		})
	}
	// 顺序稳定：按 StartCast 时间升序；同刻按 serial（设备名稳定键）
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].StartedAt != sessions[j].StartedAt {
			return sessions[i].StartedAt < sessions[j].StartedAt
		}
		return sessions[i].Serial < sessions[j].Serial
	})
	disc := a.discStatus
	disc.Tried = append([]string{}, a.discStatus.Tried...)
	var pair *PairStatus
	if a.pair != nil {
		c := *a.pair
		c.Steps = append([]PairStep{}, a.pair.Steps...)
		pair = &c
	}
	return Snapshot{
		Version:    a.cfg.Version,
		BatPath:    a.cfg.BatPath,
		AdbOK:      a.adbOK,
		AdbFailing: a.adbOK && a.adbFail > 0, // 真空期去抖：可用但正在连续失败 → 前端"刷新中"软提示
		Devices:    append([]adb.Device{}, a.devices...),
		Pending:    append([]PendingDevice{}, a.pending...),
		Discovery:  disc,
		Cast:       a.activeCastLocked(),
		Sessions:   sessions,
		Profiles:   a.listProfiles(),
		DevOrder:   a.profiles.DeviceOrder(),
		NewDevice:  a.popup.info,
		PairStatus: pair,
	}
}

// activeCastLocked 返回"当前会话"内容（兼容旧单会话 Snapshot.Cast）：
// 最近启动的活动会话优先；同刻按 serial 取大者（确定性）；
// 无活动会话 → 最近启动的会话（结束态）；无会话 → 初始态。
func (a *App) activeCastLocked() CastState {
	var latest *sessionState
	latestSerial := ""
	for serial, st := range a.sessions {
		if !st.cast.Active {
			continue
		}
		if latest == nil || st.startedAt.After(latest.startedAt) ||
			(st.startedAt.Equal(latest.startedAt) && serial > latestSerial) {
			latest, latestSerial = st, serial
		}
	}
	if latest == nil {
		for serial, st := range a.sessions {
			if latest == nil || st.startedAt.After(latest.startedAt) ||
				(st.startedAt.Equal(latest.startedAt) && serial > latestSerial) {
				latest, latestSerial = st, serial
			}
		}
	}
	if latest == nil {
		return CastState{ExitCode: -1, Log: []string{}}
	}
	c := latest.cast
	c.Log = append([]string{}, latest.cast.Log...)
	return c
}

// --- 会话启动 ---

// callerChain 取调用栈前 2 帧函数名（仅函数短名，日志用）：
// 观测"谁在调 StartCast/RestartCast"（21:29 关窗后 90s 自动重投的偶发问题——
// StartCast 现在留来源痕迹：自动路径 vs 桥调用的调用链一目了然）。
// go:noinline：保持独立栈帧，skip 语义确定（内联会打乱帧计数）。
//
//go:noinline
func callerChain(skip int) string {
	pcs := make([]uintptr, 6)
	// Callers(skip+2)：跳过 callerChain 自身 + skip 层调用方——观测目标是
	// "调用者的上层"（自动路径 vs JS 桥来源）：skip=1 时首帧=StartCast 的调用者、
	// 次帧=其上层（与 app_trace_test.go 的 traceCallerOfStart 契约一致）
	n := runtime.Callers(skip+2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var names []string
	for i := 0; i < 2; i++ {
		f, more := frames.Next()
		if !more {
			break
		}
		name := f.Function
		if j := strings.LastIndexByte(name, '/'); j >= 0 {
			name = name[j+1:]
		}
		if j := strings.IndexByte(name, '.'); j >= 0 {
			name = name[j+1:]
		}
		names = append(names, name)
	}
	return strings.Join(names, " <- ")
}

// StartCastParallel 并行会话启动入口（轮 B 目标 1【开始投屏】）：
// 与 StartCast 同路径（NO_WATCH 已废弃——watcher 会话化后每会话独立
// WATCH_TAG/flag/只关本会话 scrcpy，"对上才切"按市场名比对，并行会话各自
// watcher 互不干扰，无需再禁用）。serial 无会话时创建新会话；
// 已有会话（同 serial/同 identity 活动会话）→ 返回错误"投屏已在运行"。
func (a *App) StartCastParallel(serial string) error {
	bridge.DebugLog("[app] StartCastParallel 进入 serial=%q 调用链=%s", serial, callerChain(1))
	return a.StartCast(serial)
}

// StartCast 对指定设备启动投屏（隐藏 bat，每会话一个 bat 实例）。
// 多会话规则：同 serial 已有活动会话 → "投屏已在运行"；同 identity 已有活动会话
// （设备卡重键/双卡）→ 同样拒绝；同 serial 的结束态会话 → 原地替换（开新会话）。
func (a *App) StartCast(serial string) error {
	// 来源观测（21:29 关窗后 90s 自动重投的偶发问题）：入口即留痕——
	// 调用链自动路径 vs JS 桥调用（配合 ui 层 [js] 日志定位前端触发者）
	bridge.DebugLog("[app] StartCast 进入 serial=%q 调用链=%s", serial, callerChain(1))
	// 空串号防御：空 serial 会创建 key="" 的会话且注入不到 SCEZ_SERIAL →
	// bat 自由检测抓"第一台设备"（互抢隐患）。前端串号闭包已保证非空，
	// 这里兜底拒绝（多会话"点了 A 投到 B"的最后一道闸）。
	if serial == "" {
		return errors.New("未指定设备")
	}
	// 廉价预检：同 serial 已在投屏 → 直接拒绝，不触发下面的地址验证
	// （重复点击不白连）。identity 级拒绝仍在锁内做权威判定。
	a.mu.RLock()
	alreadyRunning := a.sessions[serial] != nil && a.sessions[serial].runner != nil
	a.mu.RUnlock()
	if alreadyRunning {
		return errors.New("投屏已在运行")
	}

	// gui36 投屏地址=档案直选不验证（无线卡；USB 卡走 USB serial，无线地址链
	// 不涉及——锁内仍按原 gui21/gui15 链做无线分支提示）：
	// ① mDNS 广播命中（设备正在公告的当前地址）→ 直接选，不验证（端口必活）；
	// ② 无广播 → 档案 OrderedAddrs[0] 直选（不验证；验证与 IP 更新归 mDNS
	//    15s 责任环——mDNS 广播=真相即替换、active 置位、失败只 60s 节流永不拉黑）；
	// ③ 无线卡无候选 → 走原 target.Serial 兜底（不变）。
	startAddr, addrVerified := "", false
	a.mu.RLock()
	t0 := findTargetDevice(serial, a.devices)
	wifiTarget := t0 != nil && t0.ConnType == "wifi"
	a.mu.RUnlock()
	if wifiTarget {
		if mdns := a.wirelessStartAddr(serial); mdns != "" {
			startAddr = mdns
		} else if cands := a.profiles.OrderedAddrs(serial); len(cands) > 0 {
			addrVerified = true
			startAddr = cands[0].Addr // gui36 档案直选不验证：活性由 mDNS/探测链每 15s 维护
		}
	}

	a.mu.Lock()
	if st, ok := a.sessions[serial]; ok && st.runner != nil {
		a.mu.Unlock()
		return errors.New("投屏已在运行")
	}
	if a.newR == nil {
		a.mu.Unlock()
		return errors.New("bat 桥接未初始化")
	}
	name := serial
	target := findTargetDevice(serial, a.devices)
	if target == nil {
		// 卡片重键兜底：serial 可能是过期的旧键（会话启动后设备 USB/无线
		// transport 切换 → 卡片 Serial 重键），按档案 identity（serials+addrs）再找一次
		if e, ok := a.profiles.Entry(serial); ok {
			target = findTargetByIdentity(e, a.devices)
		}
	}
	if target != nil {
		name = a.displayNameOf(target)
	}
	identity := ""
	if target != nil {
		identity = a.identityOf(target)
	} else if e, ok := a.profiles.Entry(serial); ok && e.Marketname != "" {
		// 设备卡不在线（恢复期/结束态重投）：档案市场名兜底身份——
		// 会话 identity 稳定，弹窗判重与防同设备双开会话不随轮询抖动
		identity = e.Marketname
	}
	// 同 identity 已有活动会话 → 拒绝（同一设备不能开两个会话）
	if identity != "" {
		for k, o := range a.sessions {
			if k == serial {
				continue
			}
			if o.runner != nil && o.identity == identity {
				a.mu.Unlock()
				return errors.New("投屏已在运行")
			}
		}
	}
	// gui36 起该分支不再可达：addrVerified=true 时 startAddr 恒非空（有候选→
	// 直选 OrderedAddrs[0]；无候选→addrVerified=false），保留代码仅作防御/注释。
	// 旧 gui32 语义是候选全部验证失败 → 友好提示「未找到可用无线地址」，现已退役。
	if target != nil && target.ConnType == "wifi" && startAddr == "" && addrVerified {
		a.mu.Unlock()
		return errors.New("未找到可用无线地址")
	}
	// 原生分辨率捕获（宽≥高，修复恢复期徽标"长边 X"）：
	// ①目标卡 Res → ②同 identity 其他卡 Res → ③档案持久化 Res——
	// 启动即定格进 CastState，投屏会话期间不随实时设备列表消失。
	nativeRes := ""
	if target != nil {
		nativeRes = target.Res
		if nativeRes == "" {
			ti := a.identityOf(target)
			for i := range a.devices {
				if a.devices[i].Res != "" && a.identityOf(&a.devices[i]) == ti {
					nativeRes = a.devices[i].Res
					break
				}
			}
		}
	}
	if nativeRes == "" {
		if e, ok := a.profiles.Entry(serial); ok {
			nativeRes = e.Res
		}
	}

	// NO_WATCH 已废弃（watcher 会话化后不再需要）：普通/并行会话一律注入
	// SCEZ_WATCH_TAG（每会话独立 watcher），不再生成 SCEZ_NO_WATCH=1——
	// watcher 按 WATCH_TAG/flag 隔离 + 市场名"对上才切"，并行会话互不干扰，
	// 插线切换对每个会话都生效（v3 修复"插线不切有线"）。

	// 参数装配：参数浮窗保存后的覆盖（按 serial）+ 档案自定义记忆（按模式独立注入）
	params := a.nextParams[serial]
	delete(a.nextParams, serial)
	p := a.profiles.Get(serial)
	if !params.Usb.Set && p.Usb.Custom {
		params.Usb = bridge.ModeParams{Res: p.Usb.Res, FPS: p.Usb.FPS, Bitrate: p.Usb.Bitrate, Set: true}
	}
	if !params.Wifi.Set && p.Wifi.Custom {
		params.Wifi = bridge.ModeParams{Res: p.Wifi.Res, FPS: p.Wifi.FPS, Bitrate: p.Wifi.Bitrate, Set: true}
	}
	// 设备锁定注入（多设备 Phase 1 轮 A + 修复）：
	//   USB 在线 → SCEZ_SERIAL=USB serial——判据 = "该 identity 存在在线 USB transport"
	//   （无论目标卡显示的 ConnType；双卡场景下同 identity 的 USB 卡也算）→ bat 首轮即 USB 投屏
	//   （修复"保存重投先无线后有线"：卡片重键后旧 serial 查不到目标 → 漏注 SERIAL）；
	//   SCEZ_ADDR=无线地址（gui36 档案直选不验证：无线卡=广播/档案
	//   OrderedAddrs[0]；USB 卡=广播/档案 BestAddr 提示——拔线后无线分支用它
	//   直连兜底）；
	//   两者都未命中（设备列表外）→ 不注入，bat 走原逻辑。
	tlsFlag := false // 当次连接形态：注入的 addr 形态=tls 时前端显示"TLS加密"
	if target != nil {
		usbSerial := ""
		if target.ConnType == "usb" {
			usbSerial = target.Serial
		} else {
			// 目标卡是无线：同 identity 的其他卡/无其他卡时按 identity 找在线 USB transport
			usbSerial = a.usbSerialByIdentity(a.identityOf(target), target.Serial, a.devices)
		}
		if usbSerial != "" {
			params.Serial = usbSerial
		}
		// gui36 地址链（主人基准：mDNS 每 15s 维护档案活性，投屏不验证）：
		//   无线卡 → 广播直接选 / 档案 OrderedAddrs[0] 直选（预检阶段已算好
		//     startAddr；档案无候选（空档/全节流）→ 当前在线地址兜底，活连接
		//     非死记忆）；投屏不再 adb connect 验证——0.5-2s 卡顿消除。
		//   USB 卡 → 原 gui21/gui15 链（广播 → 档案 BestAddr 作无线分支提示，
		//     不验证——USB serial 才是本次投屏通道）。
		// K80 场景：45005 tls 广播尚未入档也直接选中；tlsFlag 三层判定对
		// 注入的广播地址自然判 tls（AddrMode/isKnownTlsAddr/isTlsFormAddr），
		// 判定本身不动。
		addr := startAddr
		if target.ConnType == "wifi" {
			if addr == "" {
				addr = target.Serial
			}
		} else {
			addr = a.wirelessStartAddr(serial)
			if addr == "" {
				addr = a.profiles.BestAddr(serial)
			}
		}
		if addr != "" {
			params.Addr = addr
			// gui42：同时注入备用异形态地址（bat 单次降级；无备用不注入）。
			params.Addr2 = a.secondaryWirelessAddr(serial)
			// 形态判定两层：档案记录（mode=tls）→ 环境事实启发式（port!=5555）。
			tlsFlag = a.profiles.AddrMode(addr) == ModeTls || isTlsFormAddr(addr)
		}
	}
	// 档案身份注入（防抢锁定的会话本尊）：锁定会话（Serial/Addr 已注）才带
	// SCEZ_MARKET/SCEZ_MODEL——bat 无线回退时用它拒绝"共享 config.txt 被别的
	// 会话改写"的异身份设备，watcher 市场名读不到时也用 SCEZ_MARKET 比对。
	if params.Serial != "" || params.Addr != "" {
		if e, ok := a.profiles.Entry(serial); ok {
			if e.Marketname != "" {
				params.Market = e.Marketname
			}
			if e.Model != "" {
				params.Model = e.Model
			}
		}
	}

	// 创建会话级 runner：回调按 serial 绑定，App 侧再按 runner 身份防串
	// （陈旧 runner 的迟到回调不污染重启/替换后的新会话）。
	var r Runner
	r, err := a.newR(serial,
		func(line string) { a.NotifyLineFor(serial, r, line) },
		func(code int) { a.OnBatExitFor(serial, r, code) })
	if err != nil {
		a.mu.Unlock()
		return err
	}
	// 兼容占位（gui46 起 Stop 不再兜底 kill-server；保留注入以兼容旧 Runner 实现）。
	if g, ok := r.(interface{ SetCanKillServer(func() bool) }); ok {
		g.SetCanKillServer(func() bool {
			a.mu.RLock()
			defer a.mu.RUnlock()
			return !a.anyActiveSessionLocked(serial)
		})
	}

	// 会话条目：结束态同 serial 原地替换；重启（restarting）沿用 startedAt 保持标签位置
	keepStart := false
	if st, ok := a.sessions[serial]; ok && st.restarting {
		keepStart = true
	}
	startedAt := time.Now()
	if keepStart {
		startedAt = a.sessions[serial].startedAt
	}
	a.sessions[serial] = &sessionState{
		cast: CastState{
			Active:    true,
			Serial:    serial,
			DevName:   name,
			NativeRes: nativeRes,
			Phase:     "starting",
			PhaseText: "正在启动…",
			Tls:       tlsFlag,
			Log:       []string{},
			ExitCode:  -1,
		},
		runner:     r,
		lastLineAt: time.Now(), // 无输出提示计时从启动开始
		startedAt:  startedAt,
		identity:   identity,
		// gui43：保存注入参数，供 pollOnce 按 adb 实时事实选举实际 transport。
		castAddr:  params.Addr,
		castAddr2: params.Addr2,
		usbSerial: params.Serial,
	}
	// 弹窗设备已开始投屏 → 清除弹窗
	if a.popup.info != nil && (a.popup.info.Serial == serial ||
		(identity != "" && a.popup.info.Identity == identity)) {
		a.clearPopupLocked()
	}
	a.mu.Unlock()

	// 异步启动：r.Start panic 也会被 guard 捕获并落崩溃日志；
	// defer 兜底保证启动失败（含 panic）都落"启动失败"事件并进结束态。
	go a.guard("StartCast", func() {
		ok := false
		defer func() {
			if !ok {
				a.pushErrorLocked(serial, r, "启动失败：bat 进程异常（详见崩溃日志）")
				a.OnBatExitFor(serial, r, -1)
			}
		}()
		if err := r.Start(serial, params); err != nil {
			a.pushErrorLocked(serial, r, "启动失败："+err.Error())
			return
		}
		ok = true
		// 陈旧启动守卫：启动期间会话已被停止/替换 → 立刻杀掉刚启动的进程
		a.mu.Lock()
		st := a.sessions[serial]
		stale := st == nil || (st.runner != nil && st.runner != r)
		a.mu.Unlock()
		if stale {
			_ = r.Stop()
		}
	})
	return nil
}

// RefreshNow 立即触发一次设备轮询（界面"刷新"按钮）。
func (a *App) RefreshNow() {
	go a.guard("RefreshNow", func() { a.pollOnce(context.Background()) })
}

// ForceDiscover（gui48-mdns5）：connect 探测已退役，保留 RPC 兼容。
// 仅刷新设备列表/显示层，不再触发任何 connect 探测——无线状态全权由 mdns 信号决定。
func (a *App) ForceDiscover() {
	a.RefreshNow()
}

// coldSearch5555 冷启动全量地址搜索（仅一次，gui52fix4）：遍历档案全部设备的
// 所有地址（tcpip/5555 与 TLS 随机端口），并行 connect（单地址 2s 级超时）。
// 通 → active；不通 → stale——启动即用硬事实定初始 active/stale 状态。
// （gui48-mdns5 旧语义：只搜 tcpip 且失败不动——TLS 僵尸 active 来源，已废。）
func (a *App) coldSearch5555(ctx context.Context) {
	if a.disc == nil {
		return
	}
	a.mu.Lock()
	if a.coldSearch5555Done {
		a.mu.Unlock()
		return
	}
	a.coldSearch5555Done = true
	a.mu.Unlock()

	type target struct {
		id   string
		addr string
		mode string
	}
	var targets []target
	seen := map[string]bool{}
	for key, e := range a.profiles.Entries() {
		for i := range e.Addrs {
			ae := e.Addrs[i]
			if ae.Addr == "" || seen[ae.Addr] {
				continue
			}
			seen[ae.Addr] = true
			targets = append(targets, target{id: key, addr: ae.Addr, mode: addrEntryClass(ae)})
		}
	}
	if len(targets) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err := a.disc.ConnectOut(cctx, t.addr)
			cancel()
			if err == nil {
				a.profiles.AddrSuccessMode(t.id, t.addr, t.mode)
				bridge.DebugLog("[app] 冷启动全量搜索成功：%s/%s → active", t.id, t.addr)
			} else {
				a.profiles.AddrFailMode(t.id, t.addr, t.mode)
				bridge.DebugLog("[app] 冷启动全量搜索不通：%s/%s → stale（%v）", t.id, t.addr, err)
			}
		}(t)
	}
	wg.Wait()
	a.reapplyDisplay("冷启动全量搜索")
}

// probeProfileActiveAddrs 对某档案全部 active 地址做一轮 connect 验尸（gui52fix4）：
// 并行探测（单地址 2s 级超时），通 → 保持 active（AddrSuccessMode），
// 不通 → AddrFailMode（stale）。用于设备流缺席事件触发——硬事实降级，
// 不依赖 GUI 组播信号。幂等：无 active 地址时直接返回。
func (a *App) probeProfileActiveAddrs(ctx context.Context, key string) {
	if a.disc == nil || key == "" {
		return
	}
	e, ok := a.profiles.Entry(key)
	if !ok {
		return
	}
	type target struct {
		addr string
		mode string
	}
	var targets []target
	for i := range e.Addrs {
		ae := e.Addrs[i]
		if ae.Addr == "" || ae.State != AddrStateActive {
			continue
		}
		targets = append(targets, target{addr: ae.Addr, mode: addrEntryClass(ae)})
	}
	if len(targets) == 0 {
		return
	}
	var wg sync.WaitGroup
	var alive bool
	var aliveMu sync.Mutex
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err := a.disc.ConnectOut(cctx, t.addr)
			cancel()
			if err == nil {
				a.profiles.AddrSuccessMode(key, t.addr, t.mode)
				aliveMu.Lock()
				alive = true
				aliveMu.Unlock()
				bridge.DebugLog("[app] 缺席验尸：%s/%s 探测通 → 保持 active", key, t.addr)
			} else {
				a.profiles.AddrFailMode(key, t.addr, t.mode)
				bridge.DebugLog("[app] 缺席验尸：%s/%s 探测不通 → stale（%v）", key, t.addr, err)
			}
		}(t)
	}
	wg.Wait()
	// gui52-fix9：验尸判定「仍活着」后立即刷新显示——设备流缺席期间显示层
	// 停在 offline，若不重算会闪现离线卡（实测 3.5s）；档案 active=在线证据。
	aliveMu.Lock()
	aliveNow := alive
	aliveMu.Unlock()
	if aliveNow {
		a.reapplyDisplay("缺席验尸保活")
	}
}

// --- 无线调试配对向导（gui12 A：mDNS 自动发现 → 只输 6 位配对码 → TLS 接入入档） ---

// PairConnect 启动配对并连接流程（RPC 入口）：受理后立即返回（不阻塞 UI 线程），
// 过程/结果经 Snapshot.PairStatus 轮询展示。devKey=待配对卡键（自动发现场景）；
// ip/pairPort/connPort/code 由前端传（手动场景直接给值；自动场景端口留空=后端按
// mDNS 取：配对端口取 _adb-tls-pairing 同 GUID 服务、连接端口取 tls 服务端口）。
// 流程：pair（adb pair ip:pairport code）→ connect（adb connect ip:tls端口）→
// verify（getprop 验证 marketname）→ 入档 mode=tls（**不切 5555，保持 TLS 形态**）。
func (a *App) PairConnect(devKey, ip, pairPort, connPort, code string) error {
	// gui50：二维码路线经既有 RPC 通道的哨兵协议（不新增 RPC）。
	switch devKey {
	case "__qr_open__":
		return a.pairQrOpen()
	case "__qr_refresh__":
		return a.pairQrRefresh()
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 || !isDigits(code) {
		a.setPairFailed(PairErrCode, "配对码须为 6 位数字", devKey, ip)
		return nil
	}
	a.mu.Lock()
	if a.pair != nil && a.pair.Phase != PairPhaseIdle &&
		a.pair.Phase != PairPhaseSuccess && a.pair.Phase != PairPhaseFailed {
		a.mu.Unlock()
		return errors.New("配对进行中")
	}
	mode := PairModeManual
	if devKey != "" {
		mode = PairModeAuto
	}
	// gui53：配对进行中保留二维码文本（继承 pairQR/旧 pair 的码）——
	// 前端配对中二维码不白屏（原来 PairConnect 重建 PairStatus 时 QrText 丢失，
	// 前端 pairRenderQr 收到空码清空 canvas——17:07 实测）。二维码随弹窗关闭
	// 统一销毁；setPairFailed 也有失败态回填逻辑。
	qrText, qrExp := "", int64(0)
	if a.pairQR != nil && !time.Now().After(a.pairQR.expireAt) {
		qrText = a.pairQR.qrText()
		qrExp = a.pairQR.expireAt.Unix()
	} else if a.pair != nil && a.pair.QrText != "" {
		qrText = a.pair.QrText
		qrExp = a.pair.QrExpireAt
	}
	a.pair = &PairStatus{Phase: PairPhasePairing, Mode: mode, Key: devKey,
		Steps: []PairStep{{Name: PairStepPair}}, QrText: qrText, QrExpireAt: qrExp}
	a.mu.Unlock()

	// 参数预解析（快照态：待配对卡）
	p, _ := a.findPending(devKey)
	if ip == "" {
		ip = p.IP
	}
	name := p.Name
	if name == "" {
		name = "新设备"
	}
	if connPort == "" {
		connPort = addrPort(p.Addr)
	}
	bridge.DebugLog("[app] PairConnect devKey=%q ip=%q pairPort=%q connPort=%q", devKey, ip, pairPort, connPort)

	go a.guard("pair-connect", func() { a.runPairFlow(p, ip, pairPort, connPort, code, name) })
	return nil
}

// PairReset 清空配对向导状态（前端弹窗关闭/重试时调用）。
func (a *App) PairReset() {
	a.mu.Lock()
	a.pair = nil
	a.pairQR = nil
	a.mu.Unlock()
}

// pairQrOpen 打开二维码路线：生成 6 位码 + WIFI:T:ADB 文本，状态=idle。
func (a *App) pairQrOpen() error {
	a.mu.Lock()
	if a.pair != nil && a.pair.Phase != PairPhaseIdle &&
		a.pair.Phase != PairPhaseSuccess && a.pair.Phase != PairPhaseFailed {
		a.mu.Unlock()
		return errors.New("配对进行中")
	}
	q := a.generatePairQRLocked()
	a.pairQR = q
	a.pair = &PairStatus{Phase: PairPhaseIdle, Mode: PairModeQR, QrText: q.qrText(), QrExpireAt: q.expireAt.Unix()}
	a.mu.Unlock()
	bridge.DebugLog("[app] 二维码配对已打开：%s（%s 过期）", q.qrText(), q.expireAt.Format("15:04:05"))
	return nil
}

// pairQrRefresh 重新生成二维码（悬停刷新 / 2 分钟失效后自动重新生成）。
func (a *App) pairQrRefresh() error {
	a.mu.Lock()
	if a.pairQR == nil || (a.pair != nil && a.pair.Mode != PairModeQR) {
		a.mu.Unlock()
		return errors.New("二维码路线未打开")
	}
	q := a.generatePairQRLocked()
	a.pairQR = q
	if a.pair != nil {
		a.pair.QrText = q.qrText()
		a.pair.QrExpireAt = q.expireAt.Unix()
	}
	a.mu.Unlock()
	bridge.DebugLog("[app] 二维码配对已刷新：%s", q.qrText())
	return nil
}

func (a *App) generatePairQRLocked() *pairQRState {
	return &pairQRState{
		code:        fmt.Sprintf("%06d", rand.Intn(1000000)),
		serviceName: pairQrServiceName,
		expireAt:    time.Now().Add(pairQrTTL),
	}
}

func (q *pairQRState) qrText() string {
	return "WIFI:T:ADB;S:" + q.serviceName + ";P:" + q.code + ";;"
}

// maybeAutoPairQR 事件驱动：_adb-tls-pairing 广播“本次新增且服务名=二维码 S 值”
// = 手机已扫码 → 自动配对。手动配对界面广播（adb-xxx 命名）即使新增也不触发。
// 生产路径由 onMdnsTrackEvents 传入本次 added 列表；不带参数仅是旧测试直连兜底
// （扫描全量快照），生产环境不会触发全量扫描，稳定老广播不会被误判为扫码。
func (a *App) maybeAutoPairQR(added ...[]discovery.MdnsService) {
	a.mu.Lock()
	q := a.pairQR
	if q == nil {
		a.mu.Unlock()
		return
	}
	if a.pair != nil && a.pair.Phase != PairPhaseIdle {
		a.mu.Unlock()
		return
	}
	code := q.code
	qrText := q.qrText()
	qrExpireAt := q.expireAt.Unix()
	expired := time.Now().After(q.expireAt)
	a.mu.Unlock()

	var services []discovery.MdnsService
	if len(added) > 0 {
		services = added[0]
	} else {
		services = a.mdnsSnapshot()
	}

	// fix7：过期二维码的扫码广播不用于配对（旧码无法匹配）；检测到新增 ADBQR
	// 广播时自动重新生成二维码，用户按新码重扫即可。本次广播忽略。
	if expired {
		for _, s := range services {
			if s.Mode == discovery.MdnsModePairing && s.Addr != "" && s.Name == pairQrServiceName {
				_ = a.pairQrRefresh()
				bridge.DebugLog("[app] 过期二维码收到扫码广播，已自动重新生成，请用户重扫")
				return
			}
		}
		return
	}

	for _, s := range services {
		if s.Mode != discovery.MdnsModePairing || s.Addr == "" || s.Name != pairQrServiceName {
			continue
		}
		a.startPairQR(ipOfAddr(s.Addr), addrPort(s.Addr), code, qrText, qrExpireAt)
		return
	}
}

// startPairQR 由配对广播触发自动配对：ip/pairPort 来自广播，connect 端口配对
// 成功后再从 _adb-tls-connect 广播解析（runPairFlow 延迟解析）。
// fix3：不再消费 pairQR，PairStatus 保留二维码文本，失败后二维码仍可显示/刷新。
func (a *App) startPairQR(ip, pairPort, code, qrText string, qrExpireAt int64) {
	a.mu.Lock()
	if a.pair != nil && a.pair.Phase != PairPhaseIdle &&
		a.pair.Phase != PairPhaseSuccess && a.pair.Phase != PairPhaseFailed {
		a.mu.Unlock()
		return
	}
	a.pair = &PairStatus{
		Phase:      PairPhasePairing,
		Mode:       PairModeQR,
		Steps:      []PairStep{{Name: PairStepPair}},
		QrText:     qrText,
		QrExpireAt: qrExpireAt,
	}
	// 保留 a.pairQR：失败态二维码不清空；前端可悬停刷新，setPairFailed 也可重新生成。
	a.mu.Unlock()
	bridge.DebugLog("[app] 检测到 pairing 广播：%s:%s → 自动配对", ip, pairPort)
	go a.guard("pair-qr-connect", func() {
		a.runPairFlow(PendingDevice{}, ip, pairPort, "", code, "新设备")
	})
}

// runPairFlow 配对向导异步主流程（goroutine；全程 guard 防 panic）。
// fix6：生产路径先尝试 connect 目标 TLS 地址，成功=信任仍在，跳过 adb pair 直接建档；
// connect 失败再回退原 pair→connect 流程。
func (a *App) runPairFlow(p PendingDevice, ip, pairPort, connPort, code, name string) {
	devKey := p.Key
	hintSerial := p.Serial
	guid := p.Guid
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout+connectPairTimout+2*verifyTimeout+4*time.Second)
	defer cancel()

	// 连接端口解析：自动发现场景传入 tls 端口；手动/二维码场景留空 → 先按
	// 当前快照同 IP 的 tls 服务兜底。二维码路线配对成功前手机还没有 connect
	// 广播，解析推迟到配对成功后再做（下方）。
	// gui52 无线接入学习：同 IP 的 _adb-tls-connect 服务名 adb-<短号>-XXXX
	// 同时给出 guid 与设备短号（串行解析，免去依赖后续 getprop 的 serial）。
	if ip != "" {
		for _, s := range a.mdnsSnapshot() {
			if s.Mode != discovery.MdnsModeTls || s.Addr == "" || !strings.HasPrefix(s.Addr, ip+":") {
				continue
			}
			if connPort == "" {
				connPort = addrPort(s.Addr)
			}
			if guid == "" {
				guid = s.Name
			}
			if hintSerial == "" && strings.HasPrefix(s.Name, "adb-") {
				if serial := TlsServiceIdentity(s.Name); serial != "" {
					hintSerial = serial
				}
			}
			break
		}
	}

	// fix6 connect 先行快路径：connect 成功 = 手机侧仍有信任记录（删档找回场景），
	// 无需重复 adb pair；失败则回退下面的原 pair→connect 流程。
	if a.pairFastConnect && connPort != "" && ip != "" {
		fctx, fcancel := context.WithTimeout(ctx, 2*time.Second)
		out0, err0 := a.pairOps.connectFn(fctx, ip+":"+connPort)
		fcancel()
		if err0 == nil {
			a.mu.Lock()
			if a.pair != nil && len(a.pair.Steps) > 0 {
				a.pair.Steps[0].OK = true
				a.pair.Phase = PairPhaseConn
				a.pair.Steps = append(a.pair.Steps, PairStep{Name: PairStepConnect})
				a.pair.Name, a.pair.Addr = name, ip+":"+connPort
			}
			a.mu.Unlock()
			bridge.DebugLog("[app] connect 先行命中（信任仍在）：跳过 pair → %s", ip+":"+connPort)
			a.completePairFlowAfterConnect(ctx, p, ip, connPort, name, hintSerial, guid)
			return
		}
		bridge.DebugLog("[app] connect 先行未命中，回退 pair：%s %v", strings.TrimSpace(out0), err0)
	}

	qrRoute := false
	a.mu.RLock()
	if a.pair != nil {
		qrRoute = a.pair.Mode == PairModeQR
	}
	a.mu.RUnlock()

	// 手动/自动路线缺少 IP 或连接端口时立即失败（既有语义）；二维码路线配对成功前
	// 手机还没有 connect 广播，连接端口延迟到配对成功后再解析。
	if ip == "" || (connPort == "" && !qrRoute) {
		a.setPairFailed(PairErrConnPort, "缺少连接信息：请手动填写设备 IP 与端口（手机无线调试主界面显示）", devKey, ip)
		return
	}

	// ① 配对端口解析：传参 → mDNS 快照同 GUID/同 IP 配对服务 → 现场重扫。
	// 都没有 → "请打开手机配对界面或手动填写配对端口"。
	if pairPort == "" {
		if port := a.resolvePairPort(guid, ip, false); port != "" {
			pairPort = port
		} else if port := a.resolvePairPort(guid, ip, true); port != "" {
			pairPort = port
		} else {
			a.setPairFailed(PairErrPort, "未发现配对端口：请打开手机的配对界面（使用配对码配对设备），或手动填写 IP / 配对端口", devKey, ip)
			return
		}
	}

	// ② adb pair（配对码 30s 过期：单步 10s 超时，失败按输出分类）
	pctx, pcancel := context.WithTimeout(ctx, pairTimeout)
	out, err := a.pairOps.pairFn(pctx, ip, pairPort, code)
	pcancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(pctx.Err(), context.DeadlineExceeded) {
			a.setPairFailed(PairErrTimeout, "配对超时：请确认手机与电脑同一网络后重试", devKey, ip)
		} else {
			a.setPairFailed(PairErrInternal, "配对失败："+err.Error(), devKey, ip)
		}
		return
	}
	switch adb.PairErrKind(out) {
	case "":
		// 配对成功 ✓
	case "code":
		a.setPairFailed(PairErrCode, "配对码不正确或已过期。配对码约 30 秒刷新一次，请以手机屏幕当前显示为准后重试", devKey, ip)
		return
	case "port":
		a.setPairFailed(PairErrPort, "配对端口无效（手机的配对界面可能已关闭）。请重新打开配对界面或手动填写配对端口", devKey, ip)
		return
	case "addr":
		a.setPairFailed(PairErrAddr, "IP / 端口格式不正确，请检查后重试", devKey, ip)
		return
	default:
		a.setPairFailed(PairErrInternal, "配对失败："+strings.TrimSpace(out), devKey, ip)
		return
	}
	// gui50 二维码/手动路线：配对成功后手机才广播 _adb-tls-connect——此时解析
	// 连接端口（快照 + 现场重扫一次）。gui52：同时学习 guid 与设备短号。
	if connPort == "" {
		for _, s := range a.mdnsSnapshot() {
			if s.Mode != discovery.MdnsModeTls || s.Addr == "" || !strings.HasPrefix(s.Addr, ip+":") {
				continue
			}
			connPort = addrPort(s.Addr)
			if guid == "" {
				guid = s.Name
			}
			if hintSerial == "" && strings.HasPrefix(s.Name, "adb-") {
				if serial := TlsServiceIdentity(s.Name); serial != "" {
					hintSerial = serial
				}
			}
			break
		}
		if connPort == "" {
			if port, svcName := a.resolveTlsServiceFresh(ip); port != "" {
				connPort = port
				if guid == "" {
					guid = svcName
				}
				if hintSerial == "" && strings.HasPrefix(svcName, "adb-") {
					if serial := TlsServiceIdentity(svcName); serial != "" {
						hintSerial = serial
					}
				}
			}
		}
		if connPort == "" {
			a.setPairFailed(PairErrConnPort, "缺少连接端口：请确认手机无线调试主界面已显示连接端口后重试", devKey, ip)
			return
		}
	}

	a.mu.Lock()
	if a.pair != nil && len(a.pair.Steps) > 0 {
		a.pair.Steps[0].OK = true
		a.pair.Phase = PairPhaseConn
		a.pair.Steps = append(a.pair.Steps, PairStep{Name: PairStepConnect})
		a.pair.Name, a.pair.Addr = name, ip+":"+connPort
	}
	a.mu.Unlock()
	bridge.DebugLog("[app] 配对成功 %s:%s → 连接 %s", ip, pairPort, ip+":"+connPort)

	// ③ adb connect ip:tls端口（TLS 走 adb 内置协商；成功=已配对 known_device）
	cctx, ccancel := context.WithTimeout(ctx, connectPairTimout)
	out2, err2 := a.pairOps.connectFn(cctx, ip+":"+connPort)
	ccancel()
	if err2 != nil {
		if errors.Is(err2, context.DeadlineExceeded) || errors.Is(cctx.Err(), context.DeadlineExceeded) {
			a.setPairFailed(PairErrTimeout, "连接超时：请确认设备在线且网络可达后重试", devKey, ip)
		} else {
			a.setPairFailed(PairErrConnect, "连接失败："+strings.TrimSpace(out2)+"。请确认连接端口正确（手机无线调试主界面显示）", devKey, ip)
		}
		return
	}

	a.completePairFlowAfterConnect(ctx, p, ip, connPort, name, hintSerial, guid)
}

// completePairFlowAfterConnect 是 runPairFlow 的公共成功尾段：
// connect 已成功（无论 pair 后连接还是 fix6 connect 先行直通）→ 验证/入档/成功态/tcpip。
func (a *App) completePairFlowAfterConnect(ctx context.Context, p PendingDevice, ip, connPort, name, hintSerial, guid string) {
	devKey := p.Key

	a.mu.Lock()
	if a.pair != nil && len(a.pair.Steps) > 1 {
		a.pair.Steps[1].OK = true
		a.pair.Phase = PairPhaseVerify
		a.pair.Steps = append(a.pair.Steps, PairStep{Name: PairStepDone, Text: "正在验证设备信息…"})
	}
	a.mu.Unlock()

	// ④ getprop 验证（市场名/厂商/型号；失败不阻断——设备可能未授权，
	// 仍入档并以 serial 兜底展示，下一轮轮询富化）
	vctx, vcancel := context.WithTimeout(ctx, verifyTimeout)
	marketname, _ := a.pairOps.getpropFn(vctx, ip+":"+connPort, "ro.product.marketname")
	manufacturer, _ := a.pairOps.getpropFn(vctx, ip+":"+connPort, "ro.product.manufacturer")
	model, _ := a.pairOps.getpropFn(vctx, ip+":"+connPort, "ro.product.model")
	vcancel()

	// gui52：connect 服务实例名（adb-<短号>-XXXX）本身就是无线接入学习的
	// serial 来源。手动/connect-first 路线可能没有 guid（无人给服务名）——
	// connect 已成功后现场重扫一次同 IP 的 _adb-tls-connect 补齐 guid/短号，
	// 纯无线设备不再 serials:[]。
	if guid == "" || hintSerial == "" {
		if _, svcName := a.resolveTlsServiceFresh(ip); svcName != "" {
			if guid == "" {
				guid = svcName
			}
			if hintSerial == "" && strings.HasPrefix(svcName, "adb-") {
				if serial := TlsServiceIdentity(svcName); serial != "" {
					hintSerial = serial
				}
			}
		}
	}
	if hintSerial == "" && strings.HasPrefix(guid, "adb-") {
		if serial := TlsServiceIdentity(guid); serial != "" {
			hintSerial = serial
		}
	}

	// ⑤ 入档：mode=tls + tls 地址 + tlsGuid + serial + wireless=tls
	// （保持 TLS 形态，不切 5555）。
	// gui52-fix10：入档前按 mdns 快照重解析权威连接端口——配对期间 HyperOS
	// 可能重分配 TLS 端口（pair 返回值/早期快照端口已过时），以设备自报的
	// _adb-tls-connect 服务为准入档，避免过渡端口孤儿/丑名卡（45287 教训）。
	if freshPort, freshName := a.resolveTlsServiceFresh(ip); freshPort != "" {
		freshAddr := ip + ":" + freshPort
		if freshAddr != ip+":"+connPort {
			bridge.DebugLog("[app] 配对入档端口校正：mdns 权威 %s（原 %s）", freshAddr, ip+":"+connPort)
		}
		connPort = freshPort
		if guid == "" && freshName != "" {
			guid = freshName
		}
		if hintSerial == "" {
			hintSerial = TlsServiceIdentity(freshName)
		}
	}
	// gui52fix1：短号解析失败（hintSerial 空）或 identity 算不出时，先按 IP
	// 找已有档案（active 优先）——配对已建档设备绝不新建 IP:port 键双卡；
	// 仍无命中才保留旧兜底（新建 IP:port 键档案）。
	identity := adb.IdentityKey(marketname, manufacturer, model, hintSerial)
	if hintSerial == "" || identity == "" {
		if byIP := a.profiles.ResolveKeyByIP(ip); byIP != "" {
			identity = byIP
		}
	}
	if identity == "" {
		identity = adb.IdentityKey(marketname, manufacturer, model, ip+":"+connPort)
	}
	a.profiles.PairArchive(identity, hintSerial, ip+":"+connPort, guid, marketname, model)

	// ⑥ 结果：成功设备卡（含 TLS 形态）；待配对卡即时移除（下一轮 mDNS 重扫前不再出现）
	d := &adb.Device{
		Serial:       ip + ":" + connPort,
		State:        "device",
		ConnType:     "wifi",
		Name:         marketname,
		Marketname:   marketname,
		Manufacturer: manufacturer,
		Model:        model,
		Identity:     identity,
		Tls:          true,
		WirelessForm: ModeTls,
	}
	if d.Name == "" {
		d.Name = hintSerial
	}
	if d.Name == "" {
		d.Name = d.Serial
	}
	a.mu.Lock()
	if a.pair != nil {
		a.pair.Phase = PairPhaseSuccess
		// ③ 完成：标记 verify 阶段已插入的 done 步骤（幂等；不重复追加）
		if len(a.pair.Steps) > 2 && a.pair.Steps[2].Name == PairStepDone {
			a.pair.Steps[2].OK = true
		} else {
			a.pair.Steps = append(a.pair.Steps, PairStep{Name: PairStepDone, OK: true})
		}
		a.pair.Name, a.pair.Addr = d.Name, d.Serial
		a.pair.Device = d
	}
	removed := false
	for i := range a.pending {
		if a.pending[i].Key == devKey {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
			removed = true
			break
		}
	}
	a.mu.Unlock()
	if removed {
		bridge.DebugLog("[app] 配对完成：待配对卡 %s 已移除（%s 入档 mode=tls）", devKey, d.Serial)
	}

	// gui52-fix8：无线配对成功后开启设备 tcpip 5555（与插线学习一致）——仅开
	// 端口、不做 connect 探测；地址入档全权交给 mDNS（_adb._tcp 广播）。
	// 异步执行：成功态不阻塞、失败仅日志（幂等：先 getprop 检测已开即跳过）。
	go a.ensurePairTcpip5555(identity, ip, d.Serial)
	// gui52-fix14：配对遮罩——「连接中…」盖住 transport 波动窗口（adbd 重启/
	// 端口轮换），连续 device 满 2s 或 10s 兜底后退出并 probe 刷状态。
	a.pairShieldStart(identity, ip)
	// gui52-fix7：配对成功后的地址学习（TLS/5555 connect 探测）已整体移除——
	// 无线地址（TLS 随机端口与 5555）权威来源=mDNS 广播（设备自报），
	// 由 applyMdnsServiceAdded→MatchMdnsModes 即时写档 + 60s reconcile 兜底；
	// 配对瞬间的 connPort 在 HyperOS 轮换下会过时，connect 学习反而污染档案。
	// gui52-fix17：主动配对成功 = 重来——清该设备全部删除标记（同
	// clearDeletedUsbForSerial 语义的无线版；扫码/手动配对都走本函数）。
	a.clearDeletedForProfile(ip, hintSerial, guid)
}

// ensurePairTcpip5555 无线配对成功后开启设备 tcpip 5555（gui52-fix8/fix9）：
// ① getprop service.adb.tcp.port 检测：已 5555 → 幂等跳过，并直接写档 ip:5555 active
//    （gui52-fix9：mDNS 对稳定不变的 _adb._tcp 服务永不产生 added 事件——「IP 交给
//    mdns」对 5555 不成立，检测到已开即直写入档，关 TLS 时不再闪离线卡）；
// ② 未开 → adb tcpip 5555（2s 超时，失败仅日志，不阻断配对成功态），成功同写档；
// ③ 不做 connect 探测（地址活性后续由缺席验尸负责）。
func (a *App) ensurePairTcpip5555(profileKey, ip, serial string) {
	if a.pairOps.tcpipFn == nil || a.pairOps.getpropFn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	port, err := a.pairOps.getpropFn(ctx, serial, "service.adb.tcp.port")
	cancel()
	if err == nil && strings.TrimSpace(port) == "5555" {
		a.profiles.AddrSuccessMode(profileKey, ip+":5555", ModeTcpip)
		bridge.DebugLog("[app] 无线配对 tcpip 检测：%s 已开 5555（跳过，已入档 %s:5555 active）", serial, ip)
		return
	}
	bridge.DebugLog("[app] 无线配对 tcpip：%s -> 5555", serial)
	tctx, tcancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = a.pairOps.tcpipFn(tctx, serial, "5555")
	tcancel()
	if err != nil {
		bridge.DebugLog("[app] 无线配对 tcpip 失败（不阻断）：%s（%v）", serial, err)
		return
	}
	a.profiles.AddrSuccessMode(profileKey, ip+":5555", ModeTcpip)
	bridge.DebugLog("[app] 无线配对 tcpip 成功：%s 已开启 5555（已入档 %s:5555 active）", serial, ip)
}

// resolveTlsPortFresh 现场重扫一次，按 IP 解析 _adb-tls-connect 端口（二维码/
// 手动路线配对成功后的连接端口兜底）。
func (a *App) resolveTlsPortFresh(ip string) string {
	port, _ := a.resolveTlsServiceFresh(ip)
	return port
}

// resolveTlsServiceFresh 现场重扫一次，按 IP 解析 _adb-tls-connect 的
// 端口与服务实例名（gui52：实例名 adb-<短号>-XXXX 用于无线接入学习）。
func (a *App) resolveTlsServiceFresh(ip string) (port, name string) {
	if ip == "" || a.pairOps.mdnsScanFn == nil {
		return "", ""
	}
	svcs, err := a.pairOps.mdnsScanFn(context.Background(), mdnsTimeout)
	if err != nil {
		return "", ""
	}
	for _, s := range svcs {
		if s.Mode == discovery.MdnsModeTls && s.Addr != "" && strings.HasPrefix(s.Addr, ip+":") {
			return addrPort(s.Addr), s.Name
		}
	}
	return "", ""
}

// resolvePairPort 解析配对端口：按 tls 服务实例名（guid）/ IP 在 mDNS 配对服务里找。
// fresh=true 时现场重扫（配对界面刚打开、快照还没更新的场景；1.5s 截断）。
func (a *App) resolvePairPort(guid, ip string, fresh bool) string {
	var svcs []discovery.MdnsService
	if fresh {
		if a.pairOps.mdnsScanFn == nil || a.disc == nil {
			return ""
		}
		svcs, _ = a.pairOps.mdnsScanFn(context.Background(), mdnsTimeout)
	} else {
		svcs = a.mdnsSnapshot()
	}
	for _, s := range svcs {
		if s.Mode != discovery.MdnsModePairing || s.Addr == "" {
			continue
		}
		if guid != "" && s.Name == guid {
			return addrPort(s.Addr)
		}
		if ip != "" && strings.HasPrefix(s.Addr, ip+":") {
			return addrPort(s.Addr)
		}
	}
	return ""
}

// setPairFailed 落配对失败态（分类码 + 文案 + 步骤保留）。
func (a *App) setPairFailed(code, text, devKey, ip string) {
	a.mu.Lock()
	if a.pair == nil {
		a.pair = &PairStatus{Key: devKey, Addr: ip}
	}
	a.pair.Phase = PairPhaseFailed
	a.pair.ErrCode = code
	a.pair.ErrText = text
	if len(a.pair.Steps) == 0 {
		a.pair.Steps = []PairStep{{Name: PairStepPair}}
	}
	// gui50-fix3：二维码路线失败不清空二维码。缺失/过期/未带文本时立即重新生成，
	// 否则把现有 pairQR 的二维码文本回填到失败态，前端二维码区不空白。
	if a.pair.Mode == PairModeQR {
		if a.pairQR == nil || time.Now().After(a.pairQR.expireAt) || a.pair.QrText == "" {
			q := a.generatePairQRLocked()
			a.pairQR = q
			a.pair.QrText = q.qrText()
			a.pair.QrExpireAt = q.expireAt.Unix()
		} else {
			a.pair.QrText = a.pairQR.qrText()
			a.pair.QrExpireAt = a.pairQR.expireAt.Unix()
		}
	}
	a.mu.Unlock()
	bridge.DebugLog("[app] 配对失败（%s）：%s", code, text)
}

// isDigits 判定纯数字串（配对码 6 位数字）。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// --- 会话事件（按 serial 分发 + runner 身份防串） ---

// NotifyLine 是 bat 桥接的消费入口（串号版本；单测可直接调用）：
// 分类并把事件投递到对应会话。真实桥接经 NotifyLineFor（带 runner 身份）调用。
func (a *App) NotifyLine(serial, line string) {
	a.NotifyLineFor(serial, nil, line)
}

// NotifyLineFor 会话级输出行消费（带 runner 身份）：
// r 非 nil 时校验会话当前 runner 仍是 r——陈旧 runner 的迟到输出行直接丢弃，
// 不污染重启/替换后的新会话。
func (a *App) NotifyLineFor(serial string, r Runner, line string) {
	a.guard("NotifyLine", func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		st := a.sessions[serial]
		if st == nil || (r != nil && st.runner != r) {
			return
		}
		a.applyEventLocked(st, bridge.ClassifyLine(line))
	})
}

// pushErrorLocked 给会话落一条错误事件（异步启动失败路径；带 runner 身份防串）。
func (a *App) pushErrorLocked(serial string, r Runner, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.sessions[serial]
	if st == nil || (r != nil && st.runner != r) {
		return
	}
	a.applyEventLocked(st, bridge.Event{Kind: bridge.KindError, Text: text})
}

// applyEventLocked 把分类事件应用到指定会话（持锁内部版）：
// 日志缓冲（60 行截断）/模式判定/输入状态机/规格与纹理/阶段文案——全部只作用于
// 该会话自己的 cast，绝不串到其他会话。
func (a *App) applyEventLocked(st *sessionState, ev bridge.Event) {
	if !st.cast.Active && ev.Kind != bridge.KindNone {
		return
	}
	c := &st.cast
	// 任何新行（含未分类行）都刷新无输出计时并解除停滞提示
	st.lastLineAt = time.Now()
	c.Stalled = false
	c.StallSecs = 0
	c.Log = append(c.Log, ev.Text)
	if len(c.Log) > 60 {
		c.Log = c.Log[len(c.Log)-60:]
	}

	// 模式判定（修复误判）：只接受"真实模式行"（[高清]/[流畅]/[custom]/[学习] 有线/
	// 进入无线模式/无线模式尝试连接）携带的 Mode；"保存无线地址"等学习行 Mode 为空，
	// 不改变当前模式——语义 = 最近一次真实模式行。
	if ev.Mode != "" {
		c.Mode = ev.Mode
	}

	// 输入状态机：仅展示——解析到 prompt 显示菜单按钮（会话级操作，不写 stdin）
	if ev.Kind == bridge.KindPrompt {
		c.WaitingInput = true
		c.Prompt = ev.Prompt
	} else if ev.Kind != bridge.KindNone {
		// 任何新进展都视为已消费上一个 prompt
		c.WaitingInput = false
		c.Prompt = bridge.PromptNone
	}

	switch ev.Kind {
	case bridge.KindPrompt:
		// 保留 prompt，不覆盖 phase
	case bridge.KindSpec:
		// 新规格行到达即整体替换旧规格（有线/无线分支都从这里刷新）；
		// 同时记录当前投屏模式（参数浮窗按此自动定位有线/无线参数套）。
		// 切换完成的新分支规格 = 同一投屏进入新阶段，不是关闭：
		// 清 closing（自动切有线后「正在关闭…」立即复位；KindDone("已检测到
		// 窗口关闭")/KindUserClose 在切换前置的位由此解除）
		st.closing = false
		c.Spec = ev.Spec
		if ev.Spec != nil {
			if ev.Spec.Wired {
				c.Mode = "usb"
			} else {
				c.Mode = "wifi"
			}
			// bat 自动规格行 → 更新该设备该模式的动态默认值（[custom] 回显不更新）
			if !ev.Spec.Custom {
				a.updateBaseline(c.Serial, c.Mode, Baseline{Res: ev.Spec.MaxSize, FPS: ev.Spec.FPS, Bitrate: ev.Spec.Mbps})
			}
		}
	case bridge.KindTexture:
		// 真实纹理尺寸（scrcpy-server INFO: Texture: WxH）→ 徽标优先数据源：
		// 把当前规格的 Res 换成真实值（[custom] 长边档显示换算后的真实 WxH；
		// 有线档显示真实纹理）。规格未到达（c.Spec==nil）时忽略。
		if c.Spec != nil && ev.Texture != "" {
			c.Spec.Res = ev.Texture
		}
	case bridge.KindADBReset, bridge.KindDetect, bridge.KindSwitchUSB, bridge.KindReconnect:
		// 重连/插线切换/重新检测：旧规格作废，等 bat 回显当前分支的新规格行；
		// 期间前端显示"等待规格…"，不展示旧的（可能是有线分支的）数值。
		// 这些"重新检测/切换"事件 = bat 继续跑、同一投屏进入新阶段：
		// 切换/重连 = 投屏继续（同一会话），不是关闭 → 清 closing（自动切
		// 有线后「正在关闭…」立即复位，不再永久卡住）；
		// 顺带清 stopping——重连/切换也不该有停止态。
		st.closing = false
		st.stopping = false
		c.Spec = nil
		if txt, ok := phaseText(ev.Kind); ok {
			c.Phase, c.PhaseText = ev.Kind.String(), txt
		}
	case bridge.KindKeyboard:
		_, mode, _ := bridge.ParseKeyboard(ev.Text)
		c.KeyboardMode = mode
	case bridge.KindError:
		c.LastError = ev.Text
		c.Phase, c.PhaseText = ev.Kind.String(), "出错："+ev.Text
	case bridge.KindCasting:
		c.Phase, c.PhaseText = "casting", "投屏中"
	case bridge.KindDone:
		c.Phase, c.PhaseText = "done", "投屏已结束"
		// 仅"投屏窗口被点 X"这一种 Done 置 closing（bat 随后还有 :adb_cleanup +
		// ping ~1.2s 才退出——期间前端按钮显示"正在关闭…"）。
		// 其余"投屏已结束"行（如菜单退出路径的普通结束）不置，避免误报。
		if strings.Contains(ev.Text, "已检测到窗口关闭") {
			st.closing = true
		}
	case bridge.KindUserClose:
		// 哨兵行（SCRCPY_EZ_USER_CLOSE）：用户主动关闭投屏窗口的瞬间 scrcpy 打的
		// 标记——此刻 scrcpy 可能还没退出，立即置 closing（前端"正在关闭…"），
		// Active/Phase 不动（会话仍投屏中，bat 退出走 OnBatExitFor 统一复位）。
		// 与 KindDone("已检测到窗口关闭") 的置位幂等：哨兵先到、bat 行后到，都置 true 无冲突。
		st.closing = true
	case bridge.KindOfflineExit:
		c.Phase, c.PhaseText = "done", "投屏已结束"
	default:
		if txt, ok := phaseText(ev.Kind); ok {
			c.Phase, c.PhaseText = ev.Kind.String(), txt
		}
	}
}

// OnBatExit 是 bat 桥接进程退出时的回调（串号版本；单测可直接调用）。
func (a *App) OnBatExit(serial string, code int) {
	a.OnBatExitFor(serial, nil, code)
}

// OnBatExitFor 会话级退出回调（带 runner 身份）：
// 陈旧 runner 的退出回调不触碰替换后的新会话（重启/替换场景防串）。
// 所有退出分支统一进 done 结束态：主标题"投屏已结束"，副文按退出码区分。
func (a *App) OnBatExitFor(serial string, r Runner, code int) {
	a.guard("OnBatExit", func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		st := a.sessions[serial]
		if st == nil || st.runner == nil {
			return
		}
		if r != nil && st.runner != r {
			return // 陈旧 runner：会话已被替换，忽略
		}
		st.cast.ExitCode = code
		st.cast.Phase = "done"
		st.cast.PhaseText = "投屏已结束"
		if code == 0 {
			st.cast.ExitText = "窗口已关闭，投屏正常结束"
		} else {
			st.cast.ExitText = "bat 进程退出（退出码 " + itoa(code) + "）——可能因无线离线/异常，可在原始输出查看"
		}
		st.cast.Active = false
		st.cast.WaitingInput = false
		st.cast.Prompt = bridge.PromptNone
		st.cast.Stalled = false
		st.stopping = false // 停止状态由 bat 退出统一复位（"正在终止…" → 标签淡出）
		st.closing = false  // "正在关闭…"同样由 bat 退出统一复位
		st.runner = nil     // 允许该 serial 下次投屏
		st.endedAt = time.Now()
		// gui38：bat 退出（最后投屏会话退出时 :adb_cleanup kill-server）→
		// 立即恢复无线探测，不等下一轮 15s 节流，缩短离线窗口。
		// 锁内只发异步 goroutine：ForceDiscover 自取锁，不能在 mu 持有时直接调。
		// gui40：bat 退出 → stop 进程兜底 kill-server 还有 ~2s 才执行（实测
		// [exit] → [stop] adbserver）——立即 ForceDiscover 时设备仍显示在线、
		// 候选为空=空跑。延迟 2s 后设备已断，OfflineCandidateAddrs 才返回真实
		// 离线设备 → 探测真正起飞。
		go a.guard("post-cast-recover", func() {
			time.Sleep(2 * time.Second)
			a.ForceDiscover()
		})
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// phaseText 是事件 → 用户可读状态的映射（纯函数，单测覆盖）。
func phaseText(k bridge.Kind) (string, bool) {
	switch k {
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
	case bridge.KindWatchOn:
		return "无线投屏中 · USB 插线监测已开启", true
	case bridge.KindSwitchUSB:
		return "检测到 USB 插线，切换有线投屏…", true
	case bridge.KindReconnect:
		return "连接断开，自动重连中…", true
	default:
		return "", false
	}
}

// promptTick 每轮轮询调用：①各活动会话的"无输出提示"判定（只读展示架构，
// **不自动杀树**——杀树重跑是用户显式操作 RestartCast；bat 的 choice 自愈
// /t /d 与自动重连全自理，GUI 单控制源=bat，零干预）；②结束态会话 GC
// （淡出动画后由前端 ForgetSession 主动移除；超时兜底防泄漏）。
func (a *App) promptTick() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for _, st := range a.sessions {
		c := &st.cast
		if !c.Active || st.runner == nil {
			continue
		}
		// 投屏中（casting）/插线监测中（watch-on）bat 长时间静默是正常的，不做判定
		if c.Phase == "casting" || c.Phase == "watch-on" {
			if c.Stalled {
				c.Stalled = false
				c.StallSecs = 0
			}
			continue
		}
		stall := now.Sub(st.lastLineAt)
		if stall < stalledRestartWait {
			if c.Stalled {
				c.Stalled = false
				c.StallSecs = 0
			}
			continue
		}
		if !c.Stalled {
			c.Stalled = true
			bridge.DebugLog("[app] 无输出提示 serial=%s：%.0fs 无新输出（phase=%s），展示重启投屏提示",
				st.cast.Serial, stall.Seconds(), c.Phase)
		}
		c.StallSecs = int(stall.Seconds())
	}
	// 结束态会话 GC：非重启中、退出超过保留期 → 移除（正常路径由前端淡出后 ForgetSession）
	for serial, st := range a.sessions {
		if st.runner == nil && !st.restarting && now.Sub(st.endedAt) > deadSessionKeep {
			delete(a.sessions, serial)
			bridge.DebugLog("[app] 会话 %s 结束态超时清理", serial)
		}
	}
}

// --- 会话级操作（Stop / Restart / Reset / Forget） ---

// StopCast 停止指定会话（杀 bat 进程树）。幂等：会话不存在/已结束时静默成功。
// serial 可直查；卡片重键后按 identity 兜底定位。
// 状态反馈（"正在终止…"）：受理即置 stopping=true 并透传 Snapshot（前端按钮立即
// 变禁用态）；bat 退出后 OnBatExitFor 统一复位；Stop 报错复位（可重试）；
// Stop 成功但 bat 超时未退出 → stopResetTimeout 兜底复位（会话状态不变）。
// 性能（gui5 修复）：杀树链路（taskkill /F /T + powershell 残余枚举 +
// 兜底 kill-server，约 1-2s）在 goroutine 异步执行——webview RPC 回调在 GUI
// 主线程，同步执行会冻结消息循环（"点停止卡一下"）。受理即返回 nil，
// 前端靠快照轮询（~700ms）拿 stopping/报错复位，无需新推送机制。
func (a *App) StopCast(serial string) error {
	a.mu.Lock()
	st := a.findSessionLocked(serial)
	if st == nil || st.runner == nil {
		a.mu.Unlock()
		return nil
	}
	if st.stopping {
		a.mu.Unlock()
		return nil // 停止已受理：重复点击幂等（前端同步禁用按钮）
	}
	st.stopping = true
	r := st.runner
	key := st.cast.Serial // 会话键（identity 兜底定位时 serial ≠ key）
	a.mu.Unlock()

	// 异步段：杀树 + 错误复位/超时兜底（同步段只做微秒级状态操作）
	go a.guard("stop-cast", func() {
		if err := r.Stop(); err != nil {
			// 杀树失败（runner 报告错误）：复位按钮可重试；会话状态不变（现行为）
			a.mu.Lock()
			if cur := a.sessions[key]; cur == st {
				cur.stopping = false
			}
			a.mu.Unlock()
			bridge.DebugLog("[app] StopCast 失败 serial=%s: %v（stopping 已复位，按钮可重试）", key, err)
			return
		}
		// Stop 成功 → 等 onExit 复位 stopping；兜底：bat 超时未退出 → 恢复可重试
		go a.guard("stop-cast-timeout", func() { a.resetStoppingTimeout(key, st) })
	})
	return nil
}

// ShouldKillServerOnExit gui53：GUI 退出统一清理判定（gui46 起保留方法名/签名）。
// 语义修正：GUI 退出前 main 已调用 a.Close()（同步停掉全部投屏会话，runner.Stop
// 杀树完成）——退出即"无活动会话"，原「有活动会话跳过 kill-server」的判定在
// 退出路径下永远命中（sessions 里的 runner 在 Stop 后仍非 nil），导致
// kill-server 成死代码 → adb server 残留（2026-09-02 清理审计发现）。
// 返回 (adbPath, ok)：ok=true 表示应清理；真正的 exec 由 UI/main 层执行
//（Windows 隐藏窗口属性在 UI 层，app 包保持跨平台干净）。
func (a *App) ShouldKillServerOnExit() (string, bool) {
	bridge.DebugLog("[app] GUI 退出：清理 adb server（投屏会话已全停）")
	return a.cfg.AdbPath, true
}

// resetStoppingTimeout 停止兜底复位（一次性定时，非常驻轮询）：
// 仅当会话仍是同一指针、仍在 stopping 且 runner 未释放（bat 确实没退）才复位。
func (a *App) resetStoppingTimeout(serial string, st *sessionState) {
	d := stopResetDuration() // 入口快照取值：后续 Sleep/DebugLog 复用同一快照
	time.Sleep(d)
	a.mu.Lock()
	defer a.mu.Unlock()
	cur := a.sessions[serial]
	if cur == st && cur.stopping && cur.runner != nil {
		cur.stopping = false
		bridge.DebugLog("[app] 停止超时 serial=%s：bat %.0fs 未退出，按钮恢复可重试（会话状态不变）",
			serial, d.Seconds())
	}
}

// RestartCast 会话级操作（用户显式点击，按会话）：杀树 → 等待退出 → 重新 StartCast
// （bat 新会话走 :main 自动检测/connect，重新检测与配对向导都走此入口；
// 退出走 StopCast 不重启）。重复点击幂等（restarting 闩锁）。
// 结束态（bat 已退出、runner 为空但保留 serial）同样可用：直接开新会话。
// 无会话（串号从未投屏）→ 等价 StartCast 开新会话（前端防御路径）。
// 性能（gui5）：杀树同样异步——Stop 内含 powershell 残余枚举（1-2s），
// 同步执行会冻结 GUI 消息循环（"保存并重新投屏"按钮卡顿）；restarting 闩锁
// 在同步段已置位，前端"重启中"语义不变。
func (a *App) RestartCast(serial string) error {
	// 来源观测（21:29 偶发重投）：入口即留痕（调用链区分桥调用/自动路径）
	bridge.DebugLog("[app] RestartCast 进入 serial=%q 调用链=%s", serial, callerChain(1))
	a.mu.Lock()
	st := a.sessions[serial]
	if st == nil {
		a.mu.Unlock()
		return a.StartCast(serial)
	}
	if st.restarting {
		a.mu.Unlock()
		return nil // 幂等：重启进行中
	}
	r := st.runner
	st.restarting = true
	a.mu.Unlock()
	if r == nil {
		// 结束态重新投屏：无进程可杀，直接以同一设备开新会话
		bridge.DebugLog("[app] 结束态重新投屏 serial=%s", serial)
		if err := a.StartCast(serial); err != nil {
			a.mu.Lock()
			if cur := a.sessions[serial]; cur != nil {
				cur.restarting = false
			}
			a.mu.Unlock()
			return err
		}
		return nil
	}
	bridge.DebugLog("[app] 用户重启投屏 serial=%s：杀树后重跑新会话", serial)
	go a.guard("restart-cast", func() {
		_ = r.Stop() // 异步杀树（不再占 RPC/GUI 主线程）
		a.restartAfterExit(serial)
	})
	return nil
}

// GetProfile 返回指定设备的参数记忆（有线/无线两套独立；未配置=默认档）。
// 含动态 baseline：已投屏过用 [高清]/[流畅] 实际值；未投屏过用 adb 检测值推导。
func (a *App) GetProfile(serial string) DeviceProfile {
	return a.effectiveProfile(serial)
}

// effectiveProfile 计算设备参数（动态默认值）：
//   - Baseline：已保存（bat 规格行更新）用之；否则有线=设备 adb 检测值（res 长边/Hz）
//   - 默认码率 60M，无线=bat 固定档 1920/60/15M；
//   - 未自定义时 Res/FPS/Bitrate 跟随 baseline（浮窗默认显示与重置都回这里）。
func (a *App) effectiveProfile(serial string) DeviceProfile {
	p := a.profiles.Get(serial)

	a.mu.RLock()
	devices := append([]adb.Device{}, a.devices...)
	a.mu.RUnlock()

	if p.Usb.Baseline.Res == 0 {
		long, fps := 2560, 120
		for _, d := range devices {
			if d.Serial == serial {
				if v := ParseLongEdge(d.Res); v > 0 {
					long = v
				}
				if d.FPS > 0 {
					fps = d.FPS
				}
				break
			}
		}
		p.Usb.Baseline = Baseline{Res: long, FPS: fps, Bitrate: 60}
	}
	if p.Wifi.Baseline.Res == 0 {
		p.Wifi.Baseline = Baseline{Res: 1920, FPS: 60, Bitrate: 15}
	}
	if !p.Usb.Custom {
		p.Usb.Res, p.Usb.FPS, p.Usb.Bitrate = p.Usb.Baseline.Res, p.Usb.Baseline.FPS, p.Usb.Baseline.Bitrate
	}
	if !p.Wifi.Custom {
		p.Wifi.Res, p.Wifi.FPS, p.Wifi.Bitrate = p.Wifi.Baseline.Res, p.Wifi.Baseline.FPS, p.Wifi.Baseline.Bitrate
	}
	return p
}

// updateBaseline 在 [高清]/[流畅] 规格行到达时更新该设备该模式的 bat 基准。
// [custom] 回显行（Spec.Custom=true）不更新基准。
func (a *App) updateBaseline(serial, mode string, b Baseline) {
	if serial == "" || b.Res <= 0 || b.FPS <= 0 || b.Bitrate <= 0 {
		return
	}
	p := a.profiles.Get(serial)
	if mode == "wifi" {
		p.Wifi.Baseline = b
	} else {
		p.Usb.Baseline = b
	}
	_ = a.profiles.Save(serial, p)
}

// SaveProfile 设备参数管理页保存：仅写入该设备该模式的参数档（profiles.json），
// 不重启投屏（投屏前调参；下次投屏生效）。与 SaveProfileAndRestart 共用校验与写入。
func (a *App) SaveProfile(serial, mode string, res, fps, bitrate int, custom bool) error {
	if err := a.saveProfileOnly(serial, mode, res, fps, bitrate, custom); err != nil {
		return err
	}
	bridge.DebugLog("[app] 保存参数（不重投） serial=%s mode=%s res=%d fps=%d bitrate=%d custom=%v",
		serial, mode, res, fps, bitrate, custom)
	return nil
}

// saveProfileOnly 校验并写入参数档（基线随保存持久化）。
func (a *App) saveProfileOnly(serial, mode string, res, fps, bitrate int, custom bool) error {
	if err := validateProfileParams(res, fps, bitrate); err != nil {
		return err
	}
	p := a.effectiveProfile(serial) // 保留/推导 baseline（动态默认值随保存持久化）
	mp := ModeProfile{Res: res, FPS: fps, Bitrate: bitrate, Custom: custom}
	if mode == "wifi" {
		mp.Baseline = p.Wifi.Baseline
		p.Wifi = mp
	} else {
		mp.Baseline = p.Usb.Baseline
		p.Usb = mp
	}
	return a.profiles.Save(serial, p)
}

// SaveProfileAndRestart 参数浮窗保存：写入该设备该模式的参数档，
// custom=true 时以该模式覆盖参数（SCEZ_*_USB/SCEZ_*_WIFI 对应套）重启该会话；
// false=自动档重启（另一模式仍按记忆独立注入）。参数覆盖按 serial 存放，
// 多会话互不串（每个会话的下一次 StartCast 只消费自己的覆盖）。
func (a *App) SaveProfileAndRestart(serial, mode string, res, fps, bitrate int, custom bool) error {
	if err := a.saveProfileOnly(serial, mode, res, fps, bitrate, custom); err != nil {
		return err
	}
	a.mu.Lock()
	if custom {
		mp := bridge.ModeParams{Res: res, FPS: fps, Bitrate: bitrate, Set: true}
		if mode == "wifi" {
			a.nextParams[serial] = bridge.CastParams{Wifi: mp}
		} else {
			a.nextParams[serial] = bridge.CastParams{Usb: mp}
		}
	} else {
		delete(a.nextParams, serial)
	}
	a.mu.Unlock()
	bridge.DebugLog("[app] 保存参数并重新投屏 serial=%s mode=%s res=%d fps=%d bitrate=%d custom=%v",
		serial, mode, res, fps, bitrate, custom)
	return a.RestartCast(serial)
}

// restartAfterExit 等待该会话 runner 释放（waitLoop→OnBatExit）后重新投屏。
func (a *App) restartAfterExit(serial string) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		st := a.sessions[serial]
		idle := st == nil || st.runner == nil
		a.mu.Unlock()
		if idle {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	a.mu.Lock()
	st := a.sessions[serial]
	idle := st == nil || st.runner == nil
	a.mu.Unlock()
	if !idle {
		bridge.DebugLog("[app] 重启失败 serial=%s：bat 15s 未退出", serial)
		a.mu.Lock()
		if st := a.sessions[serial]; st != nil {
			st.restarting = false
			st.cast.Log = append(st.cast.Log, "[GUI] 重启失败：bat 未退出")
		}
		a.mu.Unlock()
		return
	}
	bridge.DebugLog("[app] 重启：bat 已退出，重新投屏 serial=%s", serial)
	if err := a.StartCast(serial); err != nil {
		a.mu.Lock()
		if st := a.sessions[serial]; st != nil {
			st.restarting = false
			st.cast.Log = append(st.cast.Log, "[GUI] 重启失败："+err.Error())
		}
		a.mu.Unlock()
	}
}

// ResetCast 清空全部结束态会话（兼容旧单会话"返回设备列表"路径；
// 多会话正常路径由前端标签淡出后调用 ForgetSession 移除）。
// 投屏仍在运行的会话（runner 非空）不做任何事，防止误清运行中状态。
func (a *App) ResetCast() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for serial, st := range a.sessions {
		if st.runner == nil {
			delete(a.sessions, serial)
		}
	}
}

// ForgetSession 前端标签淡出动画完成后的回调：移除该结束态会话条目。
// 会话仍在运行（runner 非空）时不移除（重启复活场景）。
func (a *App) ForgetSession(serial string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st, ok := a.sessions[serial]; ok && st.runner == nil {
		delete(a.sessions, serial)
		bridge.DebugLog("[app] 会话 %s 标签淡出完成，已移除", serial)
	}
}

// --- 标签点击 → 投屏窗口浮前（问题 3，浏览器范式） ---

// frontCandidateSerials 计算本会话 scrcpy 进程的 --serial 匹配候选（纯函数）：
// 会话键 serial + 档案中该 identity 的全部 serials/全部 addrs（档案是权威——
// 会话键可能过期/卡片重键，scrcpy 实际 --serial 是档案中某历史 serial 或无线
// 地址，如平板会话键 a743e1df（USB）而 scrcpy 以无线 192.168.31.162:5555 运行）
// + 目标卡 Serial/Wireless + 同 identity 在线卡的 Serial/Wireless。
// idOf 注入 identity 解析（App 用 a.identityOf）；entryOf 注入档案查询
// （App 用 a.profiles.Entry；测试用固定表）。
func frontCandidateSerials(serial string, devs []adb.Device, idOf func(*adb.Device) string,
	entryOf func(string) (DeviceEntry, bool)) []string {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return nil // 空串号：不匹配任何卡片（防止空 Wireless 字段被误配）
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	addEntry := func(e DeviceEntry) {
		for _, s := range e.Serials {
			add(s)
		}
		for i := range e.Addrs {
			add(e.Addrs[i].Addr)
		}
	}
	add(serial)
	// 档案权威补全（v3 修复"标签点击浮前找不到 scrcpy 进程"）：
	// 会话键（旧 USB serial）不在当前卡片上时，档案的历史 serials + addrs
	// 仍能命中 scrcpy 实际命令行（无线地址直连场景）。
	if e, ok := entryOf(serial); ok {
		addEntry(e)
	}
	for i := range devs {
		d := &devs[i]
		if d.Serial != serial && d.Wireless != serial {
			continue
		}
		add(d.Serial)
		add(d.Wireless)
		id := idOf(d)
		if id == "" {
			break
		}
		if e, ok := entryOf(id); ok {
			addEntry(e)
		}
		for j := range devs {
			if idOf(&devs[j]) == id {
				add(devs[j].Serial)
				add(devs[j].Wireless)
			}
		}
		break
	}
	return out
}

// BringCastToFront 把本会话 scrcpy 投屏窗口提到 z-order 前面（不夺 ez GUI 焦点）：
// 枚举 scrcpy 进程 → 按本会话候选 serial 匹配 pid → EnumWindows 定位窗口 →
// 最小化先 SW_RESTORE → SetWindowPos(HWND_TOP, SWP_NOACTIVATE|SWP_SHOWWINDOW)。
// 非 Windows 平台空实现；失败不阻断前端标签切换（前端 fire-and-forget）。
func (a *App) BringCastToFront(serial string) error {
	a.mu.RLock()
	devs := append([]adb.Device{}, a.devices...)
	a.mu.RUnlock()
	cands := frontCandidateSerials(serial, devs, a.identityOf, a.profiles.Entry)
	bridge.DebugLog("[app] bring-to-front serial=%s（候选 %v）", serial, cands)
	return bridge.BringCastToFront(cands)
}

// Close 释放轮询与全部会话资源。
func (a *App) Close() {
	a.mu.Lock()
	cancel := a.cancel
	runners := make([]Runner, 0, len(a.sessions))
	for _, st := range a.sessions {
		if st.runner != nil {
			runners = append(runners, st.runner)
		}
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, r := range runners {
		_ = r.Stop()
	}
}
