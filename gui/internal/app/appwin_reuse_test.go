package app

import (
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

func TestAppReuseFailureSurvivesVideoReadyUntilNewAttempt(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=reuse-failed")
	e.fireLine("INFO: Texture: 1280x720")
	if list := e.a.Snapshot().AppWins; list[0].Phase != "app-reuse-failed" {
		t.Fatal(list)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=reused")
	if list := e.a.Snapshot().AppWins; list[0].Phase != "" {
		t.Fatal(list)
	}
}

func TestIncompatibleLayoutStatusIsNotClearedByVideoOrWatch(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "com.tencent.mm", "WeChat"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=layout-incompatible")
	e.fireLine("INFO: Texture: 1280x720")
	if list := e.a.Snapshot().AppWins; list[0].Phase != "app-layout-incompatible" {
		t.Fatal(list)
	}
}

func TestExplicitProcessRestartDoesNotPersistOrChangeWindowSpecs(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	first := e.f.waitParams(t, 1)
	if err := e.a.RestartAppProcess("12345TESTA", "app.test"); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
	e.fireExit(0)
	next := e.f.waitParams(t, 2)
	if next.StartApp != "+app.test" || !next.ReuseAppTask || !next.VdKeepContent || next.VdUsb != first.VdUsb || next.VdWifi != first.VdWifi {
		t.Fatalf("explicit restart changed specs or lost retention: %+v", next)
	}
	deadline := time.Now().Add(3 * time.Second)
	for e.a.Snapshot().AppWins[0].Phase == "restarting" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := e.a.RestartAppWin("12345TESTA", "app.test"); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 2)
	e.fireExit(0)
	if p := e.f.waitParams(t, 3); p.StartApp != "app.test" || !p.ReuseAppTask {
		t.Fatal(p)
	}
}

func TestNotificationWindowNeverAcquiresOrdinaryRestartPolicy(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.startAppWin("12345TESTA", "com.tencent.mm", "Test", true); err != nil {
		t.Fatal(err)
	}
	if p := e.f.waitParams(t, 1); p.ReuseAppTask || p.StartApp != "" || !p.VdKeepContent {
		t.Fatal(p)
	}
	if !e.a.Snapshot().AppWins[0].NotificationWindow {
		t.Fatal("lost origin")
	}
	if err := e.a.RestartAppProcess("12345TESTA", "com.tencent.mm"); err == nil {
		t.Fatal("notification force-stop allowed")
	}
	if n := e.f.stopsN(); n != 0 {
		t.Fatal(n)
	}
}

func TestLaunchNoticeTracksFailedAttemptAndClearsOnSuccess(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=layout-incompatible")
	id := e.a.Snapshot().AppWins[0].LaunchFailureID
	if id == 0 {
		t.Fatal("missing launch notice")
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=layout-incompatible")
	e.fireLine("INFO: Texture: 1280x720")
	if e.a.Snapshot().AppWins[0].LaunchFailureID != id {
		t.Fatal("duplicate notice")
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=reused")
	if e.a.Snapshot().AppWins[0].LaunchFailureID != 0 {
		t.Fatal("successful launch retained stale notice")
	}
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", id, true); err == nil {
		t.Fatal("stale notice restarted a successful cast")
	}
	if e.f.stopsN() != 0 {
		t.Fatal("stale notice stopped cast")
	}
}

func TestAnyAppInUseCreatesLaunchDialogNotice(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.other", "Other"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=in-use")
	e.fireLine("INFO: Texture: 1280x720")
	item := e.a.Snapshot().AppWins[0]
	if item.LaunchFailureID == 0 || item.Phase != "app-in-use" {
		t.Fatal(item)
	}
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.other", item.LaunchFailureID, false); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
}

func TestLaunchNoticeCancelAndReplacementCannotRestartWrongAttempt(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=reuse-failed")
	id := e.a.Snapshot().AppWins[0].LaunchFailureID
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", id, false); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
	if e.a.Snapshot().AppWins[0].LaunchFailureID != 0 {
		t.Fatal("closing cast retained active notice")
	}
	e.fireExit(0)
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=reuse-failed")
	if e.a.Snapshot().AppWins[0].LaunchFailureID <= id {
		t.Fatal("replacement reused notice identity")
	}
	for _, restart := range []bool{false, true} {
		if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", id, restart); err == nil {
			t.Fatal("stale notice affected replacement")
		}
	}
	if e.f.stopsN() != 1 {
		t.Fatal("replacement was stopped")
	}
}

func TestLaunchNoticeExplicitRestartAndNotificationIsolation(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=reuse-failed")
	id := e.a.Snapshot().AppWins[0].LaunchFailureID
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", id, true); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
	e.fireExit(0)
	if p := e.f.waitParams(t, 2); p.StartApp != "+app.test" {
		t.Fatal(p)
	}
	if err := e.a.startAppWin("12345TESTA", "com.tencent.mm", "WeChat", true); err != nil {
		t.Fatal(err)
	}
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "com.tencent.mm", id, true); err == nil {
		t.Fatal("notification window acquired restart fallback")
	}
}
