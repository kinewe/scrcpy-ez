//go:build windows

package bridge

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestBatReadLoopWritesUTF8DebugLog(t *testing.T) {
	DisableDebugLog()
	path := filepath.Join(t.TempDir(), "bridge.log")
	EnableDebugLog(path)
	t.Cleanup(DisableDebugLog)
	text := "[提示] 本次连接连续失败，等待设备状态变化或手动重投\r\n"
	raw, err := simplifiedchinese.GBK.NewEncoder().String(text)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	r := &BatRunner{onLine: func(line string) { got = line }}
	r.readLoop(io.NopCloser(strings.NewReader(raw)), "out")
	DisableDebugLog()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != text || !utf8.Valid(b) || !strings.Contains(string(b), strings.TrimSpace(text)) {
		t.Fatalf("mixed log encoding: callback=%q log=%q", got, b)
	}
}

func TestBatExitDeliversFinalOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "final-output.bat")
	if err := os.WriteFile(path, []byte("@echo off\r\necho final-stdout\r\necho final-stderr 1>&2\r\nexit /b 0\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var output []string
	exit := make(chan []string, 1)
	r := NewBatRunner(path, "", func(line string) {
		mu.Lock()
		output = append(output, strings.TrimSpace(line))
		mu.Unlock()
	}, func(code int) {
		mu.Lock()
		exit <- append([]string(nil), output...)
		mu.Unlock()
	})
	if err := r.Start("test", CastParams{}); err != nil {
		t.Fatal(err)
	}
	select {
	case lines := <-exit:
		got := strings.Join(lines, "\n")
		if !strings.Contains(got, "final-stdout") || !strings.Contains(got, "final-stderr") {
			t.Fatalf("exit reached the app before the final output: %q", lines)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("batch exit callback timed out")
	}
}
