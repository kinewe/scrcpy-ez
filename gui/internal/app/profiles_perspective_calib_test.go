package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 透视标定缓存：按设备 identity 入档、按 pkg 隔离、重启 Load 后仍保留
// （老档案走 legacyDeviceEntry 中转，字段必须一起搬）。
func TestPerspectiveCalibCacheRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "profiles.json")
	raw := `{"devices":{"Dev 1":{"marketname":"Dev 1","res":"1080x1920","serials":["s1"],"addrs":[]}}}`
	if err := os.WriteFile(p, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(p)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	key := fixtureArchiveKey(s, "Dev 1")
	if env := s.PerspectiveCalibEnv(key, "com.tencent.mm"); env != "" {
		t.Fatalf("fresh device must have no calibration cache: %q", env)
	}
	geom := "v1:1920x1080:1920x1280:0"
	m := []float64{0.1, -1, 2.5, 0, 1, -0.77}
	if err := s.SetPerspectiveCalib(key, "com.tencent.mm", geom, m); err != nil {
		t.Fatal(err)
	}
	want := geom + "|0.100000,-1.000000,2.500000,0.000000,1.000000,-0.770000"
	if got := s.PerspectiveCalibEnv(key, "com.tencent.mm"); got != want {
		t.Fatalf("cache env = %q, want %q", got, want)
	}
	if got := s.PerspectiveCalibEnv(key, "com.other.app"); got != "" {
		t.Fatalf("cache must be isolated per package, got %q", got)
	}
	if err := s.SetPerspectiveCalib(key, "com.tencent.mm", geom, []float64{1, 2, 3}); err == nil {
		t.Fatal("invalid map length must be rejected")
	}
	if err := s.SetPerspectiveCalib("device:missing", "com.tencent.mm", geom, m); err == nil {
		t.Fatal("missing archive must not create a permanent entry")
	}

	s2 := NewProfileStore(p)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s2.PerspectiveCalibEnv(fixtureArchiveKey(s2, "Dev 1"), "com.tencent.mm"); got != want {
		t.Fatalf("cache lost after reload: %q", got)
	}
}
