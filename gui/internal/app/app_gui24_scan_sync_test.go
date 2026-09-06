package app

import (
	"context"
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui24 迁移：mdns 广播事件入档（原 15s 扫描同步逻辑，现由
// onMdnsTrackEvents 事件流驱动）。 ---

// seedGui24TabletArchive 播种平板档案（现场基线）：serial a743e1df、
// 旧 tcpip 地址 183:5555 已入档（active），wireless=tcpip，无 tlsGuid。
func seedGui24TabletArchive(a *App) {
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:5555", ModeTcpip)
}

// gui24TabletMdns 平板当前广播：TLS 服务新端口 37201（adb-a743e1df-On9v2R）
// + 经典 _adb._tcp 162:5555。
func gui24TabletMdns() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-On9v2R", Addr: "192.168.31.183:37201", Mode: discovery.MdnsModeTls},
		{Type: "_adb._tcp", Name: "adb-a743e1df", Addr: "192.168.31.162:5555", Mode: discovery.MdnsModeTcpip},
	}
}

// gui24FindAddr 在档案地址列表里找指定地址（无则 nil）。
func gui24FindAddr(e DeviceEntry, addr string) *AddrEntry {
	for i := range e.Addrs {
		if e.Addrs[i].Addr == addr {
			return &e.Addrs[i]
		}
	}
	return nil
}

// TestMdnsEventsSyncTlsToArchive：广播 added（首块全量）→ 档案出现 37201
// （mode=tls active fail=0）+ 162（mode=tcpip）+ wirelessForm=tls + tlsGuid。
func TestMdnsEventsSyncTlsToArchive(t *testing.T) {
	a, _ := newWirelessApp()
	seedGui24TabletArchive(a)

	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: gui24TabletMdns(),
		First:    true,
	})

	e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatal("档案应存在")
	}
	tls37201 := gui24FindAddr(e, "192.168.31.183:37201")
	if tls37201 == nil {
		t.Fatalf("TLS 新端口 37201 应入档: %+v", e.Addrs)
	}
	if tls37201.Mode != ModeTls || tls37201.State != AddrStateActive || tls37201.Fail != 0 {
		t.Fatalf("37201 应 mode=tls active fail=0: %+v", tls37201)
	}
	tcpip162 := gui24FindAddr(e, "192.168.31.162:5555")
	if tcpip162 == nil || tcpip162.Mode != ModeTcpip {
		t.Fatalf("162 应入档 mode=tcpip: %+v", e.Addrs)
	}
	if e.Wireless != ModeTls {
		t.Fatalf("无线形态应升级为 tls: %q", e.Wireless)
	}
	if e.TlsGuid != "adb-a743e1df-On9v2R" {
		t.Fatalf("tlsGuid 应记录服务实例名: %q", e.TlsGuid)
	}
}

// TestMdnsEventsNoSyncOnEmptySnapshot：空快照（首块）→ 档案地址不变（无广播=无 added）。
func TestMdnsEventsNoSyncOnEmptySnapshot(t *testing.T) {
	a, _ := newWirelessApp()
	seedGui24TabletArchive(a)

	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{
		Snapshot: nil,
		First:    true,
	})

	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if len(e.Addrs) != 1 || e.Addrs[0].Addr != "192.168.31.183:5555" {
		t.Fatalf("空快照不应写档案: %+v", e.Addrs)
	}
	if e.Wireless != ModeTcpip || e.TlsGuid != "" {
		t.Fatalf("空快照不应升级形态: wireless=%q tlsGuid=%q", e.Wireless, e.TlsGuid)
	}
}

// TestMdnsEventsIdempotent：连续两轮同快照 → 不重复入档、不破坏已有状态。
func TestMdnsEventsIdempotent(t *testing.T) {
	a, _ := newWirelessApp()
	seedGui24TabletArchive(a)

	snap := gui24TabletMdns()
	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: snap, First: true})
	first, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	first37201 := gui24FindAddr(first, "192.168.31.183:37201")
	if first37201 == nil {
		t.Fatal("第一轮 37201 应入档")
	}

	a.onMdnsTrackEvents(context.Background(), adb.MdnsTrackEvents{Snapshot: snap, First: false})

	second, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if len(second.Addrs) != len(first.Addrs) {
		t.Fatalf("第二轮不应新增/删除地址: %+v → %+v", first.Addrs, second.Addrs)
	}
	second37201 := gui24FindAddr(second, "192.168.31.183:37201")
	if second37201 == nil {
		t.Fatal("第二轮 37201 应仍在档案")
	}
	if second37201.Fail != 0 || second37201.State != AddrStateActive {
		t.Fatalf("第二轮不应破坏 fail/state: %+v", second37201)
	}
	if second37201.LastOk != first37201.LastOk {
		t.Fatalf("第二轮不应刷新 lastOk（幂等）: %d → %d", first37201.LastOk, second37201.LastOk)
	}
	if gui24FindAddr(second, "192.168.31.183:5555") != nil {
		t.Fatalf("旧地址 183:5555 应按单记忆删除: %+v", second.Addrs)
	}
	if second.Wireless != ModeTls || second.TlsGuid != "adb-a743e1df-On9v2R" {
		t.Fatalf("两轮后形态应保持 tls: wireless=%q tlsGuid=%q", second.Wireless, second.TlsGuid)
	}
}
