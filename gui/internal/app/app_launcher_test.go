package app

import (
	"slices"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

func TestAppLauncherRecentOrderAndPinPersistence(t *testing.T) {
	e := newAppWinParamsEnv(t)
	store := e.a.profiles
	apps := []AppListItem{{Pkg: "com.example.first"}, {Pkg: "com.example.second"}, {Pkg: "com.example.third"}}
	if err := store.SetApps("K80", apps); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{apps[0].Pkg, apps[1].Pkg, apps[0].Pkg, "@other"} {
		if err := store.recordAppLauncherOpen("K80", pkg); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.a.SaveAppLauncherOptions("K80", 4, true, nil); err == nil {
		t.Fatal("capacity exceeded installed apps")
	}
	l, err := e.a.SaveAppLauncherOptions("K80", 2, true, []string{apps[2].Pkg, "@other", apps[2].Pkg, apps[1].Pkg})
	if err != nil || !slices.Equal(l.Pins, []string{apps[2].Pkg, apps[1].Pkg}) ||
		!slices.Equal(l.Recent, []string{apps[0].Pkg, apps[1].Pkg}) {
		t.Fatalf("options or recents changed unexpectedly: %+v, %v", l, err)
	}
	l, err = e.a.SaveAppLauncherOptions("K80", 0, true, l.Pins)
	if err != nil || len(l.Pins) != 2 {
		t.Fatalf("disabling should preserve pin preferences: %+v, %v", l, err)
	}
	reloaded := NewProfileStore(store.Path())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	entry, ok := reloaded.Entry("K80")
	if !ok || entry.AppLauncher == nil || entry.AppLauncher.Capacity == nil || *entry.AppLauncher.Capacity != 0 ||
		!slices.Equal(entry.AppLauncher.Pins, l.Pins) || !slices.Equal(entry.AppLauncher.Recent, l.Recent) {
		t.Fatalf("launcher state did not survive reload: %+v", entry.AppLauncher)
	}
	if err := reloaded.SetApps("K80", apps[:1]); err != nil {
		t.Fatal(err)
	}
	entry, _ = reloaded.Entry("K80")
	if len(entry.AppLauncher.Pins) != 0 || !slices.Equal(entry.AppLauncher.Recent, []string{apps[0].Pkg}) {
		t.Fatalf("uninstalled apps remained in launcher settings: %+v", entry.AppLauncher)
	}
}

func TestAppLauncherRecordsClickBeforeVideoAndOnFailure(t *testing.T) {
	e := newAppWinParamsEnv(t)
	setDevices(e.a, []adb.Device{{Serial: "K80", State: "device", ConnType: "usb"}})
	const pkg = "com.example.first"
	other := "com.example.second"
	if err := e.a.profiles.SetApps("K80", []AppListItem{{Pkg: pkg}, {Pkg: other}}); err != nil {
		t.Fatal(err)
	}
	if err := e.a.StartAppWin("K80", pkg, "First"); err != nil {
		t.Fatal(err)
	}
	e.f.waitParams(t, 1)
	entry, _ := e.a.profiles.Entry("K80")
	if entry.AppLauncher == nil || !slices.Equal(entry.AppLauncher.Recent, []string{pkg}) {
		t.Fatalf("click not recorded before the first frame: %+v", entry.AppLauncher)
	}
	e.a.SetRunnerFactory(nil)
	if err := e.a.StartAppWin("K80", other, "Second"); err == nil {
		t.Fatal("expected an unavailable runner")
	}
	e.fireLine("[server] INFO: SCRCPY_EZ_APP_LAUNCH=reused")
	e.fireLine("INFO: Texture: 1280x720")
	entry, _ = e.a.profiles.Entry("K80")
	if !slices.Equal(entry.AppLauncher.Recent, []string{other, pkg}) {
		t.Fatal("a failed cast still counts and later texture output must not change recency")
	}
}
