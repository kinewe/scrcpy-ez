package app

import (
	"encoding/json"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestPerspectiveDefaultAndExplicitOptOutSurviveReload(t *testing.T) {
	if !DefaultAppWinParams().Usb.Perspective || !vdModeFromBaseline(Baseline{}).Perspective {
		t.Fatal("new window default disabled")
	}
	var old AppWinModeParams
	if err := json.Unmarshal([]byte(`{"size":"1920","fps":60}`), &old); err != nil || !old.Perspective {
		t.Fatal(old, err)
	}
	old.Perspective = false
	data, err := json.Marshal(old)
	if err != nil || !strings.Contains(string(data), `"perspective":false`) {
		t.Fatal(string(data), err)
	}
	var restored AppWinModeParams
	if err = json.Unmarshal(data, &restored); err != nil || restored.Perspective {
		t.Fatal(restored, err)
	}
}

func TestCameraToggleKeepsQQWindowGeometry(t *testing.T) {
	p := vdModeFromBaseline(Baseline{Res: 1920, FPS: 60, Bitrate: 15})
	phys := devPhys{width: 1440, height: 3200, longSide: 3200, dpi: 600}
	for _, pkg := range []string{"com.tencent.mobileqq", "com.tencent.mm", "com.xingin.xhs"} {
		p.Perspective = false
		before := appWindowVdParamsForPackage(pkg, p, phys, "3200x1440")
		p.Perspective = true
		after := appWindowVdParamsForPackage(pkg, p, phys, "3200x1440")
		after.Perspective = false
		if before != after {
			t.Fatalf("camera changed %s geometry: before=%+v after=%+v", pkg, before, after)
		}
	}
}

func TestForcePerspectiveColorKeyAtAppLaunch(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		force, usbCamera, usbKey bool
		wantUSB, wantWiFi        bool
	}{
		{"native by default", false, true, false, false, false},
		{"ignore legacy per-app override", false, true, true, false, false},
		{"global compatibility", true, true, false, true, true},
		{"camera disabled on USB", true, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newAppWinParamsEnv(t)
			const serial, pkg = "K80", "app.test"
			setDevices(e.a, []adb.Device{{Serial: serial, State: "device", ConnType: "usb"}})
			if err := e.a.SetForcePerspectiveColorKey(tc.force); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{
				"mode": "usb", "size": "1280", "fps": 60, "bitrate": 8, "flex": true,
				"perspective": tc.usbCamera, "perspectiveColorKey": tc.usbKey,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.a.SaveAppWinParams(serial, pkg, string(payload)); err != nil {
				t.Fatal(err)
			}
			if tc.usbKey {
				const currentUI = `{"mode":"usb","size":"1280","fps":60,"bitrate":8,"flex":true,"perspective":true}`
				if err := e.a.SaveAppWinParams(serial, pkg, currentUI); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.a.StartAppWin(serial, pkg, "Test"); err != nil {
				t.Fatal(err)
			}
			params := e.f.waitParams(t, 1)
			if params.PerspectiveCamera != tc.usbCamera || params.PerspectiveColorKey != tc.wantUSB ||
				params.VdUsb.PerspectiveColorKey != tc.wantUSB || params.VdWifi.PerspectiveColorKey != tc.wantWiFi {
				t.Fatalf("wrong launch policy: %+v", params)
			}
			profile, err := e.a.GetAppWinParams(serial, pkg)
			if err != nil || profile.Usb.PerspectiveColorKey != tc.usbKey || profile.Wifi.PerspectiveColorKey {
				t.Fatalf("saved profile changed by global preference: %+v %v", profile, err)
			}
		})
	}
}

func TestCameraToggleRequiresExplicitProcessRestart(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	first := e.f.waitParams(t, 1)
	if !first.PerspectiveCamera {
		t.Fatal("default missing")
	}
	save := func() {
		t.Helper()
		if err := e.a.SaveAppWinParams("12345TESTA", "app.test", `{"mode":"usb","size":"1280","fps":60,"bitrate":8,"flex":true,"perspective":false}`); err != nil {
			t.Fatal(err)
		}
	}
	save()
	notice := e.a.Snapshot().AppWins[0]
	if notice.Phase != "app-camera-mode-change" || notice.LaunchFailureID == 0 || e.f.stopsN() != 0 {
		t.Fatal(notice)
	}
	if p, _ := e.a.appWinParamsFor("12345TESTA", "app.test"); !p.Usb.Perspective {
		t.Fatal("unconfirmed change saved")
	}
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", notice.LaunchFailureID, false); err != nil {
		t.Fatal(err)
	}
	if e.f.stopsN() != 0 || len(e.a.Snapshot().AppWins) != 1 {
		t.Fatal("cancel disrupted projection")
	}
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", notice.LaunchFailureID, true); err == nil {
		t.Fatal("stale confirmation accepted")
	}
	save()
	notice = e.a.Snapshot().AppWins[0]
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", notice.LaunchFailureID, true); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
	e.fireExit(0)
	next := e.f.waitParams(t, 2)
	if next.PerspectiveCamera || next.StartApp != "+app.test" {
		t.Fatal(next)
	}
}

func TestCameraPermissionNoticeDeduplicatesWithoutRestart(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("[server] WARN: SCRCPY_EZ_CAMERA_PERMISSION_REQUIRED")
	id := e.a.Snapshot().AppWins[0].CameraPermissionID
	e.fireLine("[server] WARN: SCRCPY_EZ_CAMERA_PERMISSION_REQUIRED")
	e.fireLine("INFO: Texture: 1280x720")
	item := e.a.Snapshot().AppWins[0]
	if id == 0 || item.CameraPermissionID != id || item.LaunchFailureID != 0 || e.f.stopsN() != 0 {
		t.Fatal(item)
	}
}

func TestCameraOpenSettingsDoNotRebuildSilently(t *testing.T) {
	e := newAppWinEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "12345TESTA", State: "device", ConnType: "usb"}})
	if err := e.a.StartAppWin("12345TESTA", "app.test", "Test"); err != nil {
		t.Fatal(err)
	}
	e.fireLine("INFO: SCRCPY_EZ_PERSPECTIVE_ACTIVE=1")
	if err := e.a.SaveAppWinParams("12345TESTA", "app.test", `{"mode":"usb","size":"1280","fps":60,"bitrate":8,"flex":true,"perspective":true,"perspectiveInputFps":10}`); err != nil {
		t.Fatal(err)
	}
	item := e.a.Snapshot().AppWins[0]
	if item.Phase != "app-camera-mode-change" || item.LaunchFailureID == 0 || e.f.stopsN() != 0 {
		t.Fatal(item)
	}
	if err := e.a.ResolveAppLaunchFailure("12345TESTA", "app.test", item.LaunchFailureID, false); err != nil {
		t.Fatal(err)
	}
	if e.f.stopsN() != 0 {
		t.Fatal("cancel interrupted active camera")
	}
}
