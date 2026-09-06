package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// --- gui49-fix3：adb 半死保险（连续失败 → kill-server×2+start-server） ---

// 连续失败达到阈值 → 触发一次 RestartServer；成功后清 adbFail 计数；下一次
// 校准恢复 adbOK 与设备列表（红条去抖逻辑不变）。
func TestGui49Fix3HalfDeadHealAfterConsecutiveFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "healed")
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"devices\" ]; then\n" +
		"  if [ -f \"$G49_HEAL\" ]; then printf 'List of devices attached\\n601c9f08\\tdevice\\n'; exit 0; else exit 1; fi\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("G49_HEAL", marker)

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	var mu sync.Mutex
	restarts := 0
	a.disc.RestartServerFn = func(ctx context.Context) error {
		mu.Lock()
		restarts++
		mu.Unlock()
		f, err := os.Create(marker)
		if err != nil {
			return err
		}
		_ = f.Close()
		return nil
	}

	// 前 3 轮连续失败：第 3 轮达到阈值触发半死保险（只触发一次）。
	for i := 0; i < 3; i++ {
		a.pollOnce(context.Background())
	}
	mu.Lock()
	n := restarts
	mu.Unlock()
	if n != 1 {
		t.Fatalf("连续失败达到阈值应触发一次 kill/start 自愈: restarts=%d", n)
	}
	a.mu.RLock()
	fail := a.adbFail
	healTried := a.adbHealTried
	a.mu.RUnlock()
	if fail != 0 || healTried {
		t.Fatalf("自愈成功后应清失败计数与半死闩锁: fail=%d healTried=%v", fail, healTried)
	}

	// 自愈后下一轮恢复设备列表与 adbOK（红条去抖逻辑保持）。
	a.pollOnce(context.Background())
	s := a.Snapshot()
	if !s.AdbOK || len(s.Devices) != 1 {
		t.Fatalf("自愈后应恢复 adbOK 与设备列表: %+v", s)
	}
}

// 半死保险不重复触发：同一段连续失败只杀一次 server（防每 2s 轰炸）。
func TestGui49Fix3HalfDeadHealOncePerFailureStreak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\nif [ \"$1\" = \"devices\" ]; then exit 1; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	var mu sync.Mutex
	restarts := 0
	a.disc.RestartServerFn = func(ctx context.Context) error {
		mu.Lock()
		restarts++
		mu.Unlock()
		return context.DeadlineExceeded // 自愈失败：闩锁保持，不每 2s 重复杀 server
	}

	for i := 0; i < 6; i++ {
		a.pollOnce(context.Background())
	}
	mu.Lock()
	n := restarts
	mu.Unlock()
	if n != 1 {
		t.Fatalf("同一段连续失败只应触发一次自愈: restarts=%d", n)
	}
}
