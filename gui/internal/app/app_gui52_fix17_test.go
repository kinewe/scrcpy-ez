package app

// gui52-fix17：删除设备集泛化（USB+无线全谱）。
// 背景：无线设备删除后 adb server 的 mdns auto-connect（ADB_MDNS_AUTO_CONNECT
// 默认开启）会在数秒内自动重连 transport（模拟 2026-09-02 23:36 真实场景：
// 删除 → 1.8s 后 5555/新 TLS 端口 device → 新设备弹窗 + 卡复活 = 「删不掉」）。
// 删除集是 GUI 侧唯一防回归机制：设备流 / mDNS 入档 / 待配对卡 / 弹窗 / 显示层全过。

import (
	"testing"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

func seedPadProfile(t *testing.T, a *App) {
	t.Helper()
	a.profiles.mu.Lock()
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-On9v2R",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:36983", State: AddrStateActive, Mode: ModeTls},
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	}
	a.profiles.data.DeviceOrder = []string{"Xiaomi Pad 8 Pro"}
	a.profiles.mu.Unlock()
}

// 删除无线设备（平板）后，adb server mdns auto-connect 秒回 transport：
// 显示层无卡、无新设备弹窗、档案不重建（换端口也挡得住）。
func TestGui52Fix17DeleteWirelessBlocksRebirth(t *testing.T) {
	a, _ := newWirelessApp()
	seedPadProfile(t, a)

	// 删除前：设备在 adb 设备流（TLS 形态）→ 有卡
	before := []adb.Device{{
		Serial: "192.168.31.162:36983", State: "device", ConnType: "wifi",
		Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro", Name: "Xiaomi Pad 8 Pro",
	}}
	a.applyTrackUpdate(before)
	if !hasCard(a, "192.168.31.162:36983") {
		t.Fatalf("删除前应显示平板卡")
	}

	if err := a.DeleteDevices([]string{"Xiaomi Pad 8 Pro"}); err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}
	// 删除集键应含：档案键/市场名/短号/TlsGuid/IP 键
	for _, k := range []string{"Xiaomi Pad 8 Pro", "a743e1df", "adb-a743e1df-On9v2R", "ip:192.168.31.162"} {
		if !a.deletedUsbMarked(k) {
			t.Fatalf("删除集应含键 %q", k)
		}
	}

	// adb server mdns auto-connect 秒回（新端口 45205，与删除时端口不同）
	rebirth := []adb.Device{{
		Serial: "192.168.31.162:45205", State: "device", ConnType: "wifi",
		Marketname: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro", Name: "Xiaomi Pad 8 Pro",
	}}
	a.applyTrackUpdate(rebirth)
	if hasCard(a, "192.168.31.162:45205") {
		t.Fatalf("删除后秒回 transport 不得显示卡片")
	}
	if sn := a.Snapshot(); sn.NewDevice != nil {
		t.Fatalf("删除后秒回不得弹新设备弹窗：%+v", sn.NewDevice)
	}
	if _, ok := a.profiles.Entry("Xiaomi Pad 8 Pro"); ok {
		t.Fatalf("删除后秒回不得重建档案")
	}

	// 端口再换（5555 tcpip 形态）+ 服务名令牌形态（adb server auto-connect 的 device 形态）
	rebirth2 := []adb.Device{{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi"}}
	a.applyTrackUpdate(rebirth2)
	if hasCard(a, "192.168.31.162:5555") {
		t.Fatalf("5555 形态秒回也不得显示卡片")
	}
	token := []adb.Device{{Serial: "adb-a743e1df-On9v2R._adb-tls-connect._tcp", State: "device", ConnType: "other"}}
	a.applyTrackUpdate(token)
	if hasCard(a, "adb-a743e1df-On9v2R._adb-tls-connect._tcp") {
		t.Fatalf("mDNS 令牌形态秒回也不得显示卡片")
	}
}

// gui52-fix17b：删除集的设备仍显示为待配对卡——配对弹窗是用户主动配对意图的
// 入口（「删除=不自动复活」由设备流/mDNS 入档/弹窗过滤保证；点配对成功后
// clearDeletedForProfile 清标记并入档）。在 buildPending 过滤会让「删除后再
// 重新配对」找不到设备（00:12 实测回退）。
func TestGui52Fix17DeleteWirelessNoPendingCard(t *testing.T) {
	a, _ := newWirelessApp()
	a.setDeletedUsb("ip:192.168.31.162")
	a.setDeletedUsb("a743e1df")
	a.setDeletedUsb("adb-a743e1df-On9v2R")

	svcs := []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-On9v2R", Addr: "192.168.31.162:45205", Mode: discovery.MdnsModeTls},
	}
	a.mdnsMu.Lock()
	a.mdns = svcs
	a.mdnsMu.Unlock()
	a.buildPending(nil)
	if len(a.Snapshot().Pending) != 1 {
		t.Fatalf("删除集的设备仍应进待配对卡（主动配对入口）：%+v", a.Snapshot().Pending)
	}
	// 无删除集时的基线对照（正常未入档设备也进卡）
	p := a.Snapshot().Pending[0]
	if p.Key != "192.168.31.162:45205" {
		t.Fatalf("待配对卡 Key 异常：%+v", p)
	}
}

// 主动配对成功 = 重来：clearDeletedForProfile 全键清（扫码/手动配对成功路径）。
func TestGui52Fix17PairSuccessClearsDeleted(t *testing.T) {
	a, _ := newWirelessApp()
	for _, k := range []string{"Xiaomi Pad 8 Pro", "a743e1df", "adb-a743e1df-On9v2R", "ip:192.168.31.162"} {
		a.setDeletedUsb(k)
	}
	// 配对成功：档案重建（PairArchive 语义简化：直接种档案）
	a.profiles.mu.Lock()
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs:      []AddrEntry{{Addr: "192.168.31.162:45205", State: AddrStateActive, Mode: ModeTls}},
		Profiles:   DefaultProfile(),
	}
	a.profiles.mu.Unlock()

	a.clearDeletedForProfile("192.168.31.162", "a743e1df", "adb-a743e1df-On9v2R")
	for _, k := range []string{"Xiaomi Pad 8 Pro", "a743e1df", "adb-a743e1df-On9v2R", "ip:192.168.31.162"} {
		if a.deletedUsbMarked(k) {
			t.Fatalf("配对成功后删除集应清空：%q 仍在", k)
		}
	}
}

// 删除集拦截 mDNS 广播入档（applyMdnsServiceAdded：IP 键命中 → 不写档不翻 active）。
func TestGui52Fix17DeleteBlocksMdnsArchive(t *testing.T) {
	a, _ := newWirelessApp()
	seedPadProfile(t, a)
	// 档案地址全部置 stale（删除后离线残留场景），并置删除集 IP 键
	a.profiles.mu.Lock()
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"].Addrs[0].State = AddrStateStale
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"].Addrs[1].State = AddrStateStale
	a.profiles.mu.Unlock()
	a.setDeletedUsb("ip:192.168.31.162")

	a.applyMdnsServiceAdded(&discovery.MdnsService{
		Type: "_adb-tls-connect._tcp", Name: "adb-a743e1df-On9v2R",
		Addr: "192.168.31.162:45205", Mode: discovery.MdnsModeTls,
	})
	e, ok := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if !ok {
		t.Fatalf("档案应存在")
	}
	for _, ae := range e.Addrs {
		if ae.Addr == "192.168.31.162:45205" {
			t.Fatalf("删除集应拦截 mDNS 入档：新地址不得进档案 %+v", e.Addrs)
		}
		if ae.State != AddrStateStale {
			t.Fatalf("删除集应拦截 mDNS 入档（stale 不得翻 active）：%+v", e.Addrs)
		}
	}
}

// gui52-fix17b 回归：删除（档案含无线地址）→ USB 重插（added offline 帧无
// marketname → changed device 帧带市场名）——任意 USB 帧按索引全清，后续
// device 帧不再被删除集过滤 → SyncDevices 正常建档（2026-09-02 23:56 华为
// 连插两次不建档的真实时序）。added 帧无 marketname 也必须把市场名键清掉。
func TestGui52Fix17bDeleteUsbReplugRebuildsArchive(t *testing.T) {
	a, _ := newWirelessApp()
	// 华为档案：USB serial + 无线 5555 地址
	a.profiles.mu.Lock()
	a.profiles.data.Devices["HUAWEI FLA-TL10"] = &DeviceEntry{
		Marketname: "HUAWEI FLA-TL10",
		Serials:    []string{"H9RNW18604002288"},
		Addrs:      []AddrEntry{{Addr: "192.168.31.242:5555", State: AddrStateActive, Mode: ModeTcpip}},
		Profiles:   DefaultProfile(),
	}
	a.profiles.data.DeviceOrder = []string{"HUAWEI FLA-TL10"}
	a.profiles.mu.Unlock()

	// 删除（含市场名键 + serial 键 + IP 键；索引 serial → 全键集）
	if err := a.DeleteDevices([]string{"HUAWEI FLA-TL10"}); err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}
	// 删除后无线 transport 立即被过滤（不复活）
	a.applyTrackUpdate([]adb.Device{{Serial: "192.168.31.242:5555", State: "device", ConnType: "wifi"}})
	if _, ok := a.profiles.Entry("HUAWEI FLA-TL10"); ok {
		t.Fatalf("删除后无线秒回不得重建档案")
	}

	// 重插 USB：added offline（无 marketname——真实 adb 枚举首帧）
	offline := []adb.Device{
		{Serial: "H9RNW18604002288", State: "offline", ConnType: "usb"},
	}
	a.applyTrackUpdate(offline)
	// 索引全清必须在 added 帧生效（不再依赖 marketname 字段）
	if a.deletedUsbMarked("HUAWEI FLA-TL10") || a.deletedUsbMarked("H9RNW18604002288") || a.deletedUsbMarked("ip:192.168.31.242") {
		t.Fatalf("offline added 帧即应全清删除集（市场名键残留会挡建档）")
	}

	// changed device 帧（带市场名/身份——adb 富化后）
	device := []adb.Device{{
		Serial: "H9RNW18604002288", State: "device", ConnType: "usb",
		Marketname: "HUAWEI FLA-TL10", Identity: "HUAWEI FLA-TL10", Name: "HUAWEI FLA-TL10",
	}}
	a.applyTrackUpdate(device)
	if _, ok := a.profiles.Entry("HUAWEI FLA-TL10"); !ok {
		t.Fatalf("device 帧应正常重建档案（删除集已清，不挡建档）")
	}
	if !hasCard(a, "H9RNW18604002288") {
		t.Fatalf("重插后应显示华为卡")
	}
}
