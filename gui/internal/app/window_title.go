package app

import (
	"strings"
	"unicode"
)

// windowDeviceName uses the same naming source as cards and session labels.
// A previous transport or archive key may be used during a restart.
func (a *App) windowDeviceName(serial string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	e, known := a.profiles.Entry(serial)
	d := findTargetDevice(serial, a.devices)
	if d == nil && known {
		d = findTargetByIdentity(e, a.devices)
	}
	if d != nil {
		return a.displayNameOf(d)
	}
	if known {
		return profileCardName(e, serial)
	}
	return serial
}

func castWindowTitle(device, content string) string {
	clean := func(s, fallback string) string {
		s = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, s))
		if s == "" {
			return fallback
		}
		return s
	}
	return clean(device, "设备") + " - " + clean(content, "应用投屏")
}
