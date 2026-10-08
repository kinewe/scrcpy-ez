package bridge

import "fmt"

// ModeParams 是单模式（usb/wifi）的参数浮窗覆盖。
// Set=false 表示该模式自动档：不注入该模式环境变量，bat 该模式走自身检测逻辑。
// Set=true 时 GUI 注入该模式的三个环境变量（如 usb → SCEZ_RES_USB/SCEZ_FPS_USB/
// SCEZ_BITRATE_USB），bat 在 scrcpy 启动前用这三个值覆盖该模式检测值
// （长边/fps/码率 Mbps）。
type ModeParams struct {
	Res     int
	FPS     int
	Bitrate int
	Set     bool
	// Audio 声音档位（v2.1.78 三档滑条；独立于 Set：无论是否自定义画面参数都注入）：
	// phone→--no-audio / pc→默认不加参数 / both→--audio-dup。非空即注入 SCEZ_AUDIO_*。
	Audio string
	// LockFps/LockBitrate ABR 锁定（v2.1.79 参数浮窗「锁定」）：锁定维度不被
	// ABR 自适应调整；独立于 Set——锁定=固定"当前生效值"，自动/自定义档均可锁。
	LockFps     bool
	LockBitrate bool
	// VCodec/ACodec 编码格式（v2.1.91；独立于 Set 注入——照 Audio 模式：非空即注入，
	// bat 默认视频 h264 / 音频 opus）。GUI 归一后总是给出值。
	VCodec string
	ACodec string
}

// CastParams 投屏参数覆盖（多设备 Phase 1 轮 A 起扩展为四类注入）：
//   - Usb.Set=true → 注入 SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB（bat 有线分支覆盖）；
//   - Wifi.Set=true → 注入 SCEZ_RES_WIFI/SCEZ_FPS_WIFI/SCEZ_BITRATE_WIFI（bat 无线分支覆盖）；
//   - Serial 非空 → 注入 SCEZ_SERIAL（锁定目标设备：USB serial 或 IP:port）；
//   - Addr 非空 → 注入 SCEZ_ADDR（锁定主无线地址，跳过 config 记忆/扫描）；
//   - Addr2 非空 → 注入 SCEZ_ADDR2（备用无线地址，与主地址不同形态；bat 单次降级用）；
//   - NoWatch=true → 注入 SCEZ_NO_WATCH=1（已废弃：watcher 会话化后不再需要，
//     字段保留仅用于 bat 兼容——GUI 不再生成该值，恒 false）；
//   - Market/Model 非空 → 注入 SCEZ_MARKET/SCEZ_MODEL（档案身份：bat 无线回退防抢
//     比对与 watcher 比对本尊用；仅锁定会话注入）；
//   - OverlayVisibleSet=true → 注入 SCEZ_PARAM_OVERLAY="1"/"0"（设置面板开关 A：
//     启动投屏时投屏窗口上的参数控件默认显示/隐藏；不设该字段=不注入，客户端
//     按其自身默认（显示），与旧行为一致）。
//
// 未设置（空/零值）的字段不注入，bat 走原逻辑（回归兼容）。
type CastParams struct {
	Usb            ModeParams
	Wifi           ModeParams
	Serial         string
	ExpectedSerial string // Full confirmed device serial; independent of current USB transport.
	Addr           string
	Addr2          string // SCEZ_ADDR2（备用无线地址；空=无备用）
	NoWatch        bool
	Market         string // SCEZ_MARKET（档案 marketname，仅锁定会话）
	Model          string // SCEZ_MODEL（档案 model，仅锁定会话）
	// OverlayVisible 是参数控件（fps/码率/状态浮层）的启动可见性，仅当
	// OverlayVisibleSet=true 时注入（true→"1" 显示 / false→"0" 隐藏）。
	OverlayVisible    bool
	OverlayVisibleSet bool
	// Explicitly supply the global idle-sleep choice to every cast/reconnect.
	KeepDeviceAwake    bool
	KeepDeviceAwakeSet bool

	// --- 虚拟屏（应用窗口会话；SCEZ_VD_* 整组"未设置=不注入"，零回归）---
	// bat 侧据此组装 VD_ARGS（投屏支持.bat「gui56 虚拟屏参数」段）。
	VdSize        string // SCEZ_VD_SIZE：虚拟屏尺寸 "WxH"（如 1280x720）
	VdDpi         int    // SCEZ_VD_DPI：虚拟屏 dpi（GUI 按等比公式显式算；0=不注入）
	VdFlex        bool   // SCEZ_VD_FLEX=1 → --flex-display（窗口拖动=虚拟屏跟随）
	VdIme         string // SCEZ_VD_IME → --display-ime-policy=<v>（虚拟屏推荐 local）
	VdNoDecor     bool   // SCEZ_VD_NO_DECOR=1 → --no-vd-system-decorations
	VdKeepContent bool   // SCEZ_VD_KEEP_CONTENT=1 → --no-vd-destroy-content
	VdAudio       string // SCEZ_VD_AUDIO：声音档位单套回退（v2.1.78；phone/pc/both，空=不注入）
	StartApp      string // SCEZ_START_APP：启动应用包名（调用方带 "+" 前缀=先强停再启动）
	WinTitle      string // SCEZ_WIN_TITLE：窗口标题（应用名；已安全化）

	// --- 虚拟屏两套参数（v2.1.47 应用窗口参数面板；有线/无线分别记忆）---
	// 注入 SCEZ_VD_*_USB / SCEZ_VD_*_WIFI，bat 按当前连接形态各取一套。
	// 旧单套 VdSize/VdDpi/VdFlex/VdAudio 由调用方同步注入（启动形态那套）——
	// 新旧 bat 组合兼容（旧 bat 只读旧变量）。
	VdUsb  VdModeParams
	VdWifi VdModeParams
}

// VdModeParams 是应用窗口（虚拟屏）单模式（有线/无线）参数（二期 Step 4/5）。
// 注入为 SCEZ_VD_<KEY>_USB / SCEZ_VD_<KEY>_WIFI 两套环境变量，bat 按当前连接
// 形态（PICK 含":"=无线）选一套组装 CAST_ARGS/VD_ARGS；各字段零值=不注入。
type VdModeParams struct {
	Size    string // SCEZ_VD_SIZE_*：虚拟屏初始尺寸 "WxH"（空=该套不注入）
	Dpi     int    // SCEZ_VD_DPI_*：>0 注入（GUI 按该套尺寸等比算；0=scrcpy 默认）
	FPS     int    // SCEZ_VD_FPS_*：>0 注入（bat 无值默认 60）
	Bitrate int    // SCEZ_VD_BIT_*：>0 注入（bat 无值默认 8）
	Flex    bool   // SCEZ_VD_FLEX_*=1（窗口跟随；未注入=不跟随）
	Audio   string // SCEZ_VD_AUDIO_*=值（v2.1.78 三档滑条：phone→--no-audio / both→--audio-dup；空=不加参数）
	// LockFps/LockBitrate ABR 锁定（v2.1.80 窗口设置「锁定」）：注入
	// SCEZ_VD_LOCK_FPS_*/SCEZ_VD_LOCK_BITRATE_*=1 → bat 追加 --abr-lock-*。
	LockFps     bool
	LockBitrate bool
}

// castEnv 组装注入给 bat 的环境变量表（GUI→bat→scrcpy.exe 的整条注入链）。
// 平台无关的纯函数：bat_windows.go 的 Start 直接用它，Linux 侧单测也直接覆盖
// （Windows 专属的启动/隐藏窗口逻辑不参与）。返回项均为 "K=V"：
//   - SCEZ_NO_ADB_RESET=1：GUI 已接管 adb 生命周期（track 长连/设备档案/插线学习），
//     GUI 启动的一切投屏 bat 一律跳过 adb kill-server 重置与清理，避免插线设备被
//     清掉、双长连被断。bat 独立运行时无此变量，保留原自愈逻辑；
//   - 各可选参数（未设置/零值不产生条目，bat 走原逻辑）；
//   - SCEZ_WATCH_TAG：本会话 watcher 唯一标记（多会话隔离，恒注入）。
func castEnv(params CastParams, watchTag string) []string {
	env := []string{"SCEZ_NO_ADB_RESET=1"}
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
	// 声音档位（v2.1.78 三档滑条；独立于 Set 注入——GUI 总是给出最终档，
	// bat 侧据此追加 --no-audio / --audio-dup）。
	if params.Usb.Audio != "" {
		env = append(env, "SCEZ_AUDIO_USB="+params.Usb.Audio)
	}
	if params.Wifi.Audio != "" {
		env = append(env, "SCEZ_AUDIO_WIFI="+params.Wifi.Audio)
	}
	// 编码格式（v2.1.91）：独立于 Set（照 Audio 模式）——非空即注入；
	// bat 侧按模式覆盖 VCODEC/ACODEC（默认 h264/opus）。
	if params.Usb.VCodec != "" {
		env = append(env, "SCEZ_VCODEC_USB="+params.Usb.VCodec)
	}
	if params.Usb.ACodec != "" {
		env = append(env, "SCEZ_ACODEC_USB="+params.Usb.ACodec)
	}
	if params.Wifi.VCodec != "" {
		env = append(env, "SCEZ_VCODEC_WIFI="+params.Wifi.VCodec)
	}
	if params.Wifi.ACodec != "" {
		env = append(env, "SCEZ_ACODEC_WIFI="+params.Wifi.ACodec)
	}
	// ABR 锁定（v2.1.79 参数浮窗「锁定」）：锁定维度不被自适应调整——独立于
	// Set（自动档也可锁）；bat 侧按当前连接形态追加 --abr-lock-fps/-bitrate。
	if params.Usb.LockFps {
		env = append(env, "SCEZ_LOCK_FPS_USB=1")
	}
	if params.Usb.LockBitrate {
		env = append(env, "SCEZ_LOCK_BITRATE_USB=1")
	}
	if params.Wifi.LockFps {
		env = append(env, "SCEZ_LOCK_FPS_WIFI=1")
	}
	if params.Wifi.LockBitrate {
		env = append(env, "SCEZ_LOCK_BITRATE_WIFI=1")
	}
	if params.Serial != "" {
		env = append(env, "SCEZ_SERIAL="+params.Serial)
	}
	if params.ExpectedSerial != "" {
		env = append(env, "SCEZ_EXPECT_SERIAL="+params.ExpectedSerial)
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
	// 开关 A（设置面板）：参数控件启动可见性。bat 不解析该变量——它随 cmd.exe
	// 进程树被 scrcpy.exe 继承，客户端浮层初始化时读（1=显示，0=隐藏）。
	if params.OverlayVisibleSet {
		v := "0"
		if params.OverlayVisible {
			v = "1"
		}
		env = append(env, "SCEZ_PARAM_OVERLAY="+v)
	}
	if params.KeepDeviceAwakeSet {
		v := "0"
		if params.KeepDeviceAwake {
			v = "1"
		}
		env = append(env, "SCEZ_KEEP_ACTIVE="+v)
	}
	// 虚拟屏参数（SCEZ_VD_*）：未设置不注入——bat 未定义任何 VD 变量时
	// 行为与现状完全一致（零回归）。DPI/FLEX 等按 bat 侧 if defined 判定注入。
	if params.VdSize != "" {
		env = append(env, "SCEZ_VD_SIZE="+params.VdSize)
	}
	if params.VdDpi > 0 {
		env = append(env, fmt.Sprintf("SCEZ_VD_DPI=%d", params.VdDpi))
	}
	if params.VdFlex {
		env = append(env, "SCEZ_VD_FLEX=1")
	}
	if params.VdIme != "" {
		env = append(env, "SCEZ_VD_IME="+params.VdIme)
	}
	if params.VdNoDecor {
		env = append(env, "SCEZ_VD_NO_DECOR=1")
	}
	if params.VdKeepContent {
		env = append(env, "SCEZ_VD_KEEP_CONTENT=1")
	}
	if params.VdAudio != "" {
		env = append(env, "SCEZ_VD_AUDIO="+params.VdAudio)
	}
	if params.StartApp != "" {
		env = append(env, "SCEZ_START_APP="+params.StartApp)
	}
	if params.WinTitle != "" {
		env = append(env, "SCEZ_WIN_TITLE="+params.WinTitle)
	}
	// 虚拟屏两套参数（v2.1.47）：_USB/_WIFI 各一组；Size 空=该套不注入。
	env = vdModeEnv(env, "_USB", params.VdUsb)
	env = vdModeEnv(env, "_WIFI", params.VdWifi)
	env = append(env, "SCEZ_WATCH_TAG="+watchTag)
	return env
}

// vdModeEnv 注入单套虚拟屏参数（suffix="_USB"/"_WIFI"）；Size 空=整套不注入
// （bat 侧该形态回退旧单套变量/默认档——零回归）。
func vdModeEnv(env []string, suffix string, p VdModeParams) []string {
	if p.Size == "" {
		return env
	}
	env = append(env, "SCEZ_VD_SIZE"+suffix+"="+p.Size)
	if p.Dpi > 0 {
		env = append(env, fmt.Sprintf("SCEZ_VD_DPI%s=%d", suffix, p.Dpi))
	}
	if p.FPS > 0 {
		env = append(env, fmt.Sprintf("SCEZ_VD_FPS%s=%d", suffix, p.FPS))
	}
	if p.Bitrate > 0 {
		env = append(env, fmt.Sprintf("SCEZ_VD_BIT%s=%d", suffix, p.Bitrate))
	}
	if p.Flex {
		env = append(env, "SCEZ_VD_FLEX"+suffix+"=1")
	}
	if p.Audio != "" {
		env = append(env, "SCEZ_VD_AUDIO"+suffix+"="+p.Audio)
	}
	// ABR 锁定（v2.1.80）：锁定维度不被自适应调整（bat 追加 --abr-lock-* 到 VD_ARGS）。
	if p.LockFps {
		env = append(env, "SCEZ_VD_LOCK_FPS"+suffix+"=1")
	}
	if p.LockBitrate {
		env = append(env, "SCEZ_VD_LOCK_BITRATE"+suffix+"=1")
	}
	return env
}
