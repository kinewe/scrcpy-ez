package app

import (
	"strconv"

	"scrcpy-ez/gui/internal/adb"
)

// Cards and the specification panel use the same effective mode profile:
// native defaults, remembered baselines and explicit custom values.
func (a *App) decorateCardSpecs(d *adb.Device, key string, e DeviceEntry) {
	if d.State != "device" || d.Connecting {
		return
	}
	e.Profiles = a.effectiveProfile(key)
	if native := nativeDisplayResolution(a.devicePhysCached(key).native); native != "" {
		e.Res = native
	}
	mdns10DecorateSpecs(d, e)
}

func nativeDisplayResolution(native NativeDisplay) string {
	w, h := native.Width, native.Height
	if w <= 0 || h <= 0 {
		return ""
	}
	if h > w {
		w, h = h, w
	}
	return strconv.Itoa(w) + "x" + strconv.Itoa(h)
}

func (a *App) physicalDisplayVersion() uint64 {
	a.physMu.Lock()
	defer a.physMu.Unlock()
	return a.physVersion
}

func (a *App) cacheDevicePhys(identity string, p devPhys) {
	a.physMu.Lock()
	defer a.physMu.Unlock()
	if a.physCache == nil {
		a.physCache = map[string]devPhys{}
	}
	previous := a.physCache[identity]
	if previous.native != p.native || previous.width != p.width || previous.height != p.height {
		a.physVersion++
	}
	a.physCache[identity] = p
}
