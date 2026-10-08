package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- 设置面板两个开关：settings.json 持久化 + 与 profiles.json 分离 ---

// 出厂默认：参数控件显示（现状）、关闭窗口完整退出（现状）。
func TestSettingsDefaultsWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewSettingsStore(filepath.Join(dir, "settings.json"))
	if err := s.Load(); err != nil {
		t.Fatalf("文件缺失不应报错: %v", err)
	}
	got := s.Get()
	if !got.ShowParamOverlay || got.CloseToTray {
		t.Fatalf("默认值应为 {显示, 不最小化到托盘}，实际 %+v", got)
	}
}

// 写盘 → 新实例 Load → 值保持一致（重启 GUI 后设置保持）。
func TestSettingsPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s := NewSettingsStore(path)
	_ = s.Load()
	if err := s.Set(false, true); err != nil {
		t.Fatal(err)
	}

	s2 := NewSettingsStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s2.Get(); got.ShowParamOverlay || !got.CloseToTray {
		t.Fatalf("跨实例往返持久化失败: %+v", got)
	}
}

// 落盘内容只含全局设置（不混入设备档案），且不留 .tmp 残留。
func TestSettingsFileContentIsolated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s := NewSettingsStore(path)
	_ = s.Load()
	if err := s.Set(true, true); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("settings.json 非法 JSON: %v", err)
	}
	if len(m) != 7 || m["showParamOverlay"] != true || m["closeToTray"] != true || m["keepDeviceAwake"] != true || m["otherAppWinSystemDecorations"] != true || m["notificationDefault"] != true || m["notificationPreview"] != true || m["notificationCopyMinutes"] != float64(1440) {
		t.Fatalf("settings.json 应只含全局设置: %v", m)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("原子写不应留下 .tmp 残留: %v", err)
	}
}

// 旧文件缺键 → 缺的键用默认值（不会把"显示"被零值 false 吃掉）。
func TestSettingsPartialFileKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"closeToTray": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if !got.ShowParamOverlay || !got.CloseToTray {
		t.Fatalf("缺键应保持默认（显示），实际 %+v", got)
	}
}

// 损坏文件 → 不阻断启动，回落默认值。
func TestSettingsCorruptFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err == nil {
		t.Fatalf("损坏文件应返回错误（调用方按默认继续）")
	}
	if got := s.Get(); !got.ShowParamOverlay || got.CloseToTray {
		t.Fatalf("损坏文件应保持默认值，实际 %+v", got)
	}
}

// App 侧：SetSettings 立即反映到快照，并落盘（新 App 同路径重新加载后一致）。
func TestAppSettingsSnapshotAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	a := New(Config{BatPath: "C:\\x\\投屏启动.bat", AdbPath: "C:\\x\\adb.exe",
		Version: "test", SettingsPath: path})
	if got := a.Snapshot().Settings; !got.ShowParamOverlay || got.CloseToTray {
		t.Fatalf("新 App 快照默认值应为 {显示, 不最小化到托盘}: %+v", got)
	}
	if err := a.SetSettings(false, true); err != nil {
		t.Fatal(err)
	}
	if got := a.Snapshot().Settings; got.ShowParamOverlay || !got.CloseToTray {
		t.Fatalf("SetSettings 后快照应立即更新: %+v", got)
	}

	a2 := New(Config{BatPath: "C:\\x\\投屏启动.bat", AdbPath: "C:\\x\\adb.exe",
		Version: "test", SettingsPath: path})
	if got := a2.Settings(); got.ShowParamOverlay || !got.CloseToTray {
		t.Fatalf("重启（新实例）后设置应保持: %+v", got)
	}
}

// 开关 A 贯通：启动投屏时按当前设置注入 CastParams（本轮由 UI 的 bat 注入消费）。
func TestStartCastInjectsParamOverlaySetting(t *testing.T) {
	cases := []struct {
		name    string
		setting bool
	}{
		{"开关 A 开（默认）：注入显示", true},
		{"开关 A 关：注入隐藏", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := New(Config{BatPath: "C:\\x\\投屏启动.bat", AdbPath: "C:\\x\\adb.exe",
				Version: "test", SettingsPath: filepath.Join(dir, "settings.json")})
			f := &fakeRunner{exitCode: -1}
			a.SetRunnerFactory(func(string, func(string), func(int)) (Runner, error) { return f, nil })

			if err := a.SetSettings(tc.setting, false); err != nil {
				t.Fatal(err)
			}
			if err := a.StartCast("12345TESTA"); err != nil {
				t.Fatal(err)
			}
			p := f.waitParams(t, 1)
			if !p.OverlayVisibleSet {
				t.Fatalf("GUI 会话必须显式注入参数控件可见性: %+v", p)
			}
			if p.OverlayVisible != tc.setting {
				t.Fatalf("注入值 %v，期望 %v", p.OverlayVisible, tc.setting)
			}
		})
	}
}

// 设置与设备档案互不干扰：写设置不动 profiles.json，写档案不动 settings.json。
func TestSettingsSeparateFromProfiles(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	profilesPath := filepath.Join(dir, "profiles.json")

	a := New(Config{BatPath: "C:\\x\\投屏启动.bat", AdbPath: "C:\\x\\adb.exe",
		Version: "test", ProfilesPath: profilesPath, SettingsPath: settingsPath})
	if err := a.SetSettings(false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profilesPath); !os.IsNotExist(err) {
		t.Fatalf("写设置不应创建/修改 profiles.json: %v", err)
	}
	if err := a.SetDeviceOrder([]string{"dev-a"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("设备档案写入不应影响 settings.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || len(m) != 7 || m["notificationDefault"] != true || m["notificationPreview"] != true || m["notificationCopyMinutes"] != float64(1440) {
		t.Fatalf("settings.json 被设备档案污染: %s (err=%v)", b, err)
	}
}
