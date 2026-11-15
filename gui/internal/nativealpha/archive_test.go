package nativealpha

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture() Record {
	return Record{Schema: 1, Package: "com.example.scan", Application: Application{VersionName: "1.0", VersionCode: 10, LastUpdateTime: 123, SigningSHA256: strings.Repeat("a", 64)}, Environment: Environment{Serial: "PAD_A", SDK: 36, Fingerprint: "test/device/build", Width: 576, Height: 1280, Density: 120}, Activity: "com.example.Scan", TreeSignature: strings.Repeat("b", 64), CameraKind: "texture", Strategy: "texture-background", Steps: []Step{{Target: "camera", Operation: "setCameraAlpha", Value: 0}}, Restore: "exact", Status: "supported", Cache: "discovered", Verification: Verification{AlphaFrames: 2, SpatialAlpha: true}, ObservedAt: time.Now().UnixMilli()}
}
func sceneFixture() Record {
	r := fixture()
	r.EngineVersion = 5
	r.Package = "com.tencent.mm"
	r.Activity = "com.tencent.mm.plugin.scanner.ui.BaseScanUI"
	r.Strategy = "wechat-system-scene"
	r.CaptureSource = "window-buffer-layer"
	r.Restore = "closeScanner"
	r.Verification.CameraAlphaCoverage = 1
	r.SystemBridge = &SystemBridge{Component: "com.android.settings/.SubSettings", Fragment: "com.android.settings.DisplaySettings", VersionName: "16", VersionCode: 36, LastUpdateTime: 123, SigningSHA256: strings.Repeat("c", 64)}
	r.Steps = append(r.Steps, Step{Target: "root", Operation: "prepareSystemScene", Value: 1})
	return r
}
func TestSceneRecipeValidationAndArchive(t *testing.T) {
	r := sceneFixture()
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	got := Candidates(path, r.Package)
	if len(got) != 1 || got[0].SystemBridge == nil || *got[0].SystemBridge != *r.SystemBridge || got[0].CaptureSource != r.CaptureSource {
		t.Fatal("scene metadata lost")
	}
	clone := Clone(&Profile{Schema: 1, Recipes: []Record{r}})
	clone.Recipes[0].SystemBridge.VersionCode++
	if r.SystemBridge.VersionCode != 36 {
		t.Fatal("scene dependency snapshot aliased")
	}
}
func TestSceneCapabilityIsNotAnApplicationOrDeviceAllowlist(t *testing.T) {
	r := sceneFixture()
	r.EngineVersion = 7
	r.Package = "org.example.reader"
	r.Activity = "org.example.reader.CameraPage"
	r.Application.VersionCode = 98765
	r.Application.VersionName = "different-build"
	r.Environment.Model = "different-device"
	r.Environment.Fingerprint = "different/rom"
	r.Strategy = "system-scene"
	r.Cache = "capability"
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.SystemBridge.Component = "org.example/.ArbitraryActivity"
	if Validate(r) == nil {
		t.Fatal("arbitrary bridge accepted as capability")
	}
}
func TestSceneRecipeRejectsUnsafeOrIncompletePlans(t *testing.T) {
	cases := map[string]func(*Record){
		"foreign app":             func(r *Record) { r.Package = "com.example.scan" },
		"arbitrary activity":      func(r *Record) { r.SystemBridge.Component = "com.example/.Activity" },
		"arbitrary fragment":      func(r *Record) { r.SystemBridge.Fragment = "com.example.Fragment" },
		"missing dependency":      func(r *Record) { r.SystemBridge = nil },
		"missing operation":       func(r *Record) { r.Steps = r.Steps[:1] },
		"whole display":           func(r *Record) { r.CaptureSource = "display-mirror" },
		"wrong restore":           func(r *Record) { r.Restore = "exact" },
		"ordinary scene metadata": func(r *Record) { r.Strategy = "texture-background" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := sceneFixture()
			change(&r)
			if Validate(r) == nil {
				t.Fatal("invalid scene accepted")
			}
		})
	}
}
func TestArchivePreservesSettingsAndVersionedRecipes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	raw := `{"extra":{"untouched":true},"devices":{"device:PAD_A":{"serials":["PAD_A"],"displayName":"My Tablet","profiles":{"usb":{"fps":120}},"apps":[{"pkg":"com.example.scan","name":"Scan","sys":false,"futureField":42}]}}}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	r := fixture()
	if err := WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	r.Application.VersionCode++
	r.ObservedAt++
	if err := WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"futureField": 42`, `"untouched": true`, `"fps": 120`, `"My Tablet"`, `"versionCode": 11`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("lost field %s", want)
		}
	}
	if n := len(Candidates(path, r.Package)); n != 2 {
		t.Fatalf("recipes=%d", n)
	}
}
func TestArchiveConcurrentResultsAndInvalidOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			r := fixture()
			r.Environment.Serial = "PAD_" + string(rune('A'+i))
			errs <- WriteRecord(path, r)
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	var data struct {
		Devices map[string]json.RawMessage `json:"devices"`
	}
	if json.Unmarshal(b, &data) != nil || len(data.Devices) != 8 {
		t.Fatal("concurrent results lost")
	}
	r := fixture()
	r.Steps[0].Operation = "exec"
	if WriteRecord(path, r) == nil {
		t.Fatal("arbitrary operation accepted")
	}
	r = fixture()
	r.Verification.AlphaFrames = 1
	if Validate(r) == nil {
		t.Fatal("single frame accepted")
	}
	r = fixture()
	r.Verification.SpatialAlpha = false
	if Validate(r) == nil {
		t.Fatal("unverified alpha accepted")
	}
}
func TestArchiveCloneAndBuildIsolation(t *testing.T) {
	first := fixture()
	second := first
	second.Application.VersionCode++
	p := Merge(&Profile{Schema: 1, Recipes: []Record{first}}, &Profile{Schema: 1, Recipes: []Record{second}})
	if len(p.Recipes) != 2 {
		t.Fatal("different application versions merged")
	}
	copy := Clone(p)
	copy.Recipes[0].Steps[0].Operation = "invalid"
	if p.Recipes[0].Steps[0].Operation == "invalid" {
		t.Fatal("profile snapshot aliases live steps")
	}
}
func TestArchivePortableCandidatesAreNotCrowdedByDuplicateObservations(t *testing.T) {
	var records []Record
	r := fixture()
	r.ObservedAt = 1
	records = append(records, r)
	for i := 0; i < 24; i++ {
		other := fixture()
		other.Environment.Serial = "OTHER"
		other.ObservedAt += int64(i)
		records = append(records, other)
	}
	selected := Prioritize(records, "PAD_A")
	if len(selected) != 1 || selected[0].Environment.Serial != "OTHER" {
		t.Fatal("duplicate device observations crowded portable rules")
	}
	r = fixture()
	r.EngineVersion = 3
	r.Verification.CameraAlphaCoverage = 0.1
	if Validate(r) == nil {
		t.Fatal("transparent status bar accepted as camera alpha")
	}
}
