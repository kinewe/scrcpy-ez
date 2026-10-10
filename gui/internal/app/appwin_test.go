package app

// 应用窗口（v2.1.27：走 bat 的虚拟屏会话）生命周期测试。
// 覆盖：①启动参数注入（VD 默认档 + 设备锁定 + 等比 dpi）②closing 透传
// ③bat 退出摘除（含用户手关窗口）④停止/启动幂等 ⑤停止失败复位。

import (
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// appWinEnv 测试装置：App + 可捕获 onLine/onExit 的 fake runner。
type appWinEnv struct {
	a      *App
	f      *fakeRunner
	exitFn func(int)
	lineFn func(string)
}

func newAppWinEnv(t *testing.T) *appWinEnv {
	t.Helper()
	e := &appWinEnv{f: &fakeRunner{exitCode: -1}}
	e.a = New(Config{BatPath: `C:\x\投屏支持.bat`, AdbPath: `C:\x\adb.exe`, Version: "test"})
	e.a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		e.exitFn = onExit
		e.lineFn = onLine
		return e.f, nil
	})
	// 预置物理参数（跳过 adb 查询）：等比 dpi 按各套尺寸现算（v2.1.48 起默认档=设备推导）。
	e.a.physMu.Lock()
	e.a.physCache["device:12345TESTA"] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
	e.a.physMu.Unlock()
	return e
}

// fireExit 模拟 bat 进程退出（bridge waitLoop → onExit 回调）。
func (e *appWinEnv) fireExit(code int) {
	if e.exitFn != nil {
		e.exitFn(code)
	}
}

// fireLine 模拟 bat 输出行到达（bridge readLoop → onLine 回调）。
func (e *appWinEnv) fireLine(line string) {
	if e.lineFn != nil {
		e.lineFn(line)
	}
}

func waitAppWins(t *testing.T, a *App, n int) []AppWinItem {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		list := a.Snapshot().AppWins
		if len(list) == n {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待应用窗口数=%d 超时（当前 %d）", n, len(list))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 启动注入：默认档（v2.1.48 起=设备推导共享主投屏：有线长边/刷新率 + 尺寸按宽高比
// 换算）+ 等比 dpi + flex + IME=local + "+pkg" + 窗口标题 + 设备锁定（USB serial）。
func TestAppWinStartInjectsVdParams(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{
		{Serial: "12345TESTA", State: "device", ConnType: "usb", Name: "K80", Identity: "K80", Res: "3200x1440", FPS: 120},
	})
	if err := e.a.StartAppWin("12345TESTA", "com.android.browser", "浏览器"); err != nil {
		t.Fatal(err)
	}
	p := e.f.waitParams(t, 1)
	// v2.1.51：推导长边 snap 档位表——设备长边 3200 → 2560（与 bat 有线档分配同口径）。
	if p.VdSize != "2560x1152" || p.VdDpi != 480 || !p.VdFlex || p.VdIme != "local" {
		t.Fatalf("虚拟屏默认档参数缺失: %+v", p)
	}
	if p.StartApp != "com.android.browser" || !p.ReuseAppTask || !p.VdKeepContent {
		t.Fatalf("任务复用参数丢失: %+v", p)
	}
	if p.WinTitle != "浏览器" {
		t.Fatalf("WinTitle=%q，期望 浏览器", p.WinTitle)
	}
	if p.Serial != "12345TESTA" {
		t.Fatalf("设备锁定注入丢失: Serial=%q", p.Serial)
	}
	list := waitAppWins(t, e.a, 1)
	if list[0].Pkg != "com.android.browser" || list[0].Closing {
		t.Fatalf("快照条目异常: %+v", list[0])
	}
}

// 停止链路：受理 → closing=true（快照透传，前端"正在关闭…"遮罩）→
// runner.Stop → bat 退出（onExit）→ 摘除（前端卡片淡出）。
func TestAppWinStopClosingThenExit(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "com.android.browser", "浏览器"); err != nil {
		t.Fatal(err)
	}
	e.f.waitStarts(t, 1)

	if err := e.a.StopAppWin("12345TESTA", "com.android.browser"); err != nil {
		t.Fatal(err)
	}
	// 受理后：条目仍在、closing=true。
	list := waitAppWins(t, e.a, 1)
	if !list[0].Closing {
		t.Fatalf("停止受理后应 closing=true: %+v", list[0])
	}
	e.f.waitStops(t, 1)

	// 模拟 bat 退出 → 摘除。
	e.fireExit(0)
	waitAppWins(t, e.a, 0)

	// 幂等：已无会话，再停止不报错。
	if err := e.a.StopAppWin("12345TESTA", "com.android.browser"); err != nil {
		t.Fatal(err)
	}
}

// 幂等：重复启动不新建会话；停止受理后重复点击不重复杀。
func TestAppWinIdempotent(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "pkg.a", "A"); err != nil {
		t.Fatal(err)
	}
	if err := e.a.StartAppWin("12345TESTA", "pkg.a", "A"); err != nil {
		t.Fatal(err)
	}
	if n := e.f.startsN(); n != 1 {
		t.Fatalf("重复启动不应新建会话（starts=%d）", n)
	}

	_ = e.a.StopAppWin("12345TESTA", "pkg.a")
	_ = e.a.StopAppWin("12345TESTA", "pkg.a")
	e.f.waitStops(t, 1)
	time.Sleep(50 * time.Millisecond)
	if n := e.f.stopsN(); n != 1 {
		t.Fatalf("重复停止不应重复杀树（stops=%d）", n)
	}
}

// 用户手关窗口：无停止受理，bat 直接退出（scrcpy 码 0）→ 摘除。
func TestAppWinUserCloseRemoves(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "pkg.x", "X"); err != nil {
		t.Fatal(err)
	}
	e.f.waitStarts(t, 1)
	e.fireExit(0)
	waitAppWins(t, e.a, 0)
}

// 停止失败（runner.Stop 报错）→ closing 复位（按钮可重试；会话保留）。
func TestAppWinStopFailureResetsClosing(t *testing.T) {
	f := &errStopRunner{}
	a := New(Config{BatPath: `C:\x\投屏支持.bat`, AdbPath: `C:\x\adb.exe`, Version: "test"})
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		return f, nil
	})
	setDevices(a, []adb.Device{{Serial: "S1", State: "device", ConnType: "usb"}})
	a.physMu.Lock()
	a.physCache["device:S1"] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
	a.physMu.Unlock()

	if err := a.StartAppWin("S1", "pkg.b", "B"); err != nil {
		t.Fatal(err)
	}
	if err := a.StopAppWin("S1", "pkg.b"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		list := a.Snapshot().AppWins
		if len(list) == 1 && !list[0].Closing {
			return // 复位成功
		}
		if time.Now().After(deadline) {
			t.Fatalf("停止失败后 closing 应复位: %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 多应用并发：同设备两路应用窗口各自独立会话/runner。
func TestAppWinParallelTwoApps(t *testing.T) {
	rec := &fakeRecorder{by: map[string][]*fakeRunner{}}
	a := New(Config{BatPath: `C:\x\投屏支持.bat`, AdbPath: `C:\x\adb.exe`, Version: "test"})
	a.SetRunnerFactory(func(serial string, onLine func(string), onExit func(int)) (Runner, error) {
		f := &fakeRunner{exitCode: -1}
		rec.add(serial, f)
		return f, nil
	})
	setDevices(a, []adb.Device{{Serial: "S2", State: "device", ConnType: "wifi"}})
	a.physMu.Lock()
	a.physCache["device:S2"] = devPhys{longSide: 3200, dpi: 600, at: time.Now()}
	a.physMu.Unlock()

	if err := a.StartAppWin("S2", "pkg.one", "一"); err != nil {
		t.Fatal(err)
	}
	if err := a.StartAppWin("S2", "pkg.two", "二"); err != nil {
		t.Fatal(err)
	}
	waitAppWins(t, a, 2)
}

// v2.1.32：物理参数缓存 miss 时启动必须立即返回（不注入 dpi，后台预热）——
// StartAppWin 跑在 GUI 消息循环线程上，同步 adb 查询会冻结整个界面
// （实测"启动虚拟屏时 GUI 卡一下、无法操作"+ 两个未隐藏的控制台窗口）。
func TestAppWinStartFastOnPhysCacheMiss(t *testing.T) {
	e := newAppWinEnv(t)
	e.a.physMu.Lock()
	delete(e.a.physCache, "device:12345TESTA") // 清掉预置缓存 → 触发 miss 路径
	e.a.physMu.Unlock()
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})

	start := time.Now()
	if err := e.a.StartAppWin("12345TESTA", "com.android.browser", "浏览器"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("缓存 miss 时启动不应阻塞（耗时 %v）", elapsed)
	}
	p := e.f.waitParams(t, 1)
	if p.VdDpi != 0 {
		t.Fatalf("缓存 miss 时不应注入 dpi（got %d）", p.VdDpi)
	}
	if p.VdSize == "" || p.StartApp != "com.android.browser" || !p.ReuseAppTask {
		t.Fatalf("其余参数应正常注入: %+v", p)
	}
}

// 用户手关窗口（v2.1.28 修复）：bat 输出哨兵行/确认行 → 立即置 closing
// （快照透传 → 前端"正在关闭…"；此前只在 GUI 点停止时才显示）→ bat 退出摘除。
func TestAppWinUserCloseLineSetsClosing(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"scrcpy 哨兵行", "SCRCPY_EZ_USER_CLOSE"},
		{"bat 确认行", "[提示] 已检测到窗口关闭（退出码 0），投屏已结束，退出投屏循环"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newAppWinEnv(t)
			setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
			if err := e.a.StartAppWin("12345TESTA", "pkg.c", "C"); err != nil {
				t.Fatal(err)
			}
			e.f.waitStarts(t, 1)

			e.fireLine(tc.line)
			list := waitAppWins(t, e.a, 1)
			if !list[0].Closing {
				t.Fatalf("关窗行到达后应 closing=true: %+v", list[0])
			}
			// bat 随后退出 → 摘除（前端淡出）。
			e.fireExit(0)
			waitAppWins(t, e.a, 0)
		})
	}
}
