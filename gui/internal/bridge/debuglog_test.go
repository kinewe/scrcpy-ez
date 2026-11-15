package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDebugLogCloseDuringWrites(t *testing.T) {
	DisableDebugLog()
	EnableDebugLog(filepath.Join(t.TempDir(), "concurrent.log"))
	var wg sync.WaitGroup
	started := make(chan struct{}, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			DebugLog("writer started")
			started <- struct{}{}
			for i := 0; i < 1000; i++ {
				DebugLog("line %d", i)
			}
		}()
	}
	for n := 0; n < 8; n++ {
		<-started
	}
	DisableDebugLog()
	wg.Wait()
}

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

func TestDebugLogRotatesAndRetainsNewestFiles(t *testing.T) {
	dir := t.TempDir()
	writer := rotatingDebugLog{path: filepath.Join(dir, "bridge.log"), limit: 3}
	if err := writer.open(); err != nil {
		t.Fatal(err)
	}
	defer writer.close()
	for i := 0; i < 7; i++ {
		if err := writer.write(fmt.Sprintf("%d\n", i)); err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected an active file and three archives, got %v", entries)
	}
	for i, expected := range []string{"6\n", "3\n", "4\n", "5\n"} {
		content, err := os.ReadFile(filepath.Join(dir, entries[i].Name()))
		if err != nil || string(content) != expected {
			t.Fatalf("%s = %q, err = %v, want %q", entries[i].Name(), content, err, expected)
		}
	}
}

func TestDebugLogPrunesOnlyExpiredManagedFiles(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	writer := rotatingDebugLog{path: filepath.Join(dir, "bridge.log")}
	stamp := now.Add(-8 * 24 * time.Hour).UTC().Format(debugLogStamp)
	expired := []string{"bridge.log.ez-" + stamp, "bridge-20260101.log"}
	keep := []string{"bridge.log", "bridge-20260230.log", "bridge.log.ez-manual", "custom.log.ez-" + stamp, "frame-trace-123.jsonl"}
	for _, name := range append(append([]string{}, expired...), keep...) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("diagnostics"), 0600); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-8 * 24 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.prune(now, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range expired {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("expired managed log %s survived: %v", name, err)
		}
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("file %s should have been preserved: %v", name, err)
		}
	}
}
