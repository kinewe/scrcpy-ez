package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui12 实测修复：TLS 标签（BUG-A）+ 双卡分裂（BUG-B） ---

// setMdns 直接种入 mDNS 快照（跳过低频扫描节流）。
func setMdns(a *App, svcs []discovery.MdnsService) {
	a.mdnsMu.Lock()
	a.mdns = svcs
	a.mdnsMu.Unlock()
}

// BUG-A：StartCast 注入地址 = mDNS tls 服务地址（档案无此 addr）→
// CastState.Tls=true。复现实况：K80 走 mDNS 自动连接（adb -s ip:33895），
// 33895 未入档——档案 AddrMode 查不到形态 → 旧判定漏"TLS加密"标签。
func TestStartCastMdnsTlsAddrSetsTlsFlag(t *testing.T) {
	a, f := newWirelessApp()
	setMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.197:33895", Mode: discovery.MdnsModeTls},
	})
	setDevices(a, []adb.Device{
		{Serial: "192.168.31.197:33895", State: "device", ConnType: "wifi", Name: "REDMI K80"},
	})
	if err := a.StartCast("192.168.31.197:33895"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:33895" {
		t.Fatalf("应注入 mDNS tls 地址: %+v", p)
	}
	if s := a.Snapshot(); !s.Cast.Tls {
		t.Fatalf("mDNS tls 地址启动应置 Tls=true: %+v", s.Cast)
	}
}

// BUG-A 反向：注入 5555（mDNS 只有 33895 tls 服务在播）→ Tls=false
// （判据是同 ip+同 port，异端口不误判）。
func TestStartCastTcpipAddrWithMdnsTlsOtherPortNoTlsFlag(t *testing.T) {
	a, f := newWirelessApp()
	setMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.197:33895", Mode: discovery.MdnsModeTls},
	})
	setDevices(a, []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80"},
	})
	if err := a.StartCast("192.168.31.197:5555"); err != nil {
		t.Fatal(err)
	}
	p := f.waitParams(t, 1)
	if p.Addr != "192.168.31.197:5555" {
		t.Fatalf("无档案地址时应注入卡串号: %+v", p)
	}
	if s := a.Snapshot(); s.Cast.Tls {
		t.Fatalf("5555 连接不应标 Tls（mDNS tls 服务在别的端口）: %+v", s.Cast)
	}
}

// gui41 新语义：IP/历史 IP 线索（keyIPs）与 mDNS 服务名 IP（tlsIPKeys）不再是
// 跨设备归并证据——只有 ResolveKey(d.Serial) 直解命中的同身份卡才归并。
// 下面四个旧“IP 归并”用例改为验证“幽灵独立保留、绝不污染其他卡”。
func TestFoldGhostWirelessDoesNotMergeViaIpClue(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	setMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.197:33895", Mode: discovery.MdnsModeTls},
	})
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "192.168.31.197:33895", State: "offline", ConnType: "wifi"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 2 {
		t.Fatalf("IP 线索不应跨设备归并，幽灵应独立保留: %+v", devs)
	}
	online := gui34Find(devs, "192.168.31.197:5555")
	ghost := gui34Find(devs, "192.168.31.197:33895")
	if online == nil || online.Wireless != "" {
		t.Fatalf("在线卡 Wireless 不应被 IP 线索幽灵污染: %+v", devs)
	}
	if ghost == nil || ghost.State != "offline" {
		t.Fatalf("无直解身份的 IP 线索幽灵应保留为独立卡: %+v", devs)
	}
}

func TestFoldGhostWirelessDoesNotMergeViaProfileIpOnly(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "192.168.31.197:33895", State: "offline", ConnType: "wifi"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 2 || devs[0].Wireless != "" {
		t.Fatalf("档案 IP 线索不再用于跨设备归并: %+v", devs)
	}
	if gui34Find(devs, "192.168.31.197:33895") == nil {
		t.Fatalf("幽灵应独立保留: %+v", devs)
	}
}

func TestFoldGhostWirelessDoesNotMergeViaMdnsSerialClue(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"R58T00WA0YM"}, []string{"192.168.31.197:5555"}),
	})
	setMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.197:33895", Mode: discovery.MdnsModeTls},
	})
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "192.168.31.197:33895", State: "offline", ConnType: "wifi"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 2 || devs[0].Wireless != "" {
		t.Fatalf("mDNS 服务名 IP 线索不应用于跨设备归并: %+v", devs)
	}
	if gui34Find(devs, "192.168.31.197:33895") == nil {
		t.Fatalf("幽灵应独立保留: %+v", devs)
	}
}

func TestFoldGhostWirelessDoesNotMergeIntoUsbViaIp(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "192.168.31.197:33895", State: "offline", ConnType: "wifi"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 2 {
		t.Fatalf("IP 线索幽灵不应并入 USB 卡: %+v", devs)
	}
	usb := gui34Find(devs, "601c9f08")
	if usb == nil || usb.Wireless != "" {
		t.Fatalf("USB 卡 Wireless 不应被 IP 线索幽灵污染: %+v", devs)
	}
	if gui34Find(devs, "192.168.31.197:33895") == nil {
		t.Fatalf("幽灵应独立保留: %+v", devs)
	}
}

// BUG-B 过滤兜底：offline 无线条目无档案身份（resolve identity 失败）、
// mDNS 无服务（ip 不中）→ 从设备列表过滤（不建卡）。
func TestFoldGhostWirelessFiltersUnknown(t *testing.T) {
	a, _ := newWirelessApp()
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "192.168.31.197:33895", State: "offline", ConnType: "wifi"},
	})
	if len(devs) != 0 {
		t.Fatalf("无身份幽灵条目应过滤: %+v", devs)
	}
}

// 档案离线设备卡照常：addr 在档（5555 offline）且无同身份在线卡 → 保留。
func TestFoldGhostWirelessKeepsProfiledOfflineCard(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi"},
	})
	if len(devs) != 1 || devs[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("档案离线设备卡应保留: %+v", devs)
	}
}

// 在线条目 / USB offline / 未授权条目不参与折叠（原样保留）。
func TestFoldGhostWirelessLeavesOnlineUsbUnauthorized(t *testing.T) {
	a, _ := newWirelessApp()
	devs := a.foldGhostWireless([]adb.Device{
		{Serial: "a743e1df", State: "offline", ConnType: "usb"},
		{Serial: "192.168.31.77:41234", State: "device", ConnType: "wifi"},
		{Serial: "192.168.31.77:41234", State: "unauthorized", ConnType: "wifi"},
	})
	if len(devs) != 3 {
		t.Fatalf("在线/USB/未授权条目应原样保留: %+v", devs)
	}
}

// ResolveKey：identity/serial/IP:port 均解析到 identity 键；未知返回 ""。
func TestProfileStoreResolveKey(t *testing.T) {
	s := NewProfileStore("")
	seedProfiles(&App{profiles: s}, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
	if s.ResolveKey("REDMI K80") != "REDMI K80" ||
		s.ResolveKey("601c9f08") != "REDMI K80" ||
		s.ResolveKey("192.168.31.197:5555") != "REDMI K80" {
		t.Fatal("ResolveKey 应按 identity 键/serial/addr 解析")
	}
	if s.ResolveKey("192.168.31.197:33895") != "" || s.ResolveKey("") != "" {
		t.Fatal("未知 key 应返回空")
	}
}

// pollOnce 全链路（fake raw devices + fake mDNS 服务，Linux only，gui41 新语义）：
// raw 含 `ip:33895 offline`（无 model）+ `ip:5555 device`，mDNS tls 服务同 ip →
// 由于 33895 无直解身份，IP/服务名线索不再跨设备归并；在线卡保持干净，
// 33895 幽灵独立保留为离线卡（不污染在线卡，保证 K80 类离线场景可被探测/补卡）。
func TestPollOnceFoldsGhostWirelessCard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n192.168.31.197:33895\\toffline\\n192.168.31.197:5555\\tdevice model:REDMI_K80\\n'; exit 0; fi\n" +
		"if [ \"$3\" = \"shell\" ]; then case \"$5\" in ro.product.marketname) echo 'REDMI K80';; ro.product.manufacturer) echo 'Xiaomi';; ro.product.model) echo '24117RK2CC';; esac; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	setMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-R58T00WA0YM-Xy9zQ2", Addr: "192.168.31.197:33895", Mode: discovery.MdnsModeTls},
	})

	// 轮 1：在线卡入档；轮 2：gui46 残留清理移除无直解身份的 33895 残留，
	// 不再独立成卡（在线卡保持干净）。
	a.pollOnce(context.Background())
	a.pollOnce(context.Background())

	devs := a.Snapshot().Devices
	if len(devs) != 1 {
		t.Fatalf("gui46 应只剩在线卡（残留 33895 被清除）: %+v", devs)
	}
	online := gui34Find(devs, "192.168.31.197:5555")
	if online == nil || online.Wireless != "" || online.State != "device" {
		t.Fatalf("在线卡应保持干净: %+v", devs)
	}
	if gui34Find(devs, "192.168.31.197:33895") != nil {
		t.Fatalf("33895 残留不应独立成卡: %+v", devs)
	}
}
