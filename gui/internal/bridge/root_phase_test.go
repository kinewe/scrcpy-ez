package bridge

import "testing"

func TestRootFailureReasonAndCountdown(t *testing.T) {
	for _, tc := range []struct {
		line string
		kind Kind
		text string
	}{
		{"[root 修复] 本轮最多还剩 179 秒，可停止", KindRootPrepare, "本轮最多还剩 179 秒，可停止"},
		{"[root 修复待处理] 授权已拒绝，自动修复暂停", KindRootRequired, "授权已拒绝，自动修复暂停"},
	} {
		ev := ClassifyLine(tc.line)
		if ev.Kind != tc.kind || RootPhaseText(ev, "default") != tc.text {
			t.Fatal(ev)
		}
	}
	for _, line := range []string{"[server] INFO: [root 修复] unrelated", "Permission denied", "ERROR: permission denied"} {
		ev := ClassifyLine(line)
		if ev.Kind == KindRootPrepare || ev.Kind == KindRootRequired {
			t.Fatal(ev)
		}
	}
}
