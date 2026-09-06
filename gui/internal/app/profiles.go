package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/discovery"
)

// Baseline 是"bat 基准"（该设备该模式 bat 实际检测/使用的规格三元组）：
// 有线的 [高清] 行（如平板 2560/120/80M）、无线的 [流畅] 行（1920/60/15M）。
// 参数浮窗的默认显示与重置都回这里（动态默认值）。
type Baseline struct {
	Res     int `json:"res"`
	FPS     int `json:"fps"`
	Bitrate int `json:"bitrate"`
}

// ModeProfile 是单设备单模式（usb/wifi）的参数档。Custom=false 表示自动档
// （Res/FPS/Bitrate 保存默认展示值，不注入 bat）。
type ModeProfile struct {
	Res      int      `json:"res"`
	FPS      int      `json:"fps"`
	Bitrate  int      `json:"bitrate"`
	Custom   bool     `json:"custom"`
	Baseline Baseline `json:"baseline"`
}

// DeviceProfile 是一台设备的有线/无线两套独立参数。
type DeviceProfile struct {
	Usb  ModeProfile `json:"usb"`
	Wifi ModeProfile `json:"wifi"`
}

// DefaultProfile 返回某设备的默认档（= 该模式 bat 自动值：
// 有线 2560/120/60M，无线 1920/60/15M）。Baseline 由 App 在投屏后
// 以 [高清]/[流畅] 实际值更新；未投屏过时用设备 adb 信息推导。
func DefaultProfile() DeviceProfile {
	return DeviceProfile{
		Usb:  ModeProfile{Res: 2560, FPS: 120, Bitrate: 60},
		Wifi: ModeProfile{Res: 1920, FPS: 60, Bitrate: 15},
	}
}

// ParseLongEdge 解析 "WxH"（宽≥高）取长边数值；解析失败返回 0。
func ParseLongEdge(res string) int {
	wStr, hStr, ok := strings.Cut(res, "x")
	if !ok {
		return 0
	}
	w, err1 := strconv.Atoi(wStr)
	h, err2 := strconv.Atoi(hStr)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0
	}
	if w >= h {
		return w
	}
	return h
}

// 档位序列（大→左降序；＋自定义固定最右）
var (
	ResTiers = []int{2560, 1920, 1080, 720}
	FpsTiers = []int{120, 90, 60, 30}
	BitTiers = []int{120, 80, 60, 40, 15}
)

// LadderSeq 计算自定义值 x 插入后的档位序列（阶梯逻辑，主人定稿规则）：
//   - x ≥ 上限（seq[0]）→ 替换上限：seq = [x] + seq[1:]（级数不变）；
//   - x < 上限 → 上级档忽略：seq = [x] + [t for t in seq if t < x]（级数变少）。
//
// 例：fps 80 → [80,60,30]；200 → [200,90,60,30]（120 被替换，级数不变）；
// 码率 55 → [55,40,15]；150 → [150,80,60,40,15]。
func LadderSeq(std []int, x int) []int {
	if len(std) == 0 || x <= 0 {
		return []int{x}
	}
	if x >= std[0] {
		out := make([]int, 0, len(std)+1)
		out = append(out, x)
		out = append(out, std[1:]...)
		return out
	}
	out := []int{x}
	for _, t := range std {
		if t < x {
			out = append(out, t)
		}
	}
	return out
}

// --- 设备档案（identity 唯一化，多设备 Phase 1 轮 A） ---

const (
	AddrStateActive = "active"
	AddrStateStale  = "stale"
	// AddrStateHistory 是 gui52 之前的旧值，仅作兼容别名（旧代码/旧测试的
	// 引用与 state=="history" 一样落入 stale 语义）。新代码一律写二值。
	AddrStateHistory = AddrStateStale
)

// 无线形态（gui12）：tls=无线调试加密连接（随机端口）；tcpip=经典明文 5555。
// 旧档案无字段 → 视为 tcpip（向后兼容）；同一设备可两形态并存（tls 优先连接）。
const (
	ModeTls   = "tls"
	ModeTcpip = "tcpip"
)

// failThrottleWindow 是 gui27 失败节流窗口：地址 lastFail 距今 < 60s → 本轮
// 节流跳过（防自动轮询每轮刷失败）；达到/超过即无条件恢复参与。
// 没有"累计失败"概念——失败只记时间戳，永不拉黑、永不失忆。
const failThrottleWindow = 60 * time.Second

// addrThrottled 判定地址当前是否处于失败节流期内（lastFail=0=未失败过 → 从不节流）。
func addrThrottled(lastFail int64, now time.Time) bool {
	return lastFail > 0 && now.Unix()-lastFail < int64(failThrottleWindow/time.Second)
}

// AddrEntry 是设备档案中的一条无线地址记录。
// gui52 二态模型：state 是唯一落盘的可用性状态——active（当前可用）/
// stale（确认失效），二者互斥、无中间态。一个地址一个 state，完事。
//
//   - 成功（接入学习/探测成功/广播再现）→ state=active；
//   - 失败（探测失败/广播缺席/拔线确认）→ state=stale。
//
// Mode 记录该地址的无线形态（tls/tcpip；空=未知，按 tcpip 兼容处理）。
// Fail/LastOk/LastFail/Stale 是 gui52 之前的旧统计/打标字段：全部 json:"-"
// 不落盘，仅供进程内排序、失败节流（60s）与旧测试兼容使用——任何显示、
// 候选与判据不得再读 Stale/Fail；节流只读内存态 LastFail。
type AddrEntry struct {
	Addr  string `json:"addr"`
	State string `json:"state"` // active / stale（唯一落盘状态，二值互斥）
	Mode  string `json:"mode,omitempty"`

	// --- gui52：以下字段均为内存态（json:"-"），永不写入 profiles.json ---
	Fail     int   `json:"-"`
	LastOk   int64 `json:"-"` // 最近成功时间（内存态，排序用）
	LastFail int64 `json:"-"` // 最近失败时间（内存态，60s 节流判据）
	Stale    bool  `json:"-"` // 兼容旧代码/旧测试的冗余打标；与 State 同步维护
}

// DeviceEntry 是 identity 唯一化后的设备档案。
// identity 规则（adb.IdentityKey）：marketname 非空优先；无市场名 →
// manufacturer+model；都无 → 首个 serial。同一设备的 USB serials 累积、
// 无线 addrs 追加（IP 变化不分裂设备）。
// Wireless=最新无线形态（tls/tcpip/空）；TlsGuid=mDNS tls 连接服务实例名
// （adb-<serial>-XXXXXX，persist.adb.wifi.guid 稳定不变——端口变化后仍能匹配本机）。
type DeviceEntry struct {
	Marketname   string `json:"marketname,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	// DisplayName/DisplayNameSet 是 gui51 用户自定义名称（批量改名）：
	// Set=true 且非空 → 名称链优先显示 DisplayName；Set=false → 原算法链。
	DisplayName    string        `json:"displayName,omitempty"`
	DisplayNameSet bool          `json:"displayNameSet,omitempty"`
	Res            string        `json:"res,omitempty"` // 设备原生分辨率（宽≥高，轮询富化后持久化；徽标比例换算的会话外兜底）
	Serials        []string      `json:"serials"`
	Addrs          []AddrEntry   `json:"addrs"`
	Wireless       string        `json:"wireless,omitempty"` // 最新无线形态：tls / tcpip / 空
	TlsGuid        string        `json:"tlsGuid,omitempty"`  // mDNS tls 连接服务实例名（端口变化仍可匹配）
	Profiles       DeviceProfile `json:"profiles"`
}

// storeData 是 profiles.json 的新结构：{"devices": {identity: DeviceEntry}}。
// 旧结构 {serial: {usb,wifi}} 由 Load 检测并迁移（identity=serial 回退键），
// 设备上线后由 SyncDevices 按 marketname 重键归并；旧结构不再落盘。
type storeData struct {
	Devices map[string]*DeviceEntry `json:"devices"`
	// DeviceOrder 设备卡自定义顺序（identity 键表；gui45 后端持久化——
	// WebView2 环境 localStorage 不可用（about:blank opaque origin））。
	DeviceOrder []string `json:"deviceOrder,omitempty"`
}

// legacyStoreData/legacyDeviceEntry/legacyAddrEntry 是 gui52 之前的档案形状：
// Load 用它们把旧 JSON 里的 fail/lastOk/lastFail/stale 读出来，迁移成二态
// （state 只保留 active/stale）后立即以新形状落盘。迁移后这些统计字段不再入
// 内存（进程内需要节流时由新运行路径自己写入内存态字段）。
type legacyStoreData struct {
	Devices     map[string]*legacyDeviceEntry `json:"devices"`
	DeviceOrder []string                      `json:"deviceOrder,omitempty"`
}

type legacyDeviceEntry struct {
	Marketname     string            `json:"marketname,omitempty"`
	Manufacturer   string            `json:"manufacturer,omitempty"`
	Model          string            `json:"model,omitempty"`
	DisplayName    string            `json:"displayName,omitempty"`
	DisplayNameSet bool              `json:"displayNameSet,omitempty"`
	Res            string            `json:"res,omitempty"`
	Serials        []string          `json:"serials"`
	Addrs          []legacyAddrEntry `json:"addrs"`
	Wireless       string            `json:"wireless,omitempty"`
	TlsGuid        string            `json:"tlsGuid,omitempty"`
	Profiles       DeviceProfile     `json:"profiles"`
}

type legacyAddrEntry struct {
	Addr     string `json:"addr"`
	State    string `json:"state"`
	Fail     int    `json:"fail"`
	LastOk   int64  `json:"lastOk"`
	LastFail int64  `json:"lastFail,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Stale    bool   `json:"stale,omitempty"`
}

// ProfileStore 持久化设备档案（identity → 设备档案）。
// 默认路径 %APPDATA%\scrcpy-ez\profiles.json（Windows；os.UserConfigDir），
// 可用 SCEZ_PROFILES_PATH 覆盖；path 为空时内存模式（不落盘，测试用）。
type ProfileStore struct {
	path string
	mu   sync.Mutex
	data storeData
}

func NewProfileStore(path string) *ProfileStore {
	return &ProfileStore{path: path, data: storeData{Devices: map[string]*DeviceEntry{}}}
}

// Load 读盘；文件缺失/损坏时返回空（全部设备=自动档），不报错。
// gui52 迁移：
//   - 旧结构（无 "devices" 键）自动迁移为新结构：identity=旧 key（serial 回退键）；
//   - 新结构但 addr 条目仍带 fail/lastOk/lastFail/stale/history 的旧档案 →
//     每条地址归约成 state ∈ {active, stale} 二值，旧统计字段清除；
//   - 旧字段此后永不写入（AddrEntry 相应字段 json:"-"）。
//
// gui52fix1：清理「IP:port 键」孤儿档案（配对瞬间端口、会话已断产生的空壳）——
// 同 IP 存在主档案（active 优先）时并入主档案并删除孤儿键；无主档案保留不动。
func (s *ProfileStore) Load() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	converted := false
	var ld legacyStoreData
	if err := json.Unmarshal(b, &ld); err == nil && ld.Devices != nil {
		// gui52：新结构统一经 legacy 形状读入（旧字段读得进来），再转二态。
		s.data.Devices = map[string]*DeviceEntry{}
		for k, le := range ld.Devices {
			s.data.Devices[k] = legacyDeviceToEntry(le)
		}
		s.data.DeviceOrder = append([]string{}, ld.DeviceOrder...)
		converted = s.migrateLegacyAddrsLocked()
	} else {
		// 旧结构 {serial: {usb,wifi}}：迁移为 identity=serial 的档案（无设备信息，
		// 按回退规则建键；上线后 SyncDevices 重键为 marketname 并归并）
		var legacy map[string]DeviceProfile
		if err := json.Unmarshal(b, &legacy); err == nil && legacy != nil {
			for key, p := range legacy {
				s.data.Devices[key] = entryFromKey(key, p)
			}
			converted = true
		}
	}
	if s.data.Devices == nil {
		s.data.Devices = map[string]*DeviceEntry{}
	}
	// gui14 档案自愈：旧路径入档的无线地址 mode 可能缺失（None）——按环境事实
	// 一次性补写形态（幂等）：port != 5555 → tls；port == 5555 → tcpip。
	// gui52：normalize 同时把任何非二态 state 收敛为 stale，并按形态单记忆折叠。
	normalized := s.normalizeLocked()
	// gui52fix1：孤儿 IP:port 键档案并入同 IP 主档案（active 优先）并删除孤儿键。
	cleaned := s.cleanOrphanIPPortLocked("Load")
	if converted || normalized || cleaned {
		_ = s.persistLocked() // 旧结构删除/形态自愈/二态迁移/孤儿清理：立即落盘
	}
	return nil
}

// legacyDeviceToEntry 把旧形状档案条目转成新条目（字段名一致，addrs 类型不同）。
func legacyDeviceToEntry(le *legacyDeviceEntry) *DeviceEntry {
	if le == nil {
		return &DeviceEntry{Serials: []string{}, Addrs: []AddrEntry{}, Profiles: DefaultProfile()}
	}
	e := &DeviceEntry{
		Marketname:     le.Marketname,
		Manufacturer:   le.Manufacturer,
		Model:          le.Model,
		DisplayName:    le.DisplayName,
		DisplayNameSet: le.DisplayNameSet,
		Res:            le.Res,
		Serials:        append([]string{}, le.Serials...),
		Wireless:       le.Wireless,
		TlsGuid:        le.TlsGuid,
		Profiles:       le.Profiles,
		Addrs:          make([]AddrEntry, 0, len(le.Addrs)),
	}
	for _, a := range le.Addrs {
		ae := AddrEntry{
			Addr:     a.Addr,
			Mode:     a.Mode,
			Fail:     a.Fail,
			LastOk:   a.LastOk,
			LastFail: a.LastFail,
			Stale:    a.Stale,
		}
		if legacyAddrStale(a) {
			ae.State = AddrStateStale
		} else {
			ae.State = AddrStateActive
		}
		e.Addrs = append(e.Addrs, ae)
	}
	return e
}

// legacyAddrStale 判定旧条目是否应迁移为 stale：
//   - 显式 stale:true → stale；
//   - state=="history" / 其它非 active 值 → stale；
//   - 从未成功过（lastOk==0）但有失败记录 → stale；
//   - 其余（state=="active" 且无 stale 标）→ active。
func legacyAddrStale(a legacyAddrEntry) bool {
	if a.Stale {
		return true
	}
	switch a.State {
	case AddrStateActive:
		return false
	case "", "history":
		return a.LastOk == 0 && a.LastFail > 0 || a.State == "history"
	default:
		return true
	}
}

// migrateLegacyAddrsLocked 把旧多字段 addrs 折叠成 gui52 二态单记忆：
// 每个形态（tls/tcpip）至多保留一条——优先保留旧 active（按 lastOk 最新）；
// 无 active 时保留旧 stale/history/失败条目（按 lastOk 最新，其次 lastFail 最新），
// 其余丢弃。迁移后旧统计字段清空（旧统计不入内存）。调用方持锁；返回是否有改动。
func (s *ProfileStore) migrateLegacyAddrsLocked() bool {
	changed := false
	for _, e := range s.data.Devices {
		bestActive := map[string]int{}
		bestStale := map[string]int{}
		for i := range e.Addrs {
			a := e.Addrs[i]
			class := addrEntryClass(a)
			if a.State == AddrStateActive {
				if idx, ok := bestActive[class]; !ok || betterAddr(a, e.Addrs[idx]) {
					bestActive[class] = i
				}
			} else if idx, ok := bestStale[class]; !ok || betterAddr(a, e.Addrs[idx]) {
				bestStale[class] = i
			}
		}
		out := make([]AddrEntry, 0, 2)
		for _, class := range []string{ModeTls, ModeTcpip} {
			if idx, ok := bestActive[class]; ok {
				a := e.Addrs[idx]
				if a.Fail != 0 || a.LastOk != 0 || a.LastFail != 0 || a.Stale || a.State != AddrStateActive {
					changed = true
				}
				a.State = AddrStateActive
				a.Fail, a.LastOk, a.LastFail, a.Stale = 0, 0, 0, false
				out = append(out, a)
			} else if idx, ok := bestStale[class]; ok {
				a := e.Addrs[idx]
				if a.Fail != 0 || a.LastOk != 0 || a.LastFail != 0 || a.Stale || a.State != AddrStateStale {
					changed = true
				}
				a.State = AddrStateStale
				a.Fail, a.LastOk, a.LastFail, a.Stale = 0, 0, 0, false
				out = append(out, a)
			}
		}
		if len(out) != len(e.Addrs) {
			changed = true
		}
		e.Addrs = out
	}
	return changed
}

// betterAddr 旧字段折叠排序：优先 lastOk 更大；lastOk 相等时（尤其失败-only 条目）
// 取 lastFail 更新的条目（信息最新）。
func betterAddr(a, b AddrEntry) bool {
	if a.LastOk != b.LastOk {
		return a.LastOk > b.LastOk
	}
	return a.LastFail > b.LastFail
}

// entryFromKey 按回退键（serial 或 IP:port）构造档案。
func entryFromKey(key string, p DeviceProfile) *DeviceEntry {
	e := &DeviceEntry{Profiles: p, Serials: []string{}, Addrs: []AddrEntry{}}
	if IsIPPort(key) {
		e.Addrs = append(e.Addrs, AddrEntry{Addr: key, State: AddrStateActive})
	} else {
		e.Serials = append(e.Serials, key)
	}
	return e
}

// IsIPPort 判定字符串是否为 IP:port 形式（无线地址）。
var reIPPort = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}:\d+$`)

func IsIPPort(s string) bool {
	return reIPPort.MatchString(s)
}

// isTlsFormAddr 判定无线地址是否为 TLS 形态（gui14 环境事实启发式）：
// 本系统无线 adb 只有两种形态——tcpip 明文 5555（固定端口）与无线调试
// TLS（随机端口）。字符串形如 ip:port（ipv4）且端口解析后 != 5555 → true；
// 非 ip:port 形式一律 false（服务名/序列号/无端口字符串绝不误判）。
func isTlsFormAddr(addr string) bool {
	if !IsIPPort(addr) {
		return false
	}
	i := strings.LastIndexByte(addr, ':')
	port, err := strconv.Atoi(addr[i+1:])
	if err != nil || port <= 0 {
		return false
	}
	return port != 5555
}

// normalizeLocked 档案形态自愈（gui14）+ gui52 二态单记忆清理：
// ① addrs 里 mode 为空（旧路径入档未写 mode / mDNS 发现未落形态）的无线地址，
//
//	按环境事实补写形态——本系统无线 adb 只有两种形态：tcpip 明文 5555（固定
//	端口）与无线调试 TLS（随机端口），因此 port != 5555 → tls；port == 5555
//	→ tcpip。幂等：已有 mode 不动。
//
// ② gui52 状态收敛：任何非 active 的 state（含旧 history）→ stale；
//
//	同形态多条记录折叠为一条——active 优先（lastOk 最新者胜）；无 active 时
//	stale 最新者保留，其余删除。内存态 Stale 布尔与 State 同步维护
//	（不参与任何判据，仅兼容旧测试）。
//
// 调用方持锁；返回是否有改动（由调用方统一落盘一次）。
func (s *ProfileStore) normalizeLocked() bool {
	changed := false
	for _, e := range s.data.Devices {
		for i := range e.Addrs {
			a := &e.Addrs[i]
			if a.Mode == "" && IsIPPort(a.Addr) {
				if isTlsFormAddr(a.Addr) {
					a.Mode = ModeTls
				} else {
					a.Mode = ModeTcpip
				}
				changed = true
			}
			if a.State != AddrStateActive && a.State != AddrStateStale {
				a.State = AddrStateStale
				changed = true
			}
			if (a.State == AddrStateStale) != a.Stale {
				a.Stale = a.State == AddrStateStale
				changed = true
			}
		}

		bestActive := map[string]int{}
		bestStale := map[string]int{}
		for i := range e.Addrs {
			a := e.Addrs[i]
			class := addrEntryClass(a)
			if a.State == AddrStateActive {
				if idx, ok := bestActive[class]; !ok || betterAddr(a, e.Addrs[idx]) {
					bestActive[class] = i
				}
			} else if idx, ok := bestStale[class]; !ok || betterAddr(a, e.Addrs[idx]) {
				bestStale[class] = i
			}
		}
		kept := make([]AddrEntry, 0, 2)
		for _, class := range []string{ModeTls, ModeTcpip} {
			if idx, ok := bestActive[class]; ok {
				kept = append(kept, e.Addrs[idx])
			} else if idx, ok := bestStale[class]; ok {
				kept = append(kept, e.Addrs[idx])
			}
		}
		if len(kept) != len(e.Addrs) {
			changed = true
		}
		e.Addrs = kept
	}
	if changed {
		for _, e := range s.data.Devices {
			sortAddrs(e)
		}
	}
	return changed
}

// isOrphanIPPortArchiveLocked 判定该键是否为 gui52fix1 的孤儿档案：
// 键本身是 IP:port、无市场名/serials/tlsGuid/自定义名，且 addrs 只有键自身
// 这一条（配对瞬间端口建档、会话已断留下的空壳）。
func isOrphanIPPortArchiveLocked(key string, e *DeviceEntry) bool {
	if e == nil || !IsIPPort(key) {
		return false
	}
	if e.Marketname != "" || e.Manufacturer != "" || e.Model != "" ||
		e.DisplayName != "" || e.DisplayNameSet || len(e.Serials) != 0 || e.TlsGuid != "" {
		return false
	}
	return len(e.Addrs) == 1 && e.Addrs[0].Addr == key
}

// cleanOrphanIPPortLocked 清理孤儿 IP:port 键档案（gui52fix1；gui52fix3 事件驱动复用）：
//   - 收集全部孤儿键（排序处理，结果确定）；
//   - 对每个孤儿，按同 IP 找非孤儿主档案（有 active 地址的档案优先）；
//     命中 → addrs/serials/参数并入主档案（mergeEntryLocked 去重），删除孤儿键
//     与 deviceOrder 中的孤儿键；
//   - 无同 IP 主档案 → 保留不动；
//   - 合并后统一 normalize（同形态单记忆折叠，主档案保留其真实证据）；
//   - 无孤儿 → 零改动（调用方不落盘）。
//
// trigger 只用于诊断日志（Load / 事件驱动）。调用方持锁；返回是否有改动。
func (s *ProfileStore) cleanOrphanIPPortLocked(trigger string) bool {
	orphanSet := map[string]bool{}
	for key, e := range s.data.Devices {
		if isOrphanIPPortArchiveLocked(key, e) {
			orphanSet[key] = true
		}
	}
	if len(orphanSet) == 0 {
		return false
	}
	orphans := make([]string, 0, len(orphanSet))
	for key := range orphanSet {
		orphans = append(orphans, key)
	}
	sort.Strings(orphans)

	changed := false
	skip := map[string]bool{}
	for key := range orphanSet {
		skip[key] = true
	}
	for _, orphanKey := range orphans {
		orphan, ok := s.data.Devices[orphanKey]
		if !ok {
			continue
		}
		_ = orphan // gui52-fix12：零合入——孤儿内容不再读取（无信息可搬）
		mainKey := s.resolveKeyByIPLocked(ipOfAddr(orphanKey), skip)
		if mainKey == "" {
			continue // 无同 IP 主档案：保留（可能是真正还没入档的设备）
		}
		// gui52-fix12：孤儿 IP:port 卡不向主档案合入任何信息（地址也不搬）——
		// 它唯一可能的价值（IP）已由 mdns 广播覆盖（广播自带 IP:port，匹配即写档）；
		// 零合入=孤儿永远无法竞争主档案 active（33301 教训终结：旧端口"最后抽搐"
		// 的 LastOk 再晚也进不了主档案）。孤儿键直接删除，主档案保持权威地址。
		bridge.DebugLog("[app] 孤儿档案清除（零合入）：%s → %s（%s）", orphanKey, mainKey, trigger)
		delete(s.data.Devices, orphanKey)
		order := s.data.DeviceOrder[:0]
		for _, k := range s.data.DeviceOrder {
			if k != orphanKey {
				order = append(order, k)
			}
		}
		s.data.DeviceOrder = order
		changed = true
		bridge.DebugLog("[app] 孤儿档案合并：%s → %s（%s）", orphanKey, mainKey, trigger)
	}
	if changed && s.normalizeLocked() {
		changed = true
	}
	return changed
}

// CleanOrphanIPPort 事件驱动合卡入口（gui52fix3）：运行期（非 Load）产生
// IP:port 过渡孤儿后调用。幂等：无孤儿/无同 IP 主档案时不写盘。
func (s *ProfileStore) CleanOrphanIPPort() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.cleanOrphanIPPortLocked("事件驱动")
	if changed {
		_ = s.persistLocked()
	}
	return changed
}

// DeviceOrder 返回设备卡顺序副本（无则空切片）。
func (s *ProfileStore) DeviceOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.data.DeviceOrder...)
}

// SetDeviceOrder 更新设备卡顺序并落盘；与当前值相同则跳过（幂等，
// 前端两处触发异步写）。调用方无需持锁（内部加锁）。
func (s *ProfileStore) SetDeviceOrder(order []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if equalStringSlices(s.data.DeviceOrder, order) {
		return nil
	}
	s.data.DeviceOrder = append([]string{}, order...)
	return s.persistLocked()
}

// RemoveDevice 从档案移除一个设备（devices 条目 + deviceOrder 条目）并立即
// 写盘；返回被移除的 DeviceEntry 快照（供调用方执行 disconnect/删除标记）。
func (s *ProfileStore) RemoveDevice(key string) (DeviceEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data.Devices[key]
	if !ok {
		return DeviceEntry{}, false
	}
	clone := cloneEntry(e)
	delete(s.data.Devices, key)
	order := s.data.DeviceOrder[:0]
	for _, k := range s.data.DeviceOrder {
		if k != key {
			order = append(order, k)
		}
	}
	s.data.DeviceOrder = order
	_ = s.persistLocked()
	return clone, true
}

// SetDisplayName 设置用户自定义名称：name 非空 → DisplayNameSet=true；
// name 空 → DisplayNameSet=false（清空，回到原算法链）。立即写盘。
func (s *ProfileStore) SetDisplayName(key, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data.Devices[key]
	if !ok {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if !e.DisplayNameSet && e.DisplayName == "" {
			return false
		}
		e.DisplayNameSet = false
		e.DisplayName = ""
	} else {
		if e.DisplayNameSet && e.DisplayName == name {
			return false
		}
		e.DisplayNameSet = true
		e.DisplayName = name
	}
	_ = s.persistLocked()
	return true
}

// equalStringSlices 比较两个字符串切片是否相等（nil 与空切片等价）。
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// persistLocked 落盘新结构（原子写：tmp+rename）。调用方必须持锁。
func (s *ProfileStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Persist 立即落盘（供 SyncDevices 迁移/归并后调用；无改动时也可安全调用）。
func (s *ProfileStore) Persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

// Get 返回设备参数（按 serial / IP:port 解析到 identity 档案；未配置=默认档）。
func (s *ProfileStore) Get(key string) DeviceProfile {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.resolveLocked(key); ok {
		return e.Profiles
	}
	return DefaultProfile()
}

// Save 写入设备参数并落盘。key 为 serial 或 IP:port：
// 已有档案 → 写入其 identity 档案；未知 → 按回退键新建档案（上线后归并）。
func (s *ProfileStore) Save(key string, p DeviceProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.resolveLocked(key); ok {
		e.Profiles = p
	} else {
		s.data.Devices[key] = entryFromKey(key, p)
	}
	return s.persistLocked()
}

// resolveLocked 把 serial/IP:port 解析到 identity 档案（持锁内部版）。
// 匹配顺序：identity 键自身 → serials 集合 → addrs 集合。
func (s *ProfileStore) resolveLocked(key string) (*DeviceEntry, bool) {
	if key == "" {
		return nil, false
	}
	if e, ok := s.data.Devices[key]; ok {
		return e, true
	}
	for _, e := range s.data.Devices {
		for _, ser := range e.Serials {
			if ser == key {
				return e, true
			}
		}
		for i := range e.Addrs {
			if e.Addrs[i].Addr == key {
				return e, true
			}
		}
	}
	return nil, false
}

// Entry 返回指定 key（identity/serial/IP:port）的档案快照副本（只读用）。
func (s *ProfileStore) Entry(key string) (DeviceEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return DeviceEntry{}, false
	}
	return cloneEntry(e), true
}

// ResolveKey 返回 key（identity/serial/IP:port）解析到的档案 identity 键
// （判定规则与 resolveLocked 一致：identity 键 → serials → addrs）；
// 未命中返回 ""。供设备卡按档案身份归并/过滤（幽灵离线卡折叠）用。
func (s *ProfileStore) ResolveKey(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return ""
	}
	return s.keyOfLocked(e)
}

// ResolveKeyByIP 按 IP（忽略端口）查找已有档案（gui52fix1 配对身份归并用）：
// 任一 addrs 的 ipOfAddr(addr)==ip 即命中。不同设备可能短暂共用同一 IP ——
// 有 state=active 地址的档案严格优先；同优先级多个命中时取字典序最小键
// （确定性）。返回档案 identity 键；未命中返回 ""。
func (s *ProfileStore) ResolveKeyByIP(ip string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveKeyByIPLocked(ip, nil)
}

// resolveKeyByIPLocked 是 ResolveKeyByIP 的持锁内部版；skip 为需要跳过的
// 档案键集合（孤儿清理时避免孤儿并回孤儿）。
func (s *ProfileStore) resolveKeyByIPLocked(ip string, skip map[string]bool) string {
	if ip == "" {
		return ""
	}
	activeKey, otherKey := "", ""
	for key, e := range s.data.Devices {
		if skip[key] {
			continue
		}
		active, matched := false, false
		for i := range e.Addrs {
			if ipOfAddr(e.Addrs[i].Addr) != ip {
				continue
			}
			matched = true
			if e.Addrs[i].State == AddrStateActive {
				active = true
			}
		}
		if !matched {
			continue
		}
		if active {
			if activeKey == "" || key < activeKey {
				activeKey = key
			}
		} else if otherKey == "" || key < otherKey {
			otherKey = key
		}
	}
	if activeKey != "" {
		return activeKey
	}
	return otherKey
}

// Entries 返回全部档案快照副本（探测/展示用）。
func (s *ProfileStore) Entries() map[string]DeviceEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]DeviceEntry, len(s.data.Devices))
	for k, e := range s.data.Devices {
		out[k] = cloneEntry(e)
	}
	return out
}

func cloneEntry(e *DeviceEntry) DeviceEntry {
	c := *e
	c.Serials = append([]string{}, e.Serials...)
	c.Addrs = append([]AddrEntry{}, e.Addrs...)
	return c
}

// profileCardName 档案来源卡的统一名称回退链（gui49-fix11）：
// marketname → manufacturer+model（空格拼接）→ model → fallback。
func profileCardName(e DeviceEntry, fallback string) string {
	// gui51：用户自定义名称优先（标=1）；标=0/空走原算法链。
	if e.DisplayNameSet && e.DisplayName != "" {
		return e.DisplayName
	}
	if e.Marketname != "" {
		return e.Marketname
	}
	if e.Manufacturer != "" && e.Model != "" {
		return e.Manufacturer + " " + e.Model
	}
	if e.Model != "" {
		return e.Model
	}
	return fallback
}

// applyProfileNames 设备卡名统一（gui52）：
//   - DisplayNameSet=true 的档案 → 无论在线/离线，卡名一律显示 DisplayName
//     （gui51 的在线卡改名失效修复——在线卡名来自 adb getprop，此前被跳过）；
//   - 其余离线卡：Name 为空/等于 Serial 时按统一回退链回补
//     （marketname → manufacturer+model → model → fallback）。
//   - 其余在线卡：保留 adb 富化名（marketname 链的在线权威来源）。
//
// Entry 返回只读快照。
func applyProfileNames(devs []adb.Device, store *ProfileStore) {
	for i := range devs {
		e, ok := store.Entry(devs[i].Serial)
		// gui52-fix13：孤儿已清/IP:port 键不存在时按 IP 回退识别主档案
		// （36475→Xiaomi Pad 8 Pro），避免卡名回退为 IP:port 或空。
		if !ok && IsIPPort(devs[i].Serial) {
			if ip := ipOfAddr(devs[i].Serial); ip != "" {
				if k := store.ResolveKeyByIP(ip); k != "" {
					e, ok = store.Entry(k)
				}
			}
		}
		if !ok {
			continue
		}
		if devs[i].State == "device" {
			if e.DisplayNameSet && e.DisplayName != "" {
				devs[i].Name = e.DisplayName
			} else if devs[i].Name == "" || devs[i].Name == devs[i].Serial {
				// gui52-fix13：无自定义名且 transport 名缺失/退化为 IP 时，
				// 用档案名兜底（name 链：自定义 → marketname → IP）。
				devs[i].Name = profileCardName(e, devs[i].Serial)
			}
			continue
		}
		if devs[i].Name != "" && devs[i].Name != devs[i].Serial {
			continue
		}
		devs[i].Name = profileCardName(e, devs[i].Serial)
	}
}

// SyncDevices 用当前 adb 轮询结果合并设备档案（identity 唯一化的核心）：
//   - 旧回退键档案（serial/IP:port 键）一旦匹配到设备信息 → 重键为 marketname 等
//     规范 identity，serials 累积、addrs 追加（IP 变化不分裂设备）；
//   - 在线（device）→ 对应端口写 state=active（内存统计清零）；
//   - 列表中存在但状态非 device 的无线地址 → 对应端口写 state=stale
//     （内存态 60s 节流，不落盘统计）。
//
// 返回是否有改动（含迁移重键），供调用方落盘与触发状态刷新。
func (s *ProfileStore) SyncDevices(devs []adb.Device) bool {
	return s.syncDevices(devs, nil)
}

// SyncDevicesExempt 是带插线遮罩期豁免的 SyncDevices（gui49-fix6）：
// exempt[identity]=true 的档案在离线观察分支不打 stale、不记失败节流。
func (s *ProfileStore) SyncDevicesExempt(devs []adb.Device, exempt map[string]bool) bool {
	return s.syncDevices(devs, exempt)
}

func (s *ProfileStore) syncDevices(devs []adb.Device, exempt map[string]bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	now := time.Now()
	for i := range devs {
		d := &devs[i]
		if d.State != "device" {
			// 离线观察：只记无线地址失败（无 marketname 信息，不参与 identity 归并）。
			// gui49-fix5 豁免：该 identity 的 USB 条目仍在设备流（线插着）→
			// 无线 offline 是 adbd 连带重启瞬态，不打 stale、不记失败节流。
			if d.ConnType == "wifi" && d.Serial != "" {
				if e, ok := s.resolveLocked(d.Serial); ok {
					key := s.keyOfLocked(e)
					// gui49-fix6：插线遮罩期豁免（removed 折腾窗口也保护）。
					if exempt[key] {
						continue
					}
					if !s.usbEntryInListLocked(devs, key) {
						if s.addrFailLocked(e, d.Serial, now) {
							changed = true
						}
					}
				}
			}
			continue
		}
		identity := adb.IdentityKey(d.Marketname, d.Manufacturer, d.Model, d.Serial)
		e, ok := s.resolveLocked(d.Serial)
		if ok {
			// 模型归并（自愈档案分裂）：市场名读不到时无线地址曾被记到
			// man+model 键下（如 "Xiaomi 24117RK2CC"，无 serials、只有 addrs）——
			// 同 model 且带市场名的档案存在时（"REDMI K80"），归并回本尊键，
			// BestAddr 恢复 → SCEZ_ADDR 注得上（16:53 无线回退抢台根因之一）。
			if e.Marketname == "" && e.Model != "" && len(e.Serials) == 0 {
				for k2, other := range s.data.Devices {
					if other != e && other.Model == e.Model && other.Marketname != "" && len(other.Serials) > 0 {
						s.rekeyLocked(k2, e)
						e = s.data.Devices[k2]
						changed = true
						break
					}
				}
			}
			// 档案优先（防 identity 抖动）：多会话 adb 竞态下 marketname 可能本轮
			// 读不到（回退 man+model），已有档案存有市场名时用它定身份——
			// 否则同一设备在 "REDMI K80"/"Xiaomi 24117RK2CC" 两个键间反复重键，
			// 档案分裂 → SCEZ_ADDR 注不上、弹窗误判"新设备"（16:53 实况根源）。
			if e.Marketname != "" {
				identity = e.Marketname
				if d.Marketname == "" {
					d.Marketname = e.Marketname // 补回本轮富化缺失（下游 identity 一致）
				}
			}
			if s.keyOfLocked(e) != identity {
				// 已在其它键下（回退键/旧 identity）：重键归并到规范 identity
				s.rekeyLocked(identity, e)
				e = s.data.Devices[identity]
				changed = true
			}
		} else {
			if old, ok := s.data.Devices[identity]; ok {
				// 键已存在：resolveLocked 只是按新无线 IP 解析失败（IP 尚未入档），
				// 不能覆盖旧档案（serials/参数）——复用继续累积
				e = old
			} else {
				e = &DeviceEntry{Serials: []string{}, Addrs: []AddrEntry{}, Profiles: DefaultProfile()}
				s.data.Devices[identity] = e
			}
			changed = true
		}
		if e.Marketname == "" && d.Marketname != "" {
			e.Marketname = d.Marketname
			changed = true
		}
		if e.Model == "" && d.Model != "" {
			e.Model = d.Model
			changed = true
		}
		if e.Manufacturer == "" && d.Manufacturer != "" {
			e.Manufacturer = d.Manufacturer
			changed = true
		}
		// 卡片 Identity 归一为档案身份（本轮 marketname 读不到时不再漂移成
		// man+model 回退值——弹窗判重/会话绑定/前端身份绑定都用它）
		if d.Identity != identity {
			d.Identity = identity
			changed = true
		}
		// 原生分辨率持久化（宽≥高）：会话外兜底（设备列表暂时消失时徽标仍可换算）
		if d.Res != "" && e.Res != d.Res {
			e.Res = d.Res
			changed = true
		}
		// USB 序列号累积（serial 集合）
		if d.ConnType == "usb" && d.Serial != "" && !contains(e.Serials, d.Serial) {
			e.Serials = append(e.Serials, d.Serial)
			changed = true
		}
		// 无线地址：在线=成功（active+fail=0+lastOk，新 IP 追加 active）；
		// 同时按地址形态更新设备 wireless 字段（tls 观察优先，未知按 tcpip 回填）。
		addr := ""
		if d.ConnType == "wifi" {
			addr = d.Serial
		} else if d.Wireless != "" {
			addr = d.Wireless
		}
		if addr != "" && s.addrSuccessLocked(e, addr, now) {
			changed = true
		}
		if addr != "" {
			mode := ""
			for i := range e.Addrs {
				if e.Addrs[i].Addr == addr {
					mode = e.Addrs[i].Mode
					break
				}
			}
			// gui14 形态自愈：档案 mode 缺失时按环境事实回填（tcpip 明文固定
			// 5555；无线调试 TLS 为随机端口——port != 5555 即 TLS），
			// 首见在线即记准形态（wireless 字段随之记 tls，不再误记 tcpip）。
			if mode == "" && isTlsFormAddr(addr) {
				mode = ModeTls
				if s.addrSuccessModeLocked(e, addr, ModeTls, now) {
					changed = true
				}
			}
			if mode == ModeTls {
				changed = s.applyWirelessFormLocked(e, ModeTls) || changed
			} else if mode == "" && e.Wireless == "" {
				changed = s.applyWirelessFormLocked(e, ModeTcpip) || changed
			}
		}
	}
	if s.normalizeLocked() {
		changed = true
	}
	if changed {
		_ = s.persistLocked()
	}
	return changed
}

// usbEntryInListLocked 判断该 identity 的 USB 条目是否仍在设备流（gui49-fix5
// 离线观察豁免）：USB 条目在列=线插着，无线 offline 是 adbd 连带重启瞬态。
// 调用方必须持 s.mu。
func (s *ProfileStore) usbEntryInListLocked(devs []adb.Device, key string) bool {
	if key == "" {
		return false
	}
	for i := range devs {
		d := &devs[i]
		if d.ConnType != "usb" || d.Serial == "" {
			continue
		}
		if e, ok := s.resolveLocked(d.Serial); ok && s.keyOfLocked(e) == key {
			return true
		}
	}
	return false
}

// keyOfLocked 返回档案当前所在键（identity 或回退键）。
func (s *ProfileStore) keyOfLocked(e *DeviceEntry) string {
	for k, v := range s.data.Devices {
		if v == e {
			return k
		}
	}
	return ""
}

// rekeyLocked 把档案从旧键归并到新 identity 键（旧键删除）。
// identity 键已存在其它档案时做真合并（serials/addrs 并集、参数档按规则择优），不丢数据。
func (s *ProfileStore) rekeyLocked(identity string, e *DeviceEntry) {
	oldKey := s.keyOfLocked(e)
	if dest, ok := s.data.Devices[identity]; ok && dest != e {
		mergeEntryLocked(dest, e)
		if oldKey != "" {
			delete(s.data.Devices, oldKey)
		}
		return
	}
	if oldKey != "" {
		delete(s.data.Devices, oldKey)
	}
	s.data.Devices[identity] = e
}

// mergeEntryLocked 把 src 并入 dest（identity 归并）：
// marketname/model 空缺回填；serials/addrs 并集（addrs 按状态择优）；
// 无线形态 tls 优先、tlsGuid 非空保留；参数档每模式独立择优：custom 优先，
// 其次 baseline 非零者，最后保持 dest。
func mergeEntryLocked(dest, src *DeviceEntry) {
	if dest.Marketname == "" {
		dest.Marketname = src.Marketname
	}
	if dest.Model == "" {
		dest.Model = src.Model
	}
	if dest.Wireless == "" || dest.Wireless == ModeTcpip && src.Wireless == ModeTls {
		dest.Wireless = src.Wireless
	}
	if dest.TlsGuid == "" {
		dest.TlsGuid = src.TlsGuid
	}
	for _, ser := range src.Serials {
		if !contains(dest.Serials, ser) {
			dest.Serials = append(dest.Serials, ser)
		}
	}
	for _, a := range src.Addrs {
		found := false
		for i := range dest.Addrs {
			if dest.Addrs[i].Addr == a.Addr {
				// gui52 择优：只看 state——dest stale 而 src active → 覆盖；
				// 其余保持 dest（不读 LastOk 等内存统计）。
				if dest.Addrs[i].State != AddrStateActive && a.State == AddrStateActive {
					dest.Addrs[i] = a
				}
				found = true
				break
			}
		}
		if !found {
			// gui52-fix11：孤儿/源档案的地址不得覆盖主档案同形态 active——
			// 33301（配对瞬时 transport，LastOk 由 sync 更新更晚）曾赢过 mdns
			// 权威 42145 被 normalize 折叠选中、随后 offline 变 stale（真相丢失）。
			// 主档案同形态已有 active 时，src 该条目降级为 stale 并入（不抢 active）。
			sameClassActive := false
			for i := range dest.Addrs {
				if addrEntryClass(dest.Addrs[i]) == addrEntryClass(a) && dest.Addrs[i].State == AddrStateActive {
					sameClassActive = true
					break
				}
			}
			if sameClassActive {
				if a.State != AddrStateStale {
					a.State = AddrStateStale
					a.LastOk = 0
				}
				dest.Addrs = append(dest.Addrs, a)
			} else {
				dest.Addrs = append(dest.Addrs, a)
			}
		}
	}
	sortAddrs(dest)
	dest.Profiles.Usb = mergeMode(dest.Profiles.Usb, src.Profiles.Usb)
	dest.Profiles.Wifi = mergeMode(dest.Profiles.Wifi, src.Profiles.Wifi)
}

// mergeMode 单模式参数档归并择优：custom 优先；其次 baseline 非零者；最后保持 dst。
func mergeMode(dst, src ModeProfile) ModeProfile {
	if src.Custom && !dst.Custom {
		return src
	}
	if dst.Custom || src.Custom {
		return dst
	}
	if dst.Baseline.Res == 0 && src.Baseline.Res != 0 {
		return src
	}
	if src.Baseline.Res == 0 && dst.Res == 0 && src.Res != 0 {
		return src
	}
	return dst
}

// addrSuccessLocked 记录地址成功：覆盖对应端口写 state=active（内存统计清零），
// active 排前。mode 非空时回填/更新形态（旧档案无 mode 字段的 tls 地址在
// mDNS 再匹配时补齐）。
func (s *ProfileStore) addrSuccessLocked(e *DeviceEntry, addr string, now time.Time) bool {
	return s.addrSuccessModeLocked(e, addr, "", now)
}

// addrSuccessModeLocked 是 addrSuccessLocked 的形态感知版。
// gui52 二态语义：成功即覆盖对应端口的 IP 并打 state=active（内存态
// fail/lastFail 清零、lastOk=now、Stale 冗余标同步清除），同形态
// （tls/tcpip）的其它旧条目删除（每形态单记忆）。tcpip 规则：同一 IP
// 更新时间戳；跨 IP 的新 5555 成功 → 旧 5555 弃用。TLS 规则：新 TLS 端口
// 成功 → 旧 TLS 条目弃用（tlsGuid 更新由广播匹配路径记录）。
func (s *ProfileStore) addrSuccessModeLocked(e *DeviceEntry, addr, mode string, now time.Time) bool {
	changed := false
	// 本次成功地址的形态：显式 mode 优先；空则按环境事实（port != 5555 → tls）
	// 归类——保证"替换同类"分类正确。
	class := mode
	if class == "" {
		if isTlsFormAddr(addr) {
			class = ModeTls
		} else {
			class = ModeTcpip
		}
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr != addr {
			continue
		}
		a := &e.Addrs[i]
		if a.State != AddrStateActive || a.Fail != 0 || a.LastOk != now.Unix() || a.LastFail != 0 {
			a.State = AddrStateActive
			a.Fail = 0
			a.LastOk = now.Unix()
			a.LastFail = 0
			changed = true
		}
		// gui37：连接成功=活性事实，解除广播缺席打标（复活）。
		if a.Stale {
			a.Stale = false
			changed = true
		}
		if mode != "" && a.Mode != mode {
			a.Mode = mode
			changed = true
		}
		if mode == "" && a.Mode != "" {
			class = a.Mode // 档案已有形态优先（mode 缺省时按记录分类）
		}
		changed = s.retireSameClassLocked(e, addr, class) || changed
		sortAddrs(e)
		return changed
	}
	e.Addrs = append(e.Addrs, AddrEntry{Addr: addr, State: AddrStateActive, LastOk: now.Unix(), Mode: mode})
	changed = s.retireSameClassLocked(e, addr, class) || changed
	sortAddrs(e)
	return true
}

// retireSameClassLocked 删除与 addr 同形态（class）的其它条目（gui41 单记忆：
// 每形态只留一条——成功即覆盖对应端口的 IP，同形态旧条目删除保持档案干净）。
func (s *ProfileStore) retireSameClassLocked(e *DeviceEntry, addr, class string) bool {
	changed := false
	kept := e.Addrs[:0]
	for i := range e.Addrs {
		a := e.Addrs[i]
		if a.Addr == addr {
			kept = append(kept, a)
			continue
		}
		if addrEntryClass(a) != class {
			kept = append(kept, a)
			continue
		}
		changed = true // 同形态旧条目：删除（不再转 history）
	}
	e.Addrs = kept
	return changed
}

// addrEntryClass 返回条目有效形态（tls/tcpip）：mode 空时按环境事实
// （port != 5555 → tls）归类——与 normalizeLocked 同口径，保证"同类"分类总成立。
func addrEntryClass(a AddrEntry) string {
	if a.Mode != "" {
		return a.Mode
	}
	if isTlsFormAddr(a.Addr) {
		return ModeTls
	}
	return ModeTcpip
}

// clearStaleLocked 按地址清除广播缺席打标（gui37：广播匹配到该地址=设备自报
// 该形态存在 → 复活）。已持锁；返回是否有改动。
func (s *ProfileStore) clearStaleLocked(e *DeviceEntry, addr string) bool {
	for i := range e.Addrs {
		if e.Addrs[i].Addr != addr {
			continue
		}
		changed := false
		if e.Addrs[i].State != AddrStateActive {
			e.Addrs[i].State = AddrStateActive
			changed = true
		}
		if e.Addrs[i].Stale {
			e.Addrs[i].Stale = false
			changed = true
		}
		return changed
	}
	return false
}

// AddrSuccess 对外记录地址成功（探测结果/轮询在线），自动落盘。
// 变参 mode 兼容老的两参调用（探测/轮询不感知形态）和 gui47 三参调用
// （插线学习写 tcpip 形态）；返回是否有档案改动。
func (s *ProfileStore) AddrSuccess(key, addr string, mode ...string) bool {
	m := ""
	if len(mode) > 0 {
		m = mode[0]
	}
	return s.AddrSuccessMode(key, addr, m)
}

// AddrSuccessWithMode 对外记录地址成功并回填形态（TLS 优先探测成功路径用），自动落盘。
func (s *ProfileStore) AddrSuccessWithMode(key, addr, mode string) {
	s.AddrSuccess(key, addr, mode)
}

// AddrSuccessMode 是 AddrSuccess 的底层形态感知实现；返回是否有档案改动
// （gui47 插线学习用于"档案已对齐"判定，也便于测试断言）。自动落盘。
func (s *ProfileStore) AddrSuccessMode(key, addr, mode string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return false
	}
	changed := s.addrSuccessModeLocked(e, addr, mode, time.Now())
	if mode != "" {
		changed = s.applyWirelessFormLocked(e, mode) || changed
	}
	if changed {
		_ = s.persistLocked()
	}
	return changed
}

// StaleTls 把该设备档案中全部 TLS 形态条目标记为 stale（gui47：插线学习读到
// adb_wifi_enabled=0，说明无线调试/TLS 通道已关，TLS 地址不可用）。gui52：
// 落盘状态是 state=stale，内存态 Stale 冗余标同步。已持锁落盘；返回是否有改动。
func (s *ProfileStore) StaleTls(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return false
	}
	changed := false
	for i := range e.Addrs {
		if e.Addrs[i].State != AddrStateActive || addrEntryClass(e.Addrs[i]) != ModeTls {
			continue
		}
		e.Addrs[i].State = AddrStateStale
		e.Addrs[i].Stale = true
		changed = true
	}
	if changed {
		_ = s.persistLocked()
	}
	return changed
}

// MarkAddrStale 把档案中指定地址的条目标记为 stale（仅 State==active 打标；
// mdns 广播 removed 防抖后使用）。gui52：State 即落盘状态。返回是否有改动。
func (s *ProfileStore) MarkAddrStale(key, addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok || addr == "" {
		return false
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr != addr || e.Addrs[i].State != AddrStateActive {
			continue
		}
		e.Addrs[i].State = AddrStateStale
		e.Addrs[i].Stale = true
		_ = s.persistLocked()
		return true
	}
	return false
}

// MarkAllAddrsStale 把该设备档案中全部 active 地址标记为 stale（离线卡联合判据 /
// 启动基线对账用：设备流与 mdns 流都看不到该设备）。返回是否有改动。
func (s *ProfileStore) MarkAllAddrsStale(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return false
	}
	changed := false
	for i := range e.Addrs {
		if e.Addrs[i].State == AddrStateActive {
			e.Addrs[i].State = AddrStateStale
			e.Addrs[i].Stale = true
			changed = true
		}
	}
	if changed {
		_ = s.persistLocked()
	}
	return changed
}

// AddrMode 返回档案中该地址记录的形态（tls/tcpip）；未知地址/未记录形态返回 ""。
func (s *ProfileStore) AddrMode(addr string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(addr)
	if !ok {
		return ""
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == addr {
			return e.Addrs[i].Mode
		}
	}
	return ""
}

// applyWirelessFormLocked 按最新观察更新设备形态：tls 观察优先（tls 与 tcpip 可并存，
// tls 一旦观察到即记 tls）；tcpip 只在尚未记录形态时回填（不覆盖 tls）。
func (s *ProfileStore) applyWirelessFormLocked(e *DeviceEntry, mode string) bool {
	switch mode {
	case ModeTls:
		if e.Wireless != ModeTls {
			e.Wireless = ModeTls
			return true
		}
	case ModeTcpip:
		if e.Wireless == "" {
			e.Wireless = ModeTcpip
			return true
		}
	}
	return false
}

// HasTlsAddr 判定档案是否存在 state=active 的 mode=tls 地址（gui52 收敛：
// 只看 state 二值——active TLS = 当前可用 TLS 能力，stale 一律不算）。
func (s *ProfileStore) HasTlsAddr(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return false
	}
	for i := range e.Addrs {
		if e.Addrs[i].State == AddrStateActive && addrEntryClass(e.Addrs[i]) == ModeTls {
			return true
		}
	}
	return false
}

// TlsGuidOf 返回档案记录的 mDNS tls 连接服务实例名（未记录返回空）。
func (s *ProfileStore) TlsGuidOf(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return ""
	}
	return e.TlsGuid
}

// TlsGuidKnown 判定是否有档案记录过该 tls 服务实例名（待配对卡"档案无此 identity"判据：
// 配对成功后 tlsGuid 入档——设备换端口重播服务时仍识别为已知设备，不重复弹"待配对"）。
func (s *ProfileStore) TlsGuidKnown(guid string) bool {
	if guid == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.data.Devices {
		if e.TlsGuid == guid {
			return true
		}
	}
	return false
}

// addrFailLocked 记录地址失败（gui52 二态 + 内存态节流）：
// 失败 → 对应端口落盘 state=stale；内存态 fail++ / lastFail=now 只用于
// 60s 节流（不落盘）。节流期过后该 stale 条目恢复为候选（探测/重连可翻回
// active），永不拉黑、永不失忆。
func (s *ProfileStore) addrFailLocked(e *DeviceEntry, addr string, now time.Time) bool {
	return s.addrFailModeLocked(e, addr, "", now)
}

// addrFailModeLocked 是 addrFailLocked 的形态感知版（gui52 无线接入学习用）：
// mode 非空时给已有/新追加的失败条目补记形态（tls/tcpip），保证失败也入档
// 完整的两态地址。
func (s *ProfileStore) addrFailModeLocked(e *DeviceEntry, addr, mode string, now time.Time) bool {
	for i := range e.Addrs {
		if e.Addrs[i].Addr != addr {
			continue
		}
		e.Addrs[i].Fail++
		e.Addrs[i].LastFail = now.Unix()
		e.Addrs[i].State = AddrStateStale
		e.Addrs[i].Stale = true
		if mode != "" && e.Addrs[i].Mode == "" {
			e.Addrs[i].Mode = mode
		}
		sortAddrs(e)
		return true
	}
	// 未知地址（探测发现的新地址失败）：追加记录（state=stale + 内存态节流字段）
	e.Addrs = append(e.Addrs, AddrEntry{Addr: addr, State: AddrStateStale, Mode: mode, Fail: 1, LastFail: now.Unix(), Stale: true})
	sortAddrs(e)
	return true
}

// AddrFail 对外记录地址失败（探测失败/离线观察），自动落盘。
func (s *ProfileStore) AddrFail(key, addr string) {
	s.AddrFailMode(key, addr, "")
}

// AddrFailMode 对外记录地址失败并补记形态（gui52），自动落盘。
func (s *ProfileStore) AddrFailMode(key, addr, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return
	}
	if s.addrFailModeLocked(e, addr, mode, time.Now()) {
		_ = s.persistLocked()
	}
}

// ResetFailThrottle 解除全部地址的失败节流（gui27：手动刷新=明确要求立即重试。
// 60s 节流只防自动轮询每轮刷失败；ForceDiscover 前调用——本轮失败会被重新
// 标记 lastFail，节流自然重建）。有改动即落盘。
func (s *ProfileStore) ResetFailThrottle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, e := range s.data.Devices {
		for i := range e.Addrs {
			if e.Addrs[i].LastFail != 0 {
				e.Addrs[i].LastFail = 0
				changed = true
			}
		}
	}
	if changed {
		_ = s.persistLocked()
	}
}

// sortAddrs 排序（gui52）：state 是唯一排序判据——active 在前、stale 在后；
// 同状态保持写入顺序（稳定排序，不读 LastOk 等内存统计）。
func sortAddrs(e *DeviceEntry) {
	sort.SliceStable(e.Addrs, func(i, j int) bool {
		a, b := e.Addrs[i], e.Addrs[j]
		if a.State != b.State {
			return a.State == AddrStateActive
		}
		return false
	})
}

// BestAddr 返回该设备档案的首选无线地址（gui52 二态）：
// active 严格优先（TLS 层 → tcpip 层）；无 active 时 stale 是离线候选；
// 内存态 60s 失败节流由 OrderedAddrs 统一执行。fail/lastOk 不参与判定。
func (s *ProfileStore) BestAddr(key string) string {
	ordered := s.OrderedAddrs(key)
	if len(ordered) == 0 {
		return ""
	}
	return ordered[0].Addr
}

// OrderedAddrs 返回该设备档案的候选地址（gui52 二态，TLS 优先分层；
// 探测/投屏候选共用）。每类（tls/tcpip）至多一条：有 active → 取 active 最新；
// 无 active → 取 stale 最新（stale=离线候选，60s 内存态失败节流过后可再试）。
// 该条 lastFail 距今 < failThrottleWindow → 节流跳过（本层无候选，不回退旧
// 条目）；未失败或超时 → 参与（永不拉黑、永不失忆）。
func (s *ProfileStore) OrderedAddrs(key string) []AddrEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return nil
	}
	return s.orderedAddrsLocked(e)
}

// AllAddrs 返回该设备档案的全部地址（active 优先、stale 跟随；探测并发候选用）。
func (s *ProfileStore) AllAddrs(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(e.Addrs))
	for _, a := range e.Addrs {
		out = append(out, a.Addr)
	}
	return out
}

// OfflineCandidates 返回"档案有 addrs 但当前设备列表不在线"的候选（探测触发条件）：
// 对每个档案，若其 serials 与 addrs 均不在线，则其全部 addrs 都是候选。
// 返回 map[identity][]addr（addrs 已按 active 优先排序）。
func (s *ProfileStore) OfflineCandidates(devs []adb.Device) map[string][]string {
	m := s.OfflineCandidateAddrs(devs)
	out := make(map[string][]string, len(m))
	for k, list := range m {
		addrs := make([]string, 0, len(list))
		for i := range list {
			addrs = append(addrs, list[i].Addr)
		}
		out[k] = addrs
	}
	return out
}

// OfflineCandidateAddrs 是 OfflineCandidates 的形态感知版（gui12；gui52 二态）：
// 每档案候选按 orderedAddrsLocked 构建（每类最新一条、内存态 60s 失败节流），
// 探测据此构建 tls/tcpip 两层（每层至多一条档案兜底）。
// gui52 在线证据收敛到 state：档案存在任一 state=active 地址 → 设备视为在线
// （不得给出离线候选、不得显示离线卡）；全部 stale → 其条目都是离线候选。
func (s *ProfileStore) OfflineCandidateAddrs(devs []adb.Device) map[string][]AddrEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	onlineSer := map[string]bool{}
	onlineAddr := map[string]bool{}
	for i := range devs {
		if devs[i].State != "device" {
			continue
		}
		onlineSer[devs[i].Serial] = true
		if devs[i].ConnType == "wifi" {
			onlineAddr[devs[i].Serial] = true
		}
		if devs[i].Wireless != "" {
			onlineAddr[devs[i].Wireless] = true
		}
	}
	out := map[string][]AddrEntry{}
	for identity, e := range s.data.Devices {
		if len(e.Addrs) == 0 {
			continue
		}
		online := false
		for _, ser := range e.Serials {
			if onlineSer[ser] {
				online = true
				break
			}
		}
		for i := range e.Addrs {
			if onlineAddr[e.Addrs[i].Addr] {
				online = true
			}
			// gui52：active 地址本身就是在线证据——即使设备流暂时还没
			// 列出它，也不能当离线候选去探测（探测不会比状态更新）。
			if e.Addrs[i].State == AddrStateActive {
				online = true
			}
		}
		if online {
			continue
		}
		out[identity] = s.orderedAddrsLocked(e)
	}
	return out
}

// orderedAddrsLocked 返回档案候选地址（持锁内部版；gui52 二态语义，
// OrderedAddrs/OfflineCandidateAddrs 共用）。每类（tls/tcpip）只取
// newestOfClassLocked 的一条——有 active → active 最新；无 active → stale
// 最新（stale=离线候选）。该条处于内存态 60s 失败节流期内（lastFail 距今 <
// failThrottleWindow）→ 本层无候选（不回退旧条目）；lastFail=0 或超时 → 参与
// （永不拉黑）。判据只认 state 二值，不读 Stale/Fail。
func (s *ProfileStore) orderedAddrsLocked(e *DeviceEntry) []AddrEntry {
	now := time.Now()
	active := make([]AddrEntry, 0, 2)
	stale := make([]AddrEntry, 0, 2)
	for _, class := range []string{ModeTls, ModeTcpip} {
		if a, ok := newestOfClassLocked(e, class); ok && !addrThrottled(a.LastFail, now) {
			if a.State == AddrStateActive {
				active = append(active, a)
			} else {
				stale = append(stale, a)
			}
		}
	}
	// gui52：active 是唯一在线证据，跨形态也严格先于 stale——
	// 显示/投屏首选永不落到 stale 地址上（stale 只是离线候选）。
	out := make([]AddrEntry, 0, len(active)+len(stale))
	out = append(out, active...)
	out = append(out, stale...)
	return out
}

// newestOfClassLocked 返回该档案某形态 class（tls/tcpip）的记忆
// （gui52 二态）：active 严格优先；无 active → stale（旧 history 已归一为
// stale）。同状态取档案顺序第一条（写入路径已把最新事实排前；不读
// LastOk/Stale/Fail）。无该形态条目 → ok=false。
func newestOfClassLocked(e *DeviceEntry, class string) (AddrEntry, bool) {
	activeIdx := -1
	staleIdx := -1
	for i := range e.Addrs {
		if addrEntryClass(e.Addrs[i]) != class {
			continue
		}
		if e.Addrs[i].State == AddrStateActive {
			if activeIdx < 0 {
				activeIdx = i
			}
		} else if staleIdx < 0 {
			staleIdx = i
		}
	}
	if activeIdx >= 0 {
		return e.Addrs[activeIdx], true
	}
	if staleIdx >= 0 {
		return e.Addrs[staleIdx], true
	}
	return AddrEntry{}, false
}

// DeviceInfo 是 SyncDevices 之外补充档案身份信息的输入（mDNS 名称匹配用）。
type DeviceInfo struct {
	Marketname   string
	Manufacturer string
	Model        string
}

// MatchMdns 把 mDNS 扫描结果与档案匹配（旧签名，返回地址列表）。
// 语义与 MatchMdnsModes 一致（地址顺序同）；旧档案无形态字段 → 按 tcpip 处理。
func (s *ProfileStore) MatchMdns(services []MdnsMatch) []string {
	matched := s.MatchMdnsModes(services)
	out := make([]string, 0, len(matched))
	for i := range matched {
		out = append(out, matched[i].Addr)
	}
	return out
}

// MatchMdnsModes 把 mDNS 扫描结果与档案匹配（gui12 形态感知版）：
//   - 服务地址已存在于某档案 → 该地址是候选（形态回填，不覆盖已有 tls）；
//   - 服务名命中某档案的 serial → 服务地址是新 IP：追加 active 并入档
//     （IP 变化不分裂），同时是候选；
//   - tls 服务实例名 adb-<serial>-XXXXXX：剥前缀/后缀拿 serial 匹配；
//     命中后记录 tlsGuid（端口变化后仍可匹配本机）并更新设备形态 wireless=tls；
//   - 经典 _adb._tcp 实例名 adb-<serial>：剥前缀匹配；wireless 空时记 tcpip；
//   - 无法匹配 → 忽略（不阻塞 UI）。
//
// 返回可并发探测的地址列表（带形态，TLS 优先分层用）。
func (s *ProfileStore) MatchMdnsModes(services []MdnsMatch) []MdnsAddr {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MdnsAddr
	now := time.Now()
	for _, sv := range services {
		if sv.Addr == "" || !IsIPPort(sv.Addr) {
			continue
		}
		mode := sv.Mode
		matched := false
		for _, e := range s.data.Devices {
			for i := range e.Addrs {
				if e.Addrs[i].Addr == sv.Addr {
					a := &e.Addrs[i] // 后续 addrSuccessModeLocked 可能 sortAddrs 重排——指针仍指向原条目
					out = append(out, MdnsAddr{Addr: sv.Addr, Mode: mode})
					// 形态回填：旧档案缺 mode 的 tls 地址在 mDNS 再匹配时补齐
					if mode != "" && a.Mode == "" {
						if s.addrSuccessModeLocked(e, sv.Addr, mode, now) {
							_ = s.persistLocked()
						}
					}
					// gui27 广播=真相：设备自报"我现在在这"→ 该地址的失败节流
					// 解除（广播在场即设备可达，lastFail 归零）。只动 lastFail，
					// 不刷 lastOk（低频扫描幂等不受影响）。
					if a.LastFail != 0 {
						a.LastFail = 0
						_ = s.persistLocked()
					}
					// gui37：广播匹配到该地址 → 解除该地址的广播缺席打标。
					// 按地址搜索（不依赖上面易被 sortAddrs 重排的指针）。
					if s.clearStaleLocked(e, sv.Addr) {
						_ = s.persistLocked()
					}
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched {
			continue
		}
		// 服务名命中 serial → 新 IP 归并入该档案
		// （adb mdns services 实例名形如 "adb-<serial>"；tls 服务为
		// "adb-<serial>-XXXXXX"，先剥前缀/后缀再比——换 WiFi 换 IP 场景）
		if sv.Mode == discovery.MdnsModeTls {
			identity := TlsServiceIdentity(sv.Name)
			for _, e := range s.data.Devices {
				if identity != "" && contains(e.Serials, identity) || e.TlsGuid != "" && e.TlsGuid == sv.Name {
					if s.addrSuccessModeLocked(e, sv.Addr, ModeTls, now) {
						_ = s.persistLocked()
						bridge.DebugLog("[app] MatchMdnsModes TLS 写档成功：name=%s addr=%s key=%s addrs=%+v", sv.Name, sv.Addr, s.keyOfLocked(e), e.Addrs)
					} else {
						bridge.DebugLog("[app] MatchMdnsModes TLS 写档无变化：name=%s addr=%s key=%s", sv.Name, sv.Addr, s.keyOfLocked(e))
					}
					changed := s.applyWirelessFormLocked(e, ModeTls)
					if sv.Name != "" && e.TlsGuid != sv.Name {
						e.TlsGuid = sv.Name
						changed = true
					}
					if changed {
						_ = s.persistLocked()
					}
					out = append(out, MdnsAddr{Addr: sv.Addr, Mode: ModeTls})
					break
				}
			}
			continue
		}
		name := strings.TrimPrefix(sv.Name, "adb-")
		for _, e := range s.data.Devices {
			if contains(e.Serials, name) {
				if s.addrSuccessModeLocked(e, sv.Addr, ModeTcpip, now) {
					_ = s.persistLocked()
				}
				if s.applyWirelessFormLocked(e, ModeTcpip) {
					_ = s.persistLocked()
				}
				out = append(out, MdnsAddr{Addr: sv.Addr, Mode: ModeTcpip})
				break
			}
		}
	}
	return out
}

// MarkMdnsAbsentStale 对非空 mDNS 快照做广播缺席检视（gui37 旧扫描路径兼容
// 15s 路径调用；快照为空/daemon 挂时不调用，避免误标）：
//   - 只处理"快照内至少有任一形态广播"的设备（设备自报了存在）；
//   - 该设备某形态（tls/tcpip）在快照中缺席 → 该形态全部档案条目写
//     state=stale（"用不了"）；
//   - 广播匹配到的地址由 MatchMdnsModes 单独翻回 active（复活）；
//   - 不删条目、不动 mode——内存统计与落盘 state 收敛为二态。
func (s *ProfileStore) MarkMdnsAbsentStale(services []MdnsMatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(services) == 0 {
		return
	}
	changed := false
	for _, e := range s.data.Devices {
		present := map[string]bool{}
		for _, sv := range services {
			if sv.Addr == "" || !IsIPPort(sv.Addr) || sv.Mode == discovery.MdnsModePairing {
				continue
			}
			if mdnsServiceMatchesLocked(e, sv) {
				present[sv.Mode] = true
			}
		}
		if len(present) == 0 {
			continue
		}
		for i := range e.Addrs {
			a := &e.Addrs[i]
			class := addrEntryClass(*a)
			if !present[class] && a.State == AddrStateActive {
				a.State = AddrStateStale
				a.Stale = true
				changed = true
			}
		}
	}
	if changed {
		_ = s.persistLocked()
	}
}

// MdnsAuthoritativeDevices 返回当前有 mDNS 连接广播（tls/tcpip）命中的设备
// identity 集合（gui19 广播权威判据）。匹配规则与 MatchMdnsModes 一致：
//   - 服务地址已存在于某档案 → 命中该设备；
//   - tls 服务实例名 adb-<serial>-XXXXXX → 剥 serial 或 tlsGuid 命中；
//   - 经典 _adb._tcp 实例名 adb-<serial> → 剥前缀命中 serial；
//   - pairing 服务（仅配对窗口短暂广播）不参与判定；
//   - 无有效 IP:port 的服务（"local" 未解析）不参与判定。
//
// 命中含义：该设备自报了"当前权威地址"——广播在场时其档案旧地址不再参与
// 本轮探测（runDiscovery 构建 tiers 时跳过该 identity 的档案候选；广播地址
// connect 失败 → 本轮 notfound，不试档案旧地址，下一轮 15s 再看）。
func (s *ProfileStore) MdnsAuthoritativeDevices(services []MdnsMatch) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, sv := range services {
		if sv.Addr == "" || !IsIPPort(sv.Addr) || sv.Mode == discovery.MdnsModePairing {
			continue
		}
		for identity, e := range s.data.Devices {
			if mdnsServiceMatchesLocked(e, sv) {
				out[identity] = true
			}
		}
	}
	return out
}

// mdnsServiceMatchesLocked 判定一条 mDNS 服务是否属于该档案设备。匹配规则与
// MatchMdnsModes/MdnsAuthoritativeDevices 一致（gui21 抽公共辅助——投屏路径与
// 探测路径共用同一归属判定，防规则漂移）：
//
//	① 服务地址已存在于档案 addrs（已知地址重播）；
//	② tls 连接服务实例名 adb-<serial>-XXXXXX 剥 serial 命中 / tlsGuid 命中
//	   （端口变化后仍匹配本机）；
//	③ 经典 _adb._tcp 实例名 adb-<serial> 剥前缀命中 serial。
//
// 调用方已过滤无效 IP:port 与 pairing 服务（见 MdnsAuthoritativeDevices /
// MdnsWirelessAddr 的入口判据），此处只做归属判定。
func mdnsServiceMatchesLocked(e *DeviceEntry, sv MdnsMatch) bool {
	for i := range e.Addrs {
		if e.Addrs[i].Addr == sv.Addr {
			return true
		}
	}
	if sv.Mode == discovery.MdnsModeTls {
		id := TlsServiceIdentity(sv.Name)
		return id != "" && contains(e.Serials, id) || e.TlsGuid != "" && e.TlsGuid == sv.Name
	}
	name := strings.TrimPrefix(sv.Name, "adb-")
	return contains(e.Serials, name)
}

// MdnsWirelessAddr 返回 mDNS 广播中该设备的首选无线地址（gui21 投屏路径用）：
// 广播 = 设备自报的"当前权威地址"，分层顺序与 runDiscovery tiers 同序——
// ① tls 层（_adb-tls-connect）② tcpip 层（经典 _adb._tcp），层内按服务出现序
// 取首条；pairing 服务与未解析地址不参与。归属判定与 MdnsAuthoritativeDevices
// 共用 mdnsServiceMatchesLocked（同一规则防漂移）。无广播命中 → 返回 ""
// （调用方回退档案 BestAddr）。只读判定：不触发 MatchMdnsModes 的入档副作用
// （投屏路径不写档案——45005 tls 广播尚未入档也能选出来）。
func (s *ProfileStore) MdnsWirelessAddr(key string, services []MdnsMatch) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return ""
	}
	// ① tls 层：与 runDiscovery tiers[0] 同序（按快照出现序取首条）
	for _, sv := range services {
		if sv.Mode != discovery.MdnsModeTls || sv.Addr == "" || !IsIPPort(sv.Addr) {
			continue
		}
		if mdnsServiceMatchesLocked(e, sv) {
			return sv.Addr
		}
	}
	// ② tcpip 层：与 runDiscovery tiers[1] 同序（未知形态按 tcpip 归层）
	for _, sv := range services {
		if sv.Mode == discovery.MdnsModeTls || sv.Mode == discovery.MdnsModePairing ||
			sv.Addr == "" || !IsIPPort(sv.Addr) {
			continue
		}
		if mdnsServiceMatchesLocked(e, sv) {
			return sv.Addr
		}
	}
	return ""
}

// TlsServiceIdentity 从 tls 连接服务实例名提取设备 serial：
// "adb-<serial>-XXXXXX" → 剥 "adb-" 前缀与末尾 6 位随机后缀（AOSP mdns.cpp
// GenerateDeviceGuid：adb-<ro.serialno>-<six-random-alphanum>）。
// gui14：adb 37 的 `adb devices` 会以完整 FQN 列出 mDNS 服务条目
// （实例名._服务类型._tcp，如 "adb-601c9f08-KWqpio._adb-tls-connect._tcp"）——
// 先剥离服务类型后缀段再走原逻辑；无后缀行为不变（回归不变）。
// 无前缀/无后缀（旧 adb 输出或随机 16 位 identity）时退回剥前缀后的整体。
func TlsServiceIdentity(name string) string {
	name = stripMdnsServiceSuffix(name)
	name = strings.TrimPrefix(name, "adb-")
	if i := strings.LastIndexByte(name, '-'); i > 0 && len(name)-i-1 == 6 {
		if suffix := name[i+1:]; isAlnum6(suffix) {
			return name[:i]
		}
	}
	return name
}

// stripMdnsServiceSuffix 循环剥离 mDNS 服务名的服务类型后缀段（._xxx）：
// `adb devices` 的 mDNS 令牌形如 实例名._adb-tls-connect._tcp /
// 实例名._adb._tcp / 实例名._tcp——按已知后缀从长到短逐段去掉，
// 得到裸实例名（如 "adb-601c9f08-KWqpio"）。无后缀输入原样返回。
func stripMdnsServiceSuffix(name string) string {
	for {
		next := name
		switch {
		case strings.HasSuffix(next, "._adb-tls-connect._tcp"):
			next = strings.TrimSuffix(next, "._adb-tls-connect._tcp")
		case strings.HasSuffix(next, "._adb._tcp"):
			next = strings.TrimSuffix(next, "._adb._tcp")
		case strings.HasSuffix(next, "._adb-tls-connect"):
			next = strings.TrimSuffix(next, "._adb-tls-connect")
		case strings.HasSuffix(next, "._adb"):
			next = strings.TrimSuffix(next, "._adb")
		case strings.HasSuffix(next, "._tcp"):
			next = strings.TrimSuffix(next, "._tcp")
		}
		if next == name {
			return name
		}
		name = next
	}
}

func isAlnum6(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		return false
	}
	return true
}

// PairArchive 配对成功入档（gui12 无线调试接入向导）：
// 按 identity（优先）/serial/addr 定位或新建档案 → 记 mode=tls + tls 地址 +
// tlsGuid + 设备形态 wireless=tls；serial 累积（mDNS 实例名里的真 serial）；
// marketname/model 空缺回填。自动落盘。
func (s *ProfileStore) PairArchive(identity, serial, addr, tlsGuid, marketname, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var e *DeviceEntry
	if identity != "" {
		e = s.data.Devices[identity]
	}
	if e == nil {
		if e = s.resolveLockedAddrOnly(serial); e == nil {
			e = s.resolveLockedAddrOnly(addr)
		}
	}
	if e == nil {
		e = &DeviceEntry{Serials: []string{}, Addrs: []AddrEntry{}, Profiles: DefaultProfile()}
		if identity != "" {
			s.data.Devices[identity] = e
		} else if serial != "" {
			s.data.Devices[serial] = e
		} else {
			s.data.Devices[addr] = e
		}
	}
	changed := false
	if e.Marketname == "" && marketname != "" {
		e.Marketname = marketname
		changed = true
	}
	if e.Model == "" && model != "" {
		e.Model = model
		changed = true
	}
	if serial != "" && !contains(e.Serials, serial) {
		e.Serials = append(e.Serials, serial)
		changed = true
	}
	if tlsGuid != "" && e.TlsGuid != tlsGuid {
		e.TlsGuid = tlsGuid
		changed = true
	}
	changed = s.applyWirelessFormLocked(e, ModeTls) || changed
	if s.addrSuccessModeLocked(e, addr, ModeTls, time.Now()) {
		changed = true
	}
	if changed {
		_ = s.persistLocked()
	}
	// gui52fix3：配对建档后事件驱动合卡——若本次写入了 IP:port 过渡孤儿，
	// 且同 IP 已有主档案，立即并入主档案（不留双卡窗口）。
	if s.cleanOrphanIPPortLocked("事件驱动") {
		_ = s.persistLocked()
	}
}

// resolveLockedAddrOnly 只按 serials/addrs 集合解析档案（不按 identity 键直查；
// PairArchive 内部用——identity 键查不到时仍希望复用同 serial 的既有档案）。
func (s *ProfileStore) resolveLockedAddrOnly(key string) *DeviceEntry {
	if key == "" {
		return nil
	}
	for _, e := range s.data.Devices {
		for _, ser := range e.Serials {
			if ser == key {
				return e
			}
		}
		for i := range e.Addrs {
			if e.Addrs[i].Addr == key {
				return e
			}
		}
	}
	return nil
}

// MdnsMatch 是 mDNS 扫描结果与档案匹配的输入条目。
type MdnsMatch struct {
	Name string // 服务实例名（tls 形如 adb-<serial>-XXXXXX；经典形如 adb-<serial>）
	Addr string // 解析出的 IP:port（可能为空）
	Mode string // 服务形态：tls / tcpip / pairing（discovery.MdnsModeOf）
}

// MdnsAddr 是 mDNS 匹配结果的地址+形态（TLS 优先分层探测用）。
type MdnsAddr struct {
	Addr string
	Mode string
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

var errBadParams = errors.New("参数超出范围")

// validateProfileParams 参数浮窗输入校验（正整数 + 上限）。
func validateProfileParams(res, fps, bitrate int) error {
	if res <= 0 || fps <= 0 || bitrate <= 0 {
		return errBadParams
	}
	if res > 8192 || fps > 480 || bitrate > 1000 {
		return errBadParams
	}
	return nil
}
