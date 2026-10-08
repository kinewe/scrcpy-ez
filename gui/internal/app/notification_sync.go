package app

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/notifications"
)

// Offline catalog fallback only; no enumeration, ADB command or app-list write.
func (a *App) NotificationArtwork(identity, pkg string) notifications.Artwork {
	result := notifications.Artwork{}
	if !rePkgName.MatchString(pkg) {
		return result
	}
	items, _ := a.GetAppList(identity)
	for _, item := range items {
		if item.Pkg == pkg {
			result.App = item.Name
			break
		}
	}
	batch, _ := a.GetAppIcons(identity, []string{pkg})
	dataURL := batch.Icons[pkg]
	if strings.HasPrefix(dataURL, "data:image/png;base64,") && len(dataURL) <= 700*1024 {
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/png;base64,"))
		if err == nil {
			result.Icon = notifications.IconFromPNG(data)
		}
	}
	return result
}

type notificationService interface {
	Reconcile([]notifications.Target, notifications.Options)
	Status() []notifications.Status
	Close()
}

// Inject once before device polling starts; notification service has no casting dependencies.
func (a *App) SetNotificationService(service notificationService) {
	a.mu.Lock()
	a.notificationService = service
	a.mu.Unlock()
}

func (a *App) startNotificationEvents(ctx context.Context) {
	a.mu.RLock()
	service := a.notificationService
	a.mu.RUnlock()
	if service == nil {
		return
	}
	go func() {
		for range a.adb.EventHub().Subscribe(ctx) {
			a.reconcileNotifications()
		}
	}()
}

func notificationTargets(devices []adb.Device, entries map[string]DeviceEntry, raw deviceevents.Snapshot, settings Settings) []notifications.Target {
	if !raw.Available {
		return nil
	}
	transports := make(map[string]deviceevents.Transport)
	for _, transport := range raw.Transports {
		if transport.State == "device" {
			transports[transport.Serial] = transport
		}
	}
	wanted := make(map[string]notifications.Target)
	for _, device := range devices {
		id := device.Identity
		if _, known := entries[id]; !known || id == "" || !settings.NotificationsEnabled(id) {
			continue
		}
		serial := device.Serial
		transport, online := transports[serial]
		if !online {
			// A brief USB display shield must not suppress an already-online paired Wi-Fi transport.
			candidates := []string{device.Wireless, device.WirelessIP}
			// The displayed address may already have advanced to a newly discovered
			// IP before its ADB connection is ready. Keep an actual archived transport
			// during that handover; the Android source still verifies physical identity.
			entry := entries[id]
			candidates = append(candidates, entry.Serials...)
			for _, addr := range entry.Addrs {
				candidates = append(candidates, addr.Addr)
			}
			if entry.TlsGuid != "" {
				candidates = append(candidates, entry.TlsGuid+"._adb-tls-connect._tcp")
			}
			for _, candidate := range candidates {
				if alternate, exists := transports[candidate]; exists {
					serial = candidate
					transport = alternate
					online = true
					break
				}
			}
		}
		if !online {
			continue
		} // Never trust a display shield's synthesized online state.
		name := device.Name
		entry := entries[id]
		if entry.DisplayNameSet && entry.DisplayName != "" {
			name = entry.DisplayName
		} else if name == "" || name == device.Serial {
			name = profileCardName(entry, device.Serial)
		}
		connection := "其他 · " + serial
		kind := device.ConnType
		if serial != device.Serial {
			kind = transport.Kind
		}
		switch kind {
		case "usb":
			connection = "USB · " + serial
		case "wifi":
			address := device.WirelessIP
			if address == "" || serial != device.Serial {
				// A display IP may have advanced before the new transport is
				// online. A fallback listener must report its actual endpoint.
				address = serial
			}
			connection = "无线 · " + address
		}
		physical := adb.StableSerial(device.StableSerial)
		if physical == "" {
			if len(entry.Serials) == 1 {
				physical = adb.StableSerial(entry.Serials[0])
			} else {
				physical = adb.StableSerial(strings.TrimPrefix(id, "device:"))
			}
		}
		target := notifications.Target{Identity: id, Name: name, Connection: connection, Serial: serial, DeviceSerial: physical, Epoch: transport.Generation, ServerEpoch: raw.Epoch}
		if old, exists := wanted[id]; !exists || transport.Kind == "usb" && old.Serial != serial {
			wanted[id] = target
		}
	}
	out := make([]notifications.Target, 0, len(wanted))
	for _, target := range wanted {
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity < out[j].Identity })
	return out
}

func (a *App) reconcileNotifications() {
	// Serialize triggers, then read current facts: a stale settings/device request cannot win.
	a.notificationMu.Lock()
	defer a.notificationMu.Unlock()
	a.mu.RLock()
	service, closing := a.notificationService, a.notificationClosing
	devices := append([]adb.Device(nil), a.devices...)
	a.mu.RUnlock()
	if service == nil || closing {
		return
	}
	settings := a.settings.Get()
	var targets []notifications.Target
	if settings.NotificationDefault || len(settings.NotificationDevices) > 0 || len(settings.NotificationPolicies) > 0 {
		targets = notificationTargets(devices, a.profiles.Entries(), a.adb.EventHub().Current(), settings)
	}
	entries := a.profiles.Entries()
	policies := make(map[string]notifications.Policy, len(targets))
	for _, target := range targets {
		policy := settings.NotificationPolicy(target.Identity)
		policy.Catalog = make([]string, 0, len(entries[target.Identity].Apps))
		for _, item := range entries[target.Identity].Apps {
			policy.Catalog = append(policy.Catalog, item.Pkg)
		}
		policies[target.Identity] = policy
	}
	service.Reconcile(targets, notifications.Options{Preview: settings.NotificationPreview, CopyFallback: time.Duration(settings.NotificationCopyMinutes) * time.Minute, Policies: policies})
}

func (a *App) notificationIdentity(identity string) (string, error) {
	key := a.profiles.ResolveKey(identity)
	if _, exists := a.profiles.Entry(key); !exists || key == "" {
		return "", errors.New("设备档案不存在，请刷新后重试")
	}
	return key, nil
}

func (a *App) SetNotificationModes(identities []string, mode string) error {
	if len(identities) == 0 || len(identities) > 256 {
		return errors.New("请先选择已配对设备")
	}
	keys := make([]string, 0, len(identities))
	seen := make(map[string]bool)
	for _, identity := range identities {
		key, err := a.notificationIdentity(identity)
		if err != nil {
			return err
		}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	if err := a.settings.SetNotificationModes(keys, mode); err != nil {
		return err
	}
	a.reconcileNotifications()
	return nil
}

func (a *App) SetNotificationWhitelist(identity string, packages []string) error {
	key, err := a.notificationIdentity(identity)
	if err != nil {
		return err
	}
	if err = a.settings.SetNotificationWhitelist(key, packages); err != nil {
		return err
	}
	a.reconcileNotifications()
	return nil
}

func (a *App) SetNotificationPreview(identity string, preview bool) error {
	key, err := a.notificationIdentity(identity)
	if err != nil {
		return err
	}
	if err = a.settings.SetNotificationPreview(key, preview); err != nil {
		return err
	}
	a.reconcileNotifications()
	return nil
}

func (a *App) SetNotificationSelection(identity string, packages []string, other bool) error {
	key, err := a.notificationIdentity(identity)
	if err != nil {
		return err
	}
	items, err := a.GetAppList(key)
	if err != nil {
		return err
	}
	selected := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		selected[pkg] = true
	}
	all := other
	for _, item := range items {
		if !selected[item.Pkg] {
			all = false
		}
	}
	if err = a.settings.SetNotificationSelection(key, packages, other, all); err != nil {
		return err
	}
	a.reconcileNotifications()
	return nil
}

func (a *App) ApplyNotificationEdit(identity string, edit NotificationEdit) error {
	key, err := a.notificationIdentity(identity)
	if err != nil {
		return err
	}
	all := false
	if edit.Selection != nil {
		items, err := a.GetAppList(key)
		if err != nil {
			return err
		}
		selected := make(map[string]bool, len(edit.Selection.Packages))
		for _, pkg := range edit.Selection.Packages {
			selected[pkg] = true
		}
		all = edit.Selection.Other
		for _, item := range items {
			if !selected[item.Pkg] {
				all = false
			}
		}
	}
	if err := a.settings.ApplyNotificationEdit(key, edit, all); err != nil {
		return err
	}
	a.reconcileNotifications()
	return nil
}

// App.mu is already held by snapshotRaw; statuses contain no message text.
func (a *App) notificationStatusLocked() []notifications.Status {
	if a.notificationService == nil {
		return nil
	}
	return a.notificationService.Status()
}

func (a *App) SetNotificationSettings(enabled, preview bool) error {
	err := a.settings.SetNotificationSettings(enabled, preview)
	a.reconcileNotifications()
	return err
}

func (a *App) SetNotificationCopyMinutes(minutes int) error {
	err := a.settings.SetNotificationCopyMinutes(minutes)
	if err == nil {
		a.reconcileNotifications()
	}
	return err
}

func (a *App) SetNotificationDevice(identity, mode string) error {
	if mode != "inherit" && mode != "on" && mode != "off" {
		return errors.New("无效的通知设置")
	}
	key := a.profiles.ResolveKey(identity)
	if _, exists := a.profiles.Entry(key); !exists || key == "" {
		return errors.New("设备档案不存在，请刷新后重试")
	}
	err := a.settings.SetNotificationDevice(key, mode)
	a.reconcileNotifications()
	return err
}
