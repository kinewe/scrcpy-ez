package bridge

import "testing"

// 测试样本全部取自 投屏启动.bat 的真实 echo/choice 文案（UTF-8 转存副本）。
func TestClassifyLine(t *testing.T) {
	cases := []struct {
		line   string
		kind   Kind
		prompt PromptKind
	}{
		{"[1] 重置 adb 服务...", KindADBReset, PromptNone},
		{"[2] 检测 USB 设备...", KindDetect, PromptNone},
		{"[OK] 检测到 USB 设备：Xiaomi Pad 8 Pro（24117RK2CC）", KindUSBFound, PromptNone},
		{"[提示] 检测到 USB 设备（24117RK2CC）但未授权或未就绪，", KindUSBHint, PromptNone},
		{"[提示] 未检测到 USB 设备，进入无线模式", KindNoUSB, PromptNone},
		{"[3] 无线模式：尝试连接上次保存的地址...", KindWifiTry, PromptNone},
		{"[OK] 连接成功：192.168.31.45:5555", KindWifiOK, PromptNone},
		{"[提示] 连接失败或设备未就绪：192.168.31.45:5555（请确认同一局域网且无线调试端口已开启）", KindWifiFail, PromptNone},
		{"[3] 扫描已有无线设备（IP 条目优先，自动去重）...", KindScanWifi, PromptNone},
		{"===== 首次连接向导 =====", KindGuide, PromptNone},
		{"===== 开始投屏：Redmi K80（24117RK2CC） =====", KindCasting, PromptNone},
		{"[高清] 有线模式：检测到设备 2560x1708@120Hz，有线规格 h264/50M/2560/120fps（低延迟优化，剪贴板自动同步（电脑复制即达手机））", KindSpec, PromptNone},
		{"[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps，剪贴板自动同步（电脑复制即达手机））", KindSpec, PromptNone},
		{"[兼容] 老设备使用定制 server（已自动关闭 ABR，保留图片剪贴板）", KindLegacy, PromptNone},
		{"[键盘模式] Android SDK=34 -> uhid legacy=", KindKeyboard, PromptNone},
		{"[学习] 开启无线调试端口（adb tcpip 5555）...", KindLearning, PromptNone},
		{"[学习] 等待设备重新上线（最多 15 秒）...", KindLearning, PromptNone},
		{"[自动切换] 无线投屏中，已开启 USB 插线监测（每 2 秒检测一次）", KindWatchOn, PromptNone},
		{"[自动切换] 检测到 USB 插线，切换至有线投屏...", KindSwitchUSB, PromptNone},
		{"[提示] 检测到连接断开（退出码 1），2 秒后自动重连...", KindReconnect, PromptNone},
		{"[提示] 已检测到窗口关闭（退出码 0），投屏已结束，退出投屏循环", KindDone, PromptNone},
		{"SCRCPY_EZ_USER_CLOSE", KindUserClose, PromptNone},
		{"[提示] 无线设备 192.168.31.45:5555 已离线，投屏连接已断开，退出投屏循环", KindOfflineExit, PromptNone},
		{"[失败] adb 启动失败，请确认 adb 可用", KindError, PromptNone},
		{"请选择 [1]重新检测 [2]配对向导 [3]退出：", KindPrompt, PromptMenu123},
		{"   投屏已结束，请选择：", KindPrompt, PromptMenu123},
		{"[自动切换] 2 秒后自动重新检测并投屏 Q=退出循环 R=立即重投：", KindPrompt, PromptRetryQR},
		{"请按任意键继续. . .", KindPrompt, PromptAnyKey},
		{"Press any key to continue . . .", KindPrompt, PromptAnyKey},
		{"[OK] 已保存无线地址到 config.txt：192.168.31.45:5555", KindNone, PromptNone},
		{"", KindNone, PromptNone},
		{"完全无关的行", KindNone, PromptNone},
	}
	for _, c := range cases {
		ev := ClassifyLine(c.line)
		if ev.Kind != c.kind {
			t.Errorf("ClassifyLine(%q).Kind = %v, want %v", c.line, ev.Kind, c.kind)
		}
		if c.prompt != PromptNone && ev.Prompt != c.prompt {
			t.Errorf("ClassifyLine(%q).Prompt = %v, want %v", c.line, ev.Prompt, c.prompt)
		}
	}
}

func TestParseSpec(t *testing.T) {
	s := ParseSpec("检测到设备 2560x1708@120Hz，有线规格 h264/50M/2560/120fps")
	if s == nil {
		t.Fatal("ParseSpec 返回 nil")
	}
	if s.Res != "2560x1708" || s.FPS != 120 || s.Mbps != 50 || s.MaxSize != 2560 {
		t.Fatalf("ParseSpec 解析错误: %+v", s)
	}

	s = ParseSpec("（h264/15M/1920/60fps，剪贴板自动同步）")
	if s == nil || s.Mbps != 15 || s.FPS != 60 {
		t.Fatalf("无线规格解析错误: %+v", s)
	}

	s = ParseSpec("兼容模式 h264/6M/720/24fps（老设备）")
	if s == nil || s.Mbps != 6 || s.MaxSize != 720 || s.FPS != 24 {
		t.Fatalf("兼容规格解析错误: %+v", s)
	}

	if ParseSpec("没有规格的文字") != nil {
		t.Fatal("无规格文本应返回 nil")
	}
}

func TestParseKeyboard(t *testing.T) {
	sdk, mode, legacy := ParseKeyboard("[键盘模式] Android SDK=34 -> uhid legacy=")
	if sdk != "34" || mode != "uhid" || legacy != "" {
		t.Fatalf("解析错误: %q %q %q", sdk, mode, legacy)
	}
	sdk, mode, legacy = ParseKeyboard("[键盘模式] Android SDK=28 -> sdk legacy=1")
	if sdk != "28" || mode != "sdk" || legacy != "1" {
		t.Fatalf("解析错误: %q %q %q", sdk, mode, legacy)
	}
	if sdk, _, _ := ParseKeyboard("无关行"); sdk != "" {
		t.Fatal("无关行应返回空")
	}
}

// 规格行严格匹配（修复 ABR 日志污染）：只认行首锚定的精确格式
// [高清] 有线模式：...有线规格 h264/NM/XX/XXfps / 兼容模式 …；
// [流畅] 无线模式：...（h264/NM/XX/XXfps；[custom] …。
// [server] INFO / FRAME / ABR / PULSE 日志行与 bat 的"使用默认规格"回退行
// 一律不得分类为规格（baseline 提取只认真实检测值）。
func TestClassifySpecStrict(t *testing.T) {
	// 正常规格行
	ev := ClassifyLine("[高清] 有线模式：检测到设备 2136x3200@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化）")
	if ev.Kind != KindSpec || ev.Spec == nil || !ev.Spec.Wired || ev.Spec.Mbps != 80 ||
		ev.Spec.MaxSize != 2560 || ev.Spec.FPS != 120 || ev.Spec.Res != "2136x3200" {
		t.Fatalf("[高清] 规格行解析错误: %+v", ev)
	}
	ev = ClassifyLine("[高清] 有线模式：兼容模式 h264/6M/720/24fps（老设备）")
	if ev.Kind != KindSpec || ev.Spec == nil || !ev.Spec.Wired || !ev.Spec.Legacy ||
		ev.Spec.Mbps != 6 || ev.Spec.MaxSize != 720 || ev.Spec.FPS != 24 {
		t.Fatalf("兼容规格行解析错误: %+v", ev)
	}
	ev = ClassifyLine("[高清] 有线模式：检测到设备 2560x1708@30Hz，有线规格 h264/50M/2560/30fps（老设备限 30fps）")
	if ev.Kind != KindSpec || ev.Spec == nil || ev.Spec.FPS != 30 || ev.Spec.Mbps != 50 {
		t.Fatalf("老设备限速规格行解析错误: %+v", ev)
	}
	ev = ClassifyLine("[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps，剪贴板自动同步）")
	if ev.Kind != KindSpec || ev.Spec == nil || ev.Spec.Wired || ev.Spec.Mbps != 15 ||
		ev.Spec.MaxSize != 1920 || ev.Spec.FPS != 60 {
		t.Fatalf("[流畅] 规格行解析错误: %+v", ev)
	}

	// 不得误判为规格的行（含数字也不进 baseline）
	noSpec := []string{
		"[高清] 有线模式：设备规格读取失败，使用默认规格 h264/50M/2560/120fps（低延迟优化）",
		"[server] INFO: FRAME: type=I pts=639774221488 delayDelta=-1ms size=88KB",
		"[server] INFO: ABR: bitrate 80000000 -> 56000000",
		"[server] INFO: ABR: bitrate 50000000 -> 39200000",
		"[server] INFO: PULSE: pts=639777249807",
		"[server] INFO: Device: [Xiaomi] Xiaomi 25091RP04C (Android 16)",
		"FRAME: type=P pts=639775223012 delayDelta=72ms size=5KB",
		"ABR: bitrate 50000000 -> 80000000 bps",
		"PULSE: pts=123",
	}
	for _, l := range noSpec {
		if ev := ClassifyLine(l); ev.Kind == KindSpec || ev.Spec != nil {
			t.Errorf("误判为规格行: %q -> %+v", l, ev)
		}
	}
}

// 真实纹理行（徽标统一 WxH 的数据源）：INFO: Texture: WxH → KindTexture；
// 带 [server] 前缀变体同样识别；不含 Texture 的行不误判。
func TestClassifyTextureLine(t *testing.T) {
	ev := ClassifyLine("INFO: Texture: 1920x1280")
	if ev.Kind != KindTexture || ev.Texture != "1920x1280" {
		t.Fatalf("Texture 行解析错误: %+v", ev)
	}
	ev = ClassifyLine("[server] INFO: Texture: 1080x720")
	if ev.Kind != KindTexture || ev.Texture != "1080x720" {
		t.Fatalf("[server] Texture 行解析错误: %+v", ev)
	}
	ev = ClassifyLine("INFO: Texture: 2560x1708")
	if ev.Kind != KindTexture || ev.Texture != "2560x1708" {
		t.Fatalf("有线 Texture 行解析错误: %+v", ev)
	}
	// 其他 server 行仍不参与分类
	if ev := ClassifyLine("[server] INFO: FRAME: type=P pts=1 size=5KB"); ev.Kind == KindTexture || ev.Texture != "" {
		t.Fatalf("FRAME 行不应识别为 Texture: %+v", ev)
	}
	if ev := ClassifyLine("ABR: bitrate 50000000 -> 39200000"); ev.Kind == KindTexture || ev.Texture != "" {
		t.Fatalf("ABR 行不应识别为 Texture: %+v", ev)
	}
}

// 模式判定精确化（修复"保存无线地址"误判无线模式）：
// 只有真实模式行携带 Mode；学习行（保存无线地址/检测到手机 WiFi IP）与
// "检测到 USB 设备""无线设备已离线"等非模式短语一律 Mode 空、不改变模式状态。
func TestClassifyModePrecise(t *testing.T) {
	cases := []struct {
		line string
		kind Kind
		mode string
	}{
		{"[1] 重置 adb 服务...", KindADBReset, ""},
		{"[2] 检测 USB 设备...", KindDetect, ""},
		{"[OK] 检测到 USB 设备：Xiaomi Pad 8 Pro（a743e1df）", KindUSBFound, ""}, // 非模式短语
		{"[学习] 有线模式：检测手机 WiFi IP...", KindLearning, "usb"},
		{"[OK] 检测到手机 WiFi IP：192.168.31.197", KindNone, ""},
		{"[OK] 已保存无线地址到 config.txt：192.168.31.162:5555", KindNone, ""}, // 关键：不改模式
		{"===== 开始投屏：Xiaomi Pad 8 Pro（a743e1df） =====", KindCasting, ""},
		{"[高清] 有线模式：检测到设备 3200x2136@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化）", KindSpec, "usb"},
		{"[custom] wired res=2560 fps=120 bitrate=80 (usb)", KindSpec, "usb"},
	}
	for _, c := range cases {
		ev := ClassifyLine(c.line)
		if ev.Kind != c.kind || ev.Mode != c.mode {
			t.Errorf("ClassifyLine(%q) = kind %v mode %q, want %v %q", c.line, ev.Kind, ev.Mode, c.kind, c.mode)
		}
	}
	// 无线流程真实模式行
	if ev := ClassifyLine("[提示] 未检测到 USB 设备，进入无线模式"); ev.Kind != KindNoUSB || ev.Mode != "wifi" {
		t.Fatalf("进入无线模式行应 mode=wifi: %+v", ev)
	}
	if ev := ClassifyLine("[3] 无线模式：尝试连接上次保存的地址..."); ev.Kind != KindWifiTry || ev.Mode != "wifi" {
		t.Fatalf("[3] 无线模式行应 mode=wifi: %+v", ev)
	}
	if ev := ClassifyLine("[流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps）"); ev.Kind != KindSpec || ev.Mode != "wifi" {
		t.Fatalf("[流畅] 行应 mode=wifi: %+v", ev)
	}
	if ev := ClassifyLine("[custom] wireless res=1080 fps=60 bitrate=15 (wifi)"); ev.Kind != KindSpec || ev.Mode != "wifi" {
		t.Fatalf("[custom] wireless 应 mode=wifi: %+v", ev)
	}
	// 非模式短语不得携带 Mode
	for _, l := range []string{
		"[提示] 已保存无线地址到 config.txt：192.168.31.162:5555",
		"[提示] 无线设备 192.168.31.162:5555 已离线，投屏连接已断开，退出投屏循环",
		"[提示] 检测到 USB 设备（601c9f08）但未授权或未就绪，",
		"[OK] 检测到手机 WiFi IP：192.168.31.197",
	} {
		if ev := ClassifyLine(l); ev.Mode != "" {
			t.Errorf("非模式短语不应带 Mode: %q -> %+v", l, ev)
		}
	}
}

// 参数浮窗覆盖回显：[custom] wired/wireless res= fps= bitrate= → KindSpec。
// 行尾带模式后缀 (usb)/(wifi)（按模式分离后的新格式）与旧格式都兼容。
func TestClassifyCustomParams(t *testing.T) {
	ev := ClassifyLine("[custom] wired res=2400 fps=75 bitrate=55")
	if ev.Kind != KindSpec || ev.Spec == nil || !ev.Spec.Wired ||
		ev.Spec.MaxSize != 2400 || ev.Spec.FPS != 75 || ev.Spec.Mbps != 55 {
		t.Fatalf("有线自定义行解析错误: %+v", ev)
	}
	ev = ClassifyLine("[custom] wireless res=1920 fps=60 bitrate=15")
	if ev.Kind != KindSpec || ev.Spec == nil || ev.Spec.Wired ||
		ev.Spec.MaxSize != 1920 || ev.Spec.FPS != 60 || ev.Spec.Mbps != 15 {
		t.Fatalf("无线自定义行解析错误: %+v", ev)
	}
	// 新格式（行尾模式后缀）
	ev = ClassifyLine("[custom] wired res=2400 fps=75 bitrate=55 (usb)")
	if ev.Kind != KindSpec || ev.Spec == nil || !ev.Spec.Wired ||
		ev.Spec.MaxSize != 2400 || ev.Spec.FPS != 75 || ev.Spec.Mbps != 55 {
		t.Fatalf("有线自定义行(usb)解析错误: %+v", ev)
	}
	ev = ClassifyLine("[custom] wireless res=1920 fps=60 bitrate=15 (wifi)")
	if ev.Kind != KindSpec || ev.Spec == nil || ev.Spec.Wired ||
		ev.Spec.MaxSize != 1920 || ev.Spec.FPS != 60 || ev.Spec.Mbps != 15 {
		t.Fatalf("无线自定义行(wifi)解析错误: %+v", ev)
	}
	if ev := ClassifyLine("[custom] 无关行"); ev.Kind != KindNone {
		t.Fatalf("非自定义行不应误判: %+v", ev)
	}
}
