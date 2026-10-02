package app

import (
	"scrcpy-ez/gui/internal/rootrepair"
	"strings"
	"testing"
	"time"
)

func TestRootUpdateDisabledAndStartGuardRetained(t *testing.T) {
	a := &App{sessions: map[string]*sessionState{"usb": {runner: &fakeRunner{}}}, appWins: map[string]*appWinState{"wifi#pkg": {runner: &fakeRunner{}}}}
	reply, e := a.InstallUpdate(false, func() { t.Error("quit before confirmation") })
	if e == nil || !strings.Contains(e.Error(), "root 尝试版") || reply.NeedsConfirm {
		t.Fatalf("%+v %v", reply, e)
	}
	a.update.installing = true
	if e = a.StartCast("usb"); e == nil || !strings.Contains(e.Error(), "重启更新") {
		t.Fatal("main cast not blocked", e)
	}
	if e = a.StartAppWin("wifi", "pkg", "test"); e == nil || !strings.Contains(e.Error(), "重启更新") {
		t.Fatal("app cast not blocked", e)
	}
}

func TestRootUpdateChannelCannotOfferStableInstall(t *testing.T) {
	a := &App{cfg: Config{Version: rootrepair.Version}}
	a.BeginUpdateCheck()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		s := a.GetUpdateState()
		if s.Phase == "checking" {
			time.Sleep(time.Millisecond)
			continue
		}
		if s.Info.Current != rootrepair.Version || s.Info.RepoURL != rootrepair.RepoURL || s.Info.HasNew || s.CanInstall || s.Info.DownloadURL != "" {
			t.Fatalf("stable channel leaked: %+v", s)
		}
		if e := a.DownloadUpdate(); e == nil {
			t.Fatal("stable download allowed")
		}
		if _, e := a.InstallUpdate(true, func() { t.Error("root build quit for stable install") }); e == nil {
			t.Fatal("stable install allowed")
		}
		return
	}
	t.Fatal("local root version info did not complete")
}
