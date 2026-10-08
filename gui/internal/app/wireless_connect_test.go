package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/deviceevents"
	"scrcpy-ez/gui/internal/discovery"
	"scrcpy-ez/gui/internal/wirelessconnect"
)

func wirelessFixture() (map[string]DeviceEntry, []discovery.MdnsService, deviceevents.Snapshot) {
	entries := map[string]DeviceEntry{"device:PHONE": {Serials: []string{"PHONE"}, Addrs: []AddrEntry{{Addr: "192.0.2.2:5555", State: AddrStateActive, Mode: ModeTcpip}, {Addr: "192.0.2.1:5555", State: AddrStateStale, Mode: ModeTcpip}}}}
	svcs := []discovery.MdnsService{{Name: "adb-PHONE", Addr: "192.0.2.2:5555", Mode: ModeTcpip}}
	return entries, svcs, deviceevents.Snapshot{Available: true, Epoch: 1}
}

func TestWirelessNewIPWhileOldTransportStillOnline(t *testing.T) {
	e, s, r := wirelessFixture()
	r.Transports = []deviceevents.Transport{{Serial: "192.0.2.1:5555", State: "device", Kind: "wifi"}}
	w := wirelessConnectTargets(e, s, r, nil)
	if len(w) != 1 || w[0].Addresses[0] != "192.0.2.2:5555" {
		t.Fatal("old online socket prevented new IP recovery")
	}
	r.Transports[0].Serial = "192.0.2.2:5555"
	if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
		t.Fatal("online device required another connect")
	}
}

func TestWirelessStrongOwnershipAndInterruptions(t *testing.T) {
	e, s, r := wirelessFixture()
	for _, mode := range []string{ModeTcpip, ModeTls} {
		s[0].Mode = mode
		s[0].Name = "adb-FOREIGN"
		if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
			t.Fatal("reused IP associated a foreign device")
		}
	}
	s[0].Name = "adb-PHONE"
	s[0].Mode = discovery.MdnsModePairing
	if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
		t.Fatal("connected to pairing port")
	}
	s[0].Mode = ModeTcpip
	for _, state := range []string{"device", "unauthorized"} {
		r.Transports = []deviceevents.Transport{{Serial: s[0].Addr, State: state}}
		if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
			t.Fatal("online/unauthorized state not respected")
		}
	}
	r.Transports = nil
	r.Learning = []string{"PHONE"}
	if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
		t.Fatal("USB learning was interrupted")
	}
	r.Learning = nil
	if len(wirelessConnectTargets(e, s, r, map[string]bool{"device:PHONE": true})) != 0 {
		t.Fatal("pair/cast busy state ignored")
	}
	r.Available = false
	if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
		t.Fatal("unavailable server retained tasks")
	}
	r.Available = true
	if len(wirelessConnectTargets(nil, s, r, nil)) != 0 {
		t.Fatal("deleted archive recreated")
	}
	copy := e["device:PHONE"]
	e["duplicate"] = copy
	if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
		t.Fatal("ambiguous archive chose an owner")
	}
}

func TestWirelessTLSFirstAndNativeAliasSuppressesFallback(t *testing.T) {
	e, s, r := wirelessFixture()
	entry := e["device:PHONE"]
	entry.Addrs = append([]AddrEntry{{Addr: "192.0.2.2:37123", Mode: ModeTls, State: AddrStateActive}}, entry.Addrs...)
	e["device:PHONE"] = entry
	s = append(s, discovery.MdnsService{Name: "adb-PHONE-ABC123", Addr: "192.0.2.2:37123", Mode: ModeTls})
	w := wirelessConnectTargets(e, s, r, nil)
	if len(w) != 1 || len(w[0].Addresses) != 2 || w[0].Addresses[0] != "192.0.2.2:37123" {
		t.Fatal("TLS priority or classic fallback missing")
	}
	r.Transports = []deviceevents.Transport{{Serial: "adb-PHONE-ABC123._adb-tls-connect._tcp", Kind: "wifi", State: "device"}}
	if len(wirelessConnectTargets(e, s, r, nil)) != 0 {
		t.Fatal("native TLS auto-connect caused redundant fallback")
	}
}

func TestWirelessConnectVerifiesSerialWithoutWritingArchive(t *testing.T) {
	a := New(Config{})
	a.profiles.path = filepath.Join(t.TempDir(), "profiles.json")
	a.profiles.SyncDevices([]adb.Device{identityPhone("PHONE")})
	a.profiles.AddrSuccessMode("device:PHONE", "192.0.2.2:5555", ModeTcpip)
	a.mdns = []discovery.MdnsService{{Name: "adb-PHONE", Addr: "192.0.2.2:5555", Mode: ModeTcpip}}
	a.adb.EventHub().Publish(deviceevents.Snapshot{Available: true, Epoch: 1})
	before, _ := os.ReadFile(a.profiles.path)
	calls := 0
	a.disc.ConnectOutFn = func(context.Context, string) (string, error) { calls++; return "connected to 192.0.2.2:5555", nil }
	a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { return "PHONE", nil }
	targets := a.currentWirelessConnectTargets()
	if len(targets) != 1 {
		t.Fatal("fixture target missing")
	}
	if err := a.connectDiscoveredWireless(context.Background(), targets[0]); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("duplicate ADB connect")
	}
	a.pairOps.getpropFn = func(context.Context, string, string) (string, error) { return "FOREIGN", nil }
	if !errors.Is(a.connectDiscoveredWireless(context.Background(), targets[0]), wirelessconnect.ErrIdentity) {
		t.Fatal("identity mismatch accepted")
	}
	a.mdns[0].Addr = "192.0.2.3:5555"
	if !errors.Is(a.connectDiscoveredWireless(context.Background(), targets[0]), wirelessconnect.ErrObsolete) || calls != 2 {
		t.Fatal("late obsolete address connected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(a.connectDiscoveredWireless(ctx, targets[0]), context.Canceled) || calls != 2 {
		t.Fatal("canceled job issued a command")
	}
	after, _ := os.ReadFile(a.profiles.path)
	if string(before) != string(after) {
		t.Fatal("connect worker wrote discovery/archive state")
	}
}
