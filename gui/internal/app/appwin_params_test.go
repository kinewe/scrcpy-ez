package app

// 应用窗口参数（v2.1.47 二期 Step 4/5：面板保存 + 档案记忆 + 有线/无线两套注入 + 重启生效）。
// 覆盖：①无档案=默认档两套注入+快照形态 ②档案覆盖=按套注入（含按套等比 dpi）+
// 旧单套字段同步 ③保存往返（只改当前形态那套、两套独立）④值==默认→条目删除
// ⑤"恢复默认"（size 空）→条目删除 ⑥非法输入拒绝 ⑦保存后自动重启（新参数重开）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/bridge"
)

// newAppWinParamsEnv：带档案路径的装置（预置 K80 档案 + 物理参数 3200px@600dpi）。
func newAppWinParamsEnv(t *testing.T) *appWinEnv {
	t.Helper()
	e := &appWinEnv{f: &fakeRunner{exitCode: -1}}
	e.a = New(Config{BatPath: `C:\x\投屏支持.bat`, AdbPath: `C:\x\adb.exe`,
		Version: "test", ProfilesPath: filepath.Join(t.TempDir(), "profiles.json")})
	e.a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		e.exitFn = onExit
		e.lineFn = onLine
		return e.f, nil
	})
	e.a.physMu.Lock()
	e.a.physCache["device:K80"] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
	e.a.physMu.Unlock()
	if err := saveLegacyFixture(e.a.profiles, "K80", DefaultProfile()); err != nil {
		t.Fatal(err)
	}
	return e
}

// paramAt 安全读第 i 次 Start 的参数（paramsLog 有锁保护）。
func (f *fakeRunner) paramAt(t *testing.T, i int) bridge.CastParams {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.paramsLog) {
		t.Fatalf("paramsLog 只有 %d 条，取第 %d 条", len(f.paramsLog), i)
	}
	return f.paramsLog[i]
}

// ①无档案：默认档两套注入 + 旧字段同步（启动形态那套）+ 快照带形态。
func TestAppWinDefaultInjectionTwoSets(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb", Res: "3200x1440", FPS: 120}})
	if err := e.a.StartAppWin("K80", "com.android.browser", "浏览器"); err != nil {
		t.Fatal(err)
	}
	p := e.f.waitParams(t, 1)
	// 两套默认档（v2.1.48 共享主投屏 baseline；v2.1.51 长边 snap 档位表）：
	// 有线=设备长边 3200 → snap 2560（尺寸 2560x1152，dpi=600×2560÷3200=480）/120/60；
	// 无线=1920/60/15（尺寸 1920x864，dpi=360)。
	wantUsb := bridge.VdModeParams{Size: "2560x1152", Dpi: 480, FPS: 120, Bitrate: 60, Flex: true, AutoDpi: true, Audio: "phone"}
	if p.VdUsb != wantUsb {
		t.Fatalf("有线套默认档注入异常: %+v", p.VdUsb)
	}
	wantWifi := bridge.VdModeParams{Size: "1920x864", Dpi: 360, FPS: 60, Bitrate: 15, Flex: true, AutoDpi: true, Audio: "phone"}
	if p.VdWifi != wantWifi {
		t.Fatalf("无线套默认档注入异常: %+v", p.VdWifi)
	}
	// 旧单套字段=启动形态（USB）那套（新旧 bat 组合兼容）。
	if p.VdSize != "2560x1152" || p.VdDpi != 480 || !p.VdFlex || !p.VdAutoDpi || p.VdAudio != "phone" {
		t.Fatalf("旧单套字段同步异常: size=%s dpi=%d flex=%v audio=%s", p.VdSize, p.VdDpi, p.VdFlex, p.VdAudio)
	}
	list := waitAppWins(t, e.a, 1)
	if list[0].Mode != "usb" {
		t.Fatalf("快照 Mode=%q，期望 usb", list[0].Mode)
	}

	// 无线形态：Mode=wifi、旧字段同步无线套。
	setDevices(e.a, []adb.Device{{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Identity: e.a.profiles.ResolveKey("K80"), Res: "3200x1440", FPS: 120}})
	if err := e.a.StartAppWin("192.168.31.197:5555", "pkg.w", "W"); err != nil {
		t.Fatal(err)
	}
	p2 := e.f.waitParams(t, 2)
	if p2.VdSize != "1920x864" || p2.VdDpi != 360 {
		t.Fatalf("无线形态旧字段同步异常: %+v", p2)
	}
	list2 := waitAppWins(t, e.a, 2)
	found := false
	for _, it := range list2 {
		if it.Pkg == "pkg.w" {
			found = true
			if it.Mode != "wifi" {
				t.Fatalf("无线会话 Mode=%q，期望 wifi", it.Mode)
			}
		}
	}
	if !found {
		t.Fatal("快照里找不到 pkg.w")
	}
}

// ②档案覆盖：按套注入（dpi 按该套尺寸现算）+ 另一套保持默认。
func TestAppWinArchivedInjection(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb", Res: "3200x1440", FPS: 120}})
	// 预置档案：有线套 Size 用**旧格式 "1920x1080"**（v2.1.74 迁移路径：读取归一化为
	// 长边 "1920" → 注入时按设备宽高比换算 WxH）/ 90fps / 16M / flex 开 / 声音=手机档（无线套留空=未设置）。
	ap := AppWinParams{}
	ap.Usb = AppWinModeParams{Size: "1920x1080", FPS: 90, Bitrate: 16, Flex: true, Audio: "phone"}
	if err := e.a.profiles.SetAppParams(fixtureArchiveKey(e.a.profiles, "K80"), "com.android.browser", ap); err != nil {
		t.Fatal(err)
	}
	if err := e.a.StartAppWin("K80", "com.android.browser", "浏览器"); err != nil {
		t.Fatal(err)
	}
	p := e.f.waitParams(t, 1)
	// 注入尺寸=长边 1920 按设备 3200x1440 换算：1920x864（旧档 "1920x1080" 的短边不复用）。
	if p.VdUsb.Size != "1920x864" || p.VdUsb.FPS != 90 || p.VdUsb.Bitrate != 16 || !p.VdUsb.Flex || p.VdUsb.Audio != "phone" {
		t.Fatalf("有线套档案注入异常: %+v", p.VdUsb)
	}
	// dpi 按该套尺寸：600×1920÷3200 = 360。
	if p.VdUsb.Dpi != 360 {
		t.Fatalf("有线套 dpi=%d，期望 360", p.VdUsb.Dpi)
	}
	// 无线套仍默认档（设备推导：1920/60/15 → 1920x864）。
	if p.VdWifi.Size != "1920x864" || p.VdWifi.FPS != 60 || p.VdWifi.Bitrate != 15 || !p.VdWifi.Flex {
		t.Fatalf("无线套应保持默认档: %+v", p.VdWifi)
	}
	// 旧字段=启动形态（usb）套。
	if p.VdSize != "1920x864" || p.VdDpi != 360 || p.VdAudio != "phone" {
		t.Fatalf("旧字段同步异常: size=%s dpi=%d audio=%s", p.VdSize, p.VdDpi, p.VdAudio)
	}
}

// 自定义初始比例按现有长边换算；未设置比例仍使用原设备比例算法。
func TestAppWinInitialRatioInjectionAndPersistence(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb", Res: "3200x1440", FPS: 120}})
	if err := e.a.SaveAppWinParams("K80", "pkg.square",
		`{"mode":"usb","size":"1920","fps":60,"bitrate":8,"flex":true,"ratioW":1,"ratioH":1}`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(e.a.profiles.Path())
	if err != nil || !strings.Contains(string(raw), `"ratioW": 1`) || !strings.Contains(string(raw), `"ratioH": 1`) {
		t.Fatalf("自定义比例未持久化: %s err=%v", raw, err)
	}
	if err := e.a.StartAppWin("K80", "pkg.square", "Square"); err != nil {
		t.Fatal(err)
	}
	p := e.f.waitParams(t, 1)
	if p.VdUsb.Size != "1920x1920" || p.VdUsb.Dpi != 360 {
		t.Fatalf("自定义 1:1 未按长边注入: %+v", p.VdUsb)
	}
	if p.VdWifi.Size != "1920x864" {
		t.Fatalf("无线未设置比例时应跟随设备比例: %+v", p.VdWifi)
	}
	v, err := e.a.GetAppWinParams("K80", "pkg.square")
	if err != nil || v.Usb.RatioW != 1 || v.Usb.RatioH != 1 {
		t.Fatalf("比例档案读取失败: %+v err=%v", v.Usb, err)
	}
}

func TestAppWinInitialRatioOrientation(t *testing.T) {
	if got := appWinInitialSize("3200x1440", 1600, 9, 16); got != "900x1600" {
		t.Fatalf("竖屏比例方向丢失: got %s want 900x1600", got)
	}
	if got := appWinInitialSize("3200x1440", 1600, 0, 0); got != "1600x720" {
		t.Fatalf("空比例应保持设备比例: got %s want 1600x720", got)
	}
}

func TestNotificationWindowInitialDirectionPreservesProfiles(t *testing.T) {
	phys := devPhys{width: 1440, height: 3200, longSide: 3200, dpi: 600}
	p := AppWinModeParams{Size: "1920", FPS: 120, Bitrate: 80, Flex: true, Audio: "phone", LockFps: true}
	got := notificationVdParamsToBridge(p, phys, "3200x1440")
	if got.Size != "864x1920" || got.Dpi != 360 || got.FPS != 120 || got.Bitrate != 80 || !got.Flex || !got.AutoDpi || !got.LockFps {
		t.Fatalf("notification should use phone direction and retain encoding/density: %+v", got)
	}
	if ordinary := vdParamsToBridge(p, phys, "3200x1440"); ordinary.Size != "1920x864" {
		t.Fatalf("ordinary launch changed: %+v", ordinary)
	}
	if p.RatioW != 0 || p.RatioH != 0 {
		t.Fatal("launch must not rewrite saved profile")
	}
	landscape := devPhys{width: 3200, height: 2136, longSide: 3200, dpi: 440}
	if got := notificationVdParamsToBridge(p, landscape, "3200x2136"); got.Size != "1920x1280" {
		t.Fatalf("natural landscape device direction lost: %+v", got)
	}
	p.RatioW, p.RatioH = 16, 9
	if got := notificationVdParamsToBridge(p, phys, "3200x1440"); got.Size != "1920x1080" {
		t.Fatalf("explicit landscape ratio lost: %+v", got)
	}
	p.RatioW, p.RatioH = 0, 0
	if got := notificationVdParamsToBridge(p, devPhys{}, "3200x1440"); got.Size != "864x1920" {
		t.Fatalf("cold cache should keep phone ratio in portrait: %+v", got)
	}
}

func TestWeChatNotificationPreservesPhoneResourcesAndVideoProfile(t *testing.T) {
	phys := devPhys{width: 1440, height: 3200, longSide: 3200, dpi: 600}
	p := AppWinModeParams{Size: "1920", FPS: 60, Bitrate: 15, Flex: true, Audio: "phone"}
	got := notificationVdParamsForPackage("com.tencent.mm", p, phys, "3200x1440")
	if !got.MatchPhone || got.MaxSize != 1920 || got.Dpi != 0 || got.Flex || got.AutoDpi || got.FPS != 60 || got.Bitrate != 15 {
		t.Fatalf("WeChat compatibility lost video profile or changed resource density: %+v", got)
	}
	if cold := notificationVdParamsForPackage("com.tencent.mm", p, devPhys{}, ""); !cold.MatchPhone || cold.MaxSize != 1920 {
		t.Fatalf("cold cache reverted to a different display density: %+v", cold)
	}
	if other := notificationVdParamsForPackage("com.android.settings", p, phys, "3200x1440"); other.MatchPhone || other.MaxSize != 0 || !other.Flex {
		t.Fatalf("another app inherited WeChat compatibility: %+v", other)
	}
	p.Dpi = 320
	if manual := notificationVdParamsForPackage("com.tencent.mm", p, phys, "3200x1440"); manual.MatchPhone || manual.Dpi != 320 || !manual.Flex {
		t.Fatalf("explicit DPI ignored: %+v", manual)
	}
	p.Dpi, p.RatioW, p.RatioH = 0, 16, 9
	if manual := notificationVdParamsForPackage("com.tencent.mm", p, phys, "3200x1440"); manual.MatchPhone || manual.Size != "1920x1080" {
		t.Fatalf("explicit aspect ratio ignored: %+v", manual)
	}
}

func TestAppWindowManualDensityDoesNotFollowFlexResize(t *testing.T) {
	phys := devPhys{width: 1440, height: 3200, longSide: 3200, dpi: 600}
	p := AppWinModeParams{Size: "1920", Flex: true, Dpi: 320}
	ordinary := vdParamsToBridge(p, phys, "3200x1440")
	notification := notificationVdParamsToBridge(p, phys, "3200x1440")
	if ordinary.Dpi != 320 || ordinary.AutoDpi || notification.Dpi != 320 || notification.AutoDpi {
		t.Fatalf("manual density must remain fixed for either entry: ordinary=%+v notification=%+v", ordinary, notification)
	}
	p.Dpi, p.Flex = 0, false
	if fixed := notificationVdParamsToBridge(p, phys, "3200x1440"); fixed.Dpi != 360 || fixed.AutoDpi {
		t.Fatalf("non-flex display enabled resize scaling: %+v", fixed)
	}
}

// ③保存往返：只改当前形态那套（另一套档案原值不动）+ 落盘（payload 旧格式文本
// "1600x900" 也兼容——落档统一长边格式 "1600"）。
func TestSaveAppWinParamsRoundtrip(t *testing.T) {
	e := newAppWinParamsEnv(t)
	// 保存有线套：1600x900（旧格式文本，兼容入）/ 120fps / 32M / flex 关。
	if err := e.a.SaveAppWinParams("K80", "pkg.a",
		`{"mode":"usb","size":"1600x900","fps":120,"bitrate":32,"flex":false,"audio":"pc"}`); err != nil {
		t.Fatal(err)
	}
	v, err := e.a.GetAppWinParams("K80", "pkg.a")
	if err != nil {
		t.Fatal(err)
	}
	if v.Usb.Size != "1600" || v.Usb.FPS != 120 || v.Usb.Bitrate != 32 || v.Usb.Flex {
		t.Fatalf("有线套保存异常: %+v", v.Usb)
	}
	if !v.UsbSet || v.WifiSet {
		t.Fatalf("Set 标记异常: usbSet=%v wifiSet=%v（期望 true/false）", v.UsbSet, v.WifiSet)
	}
	if v.Wifi.Size != "1920" || v.Wifi.FPS != 60 || v.Wifi.Bitrate != 15 || !v.Wifi.Flex {
		t.Fatalf("无线套不应被有线保存影响（应为设备默认档 长边1920/60/15）: %+v", v.Wifi)
	}
	// 落盘校验（新格式：长边数字字符串）。
	raw, err := os.ReadFile(e.a.profiles.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"1600"`) {
		t.Fatalf("档案未落盘: %s", string(raw))
	}
	// 再保存无线套 → 两套独立（纯数字长边入=新格式主路径）。
	if err := e.a.SaveAppWinParams("K80", "pkg.a",
		`{"mode":"wifi","size":"960","fps":30,"bitrate":4,"flex":true,"audio":"both"}`); err != nil {
		t.Fatal(err)
	}
	v2, _ := e.a.GetAppWinParams("K80", "pkg.a")
	if v2.Wifi.Size != "960" || v2.Wifi.FPS != 30 || v2.Wifi.Bitrate != 4 || v2.Wifi.Audio != "both" {
		t.Fatalf("无线套保存异常: %+v", v2.Wifi)
	}
	if v2.Usb.Size != "1600" || v2.Usb.FPS != 120 {
		t.Fatalf("有线套不应被无线保存影响: %+v", v2.Usb)
	}
}

// ④⑤：值==默认 → 自动视为未设置；"恢复默认"（size 空）→ 条目删除。
func TestSaveAppWinParamsCompactAndReset(t *testing.T) {
	e := newAppWinParamsEnv(t)
	def := e.a.appWinDefaults("K80")

	// ④ 保存值恰好=设备默认档 → 不落条目（pkg.c 不出现）。
	payloadDefault := fmt.Sprintf(`{"mode":"usb","size":"%s","fps":%d,"bitrate":%d,"flex":true,"audio":"phone"}`,
		def.Usb.Size, def.Usb.FPS, def.Usb.Bitrate)
	if err := e.a.SaveAppWinParams("K80", "pkg.c", payloadDefault); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(e.a.profiles.Path())
	if strings.Contains(string(raw), "pkg.c") {
		t.Fatalf("值==设备默认档不应落条目: %s", string(raw))
	}

	// ⑤ 先存自定义（旧格式文本入，兼容）→ 再"恢复默认"（size 空）→ 条目删除。
	if err := e.a.SaveAppWinParams("K80", "pkg.b",
		`{"mode":"usb","size":"1920x1080","fps":90,"bitrate":16,"flex":true,"audio":"phone"}`); err != nil {
		t.Fatal(err)
	}
	raw1, _ := os.ReadFile(e.a.profiles.Path())
	if !strings.Contains(string(raw1), `"1920"`) {
		t.Fatalf("自定义未落盘: %s", string(raw1))
	}
	if err := e.a.SaveAppWinParams("K80", "pkg.b",
		`{"mode":"usb","size":"","fps":0,"bitrate":0,"flex":false,"audio":"phone"}`); err != nil {
		t.Fatal(err)
	}
	v, _ := e.a.GetAppWinParams("K80", "pkg.b")
	if v.Usb.Size != def.Usb.Size || v.Usb.FPS != def.Usb.FPS {
		t.Fatalf("恢复默认后读数异常: %+v（期望设备默认 %+v）", v.Usb, def.Usb)
	}
	raw2, _ := os.ReadFile(e.a.profiles.Path())
	if strings.Contains(string(raw2), "pkg.b") {
		t.Fatalf("恢复默认后条目未删除: %s", string(raw2))
	}
}

// ⑥非法输入拒绝（尺寸格式/范围/模式）。
func TestSaveAppWinParamsValidation(t *testing.T) {
	e := newAppWinParamsEnv(t)
	bad := []string{
		`{"mode":"xx","size":"1280x720","fps":60,"bitrate":8,"flex":true}`,
		`{"mode":"usb","size":"abc","fps":60,"bitrate":8}`,
		`{"mode":"usb","size":"12x","fps":60,"bitrate":8}`,
		`{"mode":"usb","size":"1280x720","fps":0,"bitrate":8}`,
		`{"mode":"usb","size":"1280x720","fps":60,"bitrate":0}`,
		`{"mode":"usb","size":"1280x720","fps":60,"bitrate":999}`,
		`{"mode":"usb","size":"1280x720","fps":999,"bitrate":8}`,
		`{"mode":"usb","size":"1280","fps":60,"bitrate":8,"ratioW":16}`,
		`{"mode":"usb","size":"1280","fps":60,"bitrate":8,"ratioW":-1,"ratioH":9}`,
		`{"mode":"usb","size":"1280","fps":60,"bitrate":8,"ratioW":10001,"ratioH":9}`,
		`不是json`,
	}
	for _, payload := range bad {
		if err := e.a.SaveAppWinParams("K80", "pkg.e", payload); err == nil {
			t.Fatalf("非法输入应报错: %s", payload)
		}
	}
}

// ⑦保存后自动重启：Stop 受理 → bat 退出 → 新参数重开（第二次 Start 用新档）。
func TestSaveAppWinParamsRestartsSession(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("K80", "pkg.r", "R"); err != nil {
		t.Fatal(err)
	}
	e.f.waitStarts(t, 1)

	if err := e.a.SaveAppWinParams("K80", "pkg.r",
		`{"mode":"usb","size":"1920x1080","fps":90,"bitrate":16,"flex":true,"audio":"phone"}`); err != nil {
		t.Fatal(err)
	}
	// 重启链：Stop 被调 → 模拟 bat 退出 → 新参数重开。
	e.f.waitStops(t, 1)
	e.fireExit(1)

	deadline := time.Now().Add(3 * time.Second)
	for e.f.startsN() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e.f.startsN() < 2 {
		t.Fatal("保存后未自动重开（starts<2）")
	}
	p2 := e.f.paramAt(t, 1)
	if p2.VdUsb.Size != "1920x1080" || p2.VdUsb.FPS != 90 || p2.VdUsb.Bitrate != 16 || p2.VdUsb.Dpi != 360 {
		t.Fatalf("重开参数非新档: %+v", p2.VdUsb)
	}
	// 快照里会话还在（新会话）。
	list := waitAppWins(t, e.a, 1)
	if list[0].Pkg != "pkg.r" || list[0].Closing {
		t.Fatalf("重开后快照异常: %+v", list[0])
	}
}

// ⑧ v2.1.48：默认档共享主投屏 baseline——长边按设备宽高比换算成 WxH + fps/码率。
func TestAppWinDefaultsShareMainCastBaseline(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb", Res: "2560x1708", FPS: 60}})
	e.a.updateBaseline("K80", "usb", Baseline{Res: 2560, FPS: 120, Bitrate: 60})
	e.a.updateBaseline("K80", "wifi", Baseline{Res: 1920, FPS: 60, Bitrate: 15})
	if err := e.a.StartAppWin("K80", "pkg.p", "P"); err != nil {
		t.Fatal(err)
	}
	p := e.f.waitParams(t, 1)
	wantUsbSize := mdns10ProfileRes("2560x1708", 2560)  // "2560x1708"
	wantWifiSize := mdns10ProfileRes("2560x1708", 1920) // "1920x1280"（截断 1281 → 取偶）
	if p.VdUsb.Size != wantUsbSize || p.VdUsb.FPS != 120 || p.VdUsb.Bitrate != 60 {
		t.Fatalf("有线套未共享 baseline: %+v（期望 %s/120/60）", p.VdUsb, wantUsbSize)
	}
	if p.VdWifi.Size != wantWifiSize || p.VdWifi.FPS != 60 || p.VdWifi.Bitrate != 15 {
		t.Fatalf("无线套未共享 baseline: %+v（期望 %s/60/15）", p.VdWifi, wantWifiSize)
	}
	// dpi 按各套尺寸等比：usb=600×2560÷3200=480；wifi=600×1920÷3200=360。
	if p.VdUsb.Dpi != 480 || p.VdWifi.Dpi != 360 {
		t.Fatalf("dpi 异常: usb=%d wifi=%d", p.VdUsb.Dpi, p.VdWifi.Dpi)
	}
}

// ⑨ v2.1.48：compact 口径=设备默认档——「恢复默认」保存即删条目（含对照）。
func TestSaveAppWinParamsCompactUsesDeviceDefaults(t *testing.T) {
	e := newAppWinParamsEnv(t)
	def := e.a.appWinDefaults("K80")
	// 保存值==设备默认（无线套）→ 不落条目。
	payload := fmt.Sprintf(`{"mode":"wifi","size":"%s","fps":%d,"bitrate":%d,"flex":true,"audio":"phone"}`,
		def.Wifi.Size, def.Wifi.FPS, def.Wifi.Bitrate)
	if err := e.a.SaveAppWinParams("K80", "pkg.d", payload); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(e.a.profiles.Path())
	if strings.Contains(string(raw), "pkg.d") {
		t.Fatalf("值==设备默认档不应落条目: %s", string(raw))
	}
	// 对照组：码率+1（非默认）→ 落条目。
	payload2 := fmt.Sprintf(`{"mode":"wifi","size":"%s","fps":%d,"bitrate":%d,"flex":true,"audio":"phone"}`,
		def.Wifi.Size, def.Wifi.FPS, def.Wifi.Bitrate+1)
	if err := e.a.SaveAppWinParams("K80", "pkg.d", payload2); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(e.a.profiles.Path())
	if !strings.Contains(string(raw2), "pkg.d") {
		t.Fatalf("非默认值应落条目: %s", string(raw2))
	}
}

// ⑩ v2.1.48：删除设备=全数据级联——档案条目（含 AppParams）+ 图标目录。
func TestDeleteDevicesCleansDeviceData(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb"}})
	// 造数据：图标目录 + 虚拟屏参数档案。
	dir := e.a.iconsDirFor(fixtureArchiveKey(e.a.profiles, "K80"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "com.android.browser.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.a.profiles.SetAppParams(fixtureArchiveKey(e.a.profiles, "K80"), "com.android.browser",
		AppWinParams{Usb: AppWinModeParams{Size: "1920x1080", FPS: 90, Bitrate: 16, Flex: true}}); err != nil {
		t.Fatal(err)
	}

	if err := e.a.DeleteDevices([]string{fixtureArchiveKey(e.a.profiles, "K80")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("图标目录未级联清理: %v", err)
	}
	if _, ok := e.a.profiles.Entry(fixtureArchiveKey(e.a.profiles, "K80")); ok {
		t.Fatal("设备档案条目未删除（AppParams 应随条目一并删除）")
	}
}

// ⑪ v2.1.50（主人拍板）：首次以默认档开虚拟屏 → 推导默认规格播种入档（只写一次；
// 已有值（实测/更权威）不被重播种覆盖；无线 IP:port 形态归一落进设备主档案）。
func TestAppWinSeedsBaselineOnFirstOpen(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb", Res: "3200x1440", FPS: 120}})
	// 前置：档案 baseline 未记录。
	if b := e.a.profiles.Get(fixtureArchiveKey(e.a.profiles, "K80")).Usb.Baseline; b.Res != 0 {
		t.Fatalf("前置：baseline 应为空: %+v", b)
	}
	if err := e.a.StartAppWin("K80", "pkg.s", "S"); err != nil {
		t.Fatal(err)
	}
	// 播种：有线套 baseline = 推导档（长边 3200 snap 2560 / 120fps / 60M）。
	if b := e.a.profiles.Get(fixtureArchiveKey(e.a.profiles, "K80")).Usb.Baseline; b.Res != 2560 || b.FPS != 120 || b.Bitrate != 60 {
		t.Fatalf("播种值异常: %+v（期望 2560/120/60——长边 snap 后）", b)
	}
	// 实测更新后重开窗：不被推导覆盖（播种只写一次）。
	e.a.updateBaseline("K80", "usb", Baseline{Res: 1920, FPS: 120, Bitrate: 60})
	if err := e.a.StartAppWin("K80", "pkg.s2", "S2"); err != nil {
		t.Fatal(err)
	}
	if b := e.a.profiles.Get(fixtureArchiveKey(e.a.profiles, "K80")).Usb.Baseline; b.Res != 1920 {
		t.Fatalf("已入档 baseline 不应被重播种覆盖: %+v", b)
	}
	// 无线形态：IP:port 会话归一进设备主档案（无孤儿档）。
	setDevices(e.a, []adb.Device{{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Identity: e.a.profiles.ResolveKey("K80"), Res: "3200x1440", FPS: 120}})
	if err := e.a.StartAppWin("192.168.31.197:5555", "pkg.w", "W"); err != nil {
		t.Fatal(err)
	}
	if b := e.a.profiles.Get(fixtureArchiveKey(e.a.profiles, "K80")).Wifi.Baseline; b.Res != 1920 || b.FPS != 60 || b.Bitrate != 15 {
		t.Fatalf("无线播种值异常: %+v（期望 1920/60/15）", b)
	}
	if _, ok := e.a.profiles.Entry(fixtureArchiveKey(e.a.profiles, "192.168.31.197:5555")); ok {
		t.Fatal("不应产生 IP:port 孤儿档案（播种必须用归一键）")
	}
}

// ⑫ v2.1.51（主人实测：切有线后卡片卡「正在关闭」+ 虚拟屏规格行不入档）：
// onAppWinLine 统一分类器——规格行/切换行解除 closing；形态更新；规格入档（共享 baseline）。
func TestAppWinLineSpecClearsClosingAndArchives(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Identity: e.a.profiles.ResolveKey("K80"), Res: "2136x3200", FPS: 120}})
	if err := e.a.StartAppWin("192.168.31.197:5555", "pkg.t", "T"); err != nil {
		t.Fatal(err)
	}
	if list := waitAppWins(t, e.a, 1); list[0].Mode != "wifi" {
		t.Fatalf("初始形态应为 wifi: %+v", list[0])
	}

	// 切换场景复现：切换流程关窗的副作用哨兵先到 → closing 置位（此时 bat 实际在重连）。
	e.fireLine("SCRCPY_EZ_USER_CLOSE")
	if list := waitAppWins(t, e.a, 1); !list[0].Closing {
		t.Fatalf("哨兵后应 closing: %+v", list[0])
	}
	// 新规格行（有线分支）→ 解除 closing + 形态更新 usb + 规格入档（归一键 K80）。
	e.fireLine("[高清] 有线模式：检测到设备 2136x3200@120Hz，有线规格 h264/80M/2560/120fps（低延迟优化，剪贴板自动同步（电脑复制即达手机））")
	deadline := time.Now().Add(2 * time.Second)
	for {
		list := e.a.Snapshot().AppWins
		if len(list) == 1 && !list[0].Closing && list[0].Mode == "usb" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("规格行未解除 closing/更新形态: %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b := e.a.profiles.Get(fixtureArchiveKey(e.a.profiles, "K80")).Usb.Baseline; b.Res != 2560 || b.FPS != 120 || b.Bitrate != 80 {
		t.Fatalf("规格未入档: %+v（期望 2560/120/80）", b)
	}
	if _, ok := e.a.profiles.Entry(fixtureArchiveKey(e.a.profiles, "192.168.31.197:5555")); ok {
		t.Fatal("规格入档不应产生 IP:port 孤儿档案")
	}
}

// 回归（v2.2.2）：flex=false 必须在「落盘 JSON → 读回 → API 序列化」全链显式保留——
// 缺任一跳都会让前端 `cur.flex === undefined ? true` 把「关」显示回「开」（v2.2.1 实机 bug）。
func TestFlexFalseSurvivesPersistenceAndView(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb", Res: "3200x1440", FPS: 120}})
	if err := e.a.SaveAppWinParams("K80", "pkg.noFlex",
		`{"mode":"usb","size":"1920","fps":60,"bitrate":8,"flex":false,"audio":"phone"}`); err != nil {
		t.Fatal(err)
	}
	// ① 落盘：false 不能被 omitempty 吞掉。
	raw, err := os.ReadFile(e.a.profiles.Path())
	if err != nil || !strings.Contains(string(raw), `"flex": false`) {
		t.Fatalf("落盘未保留 flex=false: %s err=%v", raw, err)
	}
	// ② 读回：normalize 不能把它洗成 true。
	v, err := e.a.GetAppWinParams("K80", "pkg.noFlex")
	if err != nil || v.Usb.Flex {
		t.Fatalf("读取应保留 flex=false: %+v err=%v", v.Usb, err)
	}
	// ③ API 序列化（前端真正拿到的）：false 必须出现在 JSON 里。
	b, err := json.Marshal(v)
	if err != nil || !strings.Contains(string(b), `"flex":false`) {
		t.Fatalf("API JSON 未包含 flex:false: %s err=%v", b, err)
	}
}
