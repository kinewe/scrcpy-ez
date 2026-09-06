package app

import (
	"os"
	"path/filepath"
	"testing"
)

// gui52-fix11：孤儿合并时，源档案（配对瞬时 transport，33301）的地址不得
// 赢过主档案同形态 active（mdns 权威 42145）——33301 曾以更晚的 LastOk
// 被 normalize 折叠选中、随后 offline 变 stale，真相 42145 丢失。
func TestGui52Fix11MergeKeepsMainActiveTls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "192.168.31.162:33301": {
      "addrs": [{"addr": "192.168.31.162:33301", "state": "active", "mode": "tls"}],
      "profiles": {"usb": {}, "wifi": {}}
    },
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "model": "25091RP04C",
      "serials": ["a743e1df"],
      "tlsGuid": "adb-a743e1df-On9v2R",
      "addrs": [
        {"addr": "192.168.31.162:5555", "state": "active", "mode": "tcpip"},
        {"addr": "192.168.31.162:42145", "state": "active", "mode": "tls", "lastOk": 1788354318}
      ],
      "profiles": {"usb": {}, "wifi": {}}
    }
  },
  "deviceOrder": ["192.168.31.162:33301", "Xiaomi Pad 8 Pro"]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	// Load 阶段即执行 fix1 孤儿合并（cleanOrphanIPPortLocked），此处校验合并结果。
	s.CleanOrphanIPPort() // 幂等：Load 后无孤儿应无改动
	e, ok := s.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatalf("主档案应存在: %+v", s.Entries())
	}
	if !gui50Fix45EntryHasAddr(e, "192.168.31.162:42145", ModeTls) {
		t.Fatalf("mdns 权威 42145 应保留 active: %+v", e.Addrs)
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.162:33301" && e.Addrs[i].State == AddrStateActive {
			t.Fatalf("配对瞬时 transport 33301 不得为主 active: %+v", e.Addrs)
		}
	}
	// 孤儿键应被删除（合并后不进 deviceOrder）
	keys := s.Entries()
	if _, exists := keys["192.168.31.162:33301"]; exists {
		t.Fatalf("孤儿键应删除: %+v", keys)
	}
}

// gui52-fix12：零合入不变式——即使主档案该形态无 active，孤儿地址也不并入
// （孤儿唯一价值=IP，已由 mdns 覆盖；并入=重新引入竞争）。
func TestGui52Fix11MergeAppendsWhenNoMainActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "192.168.31.162:33301": {
      "addrs": [{"addr": "192.168.31.162:33301", "state": "active", "mode": "tls"}],
      "profiles": {"usb": {}, "wifi": {}}
    },
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "model": "25091RP04C",
      "serials": ["a743e1df"],
      "tlsGuid": "adb-a743e1df-On9v2R",
      "addrs": [
        {"addr": "192.168.31.162:5555", "state": "active", "mode": "tcpip"},
        {"addr": "192.168.31.162:41999", "state": "stale", "mode": "tls"}
      ],
      "profiles": {"usb": {}, "wifi": {}}
    }
  },
  "deviceOrder": ["192.168.31.162:33301", "Xiaomi Pad 8 Pro"]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	// Load 阶段即执行 fix1 孤儿合并（cleanOrphanIPPortLocked），此处校验合并结果。
	s.CleanOrphanIPPort() // 幂等：Load 后无孤儿应无改动
	e, ok := s.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatal("主档案应存在")
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == "192.168.31.162:33301" {
			t.Fatalf("零合入：孤儿地址不得出现在主档案: %+v", e.Addrs)
		}
	}
	if !gui50Fix45EntryHasAddr(e, "192.168.31.162:5555", ModeTcpip) {
		t.Fatalf("主档案非孤儿地址应保留: %+v", e.Addrs)
	}
	keys := s.Entries()
	if _, exists := keys["192.168.31.162:33301"]; exists {
		t.Fatalf("孤儿键应删除: %+v", keys)
	}
}
