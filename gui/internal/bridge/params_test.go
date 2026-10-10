package bridge

import (
	"strings"
	"testing"
)

// envMap 把 castEnv 的 "K=V" 列表转成 map（断言与顺序无关）。
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestCastEnvExplicitReusePolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		values := envMap(castEnv(CastParams{VdSize: "1280x720", ReuseAppTask: enabled}, "reuse-test"))
		want := "0"
		if enabled {
			want = "1"
		}
		if values["SCEZ_REUSE_APP_TASK"] != want {
			t.Fatal(values)
		}
	}
}

func TestCastEnvFullSerialPinIndependentOfUSB(t *testing.T) {
	values := envMap(castEnv(CastParams{ExpectedSerial: "PHONE_A", Addr: "192.0.2.10:5555"}, "identity-test"))
	if values["SCEZ_EXPECT_SERIAL"] != "PHONE_A" || values["SCEZ_ADDR"] != "192.0.2.10:5555" {
		t.Fatal(values)
	}
	if _, ok := values["SCEZ_SERIAL"]; ok {
		t.Fatal("wireless identity pin pretended USB was online")
	}
}

func TestCastEnvKeepDeviceAwake(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		values := envMap(castEnv(CastParams{KeepDeviceAwake: enabled, KeepDeviceAwakeSet: true}, "awake-test"))
		want := "0"
		if enabled {
			want = "1"
		}
		if values["SCEZ_KEEP_ACTIVE"] != want {
			t.Fatal("missing explicit global policy", values)
		}
	}
	if _, ok := envMap(castEnv(CastParams{}, "legacy-test"))["SCEZ_KEEP_ACTIVE"]; ok {
		t.Fatal("legacy callers must not acquire a sleep policy")
	}
}

// 设置面板开关 A：参数控件启动可见性注入（SCEZ_PARAM_OVERLAY）。
// GUI→bat→scrcpy.exe 的注入链在 GUI 侧的最后一环就是这里——客户端侧读取逻辑
// 由 02_client/tests/overlay_env_test.c 覆盖。
func TestCastEnvParamOverlay(t *testing.T) {
	cases := []struct {
		name   string
		params CastParams
		want   string // "" = 不注入该变量
	}{
		{
			name:   "未设置：不注入（旧调用方/bat 行为不变）",
			params: CastParams{},
			want:   "",
		},
		{
			name:   "开关 A 开：注入 1（启动投屏时显示参数控件）",
			params: CastParams{OverlayVisible: true, OverlayVisibleSet: true},
			want:   "1",
		},
		{
			name:   "开关 A 关：注入 0（启动投屏时隐藏参数控件）",
			params: CastParams{OverlayVisible: false, OverlayVisibleSet: true},
			want:   "0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envMap(castEnv(tc.params, "tag-x"))
			got, ok := env["SCEZ_PARAM_OVERLAY"]
			if tc.want == "" {
				if ok {
					t.Fatalf("未设置 OverlayVisibleSet 时不应注入 SCEZ_PARAM_OVERLAY（got %q）", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("SCEZ_PARAM_OVERLAY = %q (present=%v)，期望 %q", got, ok, tc.want)
			}
		})
	}
}

// 回归：既有注入项不受新增字段影响（未设置 OverlayVisibleSet 时与历史完全一致）。
func TestCastEnvLegacyFieldsUnchanged(t *testing.T) {
	params := CastParams{
		Usb:    ModeParams{Res: 1920, FPS: 60, Bitrate: 8, Set: true},
		Serial: "12345TESTA",
		Addr:   "192.0.2.197:5555",
		Addr2:  "192.0.2.197:45005",
		Market: "Redmi K80",
		Model:  "12345TESTA",
	}
	env := envMap(castEnv(params, "tag-1"))
	want := map[string]string{
		"SCEZ_NO_ADB_RESET": "1",
		"SCEZ_RES_USB":      "1920",
		"SCEZ_FPS_USB":      "60",
		"SCEZ_BITRATE_USB":  "8",
		"SCEZ_SERIAL":       "12345TESTA",
		"SCEZ_ADDR":         "192.0.2.197:5555",
		"SCEZ_ADDR2":        "192.0.2.197:45005",
		"SCEZ_MARKET":       "Redmi K80",
		"SCEZ_MODEL":        "12345TESTA",
		"SCEZ_WATCH_TAG":    "tag-1",
	}
	for k, v := range want {
		if env[k] != v {
			t.Fatalf("%s = %q，期望 %q（env=%v）", k, env[k], v, env)
		}
	}
	// 未设置的字段/模式不产生条目（bat 走原逻辑）
	for _, k := range []string{"SCEZ_RES_WIFI", "SCEZ_FPS_WIFI", "SCEZ_BITRATE_WIFI",
		"SCEZ_NO_WATCH", "SCEZ_PARAM_OVERLAY"} {
		if _, ok := env[k]; ok {
			t.Fatalf("未设置字段不应注入 %s（env=%v）", k, env)
		}
	}
}

// 回归：开关 A 与其它注入项共存（真实 GUI 会话总是同时注入 SERIAL/ADDR/...）。
func TestCastEnvParamOverlayCoexists(t *testing.T) {
	params := CastParams{
		Wifi:              ModeParams{Res: 1920, FPS: 60, Bitrate: 12, Set: true},
		Serial:            "192.0.2.197:5555",
		Addr:              "192.0.2.197:5555",
		OverlayVisible:    false,
		OverlayVisibleSet: true,
	}
	env := envMap(castEnv(params, "tag-2"))
	if env["SCEZ_PARAM_OVERLAY"] != "0" {
		t.Fatalf("开关 A 关应注入 0: %v", env)
	}
	if env["SCEZ_RES_WIFI"] != "1920" || env["SCEZ_ADDR"] != "192.0.2.197:5555" {
		t.Fatalf("与开关 A 共存的既有注入项丢失: %v", env)
	}
}

// 虚拟屏参数注入（应用窗口走 bat，v2.1.27）：SCEZ_VD_* 整组"未设置=不注入"
// （零回归）；设置后逐项注入；开关类仅 true 才注入 "1"；dpi=0 不注入（交给 scrcpy）。
func TestCastEnvVirtualDisplay(t *testing.T) {
	vdKeys := []string{"SCEZ_VD_SIZE", "SCEZ_VD_DPI", "SCEZ_VD_FLEX", "SCEZ_VD_AUTO_DPI", "SCEZ_VD_IME",
		"SCEZ_VD_NO_DECOR", "SCEZ_VD_KEEP_CONTENT", "SCEZ_VD_AUDIO",
		"SCEZ_START_APP", "SCEZ_WIN_TITLE"}

	// ① 未设置：整组不注入（普通投屏 bat 行为与历史完全一致）。
	env := envMap(castEnv(CastParams{}, "tag-vd"))
	for _, k := range vdKeys {
		if _, ok := env[k]; ok {
			t.Fatalf("未设置虚拟屏参数时不应注入 %s（env=%v）", k, env)
		}
	}

	// ② 默认档设置：逐项注入（含 "+" 前缀原样透传、中文标题）。
	params := CastParams{
		VdSize: "1280x720", VdDpi: 240, VdFlex: true, VdIme: "local",
		StartApp: "+com.android.browser", WinTitle: "浏览器",
	}
	env = envMap(castEnv(params, "tag-vd"))
	want := map[string]string{
		"SCEZ_VD_SIZE":   "1280x720",
		"SCEZ_VD_DPI":    "240",
		"SCEZ_VD_FLEX":   "1",
		"SCEZ_VD_IME":    "local",
		"SCEZ_START_APP": "+com.android.browser",
		"SCEZ_WIN_TITLE": "浏览器",
	}
	for k, w := range want {
		if got := env[k]; got != w {
			t.Fatalf("%s = %q，期望 %q（env=%v）", k, got, w, env)
		}
	}
	// 开关类默认关：不注入（音频档位空=不注入）
	for _, k := range []string{"SCEZ_VD_NO_DECOR", "SCEZ_VD_KEEP_CONTENT", "SCEZ_VD_AUDIO"} {
		if _, ok := env[k]; ok {
			t.Fatalf("开关未开时不应注入 %s（env=%v）", k, env)
		}
	}

	// ③ 开关开：注入 "1"（v2.1.78：音频档位注入值=档名，非 "1"）
	env = envMap(castEnv(CastParams{VdSize: "1280x720", VdNoDecor: true, VdKeepContent: true, VdAudio: "phone"}, "tag-vd"))
	for _, k := range []string{"SCEZ_VD_NO_DECOR", "SCEZ_VD_KEEP_CONTENT"} {
		if env[k] != "1" {
			t.Fatalf("开关开时应注入 %s=1（got %q，env=%v）", k, env[k], env)
		}
	}
	if env["SCEZ_VD_AUDIO"] != "phone" {
		t.Fatalf("音频档位应注入 SCEZ_VD_AUDIO=phone（got %q，env=%v）", env["SCEZ_VD_AUDIO"], env)
	}

	// ④ dpi=0：不注入（scrcpy 自动算；调用方查询失败时的路径）。
	env = envMap(castEnv(CastParams{VdSize: "1280x720"}, "tag-vd"))
	if _, ok := env["SCEZ_VD_DPI"]; ok {
		t.Fatalf("VdDpi=0 时不应注入 SCEZ_VD_DPI（env=%v）", env)
	}
}

func TestPhoneLayoutAndVideoResolutionAreIndependentForEachTransport(t *testing.T) {
	env := envMap(castEnv(CastParams{VdSize: "864x1920", VdMatchPhone: true, VdMaxSize: 1920,
		VdUsb:  VdModeParams{Size: "1152x2560", MatchPhone: true, MaxSize: 2560},
		VdWifi: VdModeParams{Size: "864x1920", MatchPhone: true, MaxSize: 1920}}, "tag-native"))
	for k, value := range map[string]string{"SCEZ_VD_MATCH_PHONE": "1", "SCEZ_VD_MATCH_PHONE_USB": "1", "SCEZ_VD_MATCH_PHONE_WIFI": "1", "SCEZ_VD_MAX_SIZE": "1920", "SCEZ_VD_MAX_SIZE_USB": "2560", "SCEZ_VD_MAX_SIZE_WIFI": "1920"} {
		if env[k] != value {
			t.Fatalf("%s = %q want %q", k, env[k], value)
		}
	}
	env = envMap(castEnv(CastParams{VdSize: "864x1920", VdUsb: VdModeParams{Size: "1152x2560", MatchPhone: true}, VdWifi: VdModeParams{Size: "864x1920"}}, "tag-mixed"))
	if env["SCEZ_VD_MATCH_PHONE_WIFI"] != "0" || env["SCEZ_VD_MATCH_PHONE"] != "0" {
		t.Fatal("explicit other-transport configuration inherited phone layout")
	}
}

func TestAutomaticDensityIsIndependentForEachTransport(t *testing.T) {
	params := CastParams{
		VdSize: "864x1920", VdDpi: 360, VdFlex: true, VdAutoDpi: true,
		VdUsb:  VdModeParams{Size: "1152x2560", Dpi: 480, Flex: true, AutoDpi: true},
		VdWifi: VdModeParams{Size: "864x1920", Dpi: 320, Flex: true},
	}
	env := envMap(castEnv(params, "tag-density"))
	if env["SCEZ_VD_AUTO_DPI"] != "1" || env["SCEZ_VD_AUTO_DPI_USB"] != "1" || env["SCEZ_VD_AUTO_DPI_WIFI"] != "0" {
		t.Fatalf("manual Wi-Fi density inherited automatic USB density: %v", env)
	}
	params.VdAutoDpi = false
	params.VdUsb.AutoDpi = false
	params.VdWifi.AutoDpi = true
	env = envMap(castEnv(params, "tag-density-reverse"))
	if env["SCEZ_VD_AUTO_DPI"] != "0" || env["SCEZ_VD_AUTO_DPI_USB"] != "0" || env["SCEZ_VD_AUTO_DPI_WIFI"] != "1" {
		t.Fatalf("density modes not independently encoded: %v", env)
	}
}

// v2.1.78 声音档位注入：主屏 SCEZ_AUDIO_*（独立于 Set）+ 虚拟屏两套 SCEZ_VD_AUDIO_*。
func TestAudioModeInjection(t *testing.T) {
	params := CastParams{
		Usb:    ModeParams{Res: 2560, FPS: 120, Bitrate: 60, Set: true, Audio: "both"},
		Wifi:   ModeParams{Audio: "phone"},
		VdUsb:  VdModeParams{Size: "1280x720", Audio: "pc"},
		VdWifi: VdModeParams{Size: "1920x864", Audio: "both"},
	}
	env := envMap(castEnv(params, "tag-a"))
	want := map[string]string{
		"SCEZ_AUDIO_USB":     "both",
		"SCEZ_AUDIO_WIFI":    "phone",
		"SCEZ_VD_AUDIO_USB":  "pc",
		"SCEZ_VD_AUDIO_WIFI": "both",
	}
	for k, w := range want {
		if got := env[k]; got != w {
			t.Fatalf("%s = %q，期望 %q（env=%v）", k, got, w, env)
		}
	}
	// 空档位：不注入（bat 走默认行为——主屏 pc / 应用屏默认由 GUI 显式注入）
	env2 := envMap(castEnv(CastParams{}, "tag-b"))
	for _, k := range []string{"SCEZ_AUDIO_USB", "SCEZ_AUDIO_WIFI", "SCEZ_VD_AUDIO_USB", "SCEZ_VD_AUDIO_WIFI", "SCEZ_VD_AUDIO"} {
		if _, ok := env2[k]; ok {
			t.Fatalf("空档位不应注入 %s（env=%v）", k, env2)
		}
	}
	if env2["SCEZ_WATCH_TAG"] != "tag-b" {
		t.Fatalf("WATCH_TAG 应恒注入（env=%v）", env2)
	}
}
