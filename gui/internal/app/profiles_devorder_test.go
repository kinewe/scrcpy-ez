package app

import (
	"os"
	"path/filepath"
	"testing"
)

// --- gui45：设备卡顺序后端持久化（profiles.json deviceOrder） ---

// TestDeviceOrderPersist：SetDeviceOrder 写入 → 新实例 Load 后 DeviceOrder 一致。
func TestDeviceOrderPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	s := NewProfileStore(path)
	_ = s.Load()

	if err := s.SetDeviceOrder([]string{"A", "B"}); err != nil {
		t.Fatal(err)
	}

	s2 := NewProfileStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	got := s2.DeviceOrder()
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("跨实例往返持久化失败: %v", got)
	}
}

// TestSetDeviceOrderIdempotent：同值再次写入不改变文件内容。
func TestSetDeviceOrderIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	s := NewProfileStore(path)
	_ = s.Load()

	if err := s.SetDeviceOrder([]string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceOrder([]string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("同值 SetDeviceOrder 不应改写文件:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestSetDeviceOrderNilEmpty：nil 与空等价，且不 panic。
func TestSetDeviceOrderNilEmpty(t *testing.T) {
	s := NewProfileStore("")
	_ = s.Load()

	if err := s.SetDeviceOrder(nil); err != nil {
		t.Fatal(err)
	}
	if got := s.DeviceOrder(); got == nil || len(got) != 0 {
		t.Fatalf("nil 写回后应得到空切片: %#v", got)
	}
	if err := s.SetDeviceOrder([]string{}); err != nil {
		t.Fatal(err)
	}
	if got := s.DeviceOrder(); len(got) != 0 {
		t.Fatalf("空写回后应仍为空: %#v", got)
	}
}
