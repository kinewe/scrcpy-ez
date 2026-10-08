package app

import (
	"os"
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestKeepDeviceAwakeSettingsAndFailedPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"showParamOverlay":false,"closeToTray":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsStore(path)
	if err := s.Load(); err != nil || !s.Get().KeepDeviceAwake {
		t.Fatal("old settings must enable main-cast idle protection by default", err)
	}
	if err := s.SetKeepDeviceAwake(true); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(false, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOtherAppWinSystemDecorations(false); err != nil {
		t.Fatal(err)
	}
	reloaded := NewSettingsStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	got := reloaded.Get()
	if !got.KeepDeviceAwake || got.ShowParamOverlay || !got.CloseToTray || got.OtherAppWinSystemDecorations {
		t.Fatalf("independent choices did not survive reload: %+v", got)
	}
	if err := reloaded.SetKeepDeviceAwake(false); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(); err != nil || reloaded.Get().KeepDeviceAwake {
		t.Fatal("explicit false was not persisted", err)
	}
	blocked := NewSettingsStore(filepath.Join(path, "blocked.json"))
	if err := blocked.SetKeepDeviceAwake(false); err == nil || !blocked.Get().KeepDeviceAwake {
		t.Fatal("failed save must not change the option in memory")
	}
}

func TestKeepDeviceAwakeMainAndAppSessions(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, transport := range []string{"usb", "wifi"} {
			for _, appWindow := range []bool{false, true} {
				e := newAppWinEnv(t)
				serial := "PHONE_TEST"
				if transport == "wifi" {
					serial = "192.0.2.10:5555"
				}
				setDevices(e.a, []adb.Device{{Serial: serial, StableSerial: "PHONE_TEST", State: "device", ConnType: transport, Res: "2400x1080"}})
				if err := e.a.SetKeepDeviceAwake(enabled); err != nil {
					t.Fatal(err)
				}
				var err error
				if appWindow {
					err = e.a.StartAppWin(serial, "pkg.test", "Test")
				} else {
					err = e.a.StartCast(serial)
				}
				if err != nil {
					t.Fatal(err)
				}
				p := e.f.waitParams(t, 1)
				if !p.KeepDeviceAwakeSet || p.KeepDeviceAwake != (enabled && !appWindow) {
					t.Fatalf("usb/wifi main/app session lost the global choice: %+v", p)
				}
			}
		}
	}
}

func TestAppParameterRestartDoesNotKeepDeviceAwake(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "PHONE_TEST", State: "device", ConnType: "usb"}})
	if err := e.a.SetKeepDeviceAwake(true); err != nil {
		t.Fatal(err)
	}
	if err := e.a.StartAppWin("PHONE_TEST", "pkg.test", "Test"); err != nil {
		t.Fatal(err)
	}
	if e.f.waitParams(t, 1).KeepDeviceAwake {
		t.Fatal("app casting must not prevent the phone from sleeping")
	}
	if err := e.a.SetKeepDeviceAwake(false); err != nil {
		t.Fatal(err)
	}
	if err := e.a.SaveAppWinParams("PHONE_TEST", "pkg.test", `{"mode":"usb","size":"1920x1080","fps":60,"bitrate":8,"flex":true,"audio":"phone"}`); err != nil {
		t.Fatal(err)
	}
	e.f.waitStops(t, 1)
	e.fireExit(1)
	p := e.f.waitParams(t, 2)
	if !p.KeepDeviceAwakeSet || p.KeepDeviceAwake {
		t.Fatal("restart did not pick up the new global choice")
	}
}
