package app

import (
	"errors"

	"scrcpy-ez/gui/internal/bridge"
)

const defaultAppLauncherCapacity = 4

// AppLauncherProfile stores one device's application window layout. A nil
// capacity follows the default as the installed app catalog grows or shrinks.
type AppLauncherProfile struct {
	Capacity *int     `json:"capacity,omitempty"`
	Custom   bool     `json:"custom,omitempty"`
	Pins     []string `json:"pins,omitempty"`
	Recent   []string `json:"recent,omitempty"`
}

func installedLauncherApps(apps []AppListItem) map[string]bool {
	packages := make(map[string]bool, len(apps))
	for _, app := range apps {
		if rePkgName.MatchString(app.Pkg) {
			packages[app.Pkg] = true
		}
	}
	return packages
}

func launcherPackages(packages []string, installed map[string]bool) []string {
	var result []string
	seen := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		if installed[pkg] && !seen[pkg] {
			result = append(result, pkg)
			seen[pkg] = true
		}
	}
	return result
}

func updateLauncherCatalog(e *DeviceEntry, installed map[string]bool) {
	if e.AppLauncher == nil {
		return
	}
	l := *e.AppLauncher
	l.Pins = launcherPackages(l.Pins, installed)
	l.Recent = launcherPackages(l.Recent, installed)
	if l.Capacity != nil && *l.Capacity > len(installed) {
		capacity := len(installed)
		l.Capacity = &capacity
		l.Custom = true
	}
	e.AppLauncher = &l
}

// SaveAppLauncherOptions commits the draft without overwriting opens recorded
// while the options panel was displayed.
func (a *App) SaveAppLauncherOptions(serial string, capacity int, custom bool, pins []string) (AppLauncherProfile, error) {
	return a.profiles.saveAppLauncherOptions(a.appListKeyFor(serial), capacity, custom, pins)
}

func (s *ProfileStore) saveAppLauncherOptions(key string, capacity int, custom bool, pins []string) (AppLauncherProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return AppLauncherProfile{}, errors.New("设备身份尚未确认，暂时无法保存应用窗口选项")
	}
	installed := installedLauncherApps(e.Apps)
	if capacity < 0 || capacity > len(installed) {
		return AppLauncherProfile{}, errors.New("栏位数须在 0 和当前应用数之间")
	}
	pins = launcherPackages(pins, installed)
	if capacity > 0 && len(pins) > capacity {
		pins = pins[:capacity]
	}
	l := AppLauncherProfile{Capacity: &capacity, Custom: custom, Pins: pins}
	if !custom && capacity == min(defaultAppLauncherCapacity, len(installed)) {
		l.Capacity = nil
	}
	if e.AppLauncher != nil {
		l.Recent = e.AppLauncher.Recent
	}
	previous := e.AppLauncher
	e.AppLauncher = &l
	if err := s.persistLocked(); err != nil {
		e.AppLauncher = previous
		return AppLauncherProfile{}, err
	}
	return l, nil
}

func (a *App) recordAppLauncherOpen(serial, pkg string) {
	if a.profiles == nil || serial == "" || !rePkgName.MatchString(pkg) {
		return
	}
	if err := a.profiles.recordAppLauncherOpen(a.appListKeyFor(serial), pkg); err != nil {
		bridge.DebugLog("[appwin] 最近打开入档失败: %v", err)
	}
}

func (s *ProfileStore) recordAppLauncherOpen(identity, pkg string) error {
	if !rePkgName.MatchString(pkg) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(identity)
	if !ok {
		return nil
	}
	installed := installedLauncherApps(e.Apps)
	if len(installed) > 0 && !installed[pkg] {
		return nil
	}
	l := AppLauncherProfile{}
	if e.AppLauncher != nil {
		l = *e.AppLauncher
	}
	if len(l.Recent) > 0 && l.Recent[0] == pkg {
		if s.persistError == nil {
			return nil
		}
		return s.persistLocked()
	}
	recent := []string{pkg}
	for _, previous := range l.Recent {
		if previous != pkg && (len(installed) == 0 || installed[previous]) {
			recent = append(recent, previous)
		}
	}
	l.Recent = recent
	e.AppLauncher = &l
	return s.persistLocked()
}
