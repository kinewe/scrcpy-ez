package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui14：TLS 标签缺失 + mDNS 令牌幽灵卡 + 档案 mode 自愈 ---

// TlsServiceIdentity：adb 37 的 `adb devices` 会以完整 FQN 列出 mDNS 服务
// （实例名._服务类型._tcp）——先剥服务类型后缀段，再走 adb- 前缀 + 末尾
// 6 位随机后缀剥离；原裸名行为回归不变。
func TestTlsServiceIdentityFqn(t *testing.T) {
	cases := map[string]string{
		"adb-601c9f08-KWqpio._adb-tls-connect._tcp": "601c9f08",
		"adb-a743e1df-Ab12Cd._adb._tcp":             "a743e1df",
		"adb-a743e1df-Ab12Cd":                       "a743e1df", // 原裸名回归
		"adb-R58T00WA0YM-0x9zQ2":                    "R58T00WA0YM",
		"a743e1df":                                  "a743e1df",
		"adb-ABCDEF0123456789":                      "ABCDEF0123456789",
		"adb-abc123._tcp":                           "abc123",
		"":                                          "",
	}
	for in, want := range cases {
		if got := TlsServiceIdentity(in); got != want {
			t.Errorf("TlsServiceIdentity(%q) = %q, want %q", in, got, want)
		}
	}
}

// isTlsFormAddr 环境事实启发式：ip:port 且端口解析后 != 5555 → true；
// 5555 / 非 ip:port（服务名/无端口/空）→ false，绝不误判。
func TestIsTlsFormAddr(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"192.168.31.197:33895", true},
		{"10.0.0.8:41234", true},
		{"192.168.31.197:5555", false},
		{"192.168.31.197:05555", false}, // 端口按数值解析：05555 == 5555
		{"adb-601c9f08-KWqpio._adb-tls-connect._tcp", false},
		{"601c9f08", false},
		{"", false},
		{"192.168.31.197", false},  // 无端口
		{"192.168.31.197:", false}, // 空端口
		{"192.168.31.197:abc", false},
	}
	for _, c := range cases {
		if got := isTlsFormAddr(c.in); got != c.want {
			t.Errorf("isTlsFormAddr(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// 令牌（离线，adb 37 实况 FQN）与 K80 在线卡同 identity → 折叠成单卡；
// 副行不写令牌 FQN（只补档案 ip:port 首条，且不写等于主卡自身的地址）。
func TestFoldGhostMdnsTokenMergesIntoIdentityCard(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555", "192.168.31.197:33895"}),
	})
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "adb-601c9f08-KWqpio._adb-tls-connect._tcp", State: "offline", ConnType: "other"},
		{Serial: "192.168.31.197:33895", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 1 {
		t.Fatalf("令牌幽灵卡应折叠为单卡: %+v", devs)
	}
	if devs[0].Serial != "192.168.31.197:33895" {
		t.Fatalf("应保留同身份在线卡: %+v", devs[0])
	}
	if strings.Contains(devs[0].Wireless, "._adb") {
		t.Fatalf("副行不得写令牌 FQN: %+v", devs[0])
	}
	if devs[0].Wireless != "192.168.31.197:5555" {
		t.Fatalf("副行应补档案 ip:port 首条（5555）: %+v", devs[0])
	}
}

// 令牌身份未知 → 直接过滤（不保留成卡）。同时经 ParseDevicesL 验证令牌的
// ConnType 归 "other"（serial 含 "_adb-tls"）——折叠判据因此与 ConnType 无关。
func TestFoldGhostMdnsTokenUnknownFiltered(t *testing.T) {
	a, _ := newWirelessApp()
	raw := adb.ParseDevicesL("List of devices attached\nadb-601c9f08-KWqpio._adb-tls-connect._tcp\toffline\n")
	if len(raw) != 1 || raw[0].ConnType != "other" {
		t.Fatalf("令牌条目解析错误: %+v", raw)
	}
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: raw[0].Serial, State: raw[0].State, ConnType: raw[0].ConnType},
	})
	if len(devs) != 0 {
		t.Fatalf("身份未知的令牌应过滤: %+v", devs)
	}
}

// 21:0x 实测：令牌甚至可能是 device 态（连接活着）——同样折叠进同身份卡，
// 绝不保留成卡；副行不写令牌也不写等于主卡自身的地址。
func TestFoldGhostMdnsTokenDeviceStateFolded(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "adb-601c9f08-KWqpio._adb-tls-connect._tcp", State: "device", ConnType: "other"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 1 || devs[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("device 态令牌应同样折叠进同身份卡: %+v", devs)
	}
	if devs[0].Wireless != "" {
		t.Fatalf("副行不应写令牌/等于主卡自身的地址: %+v", devs[0])
	}
}

// 非令牌回归：USB offline / 在线无线 / 未授权 / 裸实例名（无 "._adb" 段）
// 全部原样保留；折叠只作用于令牌与 offline 无线 ip:port 幽灵。
func TestFoldGhostWirelessRegressionNonToken(t *testing.T) {
	a, _ := newWirelessApp()
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "a743e1df", State: "offline", ConnType: "usb"},
		{Serial: "192.168.31.77:41234", State: "device", ConnType: "wifi"},
		{Serial: "192.168.31.77:41234", State: "unauthorized", ConnType: "wifi"},
		{Serial: "adb-R58T00WA0YM-Xy9zQ2", State: "offline", ConnType: "other"},
	})
	if len(devs) != 4 {
		t.Fatalf("非令牌/USB/在线/未授权条目应原样保留: %+v", devs)
	}
}

// 档案加载自愈：addrs 里 mode 缺失（旧路径入档未写 mode）的无线地址按环境
// 事实补写——33895 → tls、5555 → tcpip；已有 mode 不动；幂等
// （二次加载不再改写文件，文件字节不变）。
func TestNormalizeArchivedMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "REDMI K80": {
      "marketname": "REDMI K80",
      "model": "24117RK2CC",
      "serials": ["601c9f08"],
      "addrs": [
        {"addr": "192.168.31.197:33895", "state": "active", "fail": 0, "lastOk": 1750000001},
        {"addr": "192.168.31.197:5555", "state": "history", "fail": 3, "lastOk": 1750000000},
        {"addr": "192.168.31.197:41234", "state": "active", "fail": 0, "lastOk": 1750000002, "mode": "tls"}
      ],
      "profiles": {"usb": {}, "wifi": {}}
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	e, ok := s.Entry("REDMI K80")
	if !ok {
		t.Fatalf("档案应加载成功: %v", s.Entries())
	}
	// gui52 迁移：同形态双 active 只留 lastOk 最新的 41234（mode=tls）；
	// 旧 history 5555 迁移为 state=stale（离线候选，不再删除）并补 mode=tcpip。
	if len(e.Addrs) != 2 ||
		e.Addrs[0].Addr != "192.168.31.197:41234" || e.Addrs[0].State != AddrStateActive || e.Addrs[0].Mode != ModeTls ||
		e.Addrs[1].Addr != "192.168.31.197:5555" || e.Addrs[1].State != AddrStateStale || e.Addrs[1].Mode != ModeTcpip {
		t.Fatalf("gui52 二态迁移结果错误（tls 41234 active + 5555 stale）: %+v", e.Addrs)
	}
	b1, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b1)
	if !strings.Contains(raw, `"mode": "tls"`) || !strings.Contains(raw, `"mode": "tcpip"`) {
		t.Fatalf("自愈后应落盘 tls/tcpip mode 字段:\n%s", b1)
	}
	for _, legacy := range []string{`"fail":`, `"lastOk":`, `"lastFail":`, `"stale":`, `"history":`} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("迁移后不得残留旧字段 %s:\n%s", legacy, b1)
		}
	}

	// 幂等：二次加载不再改动（文件字节不变）
	s2 := NewProfileStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("二次加载不应再改写档案（幂等）:\n--- 第一次 ---\n%s\n--- 第二次 ---\n%s", b1, b2)
	}
	e2, ok := s2.Entry("REDMI K80")
	if !ok || len(e2.Addrs) != 2 || e2.Addrs[0].Addr != "192.168.31.197:41234" ||
		e2.Addrs[1].Addr != "192.168.31.197:5555" || e2.Addrs[1].State != AddrStateStale {
		t.Fatalf("二次加载档案异常（应保持二态单记忆）: %+v", e2)
	}
}
