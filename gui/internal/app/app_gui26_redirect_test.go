package app

// --- gui26：TLS 标实时化 + 副行 IP 实时化（主人修正：显示跟随探测事实） ---
//
// 现场（2026-08-26 实机）：
//   现象 1：开无线调试（K80 TLS 广播 adb-601c9f08-KWqpio @ 197:35263）→
//     [TLS] 标 15s 内亮，但副行 IP 停在 197:5555（取当前连接地址，不随广播变）。
//   现象 2：关无线调试 1 分钟后 [TLS] 标仍亮——decorateTls 的档案记忆分支
//     （HasTlsAddr / e.Addrs mode=tls fail<2）捏着历史成功记录点亮状态标。
//
// 主人修正（20:36）：「IP 跟着 TLS 一同出现和消失……就是探测到是什么情况，
//   然后大家一起跟着变就好了，无线调试优先显示无线调试的 IP，若没有就显示
//   5555 的嘛」——不涉及档案、不做连接重定向，显示即事实：
//   - 出现：广播 TLS 出现在扫描结果（≤15s）→ 标亮 + 副行 IP 同帧变 TLS；
//   - 消失：广播 TLS 消失（关无线调试后 ≤15s）→ 标熄 + 副行 IP 同帧回 5555；
//   - 投屏按钮实际走 gui21 wirelessStartAddr 重解析（TLS 优先），显示替换
//     与投屏地址选择本就同源，替换只影响显示。
//
// 注：任务书早期草案的"连接自动重定向"（connect 新地址+disconnect 旧地址）
// 已被主人修正取消（显示即事实，无重定向、无节流）——本文件沿用任务书
// 指定的文件名 app_gui26_redirect_test.go，覆盖修正后的显示实时化语义。

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// gui26SeedK80 种入 K80 现场档案：真 serial 601c9f08 入 Serials、
// 当前在线 5555（tcpip active）入档（无线调试开着时另有 TLS 广播 35263）。
func gui26SeedK80(a *App) {
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Marketname: "REDMI K80"},
	})
	a.profiles.AddrSuccessWithMode("REDMI K80", "192.168.31.197:5555", ModeTcpip)
}

// gui26K80Card 现场 K80 无线卡：纯无线在线（serial=当前连接 197:5555）。
func gui26K80Card() []adb.Device {
	return []adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	}
}

// gui26K80Mdns 无线调试开着时的快照：TLS 35263 在播 + 经典 tcpip 5555 在播。
func gui26K80Mdns() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-601c9f08-KWqpio", Addr: "192.168.31.197:35263", Mode: discovery.MdnsModeTls},
		{Type: "_adb._tcp", Name: "adb-601c9f08", Addr: "192.168.31.197:5555", Mode: discovery.MdnsModeTcpip},
	}
}

// gui26SetMdns 设置 mDNS 快照（nil=无广播）。
func gui26SetMdns(a *App, svcs []discovery.MdnsService) {
	a.mdnsMu.Lock()
	defer a.mdnsMu.Unlock()
	a.mdns = svcs
}

// gui48-mdns4 档案化：TLS active 进入档案 → TLS 标亮 + 副行 IP 同帧变 TLS。
func TestGui26TlsTagAndIpAppearTogether(t *testing.T) {
	a, _ := newWirelessApp()
	gui26SeedK80(a)
	a.profiles.AddrSuccessWithMode("REDMI K80", "192.168.31.197:35263", ModeTls)

	devs := gui26K80Card()
	a.decorateTls(devs)
	if !devs[0].Tls {
		t.Fatalf("档案 active TLS 应标 TLS: %+v", devs[0])
	}
	if devs[0].Serial != "192.168.31.197:35263" {
		t.Fatalf("副行 IP 应同帧切到 active TLS 地址 35263: %+v", devs[0])
	}
}

// 现象 2 回归（档案化）：TLS 地址 gone → stale；TLS 标熄 + 副行 IP 回 5555。
func TestGui26TlsTagAndIpDisappearTogether(t *testing.T) {
	a, _ := newWirelessApp()
	gui26SeedK80(a)
	a.profiles.AddrSuccessWithMode("REDMI K80", "192.168.31.197:35263", ModeTls)
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:35263")

	devs := gui26K80Card()
	a.decorateTls(devs)
	if devs[0].Tls {
		t.Fatalf("TLS 地址 stale 后不应再标 TLS: %+v", devs[0])
	}
	if devs[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("副行 IP 应同帧回到 5555: %+v", devs[0])
	}
}

// TLS 全部 stale → 显示保持当前连接地址，TLS 标不亮。
func TestGui26NoBroadcastKeepsCurrentDisplay(t *testing.T) {
	a, _ := newWirelessApp()
	gui26SeedK80(a)
	a.profiles.AddrSuccessWithMode("REDMI K80", "192.168.31.197:35263", ModeTls)
	a.profiles.MarkAddrStale("REDMI K80", "192.168.31.197:35263")

	devs := gui26K80Card()
	a.decorateTls(devs)
	if devs[0].Tls {
		t.Fatalf("TLS stale 不应标 TLS: %+v", devs[0])
	}
	if devs[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("无 active TLS 时显示应保持 5555: %+v", devs[0])
	}
}

// 未建档设备的无线卡不做显示替换（防误吞待配对入口）：
// 档案无此 identity → 保持当前连接地址；广播 tls 归"待配对"卡。
func TestGui26UnknownDeviceKeepsCurrentDisplay(t *testing.T) {
	a, _ := newWirelessApp()
	gui26SetMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-601c9f08-KWqpio", Addr: "192.168.31.197:35263", Mode: discovery.MdnsModeTls},
		{Type: "_adb-tls-pairing._tcp", Name: "adb-601c9f08-KWqpio", Addr: "192.168.31.197:37033", Mode: discovery.MdnsModePairing},
	})

	devs := gui26K80Card() // 档案为空：Identity "REDMI K80" 未建档
	a.decorateTls(devs)
	if devs[0].Serial != "192.168.31.197:5555" {
		t.Fatalf("未建档设备不应替换显示地址（待配对入口）: %+v", devs[0])
	}
	a.buildPending(devs)
	pend := a.Snapshot().Pending
	if len(pend) != 1 || pend[0].Addr != "192.168.31.197:35263" {
		t.Fatalf("未建档 tls 广播应产出待配对卡（不被显示替换吞掉）: %+v", pend)
	}
}

// TLS 标档案化判定矩阵（gui48-mdns4）：
// ①档案 active TLS（即使 fail=0 健康）→ 标；
// ②档案 active TLS → 标（不读快照）；
// ③档案 active TLS 存在时无线卡显示/标均由档案决定。
func TestGui26TlsTagRealtimeMatrix(t *testing.T) {
	t.Run("档案activeTLS标", func(t *testing.T) {
		a, _ := newWirelessApp()
		gui17SeedK80(a, 0) // 档案 33895 tls fail=0 active + 5555 tcpip
		devs := []adb.Device{
			{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
				Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		}
		a.decorateTls(devs)
		if !devs[0].Tls {
			t.Fatalf("档案 active TLS 应标 TLS: %+v", devs[0])
		}
	})
	t.Run("档案activeTLS标-不读快照", func(t *testing.T) {
		a, _ := newWirelessApp()
		gui17SeedK80(a, 2) // 档案 tls active
		gui26SetMdns(a, nil)
		devs := []adb.Device{
			{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
				Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		}
		a.decorateTls(devs)
		if !devs[0].Tls {
			t.Fatalf("档案 active TLS 应标 TLS（快照不参与）: %+v", devs[0])
		}
	})
	t.Run("档案activeTLS显示同源", func(t *testing.T) {
		a, _ := newWirelessApp()
		gui17SeedK80(a, 2)
		devs := []adb.Device{
			{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro",
				Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
		}
		a.decorateTls(devs)
		if !devs[0].Tls || devs[0].Serial != "192.168.31.99:33895" {
			t.Fatalf("档案 active TLS 应驱动 TLS 标与显示地址同源: %+v", devs[0])
		}
	})
}
