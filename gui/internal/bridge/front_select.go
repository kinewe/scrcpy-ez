package bridge

// --- 投屏窗口浮前：主窗选择纯函数（跨平台，可单测） ---
//
// scrcpy.exe（SDL）一个进程会注册多个顶层窗口：主投屏窗 + 隐藏辅助窗/IME/消息窗。
// 旧实现全量置顶导致 z-order 最后落在隐藏辅助窗上——真正可见的主投屏窗被埋，
// 视觉"点了标签窗口不动"（bridge 日志 21:09:08：1 个 pid 命中 7 个窗口）。
// 规则：
//   ① 只看可见顶层窗口（IsWindowVisible == TRUE；隐藏窗口全部跳过并计数）；
//   ② 可见窗口多个时选"主窗"：无属主（GW_OWNER==0）优先 → 面积最大（GetWindowRect
//      宽×高）决胜——取一个置顶，其余可见窗口不再置顶（计数，日志注明）；
//   ③ 可见窗口全空（异常）→ 回退置顶第一个命中窗口（保持旧行为，日志注明）。

// frontWinInfo 是 EnumWindows 命中窗口的属性快照（供主窗选择纯函数）。
type frontWinInfo struct {
	hwnd    uintptr
	pid     int
	visible bool // IsWindowVisible == TRUE
	owned   bool // GetWindow(GW_OWNER) != 0（有属主的辅助窗不是主窗）
	w, h    int  // GetWindowRect 宽/高（解析失败/零面积按 0）
}

// frontWinArea 返回窗口面积（宽×高；矩形异常时按 0 处理）。
func frontWinArea(w frontWinInfo) int64 {
	if w.w <= 0 || w.h <= 0 {
		return 0
	}
	return int64(w.w) * int64(w.h)
}

// frontBetter 判定 a 是否优于 b（主窗优先序）：无属主优先 → 面积降序。
func frontBetter(a, b frontWinInfo) bool {
	if a.owned != b.owned {
		return !a.owned
	}
	return frontWinArea(a) > frontWinArea(b)
}

// pickVisibleLargestHwnd 从窗口快照里挑"可见 + 面积最大"的 hwnd
// （ez 宿主窗兜底枚举用——本进程窗口不适用属主规则，只看可见性+面积）。
// 无可见窗返回 0。
func pickVisibleLargestHwnd(wins []frontWinInfo) uintptr {
	var best uintptr
	var bestArea int64
	for _, w := range wins {
		if !w.visible {
			continue
		}
		a := frontWinArea(w)
		if best == 0 || a > bestArea {
			best, bestArea = w.hwnd, a
		}
	}
	return best
}

// frontSelect 是 selectFrontMainWin 的选择结果（日志/行为依据）。
type frontSelect struct {
	main           frontWinInfo
	ok             bool
	skippedVisible int  // 落选可见窗口数（不再置顶）
	skippedHidden  int  // 隐藏窗口跳过数
	fallback       bool // 全部不可见 → 回退第一个命中窗口
}

// selectFrontMainWin 从命中窗口集合里选主窗（纯函数，单测覆盖）：
//   - 空集合 → ok=false；
//   - 有可见窗口 → 按 frontBetter 取最优一个（落选可见窗计数）；
//   - 全部不可见 → fallback=true，取第一个命中（旧行为兜底）。
func selectFrontMainWin(wins []frontWinInfo) frontSelect {
	if len(wins) == 0 {
		return frontSelect{}
	}
	res := frontSelect{}
	var best frontWinInfo
	haveBest := false
	visibleCount, hiddenCount := 0, 0
	for _, w := range wins {
		if w.visible {
			visibleCount++
			if !haveBest || frontBetter(w, best) {
				best = w
				haveBest = true
			}
		} else {
			hiddenCount++
		}
	}
	if haveBest {
		res.main = best
		res.ok = true
		res.skippedVisible = visibleCount - 1
		res.skippedHidden = hiddenCount
	} else {
		res.main = wins[0]
		res.fallback = true
		res.ok = true // 回退主窗仍可置顶（旧行为兜底）
		res.skippedHidden = hiddenCount
	}
	return res
}
