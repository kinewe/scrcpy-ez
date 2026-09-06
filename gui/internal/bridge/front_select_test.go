package bridge

import "testing"

// --- 投屏窗口浮前：主窗选择纯函数（7 窗口乱序根因修复） ---

// 实况复现（bridge 日志 21:09:08）：1 个 scrcpy pid 命中 7 个窗口——主投屏窗
// （大、无属主、可见）+ 隐藏辅助窗/IME/消息窗 + 小可见辅助窗。旧实现全量置顶
// 导致 z-order 最后落在隐藏窗、主窗被埋。新选择：只取"可见 + 无属主 + 面积最大"
// 的一个主窗置顶，其余全部跳过。
func TestSelectFrontMainWinVisibleOnlyLargest(t *testing.T) {
	wins := []frontWinInfo{
		{hwnd: 1, visible: false, owned: false, w: 1920, h: 1080}, // 隐藏辅助窗（旧实现会置顶它）
		{hwnd: 2, visible: true, owned: true, w: 400, h: 300},     // 小可见辅助窗（有属主）
		{hwnd: 3, visible: true, owned: false, w: 1920, h: 1080},  // 主投屏窗
		{hwnd: 4, visible: false, owned: false, w: 100, h: 50},    // 隐藏 IME/消息窗
		{hwnd: 5, visible: true, owned: false, w: 800, h: 600},    // 另一个可见顶层窗（较小）
		{hwnd: 6, visible: false, owned: true, w: 10, h: 10},
		{hwnd: 7, visible: true, owned: true, w: 640, h: 480},
	}
	sel := selectFrontMainWin(wins)
	if !sel.ok || sel.fallback {
		t.Fatalf("应选中可见主窗: %+v", sel)
	}
	if sel.main.hwnd != 3 {
		t.Fatalf("主窗应为 hwnd=3（无属主+面积最大）: %+v", sel.main)
	}
	if sel.skippedVisible != 3 {
		t.Fatalf("落选可见窗应=3（hwnd 2/5/7 不再置顶）: %+v", sel)
	}
	if sel.skippedHidden != 3 {
		t.Fatalf("隐藏跳过应=3: %+v", sel)
	}
}

// 无属主优先于面积：有属主的大窗 vs 无属主的小窗 → 无属主者胜（属主辅助窗
// 不是主投屏窗，哪怕更大）。
func TestSelectFrontMainWinOwnerlessFirst(t *testing.T) {
	wins := []frontWinInfo{
		{hwnd: 1, visible: true, owned: true, w: 3000, h: 2000},  // 有属主但巨大（辅助窗）
		{hwnd: 2, visible: true, owned: false, w: 1920, h: 1080}, // 无属主主窗
	}
	sel := selectFrontMainWin(wins)
	if sel.main.hwnd != 2 {
		t.Fatalf("无属主应优先: %+v", sel.main)
	}
	if sel.skippedVisible != 1 {
		t.Fatalf("落选可见窗应=1: %+v", sel)
	}
}

// 同属主状态下面积决胜（两个无属主可见窗 → 大者胜）。
func TestSelectFrontMainWinAreaTieBreak(t *testing.T) {
	wins := []frontWinInfo{
		{hwnd: 1, visible: true, owned: false, w: 1280, h: 720},
		{hwnd: 2, visible: true, owned: false, w: 2560, h: 1440},
	}
	if sel := selectFrontMainWin(wins); sel.main.hwnd != 2 {
		t.Fatalf("面积大者应胜: %+v", sel.main)
	}
	// 矩形异常（负宽高按 0 面积）：不参与面积竞争胜出
	wins2 := []frontWinInfo{
		{hwnd: 1, visible: true, owned: false, w: -1, h: 500},
		{hwnd: 2, visible: true, owned: false, w: 640, h: 480},
	}
	if sel := selectFrontMainWin(wins2); sel.main.hwnd != 2 {
		t.Fatalf("异常矩形按 0 面积处理: %+v", sel.main)
	}
}

// 全部不可见（异常）→ 回退第一个命中窗口（旧行为兜底，日志注明）。
func TestSelectFrontMainWinAllHiddenFallback(t *testing.T) {
	wins := []frontWinInfo{
		{hwnd: 7, visible: false, owned: false, w: 1920, h: 1080},
		{hwnd: 8, visible: false, owned: true, w: 100, h: 50},
	}
	sel := selectFrontMainWin(wins)
	if !sel.ok || !sel.fallback {
		t.Fatalf("全隐藏应回退: %+v", sel)
	}
	if sel.main.hwnd != 7 {
		t.Fatalf("回退应取第一个命中: %+v", sel.main)
	}
	if sel.skippedHidden != 2 {
		t.Fatalf("隐藏跳过应=2: %+v", sel)
	}
}

// 空集合安全。
func TestSelectFrontMainWinEmpty(t *testing.T) {
	if sel := selectFrontMainWin(nil); sel.ok {
		t.Fatalf("空集合应 ok=false: %+v", sel)
	}
}

// ez 宿主窗兜底枚举（v5 二段式浮前④用）：可见+面积最大；隐藏窗跳过；
// 无可见窗/空集合 → 0。
func TestPickVisibleLargestHwnd(t *testing.T) {
	wins := []frontWinInfo{
		{hwnd: 11, visible: false, w: 3000, h: 2000}, // 隐藏大窗：不选
		{hwnd: 12, visible: true, w: 520, h: 760},    // ez 主窗
		{hwnd: 13, visible: true, w: 100, h: 50},     // 可见小辅助
		{hwnd: 14, visible: false, w: 10, h: 10},
	}
	if got := pickVisibleLargestHwnd(wins); got != 12 {
		t.Fatalf("应选可见最大窗 hwnd=12: %d", got)
	}
	if got := pickVisibleLargestHwnd([]frontWinInfo{{hwnd: 15, visible: false, w: 100, h: 100}}); got != 0 {
		t.Fatalf("无可见窗应返回 0: %d", got)
	}
	if got := pickVisibleLargestHwnd(nil); got != 0 {
		t.Fatalf("空集合应返回 0: %d", got)
	}
	// 异常矩形按 0 面积：不参与竞争
	wins2 := []frontWinInfo{
		{hwnd: 16, visible: true, w: -1, h: 500},
		{hwnd: 17, visible: true, w: 640, h: 480},
	}
	if got := pickVisibleLargestHwnd(wins2); got != 17 {
		t.Fatalf("异常矩形按 0 面积处理: %d", got)
	}
}
