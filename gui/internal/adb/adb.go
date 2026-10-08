// Package adb 封装 adb.exe 调用：设备发现与属性查询（市场名/型号/电量/分辨率/刷新率）。
// 解析函数均为纯函数，可在任意平台单测；命令执行用 os/exec（跨平台）。
package adb

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"scrcpy-ez/gui/internal/deviceevents"
)

// Device 是设备列表中的一个条目。
// 同一台设备的 USB 与无线 transport 合并为一栏：Serial/ConnType 取 USB 优先，
// Wireless 保存配对的无线地址（IP:port）；仅无线时 Serial 即 IP:port、Wireless 为空。
// Res/FPS 是设备原生参数（wm size/peak_refresh_rate），统一大数字在前（宽≥高，
// 不做方向检测——主人拍板）；WirelessRes 是按 bat 无线档（--max-size 1920）
// 等比换算的无线投屏分辨率（同样宽≥高），仅无线卡片展示（无线固定 60fps）。
type Device struct {
	Serial        string `json:"serial"`
	State         string `json:"state"`    // device / unauthorized / offline
	ConnType      string `json:"connType"` // usb / wifi / other
	Name          string `json:"name"`     // 展示名：市场名，回退 厂商+型号，再回退序列号
	Model         string `json:"model"`
	Marketname    string `json:"marketname"`              // ro.product.marketname（展示信息）
	Manufacturer  string `json:"manufacturer"`            // ro.product.manufacturer（展示信息）
	Identity      string `json:"identity"`                // 设备档案 identity（IdentityKey 规则）
	IdentityEpoch uint64 `json:"identityEpoch,omitempty"` // current online connection token for provisional operations
	StableSerial  string `json:"stableSerial,omitempty"`  // 已确认完整短号；空值不会清除档案身份
	Wireless      string `json:"wireless"`                // 同设备无线地址（USB+无线并存时），否则为空
	WirelessIP    string `json:"wirelessIP"`              // 前端副行显示用无线地址（档案 active 排序取；无 active 为空）
	WirelessRes   string `json:"wirelessRes"`             // 无线投屏分辨率（长边 1920 等比换算），未知为空
	Battery       int    `json:"battery"`                 // 0 表示未知
	Res           string `json:"res"`                     // 原生分辨率，宽≥高（大数字在前），未知为空
	FPS           int    `json:"fps"`                     // 0 表示未知
	// Tls = 该设备有 TLS 无线形态可用（档案 mode=tls 地址 / mDNS tls 服务在播）。
	// WirelessForm = 档案记录的最新无线形态（tls/tcpip/空），前端"已入档"副行标注用。
	Tls          bool   `json:"tls"`
	WirelessForm string `json:"wirelessForm"`
	// Connecting = 「连接中…」按钮态（gui34b）：插线学习（adb tcpip 5555）后
	// 8s 遮罩窗口内 app.shieldUsbLearning 合成的「USB 连接中」卡为 true
	// （前端投屏按钮禁用 + 「连接中…」文案）；真实 USB/无线/离线卡恒 false
	// （omitempty：false 不进 JSON，前端缺省即原样「投屏」，回归零影响）。
	Connecting bool `json:"connecting,omitempty"`
	// Pairing = 配对遮罩合成卡标记（gui52-fix14）：配对成功 → shieldPairing 合成
	// 的「连接中…」遮罩卡为 true；前端据此区分拔线遮罩（wifi+connecting→断开中…）
	// 与配对遮罩（wifi+connecting+pairing→连接中…）。
	Pairing bool `json:"pairing,omitempty"`
	// AppBusy = 该设备完全无图标缓存、正在首次读取。按不可变 identity 填充，
	// 完成或固定 8s 后解除；普通列表检测/差分补图不禁用入口。
	AppBusy bool `json:"appBusy,omitempty"`
}

type cachedSpec struct {
	mu           sync.Mutex // refresh and track can enrich the same device concurrently
	identity     string
	name         string
	marketname   string
	manufacturer string
	model        string
	at           time.Time
	battery      int
	res          string
	fps          int
	specAt       time.Time
	batAt        time.Time
}

// Manager 缓存每台设备的重查询结果（市场名永不过期，电量/规格带 TTL），
// 并在设备列表为空时按 config.txt 记忆地址尝试无线自恢复（30s 节流）。
type Manager struct {
	eventOnce        sync.Once
	eventHub         *deviceevents.Hub
	identityResolver func(string) (string, string)
	adbPath          string
	configPath       string
	mu               sync.Mutex
	cache            map[string]*cachedSpec

	connectMu   sync.Mutex
	lastConnect time.Time
	connectFn   func(ctx context.Context, addr string) error                     // 测试注入
	pairFn      func(ctx context.Context, ip, port, code string) (string, error) // 测试注入
	getpropFn   func(ctx context.Context, serial, prop string) (string, error)   // 测试注入
	runFn       func(context.Context, ...string) (string, error)                 // Property-refresh tests; nil uses adb.
	tcpipFn     func(ctx context.Context, serial, port string) error             // 测试注入
	getSerialFn func(ctx context.Context, serial string) (string, error)         // 测试注入

	// v2.1.77 抢庄三件套注入点（nil=真实命令；测试注入 fake 覆盖编排——
	// 缺省路径与生产完全一致）。
	srvKillFn  func(ctx context.Context) error
	srvStartFn func(ctx context.Context) error
	srvCheckFn func(ctx context.Context) string
}

func (m *Manager) EventHub() *deviceevents.Hub {
	m.eventOnce.Do(func() { m.eventHub = deviceevents.NewLearningHub() })
	return m.eventHub
}

const (
	batteryTTL   = 15 * time.Second
	specTTL      = 60 * time.Second
	connectTTL   = 30 * time.Second // 无线自恢复 connect 的最小间隔
	connectLimit = 5 * time.Second  // 单次 connect 超时
)

func New(adbPath, configPath string) *Manager {
	return &Manager{adbPath: adbPath, configPath: configPath, cache: map[string]*cachedSpec{}}
}

func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	if m.runFn != nil {
		return m.runFn(ctx, args...)
	}
	c := exec.CommandContext(ctx, m.adbPath, args...)
	HideConsole(c) // Windows: 禁止弹控制台窗口；其他平台空实现
	out, err := c.Output()
	return string(out), err
}

// Connect 执行 adb connect（无线自恢复用）。结果由下一轮 devices 轮询验证。
func (m *Manager) Connect(ctx context.Context, addr string) error {
	if m.connectFn != nil {
		return m.connectFn(ctx, addr)
	}
	c := exec.CommandContext(ctx, m.adbPath, "connect", addr)
	HideConsole(c)
	_, err := c.Output()
	return err
}

// Pair 执行 `adb pair ip:port code`（无线调试配对，唯一控制源=人工输码）。
// 返回 adb 输出供上层按文本分类（成功 "Successfully paired to ..."）。
func (m *Manager) Pair(ctx context.Context, ip, port, code string) (string, error) {
	if m.pairFn != nil {
		return m.pairFn(ctx, ip, port, code)
	}
	c := exec.CommandContext(ctx, m.adbPath, "pair", ip+":"+port, code)
	HideConsole(c)
	out, err := c.Output()
	return string(out), err
}

// Getprop 查询设备属性（shell getprop），供配对接入后的身份/市场名验证。
func (m *Manager) Getprop(ctx context.Context, serial, prop string) (string, error) {
	if m.getpropFn != nil {
		return m.getpropFn(ctx, serial, prop)
	}
	out, err := m.run(ctx, "-s", serial, "shell", "getprop", prop)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// GetSerialNo 读设备序列号（`adb -s <serial> get-serialno`，host 侧命令）。
// 这是"设备是谁"的最硬来源（gui55 配对学习/探测验身）：不依赖设备端 mDNS
// 广播、不依赖 shell 属性可读性，只要 transport 通就能拿到。未知时 adb 输出
// "unknown" → 归一为空串。serial 参数可以是 USB 序列号，也可以是 ip:port。
func (m *Manager) GetSerialNo(ctx context.Context, serial string) (string, error) {
	if m.getSerialFn != nil {
		return m.getSerialFn(ctx, serial)
	}
	out, err := m.run(ctx, "-s", serial, "get-serialno")
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(out)
	if s == "unknown" {
		return "", nil
	}
	return s, nil
}

// Tcpip 在设备上开启 TCP/IP 调试端口（`adb -s <serial> tcpip <port>`）。
// gui30「插线即学习」用：设备 adbd 重启后 service.adb.tcp.port 重置，
// 插 USB 时补学一次 5555，拔线后无线立即可用。注意 adb tcpip 会重启
// 设备端 adbd（USB 短暂断开 1-2s），调用方（GUI 轮询）按瞬态自愈处理。
func (m *Manager) Tcpip(ctx context.Context, serial, port string) error {
	if m.tcpipFn != nil {
		return m.tcpipFn(ctx, serial, port)
	}
	c := exec.CommandContext(ctx, m.adbPath, "-s", serial, "tcpip", port)
	HideConsole(c)
	_, err := c.Output()
	return err
}

// PairErrKind 分类 adb pair 失败原因（纯函数，供错误分类码映射）：
// "code"=配对码不正确或已过期；"port"=配对端口无效（配对界面可能已关闭）；
// "addr"=IP/端口格式非法；"timeout"=超时；""=成功；其他=通用失败。
// adb 官方输出：成功行以 "Successfully paired to <host>" 开头；
// 码错 "Failed: Wrong password or connection was dropped."；
// 端口不通 "Failed: Unable to start pairing client."；地址非法 "Failed to parse address ..."。
// 注意 "Failed: Successfully paired but server returned unknown response=..." 也含
// "Successfully paired" 字样——成功判定必须用行首匹配，不能 Contains。
func PairErrKind(out string) string {
	s := strings.TrimSpace(out)
	switch {
	case strings.HasPrefix(s, "Successfully paired to "):
		return ""
	case strings.Contains(s, "Wrong password"):
		return "code"
	case strings.Contains(s, "Unable to start pairing client"):
		return "port"
	case strings.Contains(s, "parse address"):
		return "addr"
	default:
		return "other"
	}
}

// --- v2.1.77：5037 抢庄（旧版 adb server 抢占时夺回，确保 37 坐庄） ---
//
// 背景：别的软件自带的旧版 adb（≤30.x，如 Iriun Webcam 29.0.1）开机自启抢占 5037
// 后，37 client 的 mdns/pair 等新服务被应答 "unknown host service"——原生 TLS
// （无线调试）永远开不上来。方案（主人拍板）：检测归属 → 不完整才抢庄。
// 先例：flect 的 "Restart ADB" 按钮（kill-server），生态已证明该动作正当；
// ez 只是自动化 + 条件化 + 带快速重试保护（压空窗、循环重试、抢后天然保庄）。

var (
	// takeoverRounds：抢庄最大轮数（拍板：3 轮——单轮失败率 <1%，3 轮全败 ≈ 百万分之一）。
	takeoverRounds = 3
	// takeoverTriesPerRound：每轮内快速重试次数（把空窗压缩至百毫秒级）。
	takeoverTriesPerRound = 6
	// takeoverRetryGap：轮内两次重试的间隔（拍板常数：~150ms）。
	takeoverRetryGap = 150 * time.Millisecond
)

// runCombined 执行 adb 命令并返回 stdout+stderr 合并输出。
// stderr 文本是检测判据（unknown host service）的关键来源（旧实现只捕 stdout 会丢）。
func (m *Manager) runCombined(ctx context.Context, args ...string) (string, error) {
	c := exec.CommandContext(ctx, m.adbPath, args...)
	HideConsole(c)
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	return buf.String(), err
}

// CheckServer 执行 `adb mdns check`（默认 5037）并返回合并输出，供归属判据使用。
// 该命令是 server 的"版本指纹"探针：5037 无 server 时命令会自动 fork 一个本版（37）
// server → 输出正常版本串 → 自动判"完整"（交给 37 坐庄）。
func (m *Manager) CheckServer(ctx context.Context) string {
	if m.srvCheckFn != nil {
		return m.srvCheckFn(ctx)
	}
	out, _ := m.runCombined(ctx, "mdns", "check")
	return out
}

// ServerHealthy 是 5037 归属判据（纯函数）：输出含 "unknown host service" → false，
// 即 5037 上坐着 ≤30.x 的旧版 server（37 client 的 mdns 服务名它不认识）；
// 其它任何应答（37 版本串 [adb discovery 0.0.0] / 31–36 的 mdns daemon unavailable /
// mdns discovery disabled）= true——pair/TLS 不依赖 mDNS 功能，31–36 算"完整"；
// 空输出（命令没跑起来）判 true：保守不动。
func ServerHealthy(out string) bool {
	return !strings.Contains(out, "unknown host service")
}

// ServerIs37 是"37 坐庄"判据（纯函数，抢庄验证的唯一判据）：
// mdns check 输出含 "adb discovery" → true（实测 37 输出
// "mdns daemon version [adb discovery 0.0.0]"，只有 37 的 mdns daemon 报这个版本串）。
func ServerIs37(out string) bool {
	return strings.Contains(out, "adb discovery")
}

// KillServer 执行 `adb kill-server`（抢庄动作：清掉当前坐庄者，含旧版）。
func (m *Manager) KillServer(ctx context.Context) error {
	if m.srvKillFn != nil {
		return m.srvKillFn(ctx)
	}
	_, err := m.runCombined(ctx, "kill-server")
	return err
}

// StartServer 执行 `adb start-server`（抢庄动作：fork 本版 37；5037 已有 server
// 时只复用——被抢回的判定交给随后的 CheckServer 验证）。
func (m *Manager) StartServer(ctx context.Context) error {
	if m.srvStartFn != nil {
		return m.srvStartFn(ctx)
	}
	_, err := m.runCombined(ctx, "start-server")
	return err
}

// TakeoverServer 抢庄（v2.1.77 核心算法）：把 5037 从"不完整 server（≤29）"手中
// 夺回并确保 37 坐庄。算法（拍板）：kill-server → 快速重试 start-server（~150ms/次）
// → 验证 37 指纹（mdns check 含 "adb discovery"）→ 最多 3 轮。
// 返回最后一次 mdns check 输出与错误（成功=nil；全败/超时=非 nil，调用方记日志）。
// 只在"归属检测判不完整"时调用——完整（≥30）环境零动作、绝不走到这里。
// 投屏在场也不跳过（跳过 = 原生 TLS 永远开不上来；投屏短暂断流由 bat 自愈重连兜底）。
func (m *Manager) TakeoverServer(ctx context.Context) (string, error) {
	var last string
	for round := 0; round < takeoverRounds; round++ {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		// 清庄：踢掉当前坐庄者（含旧版）。失败不阻断（server 可能已空/半死）。
		_ = m.KillServer(ctx)
		for i := 0; i < takeoverTriesPerRound; i++ {
			if err := ctx.Err(); err != nil {
				return last, err
			}
			// 抢跑：fork 我们的 37（若 5037 已有对手坐庄，start 只是复用——由验证判出）。
			_ = m.StartServer(ctx)
			last = m.CheckServer(ctx)
			if ServerIs37(last) {
				return last, nil
			}
			// 被对手抢回（unknown host service）→ 本轮放弃，下一轮重 kill 再抢。
			if !ServerHealthy(last) {
				break
			}
			select {
			case <-time.After(takeoverRetryGap):
			case <-ctx.Done():
				return last, ctx.Err()
			}
		}
	}
	return last, errors.New("抢庄失败：未能在 5037 上取得 37 坐庄")
}

// RecoverIfEmpty 在设备列表为空时尝试按 config.txt 记忆地址 connect 一次。
// 节流：无论成败，两次尝试至少间隔 connectTTL（30s）。
// 返回 true 表示本次发起了 connect（结果需下一轮轮询验证）。
func (m *Manager) RecoverIfEmpty(ctx context.Context) bool {
	m.connectMu.Lock()
	if time.Since(m.lastConnect) < connectTTL {
		m.connectMu.Unlock()
		return false
	}
	m.lastConnect = time.Now()
	m.connectMu.Unlock()

	addr := ReadConfigAddr(m.configPath)
	if addr == "" && m.configPath != "" {
		// 回退共享 config（与 bat 一致：本目录优先，..\..\config.txt 兜底）
		addr = ReadConfigAddr(filepath.Join(
			filepath.Dir(filepath.Dir(filepath.Dir(m.configPath))), "config.txt"))
	}
	if addr == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, connectLimit)
	defer cancel()
	_ = m.Connect(cctx, addr)
	return true
}

// ReadConfigAddr 读取 config.txt 记忆的无线地址（IP:port），
// 文件缺失/内容非法返回 ""。bat 写入格式为单行地址+CRLF。
func ReadConfigAddr(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if !reIPSerial.MatchString(s) {
		return ""
	}
	return s
}

// Transports 返回 adb devices -l 的原始 transport 列表（不做合并、不做富化）。
// 合并列表（List/buildDevice）同设备多无线条目只保留一个（TLS 端口与 5555
// 并存时会丢一个），投屏会话的「实际连接」判定必须用原始列表。
// 供 app 层 gui43 会话实时 TLS 判定。
func (m *Manager) Transports(ctx context.Context) ([]RawDevice, error) {
	out, err := m.run(ctx, "devices", "-l")
	if err != nil {
		return nil, err
	}
	return ParseDevicesL(out), nil
}

// Shell 执行设备端 shell 命令（adb -s <serial> shell <args...>），返回原始输出。
func (m *Manager) Shell(ctx context.Context, serial string, args ...string) (string, error) {
	cmdArgs := append([]string{"-s", serial, "shell"}, args...)
	return m.run(ctx, cmdArgs...)
}

// List 返回当前 adb devices -l 的合并列表（一台设备一栏）：
// 只合并已确认同身份的 USB/无线 transport（USB 优先展示与查询）；
// 并为在线设备填充展示名/型号/电量/分辨率。
func (m *Manager) List(ctx context.Context) ([]Device, error) {
	out, err := m.run(ctx, "devices", "-l")
	if err != nil {
		return nil, err
	}
	return m.devicesFromOutput(ctx, out), nil
}

// devicesFromOutput 从 `adb devices -l` 文本输出构建合并设备列表。
// track-devices 的列表块与 devices -l 同格式，track 解析复用同一套构建逻辑。
func (m *Manager) devicesFromOutput(ctx context.Context, out string) []Device {
	raw := ParseDevicesL(out)
	// Group only confirmed transports; model/name grouping loses same-model phones.
	idx := map[string]int{}
	groups := make([][]RawDevice, 0, len(raw))
	for _, r := range raw {
		key := IdentityKey("", "", "", r.Serial)
		if m.identityResolver != nil {
			if confirmed, _ := m.identityResolver(r.Serial); confirmed != "" {
				key = confirmed
			}
		}
		if j, ok := idx[key]; ok {
			groups[j] = append(groups[j], r)
		} else {
			idx[key] = len(groups)
			groups = append(groups, []RawDevice{r})
		}
	}
	devs := make([]Device, 0, len(groups))
	for _, g := range groups {
		d := m.buildDevice(ctx, g)
		d.StableSerial = StableSerial(d.Serial)
		if m.identityResolver != nil {
			if key, stable := m.identityResolver(d.Serial); key != "" {
				d.Identity, d.StableSerial = key, stable
			}
		}
		devs = append(devs, d)
	}
	return devs
}

// buildDevice 把一组同 model（或单个无 model）的 transport 条目合并为一台设备。
// 主 transport 优先 USB（getprop/电量/规格从 USB 查，有线更稳定）；无 USB 才用无线。
// 非 device 状态不触发 enrich（避免对离线设备跑 adb）。
func (m *Manager) buildDevice(ctx context.Context, g []RawDevice) Device {
	d := BuildDevice(g)
	if d.State == "device" {
		m.enrich(ctx, &d)
	}
	return d
}

// BuildDevice 是 buildDevice 的纯函数部分（transport 选择与 Wireless 并入），
// 不执行任何 adb 查询；供 track-devices 解析纯函数单测复用。
func BuildDevice(g []RawDevice) Device {
	var usb, wifi *RawDevice
	for i := range g {
		switch g[i].ConnType {
		case "usb":
			if usb == nil {
				usb = &g[i]
			}
		case "wifi":
			if wifi == nil || (g[i].State == "device" && wifi.State != "device") {
				wifi = &g[i]
			}
		}
	}
	var d Device
	switch {
	case usb != nil && usb.State == "device":
		d = Device{Serial: usb.Serial, State: usb.State, ConnType: "usb", Name: usb.Serial}
	case wifi != nil && wifi.State == "device":
		d = Device{Serial: wifi.Serial, State: wifi.State, ConnType: "wifi", Name: wifi.Serial}
	case usb != nil:
		d = Device{Serial: usb.Serial, State: usb.State, ConnType: "usb", Name: usb.Serial}
	default:
		d = Device{Serial: g[0].Serial, State: g[0].State, ConnType: g[0].ConnType, Name: g[0].Serial}
	}
	// USB 为主 transport 时把同设备无线地址并入副行信息
	if d.ConnType == "usb" && wifi != nil && wifi.Serial != d.Serial {
		d.Wireless = wifi.Serial
	}
	// 纯解析器也把 model 字段带上（enrich 在真实 List 路径会进一步补全）
	if d.Model == "" && g[0].Model != "" {
		d.Model = g[0].Model
	}
	return d
}

// mergeDevice 把已确认同身份的 b 并入 a：
// USB 优先作主 transport；无线地址并入 Wireless。
func mergeDevice(a *Device, b Device) {
	if b.ConnType == "usb" && a.ConnType != "usb" {
		*a, b = b, *a
	}
	if a.Wireless == "" {
		if b.ConnType == "wifi" && b.Serial != a.Serial {
			a.Wireless = b.Serial
		} else if b.Wireless != "" {
			a.Wireless = b.Wireless
		}
	}
}

func (m *Manager) enrich(ctx context.Context, d *Device) {
	identity := IdentityKey("", "", "", d.Serial)
	if m.identityResolver != nil {
		if confirmed, _ := m.identityResolver(d.Serial); confirmed != "" {
			identity = confirmed
		} else if strings.HasPrefix(identity, "pending:") {
			identity = "" // a missing broadcast must not repeatedly invalidate a known owner
		}
	}
	m.mu.Lock()
	c, ok := m.cache[d.Serial]
	if !ok || identity != "" && c.identity != identity {
		c = &cachedSpec{identity: identity}
		m.cache[d.Serial] = c
		d.Name = "" // failed enrichment of a new owner must not retain an old display name
	}
	m.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.name != "" {
		// 市场名补采（多会话竞态自愈）：首次 getprop 失败用 man+model 兜底缓存后，
		// 周期性重试 marketname，让展示名恢复；身份不依赖该字段。
		if c.marketname == "" && now.Sub(c.at) >= specTTL {
			if v, err := m.run(ctx, "-s", d.Serial, "shell", "getprop", "ro.product.marketname"); err == nil {
				if n := strings.TrimSpace(v); n != "" {
					c.name = n
					c.marketname = n
					c.at = now
				}
			}
		}
		d.Name, d.Model = c.name, c.model
	} else {
		// 市场名优先；空回退 厂商+型号；再失败保持序列号
		if v, err := m.run(ctx, "-s", d.Serial, "shell", "getprop", "ro.product.marketname"); err == nil {
			if n := strings.TrimSpace(v); n != "" {
				c.name = n
				c.marketname = n
			}
		}
		if c.name == "" {
			man, _ := m.run(ctx, "-s", d.Serial, "shell", "getprop", "ro.product.manufacturer")
			mod, _ := m.run(ctx, "-s", d.Serial, "shell", "getprop", "ro.product.model")
			man, mod = strings.TrimSpace(man), strings.TrimSpace(mod)
			c.model = mod
			c.manufacturer = man
			if man != "" && mod != "" {
				c.name = man + " " + mod
			}
		}
		if c.model == "" {
			c.model, _ = m.run(ctx, "-s", d.Serial, "shell", "getprop", "ro.product.model")
			c.model = strings.TrimSpace(c.model)
		}
		c.at = now
		if c.name != "" {
			d.Name = c.name
		}
		d.Model = c.model
	}
	d.Marketname = c.marketname
	d.Manufacturer = c.manufacturer
	d.Identity = IdentityKey(c.marketname, c.manufacturer, c.model, d.Serial)

	if now.Sub(c.batAt) > batteryTTL {
		if v, err := m.run(ctx, "-s", d.Serial, "shell", "dumpsys", "battery"); err == nil {
			c.battery = ParseBatteryLevel(v)
		}
		c.batAt = now
	}
	d.Battery = c.battery

	if now.Sub(c.specAt) > specTTL {
		if v, err := m.run(ctx, "-s", d.Serial, "shell", "wm", "size"); err == nil {
			if w, h, ok := ParseDisplaySize(v); ok {
				c.res = strconv.Itoa(w) + "x" + strconv.Itoa(h)
			}
		}
		if v, err := m.run(ctx, "-s", d.Serial, "shell", "settings", "get", "system", "peak_refresh_rate"); err == nil {
			c.fps = ParsePeakRefresh(v)
		}
		c.specAt = now
	}
	// 分辨率统一大数字在前（宽≥高），不做方向检测（主人拍板：减少不兼容）
	d.Res = SortWideFirst(c.res)
	d.FPS = c.fps
	d.WirelessRes = ScaleForWireless(c.res)
}

// SortWideFirst 把 "WxH" 归一化为宽≥高顺序（大数字在前）。解析失败原样返回。
func SortWideFirst(res string) string {
	wStr, hStr, ok := strings.Cut(res, "x")
	if !ok {
		return res
	}
	w, err1 := strconv.Atoi(wStr)
	h, err2 := strconv.Atoi(hStr)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return res
	}
	if w >= h {
		return res
	}
	return hStr + "x" + wStr
}

// ScaleForWireless 按 bat 无线档（WIFI_ARGS 的 --max-size 1920）换算无线投屏分辨率：
// 先归一化为宽≥高，长边（宽）>1920 时等比缩到 1920（短边整数截断 **后向下取偶**——
// 编码尺寸偶对齐，与 scrcpy 实际输出一致：2136x3200 长边 1920 实得 1920x1280），
// 输出始终宽≥高（大数字在前）。解析失败返回 ""。
func ScaleForWireless(res string) string {
	wStr, hStr, ok := strings.Cut(res, "x")
	if !ok {
		return ""
	}
	w, err1 := strconv.Atoi(wStr)
	h, err2 := strconv.Atoi(hStr)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return ""
	}
	if h > w {
		w, h = h, w
	}
	if w > 1920 {
		h = h * 1920 / w
		w = 1920
	}
	// 短边向下取偶（编码尺寸偶对齐；scrcpy 实得 1280 而非截断值 1281）。
	if h > 2 {
		h &^= 1
	}
	return strconv.Itoa(w) + "x" + strconv.Itoa(h)
}

// --- 纯解析函数（单测覆盖） ---

var (
	reDevLine  = regexp.MustCompile(`^(\S+)\s+(\S+)\s*$`)
	reBattery  = regexp.MustCompile(`(?m)^\s*level:\s*(\d+)`)
	rePhysSize = regexp.MustCompile(`(?m)(?:Physical|Override) size:\s*(\d+)x(\d+)`)
	reIPSerial = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}:\d+$`)
)

type RawDevice struct {
	Serial   string
	State    string
	ConnType string
	Model    string // adb devices -l 的 model: 展示字段；空=未知
}

// connTypeOf 判定 serial 的 transport 类型。
// USB = serial 无冒号且不含 _adb-tls（与 bat 的判定规则一致）；
// 无线 = IP:port 条目；mDNS/emulator 归 other。
func connTypeOf(serial string) string {
	switch {
	case reIPSerial.MatchString(serial):
		return "wifi"
	case strings.HasPrefix(serial, "emulator") || strings.Contains(serial, "_adb-tls"):
		return "other"
	case !strings.Contains(serial, ":"):
		return "usb"
	default:
		return "other"
	}
}

// ParseDevices 解析 `adb devices` 输出（无 -l 字段的旧格式）。
func ParseDevices(output string) []RawDevice {
	var out []RawDevice
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "List of devices") || line == "" {
			continue
		}
		m := reDevLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		serial, state := m[1], m[2]
		out = append(out, RawDevice{Serial: serial, State: state, ConnType: connTypeOf(serial)})
	}
	return out
}

// ParseDevicesL 解析 `adb devices -l` 输出。
// 行格式：<serial> <state> [key:value ...]（model: 字段用于同设备多 transport 去重）。
func ParseDevicesL(output string) []RawDevice {
	var out []RawDevice
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "List" {
			continue
		}
		serial, state := fields[0], fields[1]
		model := ""
		for _, f := range fields[2:] {
			if v, ok := strings.CutPrefix(f, "model:"); ok {
				model = v
			}
		}
		out = append(out, RawDevice{Serial: serial, State: state, ConnType: connTypeOf(serial), Model: model})
	}
	return out
}

// GroupDevices 按完整序列号分组，保持首次出现顺序。
// 未确认身份的无线地址各自成组，市场名和型号不参与分组。
func GroupDevices(raw []RawDevice) [][]RawDevice {
	idx := map[string]int{}
	var groups [][]RawDevice
	for _, r := range raw {
		key := IdentityKey("", "", "", r.Serial)
		if i, ok := idx[key]; ok {
			groups[i] = append(groups[i], r)
			continue
		}
		idx[key] = len(groups)
		groups = append(groups, []RawDevice{r})
	}
	return groups
}

// ParseBatteryLevel 解析 `dumpsys battery` 的 level 行，失败返回 0。
func ParseBatteryLevel(output string) int {
	m := reBattery.FindStringSubmatch(output)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// ParseDisplaySize 解析 `wm size` 的 Physical/Override size，失败 ok=false。
func ParseDisplaySize(output string) (w, h int, ok bool) {
	m := rePhysSize.FindStringSubmatch(output)
	if m == nil {
		return 0, 0, false
	}
	w, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	h, err = strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, false
	}
	return w, h, true
}

// ParsePeakRefresh 解析 `settings get system peak_refresh_rate`（"120" / "120.0" / "null"），失败返回 0。
func ParsePeakRefresh(output string) int {
	s := strings.TrimSpace(output)
	if s == "" || s == "null" || s == "NULL" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(f)
}
