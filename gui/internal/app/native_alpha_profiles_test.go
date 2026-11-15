package app

import (
	"path/filepath"
	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/nativealpha"
	"strings"
	"testing"
	"time"
)

func alphaRecordFixture() nativealpha.Record {
	return nativealpha.Record{Schema: 1, Package: "com.example.scan", Application: nativealpha.Application{VersionName: "1", VersionCode: 10, LastUpdateTime: 100}, Environment: nativealpha.Environment{Serial: "PAD_A", SDK: 36, Fingerprint: "test", Width: 576, Height: 1280, Density: 120}, Activity: "com.example.Scan", TreeSignature: strings.Repeat("a", 64), CameraKind: "texture", Strategy: "texture-background", Steps: []nativealpha.Step{{Target: "camera", Operation: "setCameraAlpha", Value: 0}}, Restore: "exact", Status: "supported", Cache: "discovered", Verification: nativealpha.Verification{AlphaFrames: 2, SpatialAlpha: true}, ObservedAt: time.Now().UnixMilli()}
}
func TestNativeAlphaProfileCatalogRoundTripAndSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	s := NewProfileStore(path)
	s.SyncDevices([]adb.Device{{Serial: "PAD_A", State: "device", ConnType: "usb"}})
	apps := []AppListItem{{Pkg: "com.example.scan", Name: "Scan", VersionName: "1", VersionCode: 10}}
	if err := s.SetApps("device:PAD_A", apps); err != nil {
		t.Fatal(err)
	}
	r := alphaRecordFixture()
	if err := s.RecordNativeAlpha("device:PAD_A", r); err != nil {
		t.Fatal(err)
	}
	if err := s.recordNativeAlphaPresented("device:PAD_A", r.Package, "Scan"); err != nil {
		t.Fatal(err)
	}
	r.Status = "unsupported"
	r.ObservedAt++
	if err := s.RecordNativeAlpha("device:PAD_A", r); err != nil {
		t.Fatal(err)
	}
	// A later failed attempt and a catalog refresh must preserve the historical display marker.
	apps[0].VersionCode = 11
	apps[0].LastUpdateTime = 200
	if err := s.SetApps("device:PAD_A", apps); err != nil {
		t.Fatal(err)
	}
	loaded := NewProfileStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	e, _ := loaded.Entry("device:PAD_A")
	profile := e.Apps[0].NativeAlpha
	if e.Apps[0].VersionCode != 11 || len(profile.Recipes) != 1 ||
		profile.Recipes[0].Status != "unsupported" || profile.LastPresentedAt <= 0 {
		t.Fatal("metadata/recipe lost on catalog refresh or migration")
	}
	e.Apps[0].NativeAlpha.Recipes[0].Steps[0].Operation = "changed"
	again, _ := loaded.Entry("device:PAD_A")
	if again.Apps[0].NativeAlpha.Recipes[0].Steps[0].Operation == "changed" {
		t.Fatal("snapshot changed archive")
	}
}
func TestNativeAlphaProfileExternalWriterAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	s := NewProfileStore(path)
	s.SyncDevices([]adb.Device{{Serial: "PAD_A", State: "device", ConnType: "usb"}})
	if err := s.SetApps("device:PAD_A", []AppListItem{{Pkg: "com.example.scan"}}); err != nil {
		t.Fatal(err)
	}
	r := alphaRecordFixture()
	if err := nativealpha.WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Persist(); err != nil {
		t.Fatal(err)
	}
	if len(nativealpha.Candidates(path, r.Package)) != 1 {
		t.Fatal("GUI snapshot overwrote CLI recipe")
	}
	r.Environment.Serial = "OTHER"
	if s.RecordNativeAlpha("device:PAD_A", r) == nil {
		t.Fatal("cross-device result written as local verification")
	}
}
