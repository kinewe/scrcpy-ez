//go:build windows && cgo

package notifications

import "golang.org/x/sys/windows/registry"

// Copy only presentation preferences to an unseen sender. Never copy cached
// Shell assets, notification statistics, or overwrite the new sender's choices.
// These optional DWORDs are Windows implementation details; absent values keep
// the system defaults. The application's own device policies do not change.
func migrateSenderPreferences(oldID, newID string) error {
	const root = `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\`
	old, err := registry.OpenKey(registry.CURRENT_USER, root+oldID, registry.QUERY_VALUE)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	defer old.Close()
	values := make(map[string]uint32)
	for _, name := range []string{"Enabled", "ShowBanner", "ShowInActionCenter", "AllowSound", "HideInLockScreen", "ShowOnLockScreen", "MaxVisibleNotifications", "Priority"} {
		value, kind, err := old.GetIntegerValue(name)
		if err == nil && kind == registry.DWORD {
			values[name] = uint32(value)
		}
	}
	if len(values) == 0 {
		return nil
	}
	key, existed, err := registry.CreateKey(registry.CURRENT_USER, root+newID, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if existed {
		return nil
	}
	for name, value := range values {
		if err := key.SetDWordValue(name, value); err != nil {
			_ = key.Close()
			_ = registry.DeleteKey(registry.CURRENT_USER, root+newID)
			return err
		}
	}
	return nil
}
