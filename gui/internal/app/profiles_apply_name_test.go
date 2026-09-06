package app

import (
	"os"
	"path/filepath"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// testStoreWithK80 构造含 REDMI K80 档案的 store：addrs 里有一条历史地址
// 192.168.31.197:5555（离线残留场景——地址在档但设备当前不在线）。
func testStoreWithK80(t *testing.T) *ProfileStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles.json")
	raw := `{
  "devices": {
    "REDMI K80": {
      "marketname": "REDMI K80",
      "serials": [],
      "addrs": [{"addr": "192.168.31.197:5555", "state": "active", "fail": 0, "lastOk": 1}],
      "profiles": {"usb": {"res": 2560, "fps": 120, "bitrate": 60}, "wifi": {"res": 1920, "fps": 60, "bitrate": 15}}
    }
  }
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	return s
}

// 离线设备（Name=Serial 裸 IP）→ 档案市场名回补到 Name。
func TestApplyProfileNamesOfflineBackfill(t *testing.T) {
	s := testStoreWithK80(t)
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "offline", Name: "192.168.31.197:5555"},
	}
	applyProfileNames(devs, s)
	if devs[0].Name != "REDMI K80" {
		t.Fatalf("离线卡名未回补: %q", devs[0].Name)
	}
}

// Name 为空（enrich 未执行）同样回补。
func TestApplyProfileNamesOfflineEmptyName(t *testing.T) {
	s := testStoreWithK80(t)
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "unauthorized", Name: ""},
	}
	applyProfileNames(devs, s)
	if devs[0].Name != "REDMI K80" {
		t.Fatalf("空 Name 未回补: %q", devs[0].Name)
	}
}

// 在线设备：Name 已有真实富化名（非 IP）→ 不回补（在线路径由 enrich 决定卡名）。
func TestApplyProfileNamesSkipsOnline(t *testing.T) {
	s := testStoreWithK80(t)
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", Name: "REDMI K80"},
	}
	applyProfileNames(devs, s)
	if devs[0].Name != "REDMI K80" {
		t.Fatalf("在线设备已有富化名不应回补: %q", devs[0].Name)
	}
}

// gui52-fix13：在线设备 Name 退化为 IP（富化失败/瞬态 transport）→ 回补档案名
// （36475 一闪 IP 名根因：在线 transport 富化名缺失应用档案名兜底）。
func TestApplyProfileNamesOnlineIpNameBackfill(t *testing.T) {
	s := testStoreWithK80(t)
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", Name: "192.168.31.197:5555"},
	}
	applyProfileNames(devs, s)
	if devs[0].Name != "REDMI K80" {
		t.Fatalf("在线 IP 名应回补档案名: %q", devs[0].Name)
	}
}

// 已有市场名/自定义名（Name 既不空也不等于 Serial）→ 保持不变。
func TestApplyProfileNamesKeepsExistingName(t *testing.T) {
	s := testStoreWithK80(t)
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "offline", Name: "我的 K80"},
	}
	applyProfileNames(devs, s)
	if devs[0].Name != "我的 K80" {
		t.Fatalf("已有卡名被覆盖: %q", devs[0].Name)
	}
}

// 无档案设备：Name 保持原值（IP），不 panic。
func TestApplyProfileNamesUnknownKeepsIP(t *testing.T) {
	s := testStoreWithK80(t)
	devs := []adb.Device{
		{Serial: "192.168.31.200:5555", State: "offline", Name: "192.168.31.200:5555"},
	}
	applyProfileNames(devs, s)
	if devs[0].Name != "192.168.31.200:5555" {
		t.Fatalf("无档案设备 Name 被改动: %q", devs[0].Name)
	}
}

// 档案 Marketname 为空时不回补（回填只认市场名）。
func TestApplyProfileNamesNoMarketnameKeepsIP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	raw := `{
  "devices": {
    "192.168.31.197:5555": {
      "serials": [],
      "addrs": [{"addr": "192.168.31.197:5555", "state": "history", "fail": 3, "lastOk": 0}],
      "profiles": {}
    }
  }
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	devs := []adb.Device{
		{Serial: "192.168.31.197:5555", State: "offline", Name: "192.168.31.197:5555"},
	}
	applyProfileNames(devs, s)
	if devs[0].Name != "192.168.31.197:5555" {
		t.Fatalf("档案无市场名时不应回补: %q", devs[0].Name)
	}
}
