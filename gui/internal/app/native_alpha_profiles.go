package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"scrcpy-ez/gui/internal/bridge"
	"scrcpy-ez/gui/internal/nativealpha"
	"strings"
	"time"
)

func cloneAppAlpha(apps []AppListItem) []AppListItem {
	out := append([]AppListItem(nil), apps...)
	for i := range out {
		out[i].NativeAlpha = nativealpha.Clone(out[i].NativeAlpha)
	}
	return out
}
func sameAppAlphaMetadata(a, b AppListItem) bool {
	x, y := a.NativeAlpha, b.NativeAlpha
	a.NativeAlpha = nil
	b.NativeAlpha = nil
	first, _ := json.Marshal(x)
	second, _ := json.Marshal(y)
	return a == b && bytes.Equal(first, second)
}
func mergeAppAlphaForCatalog(current, old []AppListItem) []AppListItem {
	out := cloneAppAlpha(current)
	prior := map[string]AppListItem{}
	for _, a := range old {
		prior[a.Pkg] = a
	}
	for i := range out {
		a := &out[i]
		p := prior[a.Pkg]
		a.NativeAlpha = nativealpha.Merge(a.NativeAlpha, p.NativeAlpha)
		// A legacy text-only enumeration must not erase richer package metadata.
		if a.VersionCode == 0 {
			a.VersionCode = p.VersionCode
			a.VersionName = p.VersionName
			a.LastUpdateTime = p.LastUpdateTime
		}
	}
	return out
}
func (s *ProfileStore) mergeNativeAlphaDiskLocked() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var disk storeData
	if json.Unmarshal(b, &disk) != nil {
		return
	}
	for key, d := range disk.Devices {
		if d == nil {
			continue
		}
		e, ok := s.resolveLocked(key)
		if !ok {
			// A CLI session may have created a new, physically identified device.
			valid := false
			for _, app := range d.Apps {
				if app.NativeAlpha != nil && nativealpha.Merge(app.NativeAlpha, nil) != nil {
					valid = true
				}
			}
			if valid && strings.HasPrefix(key, "device:") && len(d.Serials) > 0 {
				copy := cloneEntry(d)
				s.data.Devices[key] = &copy
			}
			continue
		}
		present := map[string]bool{}
		for _, app := range e.Apps {
			present[app.Pkg] = true
		}
		excluded := s.alphaExcludedApps[s.keyOfLocked(e)]
		for _, app := range d.Apps {
			if !present[app.Pkg] && !excluded[app.Pkg] && app.NativeAlpha != nil && nativealpha.Merge(app.NativeAlpha, nil) != nil {
				e.Apps = append(e.Apps, cloneAppAlpha([]AppListItem{app})[0])
				present[app.Pkg] = true
			}
		}
		for i := range e.Apps {
			for _, a := range d.Apps {
				if a.Pkg != e.Apps[i].Pkg {
					continue
				}
				p := &e.Apps[i]
				p.NativeAlpha = nativealpha.Merge(p.NativeAlpha, a.NativeAlpha)
				if p.VersionCode == 0 || a.LastUpdateTime > p.LastUpdateTime {
					p.VersionName = a.VersionName
					p.VersionCode = a.VersionCode
					p.LastUpdateTime = a.LastUpdateTime
				}
			}
		}
	}
}
func (s *ProfileStore) RecordNativeAlpha(key string, r nativealpha.Record) error {
	if err := nativealpha.Validate(r); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok {
		return os.ErrNotExist
	}
	// A transport address is not sufficient evidence to change another device's archive.
	matched := false
	for _, serial := range e.Serials {
		if serial == r.Environment.Serial {
			matched = true
		}
	}
	if !matched {
		return os.ErrPermission
	}
	for i := range e.Apps {
		if e.Apps[i].Pkg == r.Package {
			p := &e.Apps[i]
			p.NativeAlpha = nativealpha.Merge(p.NativeAlpha, &nativealpha.Profile{Schema: 1, Recipes: []nativealpha.Record{r}})
			if p.VersionCode == 0 || r.Application.LastUpdateTime >= p.LastUpdateTime {
				p.VersionName = r.Application.VersionName
				p.VersionCode = r.Application.VersionCode
				p.LastUpdateTime = r.Application.LastUpdateTime
			}
			return s.persistLocked()
		}
	}
	e.Apps = append(e.Apps, AppListItem{Pkg: r.Package, Name: r.Name, VersionName: r.Application.VersionName, VersionCode: r.Application.VersionCode, LastUpdateTime: r.Application.LastUpdateTime, NativeAlpha: &nativealpha.Profile{Schema: 1, Recipes: []nativealpha.Record{r}}})
	return s.persistLocked()
}

// A presentation observation is separate from recipes, so a later failed
// probe does not erase the fact that the desktop previously displayed RGBA.
func (s *ProfileStore) recordNativeAlphaPresented(identity, pkg, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.resolveLocked(identity)
	if !ok {
		return os.ErrNotExist
	}
	if s.alphaExcludedApps[s.keyOfLocked(entry)][pkg] {
		return nil
	}
	observation := &nativealpha.Profile{Schema: 1, LastPresentedAt: time.Now().UnixMilli()}
	for i := range entry.Apps {
		if entry.Apps[i].Pkg == pkg {
			entry.Apps[i].NativeAlpha = nativealpha.Merge(entry.Apps[i].NativeAlpha, observation)
			return s.persistLocked()
		}
	}
	entry.Apps = append(entry.Apps, AppListItem{Pkg: pkg, Name: name, NativeAlpha: observation})
	return s.persistLocked()
}
func (a *App) receiveNativeAlphaProfile(serial, pkg, line string) {
	const marker = "SCRCPY_EZ_NATIVE_ALPHA_PROFILE "
	at := strings.Index(line, marker)
	if at < 0 {
		return
	}
	encoded := strings.TrimSpace(line[at+len(marker):])
	if len(encoded) > 96000 {
		return
	}
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	var r nativealpha.Record
	if json.Unmarshal(b, &r) != nil || r.Package != pkg {
		return
	}
	a.mu.Lock()
	st := a.appWins[appWinKey(serial, pkg)]
	active := st != nil && !st.closing
	a.mu.Unlock()
	if !active {
		return
	}
	if err = a.profiles.RecordNativeAlpha(serial, r); err != nil {
		bridge.DebugLog("[native-alpha] 档案未保存: %v", err)
	}
}
