// Package nativealpha stores declarative, locally revalidated camera UI recipes.
package nativealpha

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"
)

type Application struct {
	VersionName    string `json:"versionName"`
	VersionCode    int64  `json:"versionCode"`
	LastUpdateTime int64  `json:"lastUpdateTime"`
	SigningSHA256  string `json:"signingSha256,omitempty"`
}
type Environment struct {
	Serial       string `json:"serial"`
	SDK          int    `json:"sdk"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Fingerprint  string `json:"fingerprint"`
	UserID       int    `json:"userId"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Density      int    `json:"density"`
	UIMode       int    `json:"uiMode"`
}
type Step struct {
	Target    string  `json:"target"`
	Operation string  `json:"operation"`
	Value     float64 `json:"value"`
}
type Verification struct {
	AlphaFrames              int     `json:"alphaFrames"`
	SpatialAlpha             bool    `json:"spatialAlpha"`
	UpdatesObserved          bool    `json:"updatesObserved"`
	CameraFunctionalVerified bool    `json:"cameraFunctionalVerified"`
	DecisionMS               int64   `json:"decisionMs"`
	ProbeMS                  int64   `json:"probeMs"`
	CameraAlphaCoverage      float64 `json:"cameraAlphaCoverage"`
}
type StreamEvidence struct {
	Protocol     int     `json:"protocol"`
	Backend      string  `json:"backend"`
	InputColour  string  `json:"inputColour"`
	OutputColour string  `json:"outputColour"`
	AlphaExact   bool    `json:"alphaExact"`
	RGBAExact    bool    `json:"rgbaExact"`
	MaxRGBError  int     `json:"maxRgbError"`
	Fallback     string  `json:"fallback,omitempty"`
	Measured     bool    `json:"measured"`
	SampleFrames int     `json:"sampleFrames,omitempty"`
	SourceFPS    float64 `json:"sourceFps,omitempty"`
	ReadMS       float64 `json:"readMs,omitempty"`
	EncodeMS     float64 `json:"encodeMs,omitempty"`
	SendMS       float64 `json:"sendMs,omitempty"`
}
type Record struct {
	RestoreEvidence string          `json:"restoreEvidence,omitempty"`
	RuleKey         string          `json:"ruleKey,omitempty"`
	Schema          int             `json:"schema"`
	Stream          *StreamEvidence `json:"stream,omitempty"`
	EngineVersion   int             `json:"engineVersion"`
	Package         string          `json:"package"`
	Name            string          `json:"name,omitempty"`
	Application     Application     `json:"application"`
	Environment     Environment     `json:"environment"`
	Activity        string          `json:"activity"`
	TreeSignature   string          `json:"treeSignature"`
	CameraKind      string          `json:"cameraKind"`
	CameraSelector  Selector        `json:"cameraSelector"`
	Strategy        string          `json:"strategy"`
	CaptureSource   string          `json:"captureSource,omitempty"`
	SystemBridge    *SystemBridge   `json:"systemBridge,omitempty"`
	Steps           []Step          `json:"steps"`
	Restore         string          `json:"restore"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason,omitempty"`
	Cache           string          `json:"cache"`
	Verification    Verification    `json:"verification"`
	ObservedAt      int64           `json:"observedAt"`
	RetryAfter      int64           `json:"retryAfter,omitempty"`
}
type SystemBridge struct {
	Component      string `json:"component"`
	Fragment       string `json:"fragment"`
	VersionName    string `json:"versionName"`
	VersionCode    int64  `json:"versionCode"`
	LastUpdateTime int64  `json:"lastUpdateTime"`
	SigningSHA256  string `json:"signingSha256"`
}
type Selector struct {
	Class      string `json:"class"`
	ResourceID string `json:"resourceId"`
}
type Profile struct {
	Schema          int      `json:"schema"`
	Recipes         []Record `json:"recipes"`
	LastPresentedAt int64    `json:"lastPresentedAt,omitempty"`
}

var packagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
var targetPattern = regexp.MustCompile(`^(root|camera|ancestor/[0-9]{1,2}|backing/[0-9]{1,2})$`)
var signaturePattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Validate(r Record) error {
	if r.RuleKey != "" && !signaturePattern.MatchString(r.RuleKey) {
		return errors.New("invalid portable rule key")
	}
	if len(r.Package) > 255 || len(r.CameraSelector.Class) > 512 || len(r.CameraSelector.ResourceID) > 512 {
		return errors.New("invalid selector bounds")
	}
	if r.EngineVersion >= 3 && r.Status == "supported" && r.Verification.CameraAlphaCoverage < 0.6 {
		return errors.New("camera area remains opaque")
	}
	if r.Schema != 1 || !packagePattern.MatchString(r.Package) || len(r.Activity) > 512 || len(r.Name) > 512 || !signaturePattern.MatchString(r.TreeSignature) {
		return errors.New("invalid alpha recipe identity")
	}
	if r.Application.VersionCode <= 0 || r.Application.VersionName == "" || len(r.Application.VersionName) > 128 || len(r.Application.SigningSHA256) > 128 {
		return errors.New("missing application build identity")
	}
	if v := r.Stream; v != nil {
		if v.Protocol != 2 || (v.Backend != "gpu-plane" && v.Backend != "gpu-colour-srgb" && v.Backend != "cpu-premult" && v.Backend != "cpu-srgb") || !v.AlphaExact || v.OutputColour != "sRGB" || len(v.InputColour) > 128 || len(v.Fallback) > 256 || v.MaxRGBError < 0 || v.MaxRGBError > 1 || (v.RGBAExact && v.MaxRGBError != 0) || v.SampleFrames < 0 || (v.Measured && v.SampleFrames < 3) {
			return errors.New("invalid stream evidence")
		}
		for _, n := range []float64{v.SourceFPS, v.ReadMS, v.EncodeMS, v.SendMS} {
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 10000 {
				return errors.New("invalid stream measurement")
			}
		}
	}

	e := r.Environment
	if e.Serial == "" || len(e.Serial) > 128 || e.SDK < 29 || e.SDK > 100 || len(e.Fingerprint) > 1024 || e.Width <= 0 || e.Height <= 0 || int64(e.Width)*int64(e.Height) > 16*1024*1024 || e.Density <= 0 || e.UserID < 0 {
		return errors.New("invalid device context")
	}
	if r.CameraKind != "texture" && r.CameraKind != "surface" {
		return errors.New("invalid preview kind")
	}
	if r.Strategy != "texture-background" && r.Strategy != "surface-zalpha" && r.Strategy != "wechat-system-scene" && r.Strategy != "system-scene" && r.Strategy != "window-direct" {
		return errors.New("unknown strategy")
	}
	if r.CaptureSource != "" && r.CaptureSource != "display-mirror" && r.CaptureSource != "window-buffer-layer" {
		return errors.New("invalid capture source")
	}
	scene := r.Strategy == "wechat-system-scene" || r.Strategy == "system-scene"
	direct := r.Strategy == "window-direct"
	if scene {
		b := r.SystemBridge
		if r.EngineVersion < 5 || (r.Strategy == "wechat-system-scene" && (r.Package != "com.tencent.mm" || r.Activity != "com.tencent.mm.plugin.scanner.ui.BaseScanUI")) || (r.Strategy == "wechat-system-scene" && r.CameraKind != "texture") || r.CaptureSource != "window-buffer-layer" || (r.Restore != "closeScanner" && !(r.EngineVersion >= 13 && (r.Restore == "exact" || r.Restore == "themeResource"))) || b == nil || b.Component != "com.android.settings/.SubSettings" || b.Fragment != "com.android.settings.DisplaySettings" || b.VersionCode <= 0 || b.VersionName == "" || len(b.VersionName) > 128 || b.LastUpdateTime <= 0 || !signaturePattern.MatchString(b.SigningSHA256) {
			return errors.New("invalid system scene recipe")
		}
	} else if direct {
		if r.EngineVersion < 15 || r.CaptureSource != "window-buffer-layer" || r.SystemBridge != nil || (r.Restore != "exact" && r.Restore != "themeResource") {
			return errors.New("invalid direct window recipe")
		}
	} else if r.CaptureSource == "window-buffer-layer" || r.SystemBridge != nil {
		return errors.New("scene metadata on ordinary recipe")
	}
	if len(r.RestoreEvidence) > 64 || (r.RestoreEvidence != "" && r.RestoreEvidence != "theme-resource-class-match") {
		return errors.New("invalid restoration evidence")
	}
	if r.Restore == "themeResource" && (r.EngineVersion < 13 || r.RestoreEvidence != "theme-resource-class-match") {
		return errors.New("missing live resource restore evidence")
	}
	if r.Restore != "exact" && r.Restore != "closeScanner" && r.Restore != "themeResource" {
		return errors.New("unknown restore policy")
	}
	if r.Status != "supported" && r.Status != "unsupported" {
		return errors.New("invalid result")
	}
	if r.Cache != "local" && r.Cache != "cross-device" && r.Cache != "discovered" && r.Cache != "builtin" && r.Cache != "capability" {
		return errors.New("invalid provenance")
	}
	if len(r.Steps) > 24 || len(r.Reason) > 256 || r.ObservedAt <= 0 || r.ObservedAt > time.Now().Add(24*time.Hour).UnixMilli() {
		return errors.New("invalid result bounds")
	}
	if r.Status == "supported" && (!r.Verification.SpatialAlpha || r.Verification.AlphaFrames < 2 || len(r.Steps) == 0) {
		return errors.New("unverified alpha result")
	}
	seen := map[string]bool{}
	for _, s := range r.Steps {
		if !targetPattern.MatchString(s.Target) {
			return errors.New("invalid target role")
		}
		switch s.Operation {
		case "clearBackground":
			if s.Target == "camera" || s.Value != 0 {
				return errors.New("invalid background operation")
			}
		case "setCameraAlpha":
			if s.Target != "camera" || s.Value != 0 {
				return errors.New("invalid camera alpha")
			}
		case "surfaceOnTop":
			if r.CameraKind != "surface" || s.Target != "camera" || s.Value != 1 {
				return errors.New("invalid surface operation")
			}
		case "prepareSystemScene":
			if !scene || s.Target != "root" || s.Value != 1 {
				return errors.New("invalid system scene operation")
			}
		case "prepareWindowSource":
			if !direct || s.Target != "root" || s.Value != 1 {
				return errors.New("invalid direct window operation")
			}
		default:
			return errors.New("non-declarative operation rejected")
		}
		key := s.Target + ":" + s.Operation
		if seen[key] {
			return errors.New("duplicate operation")
		}
		seen[key] = true
	}
	if r.Status == "supported" && !seen["camera:setCameraAlpha"] {
		return errors.New("missing camera operation")
	}
	if r.Status == "supported" && r.CameraKind == "surface" && !seen["camera:surfaceOnTop"] {
		return errors.New("missing surface ordering operation")
	}
	if scene && !seen["root:prepareSystemScene"] {
		return errors.New("missing system scene operation")
	}
	if direct && !seen["root:prepareWindowSource"] {
		return errors.New("missing direct window operation")
	}
	return nil
}

// PortableTemplates sends executable rule descriptions, not device/version observations.
func PortableTemplates(records []Record) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, r := range records {
		if Validate(r) != nil || r.Status != "supported" {
			continue
		}
		b, _ := json.Marshal(r)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		for _, key := range []string{"environment", "application", "name", "stream", "verification", "observedAt", "retryAfter", "cache", "reason"} {
			delete(v, key)
		}
		if v["systemBridge"] != nil {
			v["systemBridge"] = map[string]string{"component": r.SystemBridge.Component, "fragment": r.SystemBridge.Fragment}
		}
		out = append(out, v)
	}
	return out
}

// RuntimeRuleKey identifies portable operations, independently of observation metadata.
func RuntimeRuleKey(r Record) string {
	if r.RuleKey != "" {
		return r.Package + "/" + r.Activity + "/" + r.RuleKey
	}
	b, _ := json.Marshal(struct {
		Tree, Kind, Strategy, Source, Restore string
		Steps                                 []Step
	}{r.TreeSignature, r.CameraKind, r.Strategy, r.CaptureSource, r.Restore, r.Steps})
	return fmt.Sprintf("%s/%s/%x", r.Package, r.Activity, sha256.Sum256(b))
}

// Key is the archival observation key, never the runtime admission/matching key.
func Key(r Record) string {
	e := r.Environment
	return fmt.Sprintf("%s/%d/%s/%s/%d/%s/%d/%dx%d/%d/%d/%s", r.Activity, r.Application.VersionCode, r.Application.SigningSHA256, r.TreeSignature, e.SDK, e.Fingerprint, e.UserID, e.Width, e.Height, e.Density, e.UIMode, r.CameraKind)
}
func Merge(a, b *Profile) *Profile {
	out := &Profile{Schema: 1}
	m := map[string]Record{}
	for _, p := range []*Profile{a, b} {
		if p == nil || p.Schema != 1 {
			continue
		}
		out.LastPresentedAt = max(out.LastPresentedAt, p.LastPresentedAt)
		for _, r := range p.Recipes {
			if Validate(r) != nil {
				continue
			}
			k := Key(r)
			prior, ok := m[k]
			if !ok || r.ObservedAt > prior.ObservedAt {
				m[k] = r
			}
		}
	}
	for _, r := range m {
		out.Recipes = append(out.Recipes, r)
	}
	sort.Slice(out.Recipes, func(i, j int) bool {
		if out.Recipes[i].ObservedAt == out.Recipes[j].ObservedAt {
			return Key(out.Recipes[i]) < Key(out.Recipes[j])
		}
		return out.Recipes[i].ObservedAt > out.Recipes[j].ObservedAt
	})
	if len(out.Recipes) > 32 {
		out.Recipes = out.Recipes[:32]
	}
	if len(out.Recipes) == 0 && out.LastPresentedAt == 0 {
		return nil
	}
	return out
}
func Clone(p *Profile) *Profile {
	if p == nil {
		return nil
	}
	b, _ := json.Marshal(p)
	var out Profile
	_ = json.Unmarshal(b, &out)
	return &out
}
