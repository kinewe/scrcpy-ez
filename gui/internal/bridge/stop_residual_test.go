package bridge

import "testing"

func TestAppSessionMatchingAfterOneShotRestart(t *testing.T) {
	for _, command := range []string{"--start-app=app.test", "--start-app=+app.test", "--start-app app.test", `"--start-app=app.test"`} {
		line := "scrcpy.exe --serial TEST0001 --new-display=1280x720 " + command
		if !scrcpySessionMatch(line, []string{"TEST0001"}, "1280x720", "+app.test") {
			t.Fatal(line)
		}
		if len(bringAppWinCandidates([]scrcpyProc{{pid: 7, cmdline: line}}, []string{"TEST0001"}, "app.test")) != 1 {
			t.Fatal(line)
		}
	}
	for _, command := range []string{"--start-app=app.test.extra", "--start-app=+app.testing", "--window-title=--start-app=app.test"} {
		if scrcpyAppMatches(command, "app.test") {
			t.Fatal(command)
		}
	}
}

// --- 本会话 scrcpy 命令行判定（防前缀误杀） ---

func TestScrcpyCmdlineMatches(t *testing.T) {
	serials := []string{"TEST0001", "192.0.2.197:5555"}
	cases := []struct {
		cmdline string
		want    bool
	}{
		{`"C:\x\scrcpy.exe" --serial TEST0001 --keyboard=uhid`, true},
		{`--serial TEST0001`, true}, // 行尾（无尾随空格）
		{`--serial 192.0.2.197:5555 --max-size 1920`, true},
		{`--serial TEST00011 --keyboard=uhid`, false}, // 前缀撞串：别台设备，绝不误杀
		{`--serial TEST0002 --keyboard=uhid`, false},  // 平板（其他会话）
		{`C:\x\scrcpy.exe --serial=TEST0001`, false},  // 等号形式（bat 不用，保守不匹配）
		{`no serial here`, false},
	}
	for _, c := range cases {
		if got := scrcpyCmdlineMatches(c.cmdline, serials); got != c.want {
			t.Fatalf("cmdline=%q want=%v got=%v", c.cmdline, c.want, got)
		}
	}
}

// --- 残余 scrcpy 候选选择：父链 + 命令行（serial+形态），多会话隔离 ---

func TestResidualScrcpyCandidates(t *testing.T) {
	procs := []scrcpyProc{
		{pid: 400, ppid: 99, cmdline: `scrcpy.exe --serial TEST0002`},        // 别的会话（平板）
		{pid: 300, ppid: 42, cmdline: `scrcpy.exe --serial TEST0001`},        // 本会话：命令行命中
		{pid: 100, ppid: 42, cmdline: `scrcpy.exe --serial x`},               // 本会话：父链命中
		{pid: 200, ppid: 42, cmdline: `scrcpy.exe --serial TEST0001`},        // 本会话：双命中
		{pid: 500, ppid: 7, cmdline: `scrcpy.exe --serial 192.0.2.197:5555`}, // 本会话：无线候选命中
	}
	// 本会话=主投屏（无虚拟屏形态特征）
	got := residualScrcpyCandidates(procs, 42, []string{"TEST0001", "192.0.2.197:5555"}, "", "")
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
	if got := residualScrcpyCandidates(procs, 4242, nil, "", ""); len(got) != 0 {
		t.Fatalf("无命中应空: %+v", got)
	}
}

// v2.1.30 穿透回归：同设备主投屏 + 虚拟屏并存（scrcpy --serial 相同），
// Stop 的残余判定必须靠形态特征区分——主投屏 Stop 不得命中虚拟屏进程（曾实锤：
// 停止主投屏把虚拟屏一起优雅关窗杀掉），反向同理。
func TestResidualScrcpyNoBleedSameDevice(t *testing.T) {
	procs := []scrcpyProc{
		{pid: 600, ppid: 7, cmdline: `"C:\x\scrcpy.exe" --serial TEST0001 --keyboard=uhid --max-size 2560`},                                            // 主投屏（他会话）
		{pid: 700, ppid: 8, cmdline: `"C:\x\scrcpy.exe" --serial TEST0001 --new-display=1280x720/240 --flex-display --start-app=+com.android.browser`}, // 虚拟屏（本会话）
		{pid: 800, ppid: 9, cmdline: `"C:\x\scrcpy.exe" --serial TEST0001 --new-display=1280x720/240 --start-app=+com.android.settings`},               // 同设备另一应用窗口
	}
	// 主投屏 Stop（无形态特征）：只命中主投屏进程 600
	got := residualScrcpyCandidates(procs, 42, []string{"TEST0001"}, "", "")
	if len(got) != 1 || got[0].pid != 600 {
		t.Fatalf("主投屏 Stop 只能命中主投屏进程（防穿透）: %+v", got)
	}
	// 虚拟屏 Stop（形态=1280x720 + browser）：只命中 700
	got = residualScrcpyCandidates(procs, 42, []string{"TEST0001"}, "1280x720", "+com.android.browser")
	if len(got) != 1 || got[0].pid != 700 {
		t.Fatalf("虚拟屏 Stop 只能命中本应用窗口进程: %+v", got)
	}
}

// --- serial 候选并集（会话键 + SCEZ_SERIAL + SCEZ_ADDR 去重） ---

func TestSerialCandidates(t *testing.T) {
	got := serialCandidates("TEST0002", CastParams{Serial: "TEST0002", Addr: "192.0.2.162:5555"})
	if len(got) != 2 || got[0] != "TEST0002" || got[1] != "192.0.2.162:5555" {
		t.Fatalf("去重并集错误: %+v", got)
	}
	if got := serialCandidates("", CastParams{}); len(got) != 0 {
		t.Fatalf("全空应零候选: %+v", got)
	}
	if got := serialCandidates(" TEST0001 ", CastParams{}); len(got) != 1 || got[0] != "TEST0001" {
		t.Fatalf("应去空白: %+v", got)
	}
}

// --- 标签点击浮前：本会话 scrcpy 候选（纯命令行匹配） ---

// 浮前候选只按 --serial 命令行匹配（父链不参与——会话重启后父 pid 已变），
// 只命中本会话 serial 候选，绝不把别的会话窗口浮前；pid 去重 + 升序。
func TestBringToFrontCandidates(t *testing.T) {
	procs := []scrcpyProc{
		{pid: 400, ppid: 99, cmdline: `scrcpy.exe --serial TEST0002`}, // 别的会话（平板）
		{pid: 300, ppid: 42, cmdline: `scrcpy.exe --serial TEST0001`}, // 本会话
		{pid: 100, ppid: 77, cmdline: `scrcpy.exe --serial 192.0.2.197:5555`},
		{pid: 200, ppid: 88, cmdline: `scrcpy.exe --serial TEST0001 --max-size 1920`},
		{pid: 200, ppid: 88, cmdline: `scrcpy.exe --serial TEST0001 --max-size 1920`}, // 重复 pid 去重
		{pid: 500, ppid: 42, cmdline: `scrcpy.exe --serial TEST00011`},                // 前缀撞串：别台设备
	}
	got := bringToFrontCandidates(procs, []string{"TEST0001", "192.0.2.197:5555"})
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
	if got := bringToFrontCandidates(procs, []string{"TEST0002"}); len(got) != 1 || got[0].pid != 400 {
		t.Fatalf("平板候选应只命中平板: %+v", got)
	}
	// v2.1.30：虚拟屏（应用窗口）不进主投屏浮前候选——同设备并存时点主投屏
	// 标签，不应把虚拟屏窗口也一起提到前面。
	procs2 := []scrcpyProc{
		{pid: 600, ppid: 7, cmdline: `scrcpy.exe --serial TEST0001 --max-size 2560`},
		{pid: 700, ppid: 8, cmdline: `scrcpy.exe --serial TEST0001 --new-display=1280x720/240 --start-app=+com.android.browser`},
	}
	if got := bringToFrontCandidates(procs2, []string{"TEST0001"}); len(got) != 1 || got[0].pid != 600 {
		t.Fatalf("浮前候选应排除虚拟屏: %+v", got)
	}
}

// v3 修复组合场景（实况）：平板会话键=TEST0002（USB），scrcpy 实际命令行
// --serial 192.0.2.162:5555（无线）——候选集由 app.frontCandidateSerials
// 从档案补全（键+serials+addrs=[TEST0002, 162:5555]）后，此处纯命令行匹配
// 必须命中（含"无线地址直连"与"卡片重键回 USB"两个方向）。
func TestBringToFrontCandidatesProfileAddrCombo(t *testing.T) {
	candidates := []string{"TEST0002", "192.0.2.162:5555"}
	procs := []scrcpyProc{
		{pid: 100, ppid: 7, cmdline: `"C:\x\scrcpy.exe" --serial 192.0.2.162:5555 --max-size 1920`}, // 无线直连（v3 实况）
		{pid: 200, ppid: 8, cmdline: `scrcpy.exe --serial TEST0002`},                                // USB 形态
		{pid: 300, ppid: 9, cmdline: `scrcpy.exe --serial 192.0.2.162:5556`},                        // 前缀撞串：别台设备
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

// v2.1.46：点击应用卡片浮前——候选=指定包名的虚拟屏进程（serial 命中 +
// 含 --new-display + --start-app=+<pkg> 精确命中）；主投屏（无 --new-display）
// 与其他应用窗口（不同包名/其他设备）都不吃。
func TestBringAppWinCandidates(t *testing.T) {
	procs := []scrcpyProc{
		{pid: 100, ppid: 7, cmdline: `"C:\x\scrcpy.exe" --serial TEST0001 --keyboard=uhid --max-size 2560`},                                                      // 主投屏：不吃
		{pid: 200, ppid: 8, cmdline: `"C:\x\scrcpy.exe" --serial TEST0001 --new-display=1280x720/240 --start-app=+com.android.browser --window-title=browser`},   // 目标
		{pid: 300, ppid: 9, cmdline: `"C:\x\scrcpy.exe" --serial TEST0001 --new-display=1280x720/240 --start-app=+com.android.settings --window-title=settings`}, // 同设备其他应用窗口：不吃
		{pid: 400, ppid: 10, cmdline: `"C:\x\scrcpy.exe" --serial TEST0002 --new-display=1280x720/240 --start-app=+com.android.browser`},                         // 其他设备：不吃
	}
	got := bringAppWinCandidates(procs, []string{"TEST0001"}, "com.android.browser")
	if len(got) != 1 || got[0].pid != 200 {
		t.Fatalf("应只命中目标应用窗口: %+v", got)
	}
	if got := bringAppWinCandidates(procs, []string{"TEST0001"}, ""); len(got) != 2 {
		t.Fatalf("空 pkg=不限包名（本设备两路虚拟屏）: %+v", got)
	}
	if got := bringAppWinCandidates(procs, []string{"TEST0001"}, "com.not.exist"); len(got) != 0 {
		t.Fatalf("不存在的包名应零命中: %+v", got)
	}
}
