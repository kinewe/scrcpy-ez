package bridge

import "testing"

// 键盘模式（设备级）：只有非 auto 的具体模式才注入 SCEZ_KEYBOARD，
// auto/空一律不注入——bat 见不到该变量即走原有的 Android 版本自适应。
func TestCastEnvKeyboardMode(t *testing.T) {
	has := func(env []string, want string) bool {
		for _, e := range env {
			if e == want {
				return true
			}
		}
		return false
	}
	if env := castEnv(CastParams{Keyboard: "uhid"}, "kb-test"); !has(env, "SCEZ_KEYBOARD=uhid") {
		t.Fatalf("uhid 应注入: %v", env)
	}
	if env := castEnv(CastParams{Keyboard: "sdk"}, "kb-test"); !has(env, "SCEZ_KEYBOARD=sdk") {
		t.Fatalf("sdk 应注入: %v", env)
	}
	if env := castEnv(CastParams{Keyboard: "auto"}, "kb-test"); has(env, "SCEZ_KEYBOARD=auto") {
		t.Fatalf("auto 不应注入: %v", env)
	}
	if env := castEnv(CastParams{}, "kb-test"); has(env, "SCEZ_KEYBOARD=") {
		t.Fatalf("空值不应注入: %v", env)
	}
}
