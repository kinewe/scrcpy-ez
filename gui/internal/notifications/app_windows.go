//go:build windows && cgo

package notifications

import (
	"errors"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// All source applications share the product's sender identity and copy store.
type appWindowsSink struct {
	mu        sync.Mutex
	ownership windows.Handle
	baseID    string
	children  map[string]*WindowsSink
	closed    bool
}

func newAppWindowsSink() (Sink, error) {
	return newAppWindowsSinkWithID(WindowsAppID)
}

func newAppWindowsSinkWithID(baseID string) (*appWindowsSink, error) {
	name, _ := windows.UTF16PtrFromString(`Local\` + baseID)
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrUnavailable
	}
	return &appWindowsSink{baseID: baseID, ownership: handle, children: make(map[string]*WindowsSink)}, nil
}

func sourceIdentity(card Card) (string, string) {
	// Windows retains the old sender's Shell artwork even after IconUri and the
	// shortcut are repaired. Use one stable branding identity, not a release ID.
	return WindowsAppID + ".Unified.EzBrand", "scrcpy-ez"
}

func sourceShortcutName(label string) string {
	name := strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, label)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "手机通知"
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		name += " 通知"
	}
	return name
}

func (s *appWindowsSink) Show(card Card) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	id, label := sourceIdentity(card)
	id = s.baseID + strings.TrimPrefix(id, WindowsAppID)
	child := s.children[id]
	if child == nil {
		if len(s.children) >= 64 {
			return ErrOverflow
		}
		var err error
		if s.baseID == WindowsAppID {
			if err = migrateSenderPreferences(WindowsAppID+".Unified", id); err != nil {
				return sinkSystemError("registration", err)
			}
		}
		child, err = newWindowsSinkWithIdentity(id, label, sourceShortcutName(label), "")
		if err != nil {
			return err
		}
		s.children[id] = child
	}
	return child.Show(card)
}

func (s *appWindowsSink) Remove(group, tag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	var result error
	for _, child := range s.children {
		result = errors.Join(result, child.Remove(group, tag))
	}
	return result
}

func (s *appWindowsSink) Clear(group string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	var result error
	for _, child := range s.children {
		result = errors.Join(result, child.Clear(group))
	}
	return result
}

func (s *appWindowsSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for _, child := range s.children {
		_ = child.Close()
	}
	return windows.CloseHandle(s.ownership)
}
