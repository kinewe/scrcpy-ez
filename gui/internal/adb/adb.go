// Package adb 封装 adb.exe 调用：设备发现与属性查询（市场名/型号/电量/分辨率/刷新率）。
// 解析函数均为纯函数，可在任意平台单测；命令执行用 os/exec（跨平台）。
package adb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Device 是设备列表中的一个条目。
// 同一台设备的 USB 与无线 transport 合并为一栏：Serial/ConnType 取 USB 优先，
// Wireless 保存配对的无线地址（IP:port）；仅无线时 Serial 即 IP:port、Wireless 为空。
// Res/FPS 是设备原生参数（wm size/peak_refresh_rate），统一大数字在前（宽≥高，
// 不做方向检测——主人拍板）；WirelessRes 是按 bat 无线档（--max-size 1920）
// 等比换算的无线投屏分辨率（同样宽≥高），仅无线卡片展示（无线固定 60fps）。
type Device struct {
	Serial       string `json:"serial"`
	State        string `json:"state"`    // device / unauthorized / offline
	ConnType     string `json:"connType"` // usb / wifi / other
	Name         string `json:"name"`     // 展示名：市场名，回退 厂商+型号，再回退序列号
	Model        string `json:"model"`
	Marketname   string `json:"marketname"`   // ro.product.marketname（设备档案 identity 主键来源）
	Manufacturer string `json:"manufacturer"` // ro.product.manufacturer（identity 回退来源）
	Identity     string `json:"identity"`     // 设备档案 identity（IdentityKey 规则）
	Wireless     string `json:"wireless"`     // 同设备无线地址（USB+无线并存时），否则为空
	WirelessIP   string `json:"wirelessIP"`   // 前端副行显示用无线地址（档案 active 排序取；无 active 为空）
	WirelessRes  string `json:"wirelessRes"`  // 无线投屏分辨率（长边 1920 等比换算），未知为空
	Battery      int    `json:"battery"`      // 0 表示未知
	Res          string `json:"res"`          // 原生分辨率，宽≥高（大数字在前），未知为空
	FPS          int    `json:"fps"`          // 0 表示未知
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
}

type cachedSpec struct {
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
	adbPath    string
	configPath string
	mu         sync.Mutex
	cache      map[string]*cachedSpec

	connectMu   sync.Mutex
	lastConnect time.Time
	connectFn   func(ctx context.Context, addr string) error                     // 测试注入
	pairFn      func(ctx context.Context, ip, port, code string) (string, error) // 测试注入
	getpropFn   func(ctx context.Context, serial, prop string) (string, error)   // 测试注入
	tcpipFn     func(ctx context.Context, serial, port string) error             // 测试注入
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
// 同 model 的 USB/无线 transport 合并（USB 优先展示与查询，无线地址并入 Wireless）；
// 无 model 字段的条目按市场名二次合并；并为在线设备填充展示名/型号/电量/分辨率。
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
	groups := GroupDevices(raw)
	devs := make([]Device, 0, len(groups))
	idxByName := map[string]int{} // 无 model 条目按市场名二次合并
	for _, g := range groups {
		d := m.buildDevice(ctx, g)
		hasModel := g[0].Model != ""
		if !hasModel && d.State == "device" && d.Name != "" && d.Name != d.Serial {
			if j, ok := idxByName[d.Name]; ok {
				mergeDevice(&devs[j], d)
				continue
			}
			idxByName[d.Name] = len(devs)
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

// mergeDevice 把 b 并入 a（无 model 条目按市场名二次合并）：
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
	m.mu.Lock()
	c, ok := m.cache[d.Serial]
	if !ok {
		c = &cachedSpec{}
		m.cache[d.Serial] = c
	}
	m.mu.Unlock()

	now := time.Now()
	if c.name != "" {
		// 市场名补采（多会话竞态自愈）：首次 getprop 失败用 man+model 兜底缓存后，
		// 周期性重试 marketname——不重试则 identity 永久停在回退值（如 "Xiaomi
		// 24117RK2CC" 而非 "REDMI K80"），档案分裂、弹窗误判"新设备"随之而来。
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

// IdentityKey 计算设备档案 identity（设备唯一化规则）：
// marketname 非空优先；无市场名 → manufacturer+model（两者均非空）；
// 都无 → 首个 serial（含无线 IP:port）。同一设备的 USB/无线 transport 由此归并。
func IdentityKey(marketname, manufacturer, model, serial string) string {
	if t := strings.TrimSpace(marketname); t != "" {
		return t
	}
	man, mod := strings.TrimSpace(manufacturer), strings.TrimSpace(model)
	if man != "" && mod != "" {
		return man + " " + mod
	}
	return strings.TrimSpace(serial)
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
// 先归一化为宽≥高，长边（宽）>1920 时等比缩到 1920（短边整数截断，与 scrcpy 的
// 等比缩放一致），输出始终宽≥高（大数字在前）。解析失败返回 ""。
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
	Model    string // adb devices -l 的 model: 字段；空=未知（按市场名二次合并）
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

// GroupDevices 按 model 分组去重（一台设备一栏的前提）：
// model 相同的条目（同设备的 USB/无线 transport）归一组，保持首次出现顺序；
// 无 model 条目各自成组，由 List 在市场名富化后二次合并。
func GroupDevices(raw []RawDevice) [][]RawDevice {
	idx := map[string]int{}
	var groups [][]RawDevice
	for _, r := range raw {
		key := r.Model
		if key == "" {
			key = "serial:" + r.Serial
		}
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
