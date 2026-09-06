// Package bridge 负责 GUI 壳与 投屏支持.bat 之间的进程桥接：
// 隐藏窗口启动、GBK 输出解码、echo 行 → 界面事件分类。
package bridge

import (
	"regexp"
	"strconv"
	"strings"
)

// Kind 是 bat 输出行映射出的界面事件类型。
type Kind int

const (
	KindNone Kind = iota // 普通日志行（只进原始日志）
	KindADBReset
	KindDetect
	KindUSBFound
	KindUSBHint
	KindNoUSB
	KindWifiTry
	KindWifiOK
	KindWifiFail
	KindScanWifi
	KindGuide
	KindCasting
	KindSpec
	KindLegacy
	KindKeyboard
	KindLearning
	KindWatchOn
	KindSwitchUSB
	KindReconnect
	KindDone
	KindOfflineExit
	KindUserClose // scrcpy 用户主动关闭投屏窗口的哨兵行（SCRCPY_EZ_USER_CLOSE）
	KindError
	KindPrompt
	KindTexture // scrcpy-server INFO: Texture: WxH（真实纹理尺寸，徽标优先数据源）
)

// PromptKind 表示 bat 正在等待的 stdin 输入类型（choice/pause）。
type PromptKind int

const (
	PromptNone    PromptKind = iota
	PromptMenu123            // [1]重新检测 [2]配对向导 [3]退出
	PromptRetryQR            // Q=退出循环 R=立即重投
	PromptAnyKey             // pause 按任意键
)

func (k Kind) String() string {
	switch k {
	case KindADBReset:
		return "adb-reset"
	case KindDetect:
		return "detect"
	case KindUSBFound:
		return "usb-found"
	case KindUSBHint:
		return "usb-hint"
	case KindNoUSB:
		return "no-usb"
	case KindWifiTry:
		return "wifi-try"
	case KindWifiOK:
		return "wifi-ok"
	case KindWifiFail:
		return "wifi-fail"
	case KindScanWifi:
		return "scan-wifi"
	case KindGuide:
		return "guide"
	case KindCasting:
		return "casting"
	case KindSpec:
		return "spec"
	case KindLegacy:
		return "legacy"
	case KindKeyboard:
		return "keyboard"
	case KindLearning:
		return "learning"
	case KindWatchOn:
		return "watch-on"
	case KindSwitchUSB:
		return "switch-usb"
	case KindReconnect:
		return "reconnect"
	case KindDone:
		return "done"
	case KindOfflineExit:
		return "offline-exit"
	case KindUserClose:
		return "user-close"
	case KindError:
		return "error"
	case KindPrompt:
		return "prompt"
	case KindTexture:
		return "texture"
	default:
		return "none"
	}
}

// Spec 是从 [高清]/[流畅]/[custom] 行解析出的串流规格。
type Spec struct {
	Wired   bool   `json:"wired"`
	Legacy  bool   `json:"legacy"`
	Custom  bool   `json:"custom"`  // [custom] 行（参数浮窗覆盖回显）；非 bat 自动检测值
	Res     string `json:"res"`     // "2560x1440"
	FPS     int    `json:"fps"`     // 120
	Mbps    int    `json:"mbps"`    // 50
	MaxSize int    `json:"maxSize"` // 2560
}

// Event 是一次行分类结果。
type Event struct {
	Kind    Kind       `json:"kind"`
	Text    string     `json:"text"`
	Prompt  PromptKind `json:"prompt,omitempty"`
	Spec    *Spec      `json:"spec,omitempty"`
	Texture string     `json:"texture,omitempty"` // KindTexture：真实纹理 WxH
	// Mode 是该行隐含的投屏模式（精确短语判定）："usb"/"wifi"。
	// 与模式无关的行（"保存无线地址"等学习行、"检测到 USB 设备"等）保持空——
	// app 按"最近一次真实模式行"更新，学习行不改变模式状态（防误判）。
	Mode string `json:"mode,omitempty"`
}

var (
	reSpecRes = regexp.MustCompile(`(\d{3,4})x(\d{3,4})@(\d{1,3})Hz`)
	reSpecBr  = regexp.MustCompile(`h264/(\d{1,3})M/(\d{2,4})/(\d{1,3})fps`)
	reKeySDK  = regexp.MustCompile(`SDK=(\d+) -> (\w+) legacy=(\w*)`)
	reCustom  = regexp.MustCompile(`^\[custom\] (wired|wireless) res=(\d+) fps=(\d+) bitrate=(\d+)( \((usb|wifi)\))?$`)
	// 规格行精确格式（行首锚定 + 全格式匹配）：baseline 提取只认这三类 bat 回显行。
	// 排除 bat 的"使用默认规格"回退行（检测失败时的 50M 默认值不是设备真实规格），
	// 也天然排除任何 [server] INFO / FRAME / ABR / PULSE 日志行（行首不匹配）。
	reSpecHD   = regexp.MustCompile(`^\[高清\] 有线模式：.*(?:有线规格|兼容模式) h264/(\d{1,3})M/(\d{2,4})/(\d{1,3})fps`)
	reSpecWiFi = regexp.MustCompile(`^\[流畅\] 无线模式：.*（h264/(\d{1,3})M/(\d{2,4})/(\d{1,3})fps`)
	// scrcpy-server 日志行：只进原始日志，绝不参与分类（ABR 行含 bitrate 数字，防误匹配规格）。
	reServerLog = regexp.MustCompile(`^\[server\] |^(FRAME|ABR|PULSE):`)
	// 真实纹理行：INFO: Texture: WxH（徽标优先数据源——自定义长边档的短边按此真实值显示）
	reTexture = regexp.MustCompile(`(?i)Texture:\s*(\d{2,5})x(\d{2,5})`)
)

// ParseSpec 从规格说明文本中解析分辨率/码率。失败返回 nil。
func ParseSpec(text string) *Spec {
	s := &Spec{}
	if m := reSpecRes.FindStringSubmatch(text); m != nil {
		s.Res = m[1] + "x" + m[2]
		if f, err := strconv.Atoi(m[3]); err == nil {
			s.FPS = f
		}
	}
	if m := reSpecBr.FindStringSubmatch(text); m != nil {
		if b, err := strconv.Atoi(m[1]); err == nil {
			s.Mbps = b
		}
		if x, err := strconv.Atoi(m[2]); err == nil {
			s.MaxSize = x
		}
		if f, err := strconv.Atoi(m[3]); err == nil {
			s.FPS = f
		}
	}
	if s.Res == "" && s.Mbps == 0 && s.FPS == 0 {
		return nil
	}
	return s
}

// specFromHD 从 [高清] 有线模式行解析规格（行首锚定的精确格式，
// 兼容"有线规格"/"兼容模式"两种文案；"使用默认规格"回退行不匹配）。
func specFromHD(t string) *Spec {
	m := reSpecHD.FindStringSubmatch(t)
	if m == nil {
		return nil
	}
	mbps, _ := strconv.Atoi(m[1])
	maxSize, _ := strconv.Atoi(m[2])
	fps, _ := strconv.Atoi(m[3])
	s := &Spec{MaxSize: maxSize, FPS: fps, Mbps: mbps, Wired: true}
	if rm := reSpecRes.FindStringSubmatch(t); rm != nil {
		s.Res = rm[1] + "x" + rm[2]
	}
	if strings.Contains(t, "兼容模式") {
		s.Legacy = true
	}
	return s
}

// specFromWiFi 从 [流畅] 无线模式行解析规格（行首锚定的精确格式）。
func specFromWiFi(t string) *Spec {
	m := reSpecWiFi.FindStringSubmatch(t)
	if m == nil {
		return nil
	}
	mbps, _ := strconv.Atoi(m[1])
	maxSize, _ := strconv.Atoi(m[2])
	fps, _ := strconv.Atoi(m[3])
	return &Spec{MaxSize: maxSize, FPS: fps, Mbps: mbps, Wired: false}
}

// ParseKeyboard 解析 [键盘模式] Android SDK=34 -> uhid legacy= 行。
// 返回 (sdk 字符串, 键盘模式, legacy 标记)。
func ParseKeyboard(text string) (sdk, mode, legacy string) {
	m := reKeySDK.FindStringSubmatch(text)
	if m == nil {
		return "", "", ""
	}
	return m[1], m[2], m[3]
}

// ClassifyLine 把 bat 的一行输出（已按 GBK 解码）分类为界面事件。
// 只做前缀/关键词匹配：识别不了的行落 KindNone（原始日志），不影响投屏。
func ClassifyLine(line string) Event {
	t := strings.TrimRight(line, "\r\n")
	ev := Event{Kind: KindNone, Text: t}

	switch {
	case reTexture.MatchString(t):
		// scrcpy-server 真实纹理尺寸（INFO: Texture: WxH，可能带 [server] 前缀）：
		// 徽标优先数据源——自定义长边档的短边按此真实值显示。
		// 这是 server 日志行唯一允许参与分类的例外（只更新展示，不写 baseline）。
		if m := reTexture.FindStringSubmatch(t); m != nil {
			ev.Kind, ev.Texture = KindTexture, m[1]+"x"+m[2]
		}
	case reServerLog.MatchString(t):
		// [server] INFO / FRAME / ABR / PULSE 日志行：只进原始日志，不参与任何分类
	case strings.Contains(t, "[1] 重置 adb"):
		ev.Kind = KindADBReset
	case strings.HasPrefix(t, "[custom] "):
		// 参数浮窗覆盖回显（bat 注入按模式的 SCEZ_*_USB/SCEZ_*_WIFI 后输出，
		// 行尾带模式后缀 (usb)/(wifi)）：替换上一行 [高清]/[流畅] 的自动规格为
		// 自定义值（Res 由前端按设备比例计算）
		if m := reCustom.FindStringSubmatch(t); m != nil {
			res, _ := strconv.Atoi(m[2])
			fps, _ := strconv.Atoi(m[3])
			mbps, _ := strconv.Atoi(m[4])
			wired := m[1] == "wired"
			ev.Kind, ev.Spec = KindSpec, &Spec{MaxSize: res, FPS: fps, Mbps: mbps, Wired: wired, Custom: true}
			if wired {
				ev.Mode = "usb"
			} else {
				ev.Mode = "wifi"
			}
		}
	case strings.Contains(t, "[2] 检测 USB 设备"):
		ev.Kind = KindDetect
	case strings.Contains(t, "[OK] 检测到 USB 设备"):
		ev.Kind = KindUSBFound // 非模式短语：不改 Mode（由 [学习] 有线模式 等真实模式行定）
	case strings.Contains(t, "但未授权或未就绪"):
		ev.Kind = KindUSBHint
	case strings.Contains(t, "未检测到 USB 设备，进入无线模式"):
		ev.Kind, ev.Mode = KindNoUSB, "wifi"
	case strings.Contains(t, "[3] 无线模式") || strings.Contains(t, "尝试连接上次保存的地址"):
		ev.Kind, ev.Mode = KindWifiTry, "wifi"
	case strings.Contains(t, "[OK] 连接成功"):
		ev.Kind = KindWifiOK
	case strings.Contains(t, "连接失败或设备未就绪"):
		ev.Kind = KindWifiFail
	case strings.Contains(t, "扫描已有无线设备"):
		ev.Kind = KindScanWifi
	case strings.Contains(t, "首次连接向导"):
		ev.Kind = KindGuide
	case strings.Contains(t, "===== 开始投屏"):
		ev.Kind = KindCasting
	case reSpecHD.MatchString(t):
		// [高清] 有线模式：...有线规格 h264/NM/XX/XXfps（含兼容模式行；行首锚定，
		// "使用默认规格"回退行与 server 日志不匹配 → 不进 baseline）
		if s := specFromHD(t); s != nil {
			ev.Kind, ev.Spec, ev.Mode = KindSpec, s, "usb"
		}
	case reSpecWiFi.MatchString(t):
		// [流畅] 无线模式：...（h264/NM/XX/XXfps（行首锚定）
		if s := specFromWiFi(t); s != nil {
			ev.Kind, ev.Spec, ev.Mode = KindSpec, s, "wifi"
		}
	case strings.Contains(t, "[兼容] 老设备"):
		ev.Kind = KindLegacy
	case strings.Contains(t, "[键盘模式]"):
		ev.Kind = KindKeyboard
	case strings.Contains(t, "[学习] 有线模式"):
		// 有线模式学习行（检测手机 WiFi IP）：真实模式行 → usb
		ev.Kind, ev.Mode = KindLearning, "usb"
	case strings.Contains(t, "[学习] 开启无线调试端口") || strings.Contains(t, "[学习] 等待设备重新上线"):
		ev.Kind = KindLearning
	case strings.Contains(t, "已开启 USB 插线监测"):
		ev.Kind = KindWatchOn
	case strings.Contains(t, "检测到 USB 插线，切换至有线投屏"):
		ev.Kind = KindSwitchUSB
	case strings.Contains(t, "检测到连接断开"):
		ev.Kind = KindReconnect
	case strings.Contains(t, "SCRCPY_EZ_USER_CLOSE"):
		// scrcpy 用户主动关闭投屏窗口的哨兵行（fork 源码在 SDL_EVENT_QUIT 处
		// fprintf(stdout)）——GUI 据此立即置 closing（"正在关闭…"），
		// 与"拔线/断流/杀树"彻底分家：那些路径不会打印哨兵。
		ev.Kind = KindUserClose
	case strings.Contains(t, "投屏已结束") || strings.Contains(t, "已检测到窗口关闭"):
		ev.Kind = KindDone
	case strings.Contains(t, "已离线，投屏连接已断开") || strings.Contains(t, "退出投屏循环"):
		ev.Kind = KindOfflineExit
	case strings.Contains(t, "[失败]"):
		ev.Kind = KindError
	}

	// choice / pause 提示行（等待 stdin 输入）
	switch {
	case strings.Contains(t, "Q=退出循环") || strings.Contains(t, "R=立即重投"):
		ev.Kind = KindPrompt
		ev.Prompt = PromptRetryQR
	case strings.Contains(t, "请选择 [1]重新检测"):
		ev.Kind = KindPrompt
		ev.Prompt = PromptMenu123
	case strings.Contains(t, "投屏已结束，请选择"):
		ev.Kind = KindPrompt
		ev.Prompt = PromptMenu123
	case strings.Contains(t, "按任意键") || strings.Contains(t, "Press any key"):
		ev.Kind = KindPrompt
		ev.Prompt = PromptAnyKey
	}

	return ev
}
