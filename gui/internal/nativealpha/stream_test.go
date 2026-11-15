package nativealpha

import (
	"math"
	"path/filepath"
	"testing"
)

func TestNativeAlphaStreamEvidencePersistsWithoutAliasing(t *testing.T) {
	r := fixture()
	r.Stream = &StreamEvidence{Protocol: 2, Backend: "gpu-colour-srgb", InputColour: "Display P3", OutputColour: "sRGB", AlphaExact: true, MaxRGBError: 1, Measured: true, SampleFrames: 35, SourceFPS: 17.5, ReadMS: 24, EncodeMS: 12, SendMS: 0.3}
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	got := Candidates(path, r.Package)
	if len(got) != 1 || got[0].Stream == nil || *got[0].Stream != *r.Stream {
		t.Fatal("stream performance/colour evidence lost")
	}
	clone := Clone(&Profile{Schema: 1, Recipes: got})
	clone.Recipes[0].Stream.Backend = "cpu-srgb"
	if got[0].Stream.Backend != "gpu-colour-srgb" {
		t.Fatal("stream snapshot aliases live profile")
	}
}
func TestNativeAlphaStreamRejectsUnverifiedOrInvalidMeasurements(t *testing.T) {
	for name, change := range map[string]func(*StreamEvidence){
		"alpha loss":           func(s *StreamEvidence) { s.AlphaExact = false },
		"colour drift":         func(s *StreamEvidence) { s.MaxRGBError = 2 },
		"false exact colour":   func(s *StreamEvidence) { s.RGBAExact = true; s.MaxRGBError = 1 },
		"unregistered backend": func(s *StreamEvidence) { s.Backend = "arbitrary-script" },
		"too few samples":      func(s *StreamEvidence) { s.Measured = true; s.SampleFrames = 2 },
		"nan":                  func(s *StreamEvidence) { s.SourceFPS = math.NaN() },
		"negative time":        func(s *StreamEvidence) { s.ReadMS = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			r := fixture()
			r.Stream = &StreamEvidence{Protocol: 2, Backend: "gpu-plane", InputColour: "sRGB", OutputColour: "sRGB", AlphaExact: true}
			change(r.Stream)
			if Validate(r) == nil {
				t.Fatal("invalid stream evidence accepted")
			}
		})
	}
}
func TestNativeAlphaRuntimeRuleIgnoresDiagnosticMetadata(t *testing.T) {
	a := fixture()
	b := fixture()
	b.Environment.Serial = "PHONE"
	b.Environment.Fingerprint = "different/rom"
	b.Environment.SDK = 35
	b.Environment.Width = 1440
	b.Environment.Density = 600
	b.Environment.UserID = 10
	b.Environment.UIMode = 32
	b.Application.VersionCode = 999
	b.Application.VersionName = "other"
	b.Application.LastUpdateTime = 999
	b.Application.SigningSHA256 = "different"
	if RuntimeRuleKey(a) != RuntimeRuleKey(b) {
		t.Fatal("diagnostic metadata gated portable rule")
	}
	if Key(a) == Key(b) {
		t.Fatal("archival observations were incorrectly collapsed")
	}
	b.Steps = append([]Step(nil), b.Steps...)
	b.Restore = "closeScanner"
	if RuntimeRuleKey(a) == RuntimeRuleKey(b) {
		t.Fatal("restore boundary missing from rule identity")
	}
}

func TestNativeAlphaPortableWireContainsNoDiagnosticMatchingFields(t *testing.T) {
	r := fixture()
	r.Environment.Serial = "private-device"
	r.Environment.Fingerprint = "private/rom"
	r.Stream = &StreamEvidence{Protocol: 2, Backend: "cpu-srgb", AlphaExact: true, OutputColour: "sRGB"}
	wire := PortableTemplates([]Record{r})
	if len(wire) != 1 {
		t.Fatal("missing rule")
	}
	for _, key := range []string{"environment", "application", "stream", "observedAt", "verification"} {
		if _, ok := wire[0][key]; ok {
			t.Fatalf("diagnostic field %s on matching wire", key)
		}
	}
	if wire[0]["activity"] != r.Activity || wire[0]["steps"] == nil || wire[0]["cameraSelector"] == nil {
		t.Fatal("portable operations were removed")
	}
}

func TestNativeAlphaSurfaceSceneRecoveryIsCapabilityBased(t *testing.T) {
	r := sceneFixture()
	r.EngineVersion = 10
	r.Package = "org.example.anyscanner"
	r.Activity = "org.example.anyscanner.Scan"
	r.Strategy = "system-scene"
	r.CameraKind = "surface"
	r.Steps = append(r.Steps, Step{Target: "camera", Operation: "surfaceOnTop", Value: 1})
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.Steps = r.Steps[:len(r.Steps)-1]
	if Validate(r) == nil {
		t.Fatal("surface scene accepted without camera ordering")
	}
}

func TestNativeAlphaRootSceneRestoreRules(t *testing.T) {
	r := sceneFixture()
	r.EngineVersion = 13
	r.Strategy = "system-scene"
	r.Restore = "exact"
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.Restore = "themeResource"
	if Validate(r) == nil {
		t.Fatal("resource restoration accepted without evidence")
	}
	r.RestoreEvidence = "theme-resource-class-match"
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.EngineVersion = 12
	if Validate(r) == nil {
		t.Fatal("legacy record admitted new restoration route")
	}
}

func TestNativeAlphaDirectWindowRequiresRestorableProperties(t *testing.T) {
	r := fixture()
	r.EngineVersion = 15
	r.Verification.CameraAlphaCoverage = 1
	r.Strategy = "window-direct"
	r.CaptureSource = "window-buffer-layer"
	r.Steps = append(r.Steps, Step{Target: "root", Operation: "prepareWindowSource", Value: 1})
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.EngineVersion = 14
	if Validate(r) == nil {
		t.Fatal("legacy engine admitted direct window route")
	}
	r.EngineVersion = 15
	r.Restore = "closeScanner"
	if Validate(r) == nil {
		t.Fatal("direct window source accepted un-restorable page")
	}
	r.Restore = "exact"
	r.SystemBridge = sceneFixture().SystemBridge
	if Validate(r) == nil {
		t.Fatal("direct route accepted scene metadata")
	}
	r.SystemBridge = nil
	r.Steps = r.Steps[:len(r.Steps)-1]
	if Validate(r) == nil {
		t.Fatal("direct route accepted missing source preparation")
	}
}
