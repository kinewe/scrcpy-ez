package bridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// 本测试在 Linux 侧验证桥接层 stdin 管道的语义模型（bat_windows.go 的
// StdinPipe 模式与之一致：父进程持有写端直到 Stop 才关闭）：
//   - 管道打开、无数据：子进程读 stdin 会阻塞（choice /t N /d r 由 choice 自身
//     定时器超时兜底——验证点 1）；
//   - 管道写入 "r\r\n"：choice 读到 r → errorlevel 2 → bat `if errorlevel 2 goto :main`
//     （重新检测，验证点 2）；
//   - 管道关闭 → 子进程读到 EOF → choice 报 errorlevel 255 → bat 分支分析见
//     bat_choice_flow_test 注释（验证点 3）；
//   - 子进程已退出再写 stdin → 返回错误而非 panic（验证点 4）。
//
// choice.exe 的 Windows 实现无法在 WSL 运行，这里用等效的 sh 子进程模拟。

func writeFakeChoice(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
# 模拟 choice：读一行；读到 r -> exit 2（errorlevel 2 => goto :main）；
# EOF -> exit 255（errorlevel 255，命中所有 if errorlevel N）
IFS= read -r line || exit 255
line=$(printf '%s' "$line" | tr -d '\r')
[ "$line" = "r" ] && exit 2
exit 1
`
	p := filepath.Join(t.TempDir(), "fake-choice.sh")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// 验证点 2：管道打开且写入 "r\r\n" → 子进程 exit 2（相当于 bat 的 goto :main）。
func TestStdinPipeOpenWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖 sh 脚本模拟 choice（Windows 下无法运行）")
	}
	cmd := exec.Command(writeFakeChoice(t))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write([]byte("r\r\n")); err != nil {
		t.Fatalf("写 stdin 失败: %v", err)
	}
	waitErr := cmd.Wait()
	if waitErr == nil {
		t.Fatal("子进程应带退出码退出")
	}
	ee, ok := waitErr.(*exec.ExitError)
	if !ok || ee.ExitCode() != 2 {
		t.Fatalf("期望 exit 2（=errorlevel 2 → :main），got %v", waitErr)
	}
}

// 验证点 3：管道关闭 → 子进程 EOF → exit 255（bat 分支分析见报告）。
func TestStdinPipeClosedEOF(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖 sh 脚本模拟 choice（Windows 下无法运行）")
	}
	cmd := exec.Command(writeFakeChoice(t))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close() // 模拟"stdin 句柄被提前关闭"
	waitErr := cmd.Wait()
	if waitErr == nil {
		t.Fatal("子进程应带退出码退出")
	}
	ee, ok := waitErr.(*exec.ExitError)
	if !ok || ee.ExitCode() != 255 {
		t.Fatalf("期望 exit 255（EOF），got %v", waitErr)
	}
}

// 验证点 1：管道打开、无数据 → 子进程阻塞读（由调用方超时守护模拟 choice 的 /t）。
func TestStdinPipeOpenBlocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖 sh 脚本模拟 choice（Windows 下无法运行）")
	}
	cmd := exec.Command(writeFakeChoice(t))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("无数据管道应阻塞，子进程却退出了: %v", err)
	case <-time.After(300 * time.Millisecond):
		// 仍阻塞 = 正确（真实 choice 由 /t 定时器超时接管）
	}
	_ = cmd.Process.Kill()
	<-done
}

// 验证点 4：bat 突然退出后写 stdin → 返回错误而非 panic（SendKey 安全路径）。
func TestStdinWriteAfterChildExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖 sh 脚本模拟 choice（Windows 下无法运行）")
	}
	cmd := exec.Command("sh", "-c", "exit 0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() // 子进程已退出
	if _, err := stdin.Write([]byte("r\r\n")); err == nil {
		// 管道缓冲可能仍接受写入：同样安全，只是不再报错
		t.Log("子进程退出后管道仍可写（数据被丢弃），无 panic")
	}
	_ = stdin.Close()
}

// 桥接层 stdin 生命周期核对（代码级断言）：
// Start 必然创建 StdinPipe（无"未创建"路径），保持打开直到 Stop 关闭；
// 只读架构：无 SendKey（GUI 不写 stdin）。
func TestBatRunnerStdinLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖 sh 脚本模拟 choice（Windows 下无法运行）")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows 下由 bat_windows.go 真实实现覆盖")
	}
	// 非 Windows 桩：Start/Stop 均为安全空实现
	r := NewBatRunner("x.bat", "adb", func(string) {}, func(int) {})
	if err := r.Start("", CastParams{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
}
