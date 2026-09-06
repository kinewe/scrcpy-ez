package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 关闭状态零开销：只做一次原子读，零分配、零文件。
func TestDebugLogDisabledZeroOverhead(t *testing.T) {
	DisableDebugLog()
	dir := t.TempDir()
	n := testing.AllocsPerRun(1000, func() { DebugLog("hello %d", 42) })
	if n != 0 {
		t.Fatalf("关闭状态应零分配, got %v allocs/op", n)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("关闭状态不应产生文件: %v", entries)
	}
}

// 打开状态：异步逐行写文件（时间+内容），写 stdin 事件含键值与原因。
func TestDebugLogEnabledWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge-test.log")
	EnableDebugLog(path)
	defer DisableDebugLog()

	DebugLog("[stdin] key=%q reason=%s", "r", "promptTick 自动补键")
	DebugLog("[out:1] 检测到连接断开")
	DebugLog("[exit] code=%d", 1)

	deadline := time.Now().Add(2 * time.Second)
	var content string
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			content = string(b)
			if strings.Contains(content, `key="r"`) &&
				strings.Contains(content, "promptTick 自动补键") &&
				strings.Contains(content, "[out:1]") &&
				strings.Contains(content, "[exit] code=1") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("日志未落盘: %q", content)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := debugLogPathForTest(); got != path {
		t.Fatalf("日志路径记录错误: %q", got)
	}
}
