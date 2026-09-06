package bridge

import (
	"strings"
	"testing"
)

// --- Stop 杀树顺序契约（双投屏修复） ---

// 顺序契约：先整树 taskkill /T（cmd 存活），再 Process.Kill 兜底——历史 bug 是先
// Kill（只杀 cmd）→ taskkill 128 → scrcpy 逃逸成孤儿 → 保存重投后双窗口。
// 残余 scrcpy 兜底复查必须在 adb kill-server 之前（杀服前窗口先归零）。
func TestStopStepsOrderContract(t *testing.T) {
	idx := func(name string) int {
		for i, s := range stopSteps {
			if s == name {
				return i
			}
		}
		t.Fatalf("stopSteps 缺步骤 %s: %v", name, stopSteps)
		return -1
	}
	if idx("treekill") != 0 {
		t.Fatalf("第一步必须是整树 taskkill /T: %v", stopSteps)
	}
	if idx("treekill") >= idx("killcmd") {
		t.Fatalf("treekill 必须先于 killcmd: %v", stopSteps)
	}
	if idx("killcmd") >= idx("watchtag") {
		t.Fatalf("killcmd 必须先于 watcher 补杀: %v", stopSteps)
	}
	if idx("watchtag") >= idx("residual") {
		t.Fatalf("watcher 补杀必须先于残余 scrcpy 复查: %v", stopSteps)
	}
	if idx("residual") >= idx("adbserver") {
		t.Fatalf("残余 scrcpy 复查必须先于杀服: %v", stopSteps)
	}
	if len(stopSteps) != 5 {
		t.Fatalf("步骤数异常: %v", stopSteps)
	}
}

// --- scrcpy 进程枚举解析 ---

func TestParseScrcpyProcs(t *testing.T) {
	out := "1234|5678|\"C:\\x\\scrcpy.exe\" --serial 601c9f08 --keyboard=uhid\r\n" +
		"garbage line\n" +
		"notint|999|cmd\n" +
		"2345|0|--serial 192.168.31.162:5555\n"
	procs := parseScrcpyProcs(out)
	if len(procs) != 2 {
		t.Fatalf("应解析出 2 条有效记录: %+v", procs)
	}
	if procs[0].pid != 1234 || procs[0].ppid != 5678 || !strings.Contains(procs[0].cmdline, "601c9f08") {
		t.Fatalf("第一条解析错误: %+v", procs[0])
	}
	if procs[1].pid != 2345 || procs[1].ppid != 0 || !strings.Contains(procs[1].cmdline, "192.168.31.162") {
		t.Fatalf("第二条解析错误: %+v", procs[1])
	}
	if got := parseScrcpyProcs(""); len(got) != 0 {
		t.Fatalf("空输出应零记录: %+v", got)
	}
}

// --- 本会话 scrcpy 命令行判定（防前缀误杀） ---

func TestScrcpyCmdlineMatches(t *testing.T) {
	serials := []string{"601c9f08", "192.168.31.197:5555"}
	cases := []struct {
		cmdline string
		want    bool
	}{
		{`"C:\x\scrcpy.exe" --serial 601c9f08 --keyboard=uhid`, true},
		{`--serial 601c9f08`, true}, // 行尾（无尾随空格）
		{`--serial 192.168.31.197:5555 --max-size 1920`, true},
		{`--serial 601c9f081 --keyboard=uhid`, false}, // 前缀撞串：别台设备，绝不误杀
		{`--serial a743e1df --keyboard=uhid`, false},  // 平板（其他会话）
		{`C:\x\scrcpy.exe --serial=601c9f08`, false},  // 等号形式（bat 不用，保守不匹配）
		{`no serial here`, false},
	}
	for _, c := range cases {
		if got := scrcpyCmdlineMatches(c.cmdline, serials); got != c.want {
			t.Fatalf("cmdline=%q want=%v got=%v", c.cmdline, c.want, got)
		}
	}
}

// --- 残余 scrcpy 候选选择：父链 + 命令行，多会话隔离 ---

func TestResidualScrcpyCandidates(t *testing.T) {
	procs := []scrcpyProc{
		{pid: 400, ppid: 99, cmdline: `scrcpy.exe --serial a743e1df`},           // 别的会话（平板）
		{pid: 300, ppid: 42, cmdline: `scrcpy.exe --serial 601c9f08`},           // 本会话：命令行命中
		{pid: 100, ppid: 42, cmdline: `scrcpy.exe --serial x`},                  // 本会话：父链命中
		{pid: 200, ppid: 42, cmdline: `scrcpy.exe --serial 601c9f08`},           // 本会话：双命中
		{pid: 500, ppid: 7, cmdline: `scrcpy.exe --serial 192.168.31.197:5555`}, // 本会话：无线候选命中
	}
	got := residualScrcpyCandidates(procs, 42, []string{"601c9f08", "192.168.31.197:5555"})
	want := []int{100, 200, 300, 500}
	if len(got) != len(want) {
		t.Fatalf("候选数错误: %+v", got)
	}
	for i, p := range got {
		if p.pid != want[i] {
			t.Fatalf("候选第 %d 个应为 pid=%d（升序）: %+v", i, want[i], got)
		}
	}
	// 无命中 → 空
	if got := residualScrcpyCandidates(procs, 4242, nil); len(got) != 0 {
		t.Fatalf("无命中应空: %+v", got)
	}
}

// --- serial 候选并集（会话键 + SCEZ_SERIAL + SCEZ_ADDR 去重） ---

func TestSerialCandidates(t *testing.T) {
	got := serialCandidates("a743e1df", CastParams{Serial: "a743e1df", Addr: "192.168.31.162:5555"})
	if len(got) != 2 || got[0] != "a743e1df" || got[1] != "192.168.31.162:5555" {
		t.Fatalf("去重并集错误: %+v", got)
	}
	if got := serialCandidates("", CastParams{}); len(got) != 0 {
		t.Fatalf("全空应零候选: %+v", got)
	}
	if got := serialCandidates(" 601c9f08 ", CastParams{}); len(got) != 1 || got[0] != "601c9f08" {
		t.Fatalf("应去空白: %+v", got)
	}
}

// --- 标签点击浮前：本会话 scrcpy 候选（纯命令行匹配） ---

// 浮前候选只按 --serial 命令行匹配（父链不参与——会话重启后父 pid 已变），
// 只命中本会话 serial 候选，绝不把别的会话窗口浮前；pid 去重 + 升序。
func TestBringToFrontCandidates(t *testing.T) {
	procs := []scrcpyProc{
		{pid: 400, ppid: 99, cmdline: `scrcpy.exe --serial a743e1df`}, // 别的会话（平板）
		{pid: 300, ppid: 42, cmdline: `scrcpy.exe --serial 601c9f08`}, // 本会话
		{pid: 100, ppid: 77, cmdline: `scrcpy.exe --serial 192.168.31.197:5555`},
		{pid: 200, ppid: 88, cmdline: `scrcpy.exe --serial 601c9f08 --max-size 1920`},
		{pid: 200, ppid: 88, cmdline: `scrcpy.exe --serial 601c9f08 --max-size 1920`}, // 重复 pid 去重
		{pid: 500, ppid: 42, cmdline: `scrcpy.exe --serial 601c9f081`},                // 前缀撞串：别台设备
	}
	got := bringToFrontCandidates(procs, []string{"601c9f08", "192.168.31.197:5555"})
	want := []int{100, 200, 300}
	if len(got) != len(want) {
		t.Fatalf("候选数错误: %+v", got)
	}
	for i, p := range got {
		if p.pid != want[i] {
			t.Fatalf("候选第 %d 个应为 pid=%d（去重+升序）: %+v", i, want[i], got)
		}
	}
	// 无命中 → 空
	if got := bringToFrontCandidates(procs, nil); len(got) != 0 {
		t.Fatalf("无命中应空: %+v", got)
	}
	if got := bringToFrontCandidates(procs, []string{"a743e1df"}); len(got) != 1 || got[0].pid != 400 {
		t.Fatalf("平板候选应只命中平板: %+v", got)
	}
}

// v3 修复组合场景（实况）：平板会话键=a743e1df（USB），scrcpy 实际命令行
// --serial 192.168.31.162:5555（无线）——候选集由 app.frontCandidateSerials
// 从档案补全（键+serials+addrs=[a743e1df, 162:5555]）后，此处纯命令行匹配
// 必须命中（含"无线地址直连"与"卡片重键回 USB"两个方向）。
func TestBringToFrontCandidatesProfileAddrCombo(t *testing.T) {
	candidates := []string{"a743e1df", "192.168.31.162:5555"}
	procs := []scrcpyProc{
		{pid: 100, ppid: 7, cmdline: `"C:\x\scrcpy.exe" --serial 192.168.31.162:5555 --max-size 1920`}, // 无线直连（v3 实况）
		{pid: 200, ppid: 8, cmdline: `scrcpy.exe --serial a743e1df`},                                   // USB 形态
		{pid: 300, ppid: 9, cmdline: `scrcpy.exe --serial 192.168.31.162:5556`},                        // 前缀撞串：别台设备
	}
	got := bringToFrontCandidates(procs, candidates)
	want := []int{100, 200}
	if len(got) != len(want) {
		t.Fatalf("候选数错误: %+v", got)
	}
	for i, p := range got {
		if p.pid != want[i] {
			t.Fatalf("候选第 %d 个应为 pid=%d: %+v", i, want[i], got)
		}
	}
}
