package app

import (
	"slices"
	"testing"
	"time"
)

func TestClosedAppLogSurvivesReopenAcrossTransports(t *testing.T) {
	a, _ := newTestApp()
	const identity, pkg = "device:PHONE_A", "com.tencent.mm"
	usb := "USB_A"
	r1, r2 := &fakeRunner{}, &fakeRunner{}
	a.appWins[appWinKey(usb, pkg)] = &appWinState{serial: usb, identity: identity, pkg: pkg, runner: r1}
	a.onAppWinLineFor(usb, pkg, r1, "旧会话的最后一行")
	a.onAppWinExit(usb, pkg, r1, 0)
	closed := a.snapshotRaw()
	if len(closed.AppWins) != 0 || len(closed.RecentCastLogs) != 1 || closed.RecentCastLogs[0].Log[0] != "旧会话的最后一行" {
		t.Fatalf("closed log did not outlive the app window: %+v", closed.RecentCastLogs)
	}
	if left := time.Until(closed.RecentCastLogs[0].expiresAt); left <= 9*time.Second || left > 10*time.Second {
		t.Fatalf("wrong retention period: %v", left)
	}

	wifi := "192.0.2.10:5555"
	a.appWins[appWinKey(wifi, pkg)] = &appWinState{serial: wifi, identity: identity, pkg: pkg, runner: r2}
	a.onAppWinLineFor(wifi, pkg, r1, "陈旧进程的输出")
	a.onAppWinExit(wifi, pkg, r1, 0)
	a.onAppWinLineFor(wifi, pkg, r2, "新会话第一行")
	open := a.snapshotRaw()
	want := []string{"旧会话的最后一行", "[EZ] 投屏已结束（退出码 0）", "[EZ] ── 新投屏会话 ──", "新会话第一行"}
	if len(open.RecentCastLogs) != 0 || len(open.AppWins) != 1 || !slices.Equal(open.AppWins[0].Log, want) {
		t.Fatalf("reopened app should have one log with distinct sessions: %+v", open.AppWins)
	}
	key := castLogKeyFor(usb, identity, pkg)
	old := a.recentCastLogs[key]
	old.expiresAt = time.Now().Add(-time.Millisecond)
	a.recentCastLogs[key] = old
	if got := a.snapshotRaw().AppWins[0].Log; !slices.Equal(got, []string{"新会话第一行"}) {
		t.Fatalf("expired output still mixed with new cast: %q", got)
	}
	a.promptTick()
	if len(a.recentCastLogs) != 0 {
		t.Fatal("expired output was not released")
	}
	a.onAppWinExit(wifi, pkg, r2, 0)
	if next := a.snapshotRaw().RecentCastLogs; len(next) != 1 || next[0].Log[0] != "新会话第一行" {
		t.Fatalf("second closure retained the wrong session: %+v", next)
	}
}

func TestMainCastLogOutlivesForgetSession(t *testing.T) {
	a, _ := newTestApp()
	r := &fakeRunner{}
	st := &sessionState{runner: r, identity: "device:PHONE_A", cast: CastState{Active: true, Log: []string{"最后一帧"}}}
	a.sessions["USB_A"] = st
	a.OnBatExitFor("USB_A", r, 0)
	a.ForgetSession("USB_A")
	snapshot := a.snapshotRaw()
	if len(snapshot.Sessions) != 0 || len(snapshot.RecentCastLogs) != 1 || snapshot.RecentCastLogs[0].Log[0] != "最后一帧" {
		t.Fatalf("forgetting live UI state lost the log: %+v", snapshot.RecentCastLogs)
	}
}

func TestAppCastTransportSwitchKeepsLiveOutput(t *testing.T) {
	e := newAppWinEnv(t)
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("无线阶段")
	e.fireLine("[自动切换] 检测到 USB 插线")
	e.fireLine("有线阶段")
	snapshot := e.a.snapshotRaw()
	if len(snapshot.RecentCastLogs) != 0 || len(snapshot.AppWins) != 1 ||
		!slices.Equal(snapshot.AppWins[0].Log, []string{"无线阶段", "[自动切换] 检测到 USB 插线", "有线阶段"}) {
		t.Fatalf("transport switch broke the live log: %+v", snapshot.AppWins)
	}
}
