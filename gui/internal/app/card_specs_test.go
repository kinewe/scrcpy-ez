package app

import (
	"sync"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

const cardSpecsKey = "device:SPECDEVICE"

func newCardSpecsApp(p DeviceProfile) (*App, *fakeRunner) {
	a, runner := newTestApp()
	a.cfg.AdbPath = "" // These projection tests never query a connected device.
	gui15Seed(a.profiles, cardSpecsKey, &DeviceEntry{
		Marketname: "Spec Device", Serials: []string{"SPECDEVICE"}, Res: "2560x1280",
		Addrs:    []AddrEntry{{Addr: "192.0.2.25:5555", State: AddrStateActive, Mode: ModeTcpip}},
		Profiles: p,
	})
	return a, runner
}

func publishCardSpecs(a *App, mode string) {
	raw := adb.Device{Serial: "SPECDEVICE", StableSerial: "SPECDEVICE", State: "device", ConnType: mode, Res: "2560x1280", FPS: 120}
	if mode == "wifi" {
		raw.Serial = "192.0.2.25:5555"
	}
	a.mu.Lock()
	a.lastTrack = []adb.Device{raw}
	a.mu.Unlock()
	a.displayMu.Lock()
	defer a.displayMu.Unlock()
	a.projectDisplayLocked([]adb.Device{raw}, "spec test")
}

func assertCardSpecs(t *testing.T, a *App, mode, res string, fps int) {
	t.Helper()
	snapshot := a.Snapshot()
	if len(snapshot.Devices) != 1 {
		t.Fatalf("one identity must produce one card: %+v", snapshot.Devices)
	}
	d := snapshot.Devices[0]
	gotRes := d.Res
	if mode == "wifi" {
		gotRes = d.WirelessRes
	}
	if d.ConnType != mode || gotRes != res || d.FPS != fps {
		t.Fatalf("card should be %s %s at %d FPS: %+v", mode, res, fps, d)
	}
	p := a.GetProfile(cardSpecsKey)
	mp := p.Usb
	if mode == "wifi" {
		mp = p.Wifi
	}
	if ParseLongEdge(gotRes) != mp.Res || d.FPS != mp.FPS {
		t.Fatalf("card and specification panel disagree: card=%+v profile=%+v", d, mp)
	}
}

func TestCardSpecsUsesEffectiveNativeDefaultsAndCustomModes(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "native-defaults"
		if custom {
			name = "independent-custom-values"
		}
		t.Run(name, func(t *testing.T) {
			p := DefaultProfile()
			p.Usb.Baseline = Baseline{Res: 2560, FPS: 120, Bitrate: 60}
			p.Wifi.Baseline = Baseline{Res: 1920, FPS: 120, Bitrate: 15}
			if custom {
				p.Usb.Custom, p.Wifi.Custom = true, true
				p.Usb.Res, p.Usb.FPS = 2400, 90
				p.Wifi.Res, p.Wifi.FPS = 1600, 75
			}
			a, _ := newCardSpecsApp(p)
			a.cacheDevicePhys(cardSpecsKey, devPhys{native: NativeDisplay{Width: 1440, Height: 3200, LongEdge: 3200, FPS: 144}})
			for _, mode := range []string{"usb", "wifi", "usb"} {
				publishCardSpecs(a, mode)
				wantRes, wantFPS := "3200x1440", 144
				if mode == "wifi" {
					wantFPS = 60
				}
				if custom {
					wantRes, wantFPS = "2400x1080", 90
					if mode == "wifi" {
						wantRes, wantFPS = "1600x720", 75
					}
				}
				assertCardSpecs(t, a, mode, wantRes, wantFPS)
			}
			entry, _ := a.profiles.Entry(cardSpecsKey)
			if entry.Res != "2560x1280" || a.lastTrack[0].Res != "2560x1280" || a.lastTrack[0].FPS != 120 {
				t.Fatal("rendered casting specifications polluted physical source data")
			}
		})
	}
}

func TestCardSpecsUsesBaselineWhileNativeMetadataPending(t *testing.T) {
	p := DefaultProfile()
	p.Usb.Baseline = Baseline{Res: 2400, FPS: 75, Bitrate: 60}
	p.Wifi.Baseline = Baseline{Res: 1800, FPS: 144, Bitrate: 15}
	a, _ := newCardSpecsApp(p)
	publishCardSpecs(a, "usb")
	assertCardSpecs(t, a, "usb", "2400x1200", 75)
	publishCardSpecs(a, "wifi")
	assertCardSpecs(t, a, "wifi", "1800x900", 60)
}

func TestCardSpecsLateNativeMetadataRefreshesWithoutDeviceEvent(t *testing.T) {
	a, _ := newCardSpecsApp(DefaultProfile())
	publishCardSpecs(a, "usb")
	assertCardSpecs(t, a, "usb", "2560x1280", 120)
	version := a.profiles.displayVersion()
	a.cacheDevicePhys(cardSpecsKey, devPhys{native: NativeDisplay{Width: 1200, Height: 1920, LongEdge: 1920, FPS: 60}})
	assertCardSpecs(t, a, "usb", "1920x1200", 60)
	if version != a.profiles.displayVersion() {
		t.Fatal("physical refresh must not rewrite a user's profile")
	}
	publishCardSpecs(a, "wifi")
	assertCardSpecs(t, a, "wifi", "1920x1200", 60)
}

func TestCardSpecsSaveAndResetRefreshCurrentModeImmediately(t *testing.T) {
	a, _ := newCardSpecsApp(DefaultProfile())
	a.cacheDevicePhys(cardSpecsKey, devPhys{native: NativeDisplay{Width: 1440, Height: 3200, LongEdge: 3200, FPS: 144}})
	publishCardSpecs(a, "usb")
	if err := a.SaveProfile(cardSpecsKey, "usb", 2400, 90, 60, true, "pc", false, false, "h264", "opus"); err != nil {
		t.Fatal(err)
	}
	assertCardSpecs(t, a, "usb", "2400x1080", 90)
	if err := a.SaveProfile(cardSpecsKey, "wifi", 1600, 75, 15, true, "pc", false, false, "h264", "opus"); err != nil {
		t.Fatal(err)
	}
	assertCardSpecs(t, a, "usb", "2400x1080", 90)
	publishCardSpecs(a, "wifi")
	assertCardSpecs(t, a, "wifi", "1600x720", 75)
	if err := a.SaveProfile(cardSpecsKey, "usb", 1080, 30, 60, false, "pc", false, false, "h264", "opus"); err != nil {
		t.Fatal(err)
	}
	publishCardSpecs(a, "usb")
	assertCardSpecs(t, a, "usb", "3200x1440", 144)
}

func TestCardSpecsDoNotFeedScaledValuesBackIntoDefaults(t *testing.T) {
	a, _ := newCardSpecsApp(DefaultProfile())
	a.mu.Lock()
	a.lastTrack = []adb.Device{{Serial: "SPECDEVICE", ConnType: "usb", State: "device", Res: "3200x2136", FPS: 144}}
	a.devices = []adb.Device{{Serial: "SPECDEVICE", Identity: cardSpecsKey, ConnType: "wifi", State: "device", Res: "1080x720", FPS: 60}}
	a.mu.Unlock()
	p := a.effectiveProfile(cardSpecsKey)
	if p.Usb.Res != 2560 || p.Usb.FPS != 144 {
		t.Fatalf("scaled wireless card changed USB physical fallback: %+v", p.Usb)
	}
}

func TestCardSpecsCastUsesPhysicalGeometryAndSameCustomProfile(t *testing.T) {
	p := DefaultProfile()
	p.Usb.Custom, p.Usb.Res, p.Usb.FPS = true, 1080, 90
	a, runner := newCardSpecsApp(p)
	a.cacheDevicePhys(cardSpecsKey, devPhys{native: NativeDisplay{Width: 1440, Height: 3200, LongEdge: 3200, FPS: 144}})
	publishCardSpecs(a, "usb")
	assertCardSpecs(t, a, "usb", "1080x486", 90)
	if err := a.StartCast("SPECDEVICE"); err != nil {
		t.Fatal(err)
	}
	params := runner.waitParams(t, 1)
	if params.Usb.Res != 1080 || params.Usb.FPS != 90 || a.Snapshot().Cast.NativeRes != "3200x1440" {
		t.Fatalf("cast, panel and card diverged: params=%+v state=%+v", params.Usb, a.Snapshot().Cast)
	}
}

func TestCardSpecsConcurrentPhysicalRefreshAndSnapshot(t *testing.T) {
	a, _ := newCardSpecsApp(DefaultProfile())
	publishCardSpecs(a, "usb")
	var wg sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if worker == 0 {
					a.cacheDevicePhys(cardSpecsKey, devPhys{native: NativeDisplay{Width: 1440, Height: 3200, LongEdge: 3200, FPS: 120 + i%2*24}})
				} else {
					a.Snapshot()
				}
			}
		}(worker)
	}
	wg.Wait()
	a.cacheDevicePhys(cardSpecsKey, devPhys{native: NativeDisplay{Width: 1200, Height: 1920, LongEdge: 1920, FPS: 60}})
	assertCardSpecs(t, a, "usb", "1920x1200", 60)
}
