package app

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/notifications"
)

func TestNotificationDisplayPreferenceKeepsVerifiedOnlineListener(t *testing.T) {
	a := New(Config{Version: "test"})
	if err := a.profiles.Save("PHONE", DeviceProfile{}); err != nil {
		t.Fatal(err)
	}
	a.adb.EventHub().Publish(deviceevents.Snapshot{Available: true, Epoch: 1, Transports: []deviceevents.Transport{
		{Serial: "192.0.2.11:5555", Kind: "wifi", State: "device"},
		{Serial: "adb-PHONE._adb-tls-connect._tcp", Kind: "wifi", State: "device"},
	}})
	service := &notificationRecorder{}
	a.SetNotificationService(service)
	a.devices = []adb.Device{{Identity: "device:PHONE", StableSerial: "PHONE", Serial: "192.0.2.11:5555", ConnType: "wifi", Name: "Phone"}}
	a.reconcileNotifications()
	first := service.targets[0]
	a.devices[0].Serial = "adb-PHONE._adb-tls-connect._tcp"
	a.devices[0].WirelessIP = "192.0.2.11:37123"
	a.devices[0].Name = "Renamed phone"
	a.reconcileNotifications()
	second := service.targets[0]
	if second.Serial != first.Serial || second.Epoch != first.Epoch || second.ServerEpoch != first.ServerEpoch || second.DeviceSerial != first.DeviceSerial {
		t.Fatalf("display preference revoked the live notification session: %v -> %v", first, second)
	}
	if second.Name != "Renamed phone" || second.Connection != first.Connection {
		t.Fatal("name should refresh while reporting the actual listener connection")
	}
}

func TestNotificationTransportRetentionStopsAtActualLifecycleBoundaries(t *testing.T) {
	old := notifications.Target{Identity: "phone", DeviceSerial: "PHONE", Serial: "old", Epoch: 7, ServerEpoch: 3, Connection: "old connection"}
	next := notifications.Target{Identity: "phone", DeviceSerial: "PHONE", Serial: "new", Epoch: 9, ServerEpoch: 3, Connection: "new connection"}
	for _, test := range []struct {
		name       string
		available  bool
		epoch      uint64
		generation uint64
		state      string
		physical   string
		want       string
	}{
		{"healthy parallel route", true, 3, 7, "device", "PHONE", "old"},
		{"old offline", true, 3, 7, "offline", "PHONE", "new"},
		{"old transport replaced", true, 3, 8, "device", "PHONE", "new"},
		{"server restarted", true, 4, 7, "device", "PHONE", "new"},
		{"server unavailable", false, 3, 7, "device", "PHONE", "new"},
		{"different physical device", true, 3, 7, "device", "OTHER", "new"},
		{"unknown physical device", true, 3, 7, "device", "", "new"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := next
			target.DeviceSerial = test.physical
			raw := deviceevents.Snapshot{Available: test.available, Epoch: test.epoch, Transports: []deviceevents.Transport{{Serial: "old", State: test.state, Generation: test.generation}}}
			got := keepNotificationTransports([]notifications.Target{target}, []notifications.Target{old}, raw)
			if got[0].Serial != test.want {
				t.Fatalf("retained %q, want %q", got[0].Serial, test.want)
			}
			if len(keepNotificationTransports(nil, []notifications.Target{old}, raw)) != 0 {
				t.Fatal("disabled policy resurrected a previous source")
			}
		})
	}
}
