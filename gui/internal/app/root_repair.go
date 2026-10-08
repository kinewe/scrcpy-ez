package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/rootrepair"
)

func repairRouteCurrent(h *deviceevents.Hub, r rootrepair.Request) bool {
	s := h.Current()
	if !s.Available {
		return false
	}
	for _, t := range s.Transports {
		if t.State == "device" && t.Serial == r.Serial && fmt.Sprintf("%d/%s/%d", s.Epoch, t.Serial, t.Generation) == r.Key {
			return true
		}
	}
	return false
}

func (a *App) startRootRepair(ctx context.Context) {
	hub := a.adb.EventHub()
	path := ""
	if a.cfg.ProfilesPath != "" {
		path = filepath.Join(filepath.Dir(a.cfg.ProfilesPath), "root-repair.json")
	}
	c := rootrepair.NewCoordinator(path, func(work context.Context, r rootrepair.Request) (rootrepair.Report, error) {
		if !repairRouteCurrent(hub, r) {
			return rootrepair.Report{}, context.Canceled
		}
		work, cancel := context.WithCancel(work)
		defer cancel()
		release := context.AfterFunc(ctx, cancel)
		defer release()
		go func() {
			for range hub.Subscribe(work) {
				if !repairRouteCurrent(hub, r) {
					cancel()
					return
				}
			}
		}()
		dir := filepath.Join(filepath.Dir(a.cfg.ProfilesPath), "root-repair-logs")
		return rootrepair.PrepareLocked(work, rootrepair.Options{ADB: a.cfg.AdbPath, Serial: r.Serial, Identity: r.Identity, LogDir: dir, Execute: rootrepair.CommandExecutor(a.cfg.AdbPath)})
	})
	a.mu.Lock()
	a.rootRepair = c
	a.mu.Unlock()
	endpoint, token, e := rootrepair.Serve(ctx, c, func(r rootrepair.Request) bool { return repairRouteCurrent(hub, r) })
	if e == nil {
		os.Setenv("SCEZ_ROOT_ENDPOINT", endpoint)
		os.Setenv("SCEZ_ROOT_TOKEN", token)
	}
}

// RetryRootRepair is a deliberate user action, available only for a failed
// upload. It resets denial suppression, persists consent, and restarts one cast.
func (a *App) RetryRootRepair(serial, pkg string) error {
	a.mu.RLock()
	c := a.rootRepair
	identity := ""
	if pkg == "" {
		if st := a.sessions[serial]; st != nil && st.cast.Phase == "root-required" {
			identity = st.identity
		}
	} else if st := a.appWins[appWinKey(serial, pkg)]; st != nil && st.phase == "root-required" {
		identity = st.identity
	}
	a.mu.RUnlock()
	if c == nil || identity == "" || strings.Contains(identity, ":") {
		return errors.New("当前会话没有待处理的上传权限错误")
	}
	if e := c.SetEnabled(identity, true); e != nil {
		return e
	}
	if pkg != "" {
		return a.RestartAppWin(serial, pkg)
	}
	return a.RestartCast(serial)
}

func (a *App) DisableRootRepair(serial string) error {
	return a.SetRootRepairEnabled(serial, false)
}

func (a *App) GetRootRepairEnabled(serial string) bool {
	identity := a.appListKeyFor(serial)
	a.mu.RLock()
	c := a.rootRepair
	a.mu.RUnlock()
	return c != nil && c.Enabled(identity)
}

func (a *App) SetRootRepairEnabled(serial string, enabled bool) error {
	identity := a.appListKeyFor(serial)
	a.mu.RLock()
	c := a.rootRepair
	a.mu.RUnlock()
	if c == nil || identity == "" {
		return errors.New("设备身份未确认")
	}
	if strings.Contains(identity, ":") {
		return errors.New("物理设备身份尚未确认")
	}
	return c.SetEnabled(identity, enabled)
}
