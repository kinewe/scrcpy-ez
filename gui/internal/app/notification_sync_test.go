package app

import (
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/notifications"
)

func TestNotificationSettingsDefaultsOverridesAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := NewSettingsStore(path)
	if !s.Get().NotificationsEnabled("a") || !s.Get().NotificationPreview {
		t.Fatal("fresh settings must allow all sources and display message bodies")
	}
	if err := s.SetNotificationSettings(true, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotificationDevice("a", "off"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(false, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOtherAppWinSystemDecorations(false); err != nil {
		t.Fatal(err)
	}
	loaded := NewSettingsStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	got := loaded.Get()
	if got.NotificationsEnabled("a") || !got.NotificationsEnabled("b") || !got.NotificationPreview || got.ShowParamOverlay || !got.CloseToTray || got.OtherAppWinSystemDecorations {
		t.Fatal("independent settings did not persist")
	}
	got.NotificationDevices["a"] = true
	if loaded.Get().NotificationsEnabled("a") {
		t.Fatal("mutable settings snapshot")
	}
	_ = loaded.SetNotificationSettings(false, false)
	_ = loaded.SetNotificationDevice("a", "on")
	if !loaded.Get().NotificationsEnabled("a") || loaded.Get().NotificationsEnabled("b") {
		t.Fatal("per-device on did not override global off")
	}
	_ = loaded.SetNotificationDevice("a", "inherit")
	if loaded.Get().NotificationsEnabled("a") {
		t.Fatal("inherit did not restore default")
	}
}

type notificationRecorder struct {
	mu      sync.Mutex
	targets []notifications.Target
	closing chan struct{}
	release chan struct{}
}

func (s *notificationRecorder) Reconcile(targets []notifications.Target, _ notifications.Options) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets = append([]notifications.Target(nil), targets...)
}
func (s *notificationRecorder) Status() []notifications.Status { return nil }
func (s *notificationRecorder) Close()                         { close(s.closing); <-s.release }

func TestNotificationSettingStartsWithoutCastAndExitJoinsCleanup(t *testing.T) {
	a := New(Config{Version: "test"})
	if err := a.profiles.Save("phone", DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	a.devices = []adb.Device{{Identity: "device:phone", Serial: "usb", Name: "K80", State: "device"}}
	a.adb.EventHub().Publish(deviceevents.Snapshot{Epoch: 1, Available: true, Transports: []deviceevents.Transport{{Serial: "usb", State: "device", Kind: "usb"}}})
	service := &notificationRecorder{closing: make(chan struct{}), release: make(chan struct{})}
	a.SetNotificationService(service)
	if err := a.SetNotificationSettings(true, false); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	count := len(service.targets)
	service.mu.Unlock()
	if count != 1 || len(a.sessions) != 0 {
		t.Fatal("notification sync depended on casting")
	}
	if err := a.SetNotificationDevice("phone", "off"); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	count = len(service.targets)
	service.mu.Unlock()
	if count != 0 {
		t.Fatal("device override did not stop the listener")
	}
	if err := a.SetNotificationDevice("phone", "inherit"); err != nil {
		t.Fatal(err)
	}
	done := a.BeginClose()
	select {
	case <-service.closing:
	case <-time.After(2 * time.Second):
		t.Fatal("notification cleanup not started")
	}
	select {
	case <-done:
		t.Fatal("GUI exit did not join notification cleanup")
	default:
	}
	close(service.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GUI cleanup did not finish")
	}
}

func TestNotificationTargetsUsePairedOnlineFactsWithoutCasting(t *testing.T) {
	devices := []adb.Device{{Identity: "a", Serial: "usb", Name: "K80", State: "device"}, {Identity: "a", Serial: "wifi", Name: "K80", State: "device"}, {Identity: "b", Serial: "other"}, {Identity: "unknown", Serial: "unpaired"}}
	entries := map[string]DeviceEntry{"a": {}, "b": {}}
	settings := DefaultSettings()
	settings.NotificationDefault = true
	settings.NotificationDevices = map[string]bool{"b": false}
	raw := deviceevents.Snapshot{Available: true, Epoch: 7, Transports: []deviceevents.Transport{{Serial: "usb", State: "device", Kind: "usb", Generation: 4}, {Serial: "wifi", State: "device", Kind: "wifi", Generation: 9}, {Serial: "other", State: "device"}, {Serial: "unpaired", State: "device"}}}
	targets := notificationTargets(devices, entries, raw, settings)
	if len(targets) != 1 || targets[0].Serial != "usb" || targets[0].Epoch != 4 || targets[0].ServerEpoch != 7 {
		t.Fatalf("wrong target election: %+v", targets)
	}
	raw.Transports = raw.Transports[1:]
	targets = notificationTargets(devices, entries, raw, settings)
	if len(targets) != 1 || targets[0].Serial != "wifi" {
		t.Fatal("USB display shield prevented Wi-Fi handover")
	}
	raw.Available = false
	if len(notificationTargets(devices, entries, raw, settings)) != 0 {
		t.Fatal("unavailable ADB server kept a listener")
	}
}

func TestNotificationDeviceLabelsMatchFrontendConnectionRules(t *testing.T) {
	settings := DefaultSettings()
	settings.NotificationDefault = true
	entries := map[string]DeviceEntry{"device:PAD": {DisplayNameSet: true, DisplayName: "我的平板", Marketname: "Market tablet"}}
	raw := deviceevents.Snapshot{Available: true, Transports: []deviceevents.Transport{{Serial: "adb-PAD._adb-tls-connect._tcp", State: "device", Kind: "wifi"}, {Serial: "PAD", State: "device", Kind: "usb"}}}
	devices := []adb.Device{{Identity: "device:PAD", Name: "Market tablet", Serial: "adb-PAD._adb-tls-connect._tcp", ConnType: "wifi", WirelessIP: "192.0.2.11:37123"}}
	targets := notificationTargets(devices, entries, raw, settings)
	if len(targets) != 1 || targets[0].Name != "我的平板" || targets[0].Connection != "无线 · 192.0.2.11:37123" {
		t.Fatal("custom name or frontend Wi-Fi address did not win")
	}
	devices = append(devices, adb.Device{Identity: "device:PAD", Name: "Market tablet", Serial: "PAD", ConnType: "usb"})
	targets = notificationTargets(devices, entries, raw, settings)
	if len(targets) != 1 || targets[0].Connection != "USB · PAD" {
		t.Fatal("dual transport did not elect one USB identity")
	}
	entries["device:PAD"] = DeviceEntry{Marketname: "Market tablet"}
	devices[1].Name = ""
	targets = notificationTargets(devices, entries, raw, settings)
	if targets[0].Name != "Market tablet" {
		t.Fatal("market name fallback failed")
	}
}

func TestNotificationUSBDisplayShieldDoesNotStopOnlineWiFi(t *testing.T) {
	settings := DefaultSettings()
	settings.NotificationDefault = true
	device := adb.Device{Identity: "device:PAD", StableSerial: "PAD", Name: "工作平板", Serial: "PAD", ConnType: "usb", Wireless: "192.0.2.11:5555", WirelessIP: "192.0.2.11:5555", Connecting: true}
	raw := deviceevents.Snapshot{Available: true, Transports: []deviceevents.Transport{{Serial: device.Wireless, State: "device", Kind: "wifi", Generation: 9}}}
	targets := notificationTargets([]adb.Device{device}, map[string]DeviceEntry{device.Identity: {}}, raw, settings)
	if len(targets) != 1 || targets[0].Serial != device.Wireless || targets[0].Connection != "无线 · "+device.Wireless || targets[0].DeviceSerial != "PAD" {
		t.Fatal("synthetic USB display suppressed online paired Wi-Fi")
	}
}

func TestNotificationDisplayNewIPKeepsRealOldTransportDuringHandover(t *testing.T) {
	settings := DefaultSettings()
	settings.NotificationDefault = true
	device := adb.Device{Identity: "device:PAD", Serial: "192.0.2.12:5555", WirelessIP: "192.0.2.12:5555", ConnType: "wifi", Name: "平板"}
	entries := map[string]DeviceEntry{device.Identity: {Serials: []string{"PAD"}, Addrs: []AddrEntry{{Addr: "192.0.2.11:5555", State: AddrStateStale}}}}
	raw := deviceevents.Snapshot{Available: true, Transports: []deviceevents.Transport{{Serial: "192.0.2.11:5555", State: "device", Kind: "wifi", Generation: 4}}}
	targets := notificationTargets([]adb.Device{device}, entries, raw, settings)
	if len(targets) != 1 || targets[0].Serial != "192.0.2.11:5555" || targets[0].DeviceSerial != "PAD" || targets[0].Connection != "无线 · 192.0.2.11:5555" {
		t.Fatal("display address suppressed the real old transport")
	}
	raw.Transports = append(raw.Transports, deviceevents.Transport{Serial: device.Serial, State: "device", Kind: "wifi", Generation: 7})
	targets = notificationTargets([]adb.Device{device}, entries, raw, settings)
	if len(targets) != 1 || targets[0].Serial != device.Serial || targets[0].Epoch != 7 || targets[0].Connection != "无线 · 192.0.2.12:5555" {
		t.Fatal("listener did not hand over to new current address")
	}
	raw.Transports = nil
	if len(notificationTargets([]adb.Device{device}, entries, raw, settings)) != 0 {
		t.Fatal("offline archive started a listener")
	}
}

func TestNotificationArtworkUsesOfflineArchiveAndBoundedDeviceIcons(t *testing.T) {
	a, recorder := multiTestApp()
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE_A"), identityPhone("PHONE_B")})
	identity, pkg := "device:PHONE_A", "com.example.mail"
	if err := a.profiles.SetApps(identity, []AppListItem{{Pkg: pkg, Name: "档案邮件"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.iconsDirFor(identity), pkg+".png")
	writeTestPNG(t, path, color.White)
	before, _ := os.ReadFile(a.profiles.Path())
	got := a.NotificationArtwork(identity, pkg)
	if got.App != "档案邮件" || got.Icon.ID == "" {
		t.Fatal("offline archive label/artwork not reused")
	}
	other := a.NotificationArtwork("device:PHONE_B", pkg)
	if other.App != "" || other.Icon.ID != "" {
		t.Fatal("another physical device inherited cached app metadata")
	}
	a.appListCache[identity] = appListEntry{items: []AppListItem{{Pkg: pkg, Name: "内存邮件"}}, at: time.Now()}
	if a.NotificationArtwork(identity, pkg).App != "内存邮件" {
		t.Fatal("current app cache did not win")
	}
	if err := os.WriteFile(path, make([]byte, maxIconBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if a.NotificationArtwork(identity, pkg).Icon.ID != "" {
		t.Fatal("oversized offline icon accepted")
	}
	if a.NotificationArtwork(identity, "../escape").App != "" {
		t.Fatal("invalid package accepted")
	}
	after, _ := os.ReadFile(a.profiles.Path())
	if string(before) != string(after) || recorder.count("PHONE_A") != 0 || len(a.appListBusy) != 0 {
		t.Fatal("offline lookup wrote archive or started a device job")
	}
}
