package app

import (
	"strings"
	"testing"
)

// --- StartCast 全链路来源留痕（callerChain 观测函数） ---
// 契约（与 app.go callerChain 注释一致，勿再改帧序语义）：
//   callerChain(skip) 返回调用链前 2 帧短名：跳过 callerChain 自身与 skip 层
//   直接调用方——观测目标是"调用者的上层"（自动路径 vs JS 桥来源）。
//   skip=0：首帧=直接调用方；skip=1：首帧=直接调用方的上层（StartCast/RestartCast/
//   StartCastParallel 入口日志即用 skip=1 → 首帧=来源、次帧=来源的上层）。

// traceCallerOfStart 模拟 StartCast 内的 callerChain(1) 调用。
// go:noinline：保持独立栈帧，帧序可断言（内联会打乱帧计数）。
//
//go:noinline
func traceCallerOfStart() string {
	return callerChain(1)
}

func TestCallerChainTrace(t *testing.T) {
	// skip=0：首帧=直接调用方（本测试函数）
	if got := callerChain(0); !strings.HasPrefix(got, "TestCallerChainTrace") {
		t.Fatalf("callerChain(0) 首帧应为直接调用方: %q", got)
	}
	// skip=1（模拟 StartCast 位）：首帧=来源（本测试函数）、次帧=其上层（tRunner）
	if got := traceCallerOfStart(); !strings.HasPrefix(got, "TestCallerChainTrace") ||
		!strings.Contains(got, "tRunner") {
		t.Fatalf("callerChain(1) 首帧=来源、次帧=上层: %q", got)
	}
	// 只保留短名（去包路径）
	if got := callerChain(0); strings.Contains(got, "scrcpy-ez/gui/internal/app.") {
		t.Fatalf("callerChain 应只保留短名（去包路径）: %q", got)
	}
}
