//go:build windows

package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGui42ParamsAddr2Injected：BatRunner.Start 注入 SCEZ_ADDR2 到 bat 环境。
func TestGui42ParamsAddr2Injected(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "addr2.txt")
	resetOut := filepath.Join(dir, "noadbreset.txt")
	bat := filepath.Join(dir, "t.bat")
	// 简单 bat：把环境变量写入文件后退出（验证注入而非真实投屏）。
	// p2fix：GUI 启动必须无条件注入 SCEZ_NO_ADB_RESET=1。
	content := "@echo off\r\n" +
		"echo %SCEZ_NO_ADB_RESET% > \"" + resetOut + "\"\r\n" +
		"echo %SCEZ_ADDR2%> \"" + out + "\"\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(bat, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	r := NewBatRunner(bat, "adb.exe", func(string) {}, func(code int) { done <- code })
	if err := r.Start("X", CastParams{Addr: "192.168.31.197:33895", Addr2: "192.168.31.197:5555"}); err != nil {
		t.Fatal(err)
	}
	<-done

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "192.168.31.197:5555" {
		t.Fatalf("SCEZ_ADDR2 应注入 bat 环境: %q", got)
	}

	rb, err := os.ReadFile(resetOut)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(rb)); got != "1" {
		t.Fatalf("SCEZ_NO_ADB_RESET 应无条件注入且为 1: %q", got)
	}
}
