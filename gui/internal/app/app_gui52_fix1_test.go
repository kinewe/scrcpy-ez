package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/discovery"
)

// --- gui52fix1：配对 identity 归并 + 孤儿 IP:port 档案清理 ---

// TestGui52Fix1PairMergeByIPWhenSerialUnknown：hintSerial 解析失败且 getprop 全空
// → identity 算不出 → 必须按 IP 归并到已有档案（active 优先），绝不新建
// IP:port 键档案（双卡根因回归）。
func TestGui52Fix1PairMergeByIPWhenSerialUnknown(t *testing.T) {
	a, _ := newWirelessApp()
	// 已配对档案：serial + tlsGuid + 同 IP 的 5555/旧 TLS 地址
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Model:      "25091RP04C",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.183:5555", State: AddrStateActive, Mode: ModeTcpip},
			{Addr: "192.168.31.183:40725", State: AddrStateActive, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	})

	var mu sync.Mutex
	var conns []string
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		conns = append(conns, addr)
		mu.Unlock()
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		return "", errors.New("getprop unavailable") // marketname/man/model 全空
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil // 现场重扫也取不到服务名 → hintSerial 保持空
	}

	if err := a.PairConnect("", "192.168.31.183", "37033", "38167", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)

	// 等待无线接入学习完成：38167 TLS + 5555 探测入档
	waitFor(t, 3*time.Second, func() bool {
		e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
		return ok && gui50Fix45EntryHasAddr(e, "192.168.31.183:38167", ModeTls)
	}, "配对端口应并入已有档案")

	entries := a.profiles.Entries()
	if _, ok := entries["192.168.31.183:38167"]; ok {
		t.Fatalf("hintSerial 解析失败时不得新建 IP:port 键档案: %v", entries)
	}
	if len(entries) != 1 {
		t.Fatalf("应只有一条主档案: %v", entries)
	}
	e, ok := entries["Xiaomi Pad 8 Pro"]
	if !ok {
		t.Fatalf("主档案应保留: %v", entries)
	}
	if !contains(e.Serials, "a743e1df") {
		t.Fatalf("serials 不得丢失: %+v", e.Serials)
	}
	tls := gui24FindAddr(e, "192.168.31.183:38167")
	if tls == nil || tls.State != AddrStateActive || tls.Mode != ModeTls {
		t.Fatalf("配对 TLS 地址应合并入主档案 active: %+v", e.Addrs)
	}
	if !gui50Fix45EntryHasAddr(e, "192.168.31.183:5555", ModeTcpip) {
		t.Fatalf("5555 应仍归主档案: %+v", e.Addrs)
	}
}

// TestGui52Fix1PairKnownSerialBehaviorUnchanged：hintSerial 正常解析 → 仍走
// 既有 identity 路径归并，不新建 IP:port 键档案。
func TestGui52Fix1PairKnownSerialBehaviorUnchanged(t *testing.T) {
	a, _ := newWirelessApp()
	gui15Seed(a.profiles, "Xiaomi Pad 8 Pro", &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Model:      "25091RP04C",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.183:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	})
	// 快照给出 TLS 服务名 → hintSerial=a743e1df
	a.mdnsMu.Lock()
	a.mdns = []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-Ab12Cd", Addr: "192.168.31.183:38167", Mode: discovery.MdnsModeTls},
	}
	a.mdnsMu.Unlock()

	var mu sync.Mutex
	a.pairOps.pairFn = func(ctx context.Context, ip, port, code string) (string, error) {
		return "Successfully paired to " + ip + ":" + port, nil
	}
	a.pairOps.connectFn = func(ctx context.Context, addr string) (string, error) {
		mu.Lock()
		mu.Unlock()
		return "connected to " + addr, nil
	}
	a.pairOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		switch prop {
		case "ro.product.marketname":
			return "Xiaomi Pad 8 Pro", nil
		case "ro.product.manufacturer":
			return "Xiaomi", nil
		case "ro.product.model":
			return "25091RP04C", nil
		}
		return "", nil
	}
	a.pairOps.mdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil
	}

	if err := a.PairConnect("", "192.168.31.183", "37033", "38167", "123456"); err != nil {
		t.Fatal(err)
	}
	waitPairPhase(t, a, PairPhaseSuccess)

	waitFor(t, 3*time.Second, func() bool {
		e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
		return ok && gui50Fix45EntryHasAddr(e, "192.168.31.183:38167", ModeTls)
	}, "TLS 地址应归并完成")
	entries := a.profiles.Entries()
	if len(entries) != 1 {
		t.Fatalf("hintSerial 正常时行为不应变化: %v", entries)
	}
	if _, ok := entries["192.168.31.183:38167"]; ok {
		t.Fatalf("不应出现 IP:port 键档案: %v", entries)
	}
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if e.TlsGuid != "adb-a743e1df-Ab12Cd" {
		t.Fatalf("tlsGuid 应更新: %+v", e)
	}
}

// TestGui52Fix1LoadCleansOrphanIPPortArchive：Load 时孤儿 IP:port 键并入
// 同 IP 主档案并删除；deviceOrder 同步清理；二次加载幂等。
func TestGui52Fix1LoadCleansOrphanIPPortArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "192.168.31.183:38167": {
      "addrs": [{"addr": "192.168.31.183:38167", "state": "active", "mode": "tls"}],
      "profiles": {"usb": {}, "wifi": {}}
    },
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "model": "25091RP04C",
      "serials": ["a743e1df"],
      "tlsGuid": "adb-a743e1df-KWqpio",
      "addrs": [
        {"addr": "192.168.31.183:5555", "state": "active", "mode": "tcpip"},
        {"addr": "192.168.31.183:40725", "state": "active", "mode": "tls"}
      ],
      "profiles": {"usb": {}, "wifi": {}}
    }
  },
  "deviceOrder": ["192.168.31.183:38167", "Xiaomi Pad 8 Pro"]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	entries := s.Entries()
	if _, ok := entries["192.168.31.183:38167"]; ok {
		t.Fatalf("孤儿 IP:port 键应被删除: %v", entries)
	}
	main, ok := entries["Xiaomi Pad 8 Pro"]
	if !ok {
		t.Fatalf("主档案应保留: %v", entries)
	}
	if len(main.Addrs) != 2 {
		t.Fatalf("同 IP 孤儿应并入主档案（同形态折叠后 TLS+5555 各一条）: %+v", main.Addrs)
	}
	if !gui50Fix45EntryHasAddr(main, "192.168.31.183:5555", ModeTcpip) ||
		!gui50Fix45EntryHasAddr(main, "192.168.31.183:40725", ModeTls) {
		t.Fatalf("主档案真实证据应保留: %+v", main.Addrs)
	}
	if got := s.DeviceOrder(); len(got) != 1 || got[0] != "Xiaomi Pad 8 Pro" {
		t.Fatalf("deviceOrder 应移除孤儿键: %v", got)
	}

	// 幂等：二次加载无变化
	b1, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewProfileStore(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("二次加载应幂等:\n---1---\n%s\n---2---\n%s", b1, b2)
	}
	raw := string(b2)
	for _, legacy := range []string{`"fail":`, `"lastOk":`, `"lastFail":`, `"stale":`} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("落盘不得含旧字段 %s:\n%s", legacy, b2)
		}
	}
}

// TestGui52Fix1LoadKeepsOrphanWithoutSameIPMain：不同 IP → 孤儿保留（可能是
// 真正还没入档的设备，不能误删）。
func TestGui52Fix1LoadKeepsOrphanWithoutSameIPMain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	data := `{
  "devices": {
    "192.168.31.184:38167": {
      "addrs": [{"addr": "192.168.31.184:38167", "state": "active", "mode": "tls"}],
      "profiles": {"usb": {}, "wifi": {}}
    },
    "Xiaomi Pad 8 Pro": {
      "marketname": "Xiaomi Pad 8 Pro",
      "serials": ["a743e1df"],
      "addrs": [{"addr": "192.168.31.183:5555", "state": "active", "mode": "tcpip"}],
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
	entries := s.Entries()
	if _, ok := entries["192.168.31.184:38167"]; !ok {
		t.Fatalf("无同 IP 主档案的孤儿应保留: %v", entries)
	}
	if len(entries) != 2 {
		t.Fatalf("不同 IP 不得误并: %v", entries)
	}
}

// TestGui52Fix1ResolveKeyByIPActivePriority：同 IP 多档案时 active 状态优先。
func TestGui52Fix1ResolveKeyByIPActivePriority(t *testing.T) {
	s := NewProfileStore("")
	s.mu.Lock()
	s.data.Devices["StaleDevice"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.168.31.183:44444", State: AddrStateStale, Mode: ModeTls}},
		Profiles: DefaultProfile(),
	}
	s.data.Devices["ActiveDevice"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.168.31.183:5555", State: AddrStateActive, Mode: ModeTcpip}},
		Profiles: DefaultProfile(),
	}
	s.mu.Unlock()

	if got := s.ResolveKeyByIP("192.168.31.183"); got != "ActiveDevice" {
		t.Fatalf("同 IP 应按 active 状态优先，got %q", got)
	}
	// 全 stale 时仍有确定性命中
	s.mu.Lock()
	s.data.Devices["ActiveDevice"].Addrs[0].State = AddrStateStale
	s.data.Devices["ActiveDevice"].Addrs[0].Stale = true
	s.mu.Unlock()
	if got := s.ResolveKeyByIP("192.168.31.183"); got == "" {
		t.Fatal("无 active 时 stale 档案仍应按 IP 命中")
	}
}
