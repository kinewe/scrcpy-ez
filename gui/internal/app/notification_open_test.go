package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/notifications"
	"testing"
	"time"
)

type testNotificationOpener struct {
	valid     bool
	display   int
	calls     int
	request   notifications.OpenRequest
	openErr   error
	afterOpen func()
}

func (s *testNotificationOpener) Current(notifications.OpenRequest) bool { return s.valid }
func (s *testNotificationOpener) Open(_ context.Context, request notifications.OpenRequest, display int) error {
	s.display = display
	s.request = request
	s.calls++
	if s.afterOpen != nil {
		s.afterOpen()
	}
	return s.openErr
}

func TestNotificationRemovedAfterAcceptedActionKeepsDetailWindow(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb", Identity: "tablet", Name: "Pad"}})
	_ = e.a.profiles.Save("tablet", DeviceProfile{})
	source := &testNotificationOpener{valid: true}
	source.afterOpen = func() {
		source.valid = false
		e.fireLine("INFO: Texture (D3D11VA): 1280x720")
	}
	request := notifications.OpenRequest{Identity: "tablet", Package: "com.example.mail", OwnerPackage: "com.example.mail", App: "邮件"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.a.OpenNotification(ctx, request, source) }()
	e.f.waitParams(t, 1)
	e.fireLine("SCRCPY_EZ_READY pid=987654321")
	e.fireLine("[server] INFO: New display: 1280x720 (id=17)")
	e.fireLine("[server] INFO: SCRCPY_EZ_NOTIFICATION_DISPLAY_READY id=17")
	if err := <-done; err != nil {
		t.Fatal("opening detail canceled when app withdrew its notification:", err)
	}
	if source.calls != 1 || len(e.a.Snapshot().AppWins) != 1 {
		t.Fatal("accepted action lost its existing detail window")
	}
}

func TestNotificationDeviceSwitchPersistsAndDefaultsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// Legacy global trial choices do not override the new device defaults.
	if err := os.WriteFile(path, []byte(`{"notificationOpenEnabled":false,"notificationPolicies":{"tablet":{"mode":"all"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsStore(path)
	if err := settings.Load(); err != nil {
		t.Fatal(err)
	}
	if !settings.Get().NotificationPolicy("tablet").DetailEnabled() || !settings.Get().NotificationPolicy("phone").DetailEnabled() {
		t.Fatal("missing per-device setting must default on")
	}
	enabled := false
	if err := settings.ApplyNotificationEdit("tablet", NotificationEdit{OpenEnabled: &enabled}, false); err != nil {
		t.Fatal(err)
	}
	// Settings own their pointers, so caller and snapshot mutations cannot leak.
	enabled = true
	snapshot := settings.Get()
	*snapshot.NotificationPolicies["tablet"].OpenEnabled = true
	reloaded := NewSettingsStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.Get().NotificationPolicy("tablet").DetailEnabled() || settings.Get().NotificationPolicy("tablet").DetailEnabled() || !reloaded.Get().NotificationPolicy("phone").DetailEnabled() {
		t.Fatal("switch persistence, pointer ownership or device isolation failed")
	}
	if err := reloaded.SetNotificationModes([]string{"tablet"}, "otp"); err != nil {
		t.Fatal(err)
	}
	if reloaded.Get().NotificationPolicy("tablet").DetailEnabled() {
		t.Fatal("mode change reset switch")
	}
}

func TestNotificationCreatesBlankDisplayThenReusesItWithoutRestart(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb", Identity: "tablet", Name: "Pad"}})
	if err := e.a.profiles.Save("tablet", DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	source := &testNotificationOpener{valid: true}
	request := notifications.OpenRequest{Identity: "tablet", Package: "com.example.mail", OwnerPackage: "com.example.mail", App: "邮件"}
	if err := e.a.SaveAppWinParams("tablet", request.Package,
		`{"mode":"usb","size":"1280","ratioW":4,"ratioH":3,"fps":60,"bitrate":8,"flex":true}`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.a.OpenNotification(context.Background(), request, source) }()
	params := e.f.waitParams(t, 1)
	if params.StartApp != "" || !params.VdKeepContent || params.AppPowerSavePackage != request.Package {
		t.Fatalf("notification must not start/force-stop app: %+v", params)
	}
	if params.VdUsb.Size != "1280x960" || !params.VdUsb.Flex || params.VdUsb.FollowMainOrientation {
		t.Fatalf("notification ignored the selected ratio or Flex: %+v", params.VdUsb)
	}
	deadline := time.Now().Add(time.Second)
	for {
		e.a.mu.RLock()
		window := e.a.appWins[appWinKey("tablet", request.Package)]
		e.a.mu.RUnlock()
		if window != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("window state missing")
		}
		time.Sleep(time.Millisecond)
	}
	// Opposite pipe arrival orders are both valid; no first-texture deadlock.
	e.fireLine("SCRCPY_EZ_READY pid=0")
	e.fireLine("[server] INFO: New display: 1280x960 (id=17)")
	e.fireLine("SCRCPY_EZ_READY pid=987654321")
	e.fireLine("INFO: Texture (D3D11VA): 1280x960")
	e.fireLine("[server] INFO: SCRCPY_EZ_NOTIFICATION_DISPLAY_READY id=18")
	select {
	case err := <-done:
		t.Fatalf("notification sent before its display completed the power-save setup: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_NOTIFICATION_DISPLAY_READY id=17")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if source.display != 17 || source.calls != 1 {
		t.Fatal("wrong detail destination")
	}
	if err := e.a.OpenNotification(context.Background(), request, source); err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 || e.f.startsN() != 1 {
		t.Fatal("second detail click restarted window")
	}
	disabled := false
	_ = e.a.settings.ApplyNotificationEdit("tablet", NotificationEdit{OpenEnabled: &disabled}, false)
	if e.a.OpenNotification(context.Background(), request, source) == nil || source.calls != 2 {
		t.Fatal("disabled switch accepted an old action")
	}
}

func TestNotificationReusesOrdinaryAppCast(t *testing.T) {
	e := newAppWinEnv(t)
	const serial = "12345TESTA"
	const identity = "device:" + serial
	setDevices(e.a, []adb.Device{{Serial: serial, State: "device", ConnType: "usb", Identity: identity, Name: "Pad"}})
	if err := e.a.profiles.Save(identity, DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	request := notifications.OpenRequest{Identity: identity, Package: "com.example.mail", OwnerPackage: "com.example.mail", Key: "message-2", Token: "detail-token"}
	if err := e.a.StartAppWin(serial, request.Package, "Mail"); err != nil {
		t.Fatal(err)
	}
	e.a.mu.RLock()
	window := e.a.appWins[appWinKey(serial, request.Package)]
	e.a.mu.RUnlock()
	if window == nil {
		t.Fatal("ordinary application window missing")
	}
	params := e.f.waitParams(t, 1)
	if params.StartApp != request.Package || params.AppPowerSavePackage != "" {
		t.Fatal("ordinary application launch changed")
	}
	e.fireLine("[server] INFO: New display: 1280x720 (id=17)")
	e.fireLine("SCRCPY_EZ_READY pid=987654321")
	e.fireLine("INFO: Texture (D3D11VA): 1280x720")
	source := &testNotificationOpener{valid: true}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := e.a.OpenNotification(ctx, request, source); err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || source.request != request || source.display != 17 {
		t.Fatal("the original notification action did not go to the existing display")
	}
	source.openErr = errors.New("notification action canceled")
	if err := e.a.OpenNotification(ctx, request, source); !errors.Is(err, source.openErr) {
		t.Fatalf("unexpected error: %v", err)
	}
	e.a.mu.RLock()
	sameWindow := e.a.appWins[appWinKey(serial, request.Package)] == window && !window.closing
	e.a.mu.RUnlock()
	if !sameWindow || e.f.startsN() != 1 {
		t.Fatal("opening a notification replaced or closed the ordinary app window")
	}
}

func TestNotificationAcceptedActionWithoutVideoClosesOnlyNewWindow(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb", Identity: "tablet", Name: "Pad"}})
	_ = e.a.profiles.Save("tablet", DeviceProfile{})
	source := &testNotificationOpener{valid: true}
	request := notifications.OpenRequest{Identity: "tablet", Package: "com.example.mail", OwnerPackage: "com.example.mail", App: "邮件"}
	e.a.RecordNotificationOpenClick(request)
	entry, _ := e.a.profiles.Entry("tablet")
	if entry.AppLauncher == nil || len(entry.AppLauncher.Recent) != 1 || entry.AppLauncher.Recent[0] != request.Package {
		t.Fatal("notification click should count before launching a window")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.a.OpenNotification(ctx, request, source) }()
	e.f.waitParams(t, 1)
	deadline := time.Now().Add(time.Second)
	for {
		e.a.mu.RLock()
		window := e.a.appWins[appWinKey("tablet", request.Package)]
		e.a.mu.RUnlock()
		if window != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("window state missing")
		}
		time.Sleep(time.Millisecond)
	}
	e.fireLine("SCRCPY_EZ_READY pid=987654321")
	e.fireLine("[server] INFO: New display: 1280x720 (id=17)")
	e.fireLine("[server] INFO: SCRCPY_EZ_NOTIFICATION_DISPLAY_READY id=17")
	if err := <-done; err == nil {
		t.Fatal("empty decoder claimed detail success")
	}
	e.f.waitStops(t, 1)
	if source.calls != 1 {
		t.Fatal("original action was retried")
	}
}

func TestNotificationParameterRestartDoesNotForceStopApp(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "PHONE_TEST", State: "device", ConnType: "usb"}})
	if err := e.a.startAppWin("PHONE_TEST", "com.example.mail", "Mail", true); err != nil {
		t.Fatal(err)
	}
	first := e.f.waitParams(t, 1)
	if first.StartApp != "" || !first.VdKeepContent || first.AppPowerSavePackage != "com.example.mail" {
		t.Fatal("initial notification launched app home")
	}
	if err := e.a.SaveAppWinParams("PHONE_TEST", "com.example.mail", `{"mode":"usb","size":"1920x1080","fps":60,"bitrate":8,"flex":true,"audio":"phone"}`); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
	e.fireExit(1)
	next := e.f.waitParams(t, 2)
	if next.StartApp != "" || !next.VdKeepContent || next.AppPowerSavePackage != first.AppPowerSavePackage {
		t.Fatal("notification restart switched to force-stop/home launch")
	}
	e.a.mu.RLock()
	window := e.a.appWins[appWinKey("PHONE_TEST", "com.example.mail")]
	stillNotification := window != nil && window.notificationWindow
	e.a.mu.RUnlock()
	if !stillNotification {
		t.Fatal("restart lost notification window behavior")
	}
}
