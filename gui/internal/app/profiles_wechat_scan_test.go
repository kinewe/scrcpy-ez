package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 成功一次即入档，跟随设备：写盘 → 重新 Load 必须保留（老档案走 legacy 中转也不能丢）。
func TestWeChatScanVerifiedRoundtrip(t *testing.T) {
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
	if s.WeChatScanVerified(key) {
		t.Fatal("新设备不应预先带成功实证")
	}
	added, err := s.MarkWeChatScanVerified(key)
	if err != nil || !added {
		t.Fatalf("first success must be persisted: added=%v err=%v", added, err)
	}
	if !s.WeChatScanVerified(key) {
		t.Fatal("in-memory archive must reflect the proof")
	}
	if added2, err := s.MarkWeChatScanVerified(key); err != nil || added2 {
		t.Fatalf("duplicate success must not rewrite archive: added=%v err=%v", added2, err)
	}
	if _, err := s.MarkWeChatScanVerified("device:unknown"); err == nil {
		t.Fatal("unknown device must not create a permanent archive entry")
	}

	// 重启加载后仍保留（跟随设备 identity）。
	s2 := NewProfileStore(p)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if !s2.WeChatScanVerified(fixtureArchiveKey(s2, "Dev 1")) {
		t.Fatal("success proof lost after reload (legacy relay field missing?)")
	}
}
