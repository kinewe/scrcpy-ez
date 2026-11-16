package app

import (
	"testing"

	"scrcpy-ez/gui/internal/bridge"
)

// 键盘模式（设备级）的取值语义：
//   - uhid/sdk/aoa 落盘，并由 StartCast 装配进启动参数（bat 侧 SCEZ_KEYBOARD）；
//   - auto 归一为"未设置"，不注入——保持原有 Android 版本自适应行为；
//   - 空串=调用方未提供，保留档案现值（旧调用路径不清用户设置）。
func TestKeyboardModePersistAndAssembly(t *testing.T) {
	a, _ := newTestApp()
	key := fixtureArchiveKey(a.profiles, "X")

	if err := a.SaveProfileAndRestartKb("X", "usb", 2400, 75, 55, true, "pc", false, false, "h264", "opus", "uhid"); err != nil {
		t.Fatal(err)
	}
	if p := a.profiles.Get(key); p.Keyboard != "uhid" {
		t.Fatalf("uhid 未落盘: %q", p.Keyboard)
	}
	if got := assembleKeyboard(a, key); got != "uhid" {
		t.Fatalf("uhid 未装配进启动参数: %q", got)
	}

	if err := a.SaveProfileAndRestartKb("X", "usb", 2400, 75, 55, true, "pc", false, false, "h264", "opus", "auto"); err != nil {
		t.Fatal(err)
	}
	if p := a.profiles.Get(key); p.Keyboard != "auto" {
		t.Fatalf("auto 应归一: %q", p.Keyboard)
	}
	if got := assembleKeyboard(a, key); got != "" {
		t.Fatalf("auto 不应注入: %q", got)
	}

	// 空串=不改动：先设 sdk，再传空串保存，档案必须还是 sdk。
	if err := a.SaveProfileAndRestartKb("X", "usb", 2400, 75, 55, true, "pc", false, false, "h264", "opus", "sdk"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveProfileAndRestartKb("X", "usb", 2400, 75, 55, true, "pc", false, false, "h264", "opus", ""); err != nil {
		t.Fatal(err)
	}
	if p := a.profiles.Get(key); p.Keyboard != "sdk" {
		t.Fatalf("空串应保留现值: %q", p.Keyboard)
	}
	if got := assembleKeyboard(a, key); got != "sdk" {
		t.Fatalf("sdk 未装配: %q", got)
	}
}

// assembleKeyboard 复现 StartCast 的键盘装配规则（auto/空=不注入），
// 直接用档案值验证——避免依赖真实设备重启。
func assembleKeyboard(a *App, key string) string {
	p := a.profiles.Get(key)
	params := bridge.CastParams{}
	if kb := NormalizeKeyboardMode(p.Keyboard); kb != "auto" {
		params.Keyboard = kb
	}
	return params.Keyboard
}

// NormalizeKeyboardMode：合法值小写归一，空/非法回 auto。
func TestNormalizeKeyboardMode(t *testing.T) {
	cases := map[string]string{
		"": "auto", "auto": "auto", "uhid": "uhid", "UHID": "uhid",
		"SDK": "sdk", "aoa": "aoa", "bogus": "auto", " uhid": "uhid",
	}
	for in, want := range cases {
		if got := NormalizeKeyboardMode(in); got != want {
			t.Fatalf("NormalizeKeyboardMode(%q)=%q want %q", in, got, want)
		}
	}
}
