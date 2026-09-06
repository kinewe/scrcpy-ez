package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui29：档案设备离线卡补齐（设备从 adb 干净消失仍在列表显示） ---
//
// 根因（2026-08-26 21:06 实机）：foldGhostWireless 是保留机制不是构建机制——
// adb devices 里的 offline 幽灵条目（addr 在档）才保留为离线卡；设备从
// adb devices 完全干净移除（拔 USB 且无无线连接/无 offline 残留）→ devs 无
// 任何条目 → 前端不显示该设备（"凭空消失"）。gui29 在 pollOnce 设备列表
// 构建末端按档案补齐离线卡（市场名 + 副行最近无线地址），设备"常在"。

// gui29FindDev 在设备列表按 Serial 找卡（无则 nil）。
func gui29FindDev(devs []adb.Device, serial string) *adb.Device {
	for i := range devs {
		if devs[i].Serial == serial {
			return &devs[i]
		}
	}
	return nil
}

// gui29CountIdentity 统计设备列表中解析到指定档案 identity 的卡数（Serial /
// Wireless 双重匹配，与 appendProfileOfflineCards 的"已有卡"判据同口径）。
func gui29CountIdentity(a *App, devs []adb.Device, key string) int {
	n := 0
	for i := range devs {
		if a.profiles.ResolveKey(devs[i].Serial) == key {
			n++
		}
		if devs[i].Wireless != "" && a.profiles.ResolveKey(devs[i].Wireless) == key {
			n++
		}
	}
	return n
}

// 档案设备 A（K80）/B（平板）：devs 无 A 的卡（在线+残留均无）、B 有在线卡 →
// 输出追加 A 离线卡（市场名 + 最近 lastOk 地址）；B 不重复、原在线卡保持在前。
func TestGui29OfflineCardAppendedOnlyForMissingProfileDevice(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Model:      "24117RK2CC",
			Serials:    []string{"601c9f08"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 1750000010, Mode: ModeTcpip},
				{Addr: "192.168.31.197:33895", State: AddrStateActive, LastOk: 1750000020, Mode: ModeTls},
			},
			Profiles: DefaultProfile(),
		},
		"Xiaomi Pad 8 Pro": {
			Marketname: "Xiaomi Pad 8 Pro",
			Model:      "25091RP04C",
			Serials:    []string{"a743e1df"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.162:5555", State: AddrStateActive, LastOk: 1750000030, Mode: ModeTcpip},
			},
			Profiles: DefaultProfile(),
		},
	})

	devs := a.appendProfileOfflineCards([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro", Identity: "Xiaomi Pad 8 Pro"},
	})
	if len(devs) != 2 {
		t.Fatalf("应追加一张 K80 离线卡（平板在线不补）: %+v", devs)
	}
	if devs[0].Serial != "a743e1df" || devs[0].State != "device" {
		t.Fatalf("原有在线卡应保持在前: %+v", devs[0])
	}
	if gui29CountIdentity(a, devs, "Xiaomi Pad 8 Pro") != 1 {
		t.Fatalf("平板已有在线卡，不得重复补离线卡: %+v", devs)
	}
	k80 := devs[1]
	if k80.Serial != "192.168.31.197:33895" {
		t.Fatalf("离线卡副行地址应为最近 lastOk 地址（tls 33895=20 胜 5555=10）: %+v", k80)
	}
	if k80.State != "device" || k80.ConnType != "wifi" || k80.Name != "REDMI K80" || k80.Identity != "REDMI K80" {
		t.Fatalf("档案含 active 应补在线合成卡（市场名+最近地址）: %+v", k80)
	}
}

// 合成在线卡地址择优（gui52 改为二态）：active TLS 优先 → active tcpip；
// stale/旧 history 只是离线候选，不压过任何 active 地址；lastOk 不入判据。
func TestGui29OfflineCardAddrPicksLatestLastOkPerClass(t *testing.T) {
	cases := []struct {
		name  string
		addrs []AddrEntry
		want  string
	}{
		{
			name: "active 严格优先（stale lastOk 再大也不压）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 150, Mode: ModeTcpip},
				{Addr: "192.168.31.162:5555", State: AddrStateStale, LastOk: 90, Mode: ModeTcpip},
				{Addr: "192.168.31.197:33895", State: AddrStateActive, LastOk: 100, Mode: ModeTls},
				{Addr: "192.168.31.197:42449", State: AddrStateStale, LastOk: 200, Mode: ModeTls},
			},
			want: "192.168.31.197:33895", // active TLS 33895 > active tcpip 5555
		},
		{
			name: "tls 形态优先（tcpip lastOk 更新仍选 tls）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 400, Mode: ModeTcpip},
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 300, Mode: ModeTls},
			},
			want: "192.168.31.197:42449",
		},
		{
			name: "同刻 tls 优先",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 300, Mode: ModeTcpip},
				{Addr: "192.168.31.197:42449", State: AddrStateActive, LastOk: 300, Mode: ModeTls},
			},
			want: "192.168.31.197:42449",
		},
		{
			name: "失败节流不影响展示（lastFail=now 仍显示）",
			addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 300, LastFail: time.Now().Unix(), Mode: ModeTcpip},
			},
			want: "192.168.31.197:5555",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newWirelessApp()
			seedProfiles(a, map[string]*DeviceEntry{
				"REDMI K80": {
					Marketname: "REDMI K80",
					Serials:    []string{"601c9f08"},
					Addrs:      c.addrs,
					Profiles:   DefaultProfile(),
				},
			})
			devs := a.appendProfileOfflineCards(nil)
			if len(devs) != 1 || devs[0].Serial != c.want {
				t.Fatalf("离线卡副行地址应为 %q: %+v", c.want, devs)
			}
		})
	}
}

// 无地址档案设备（从未无线学习过）→ 仍显示离线卡：市场名 + 副行回退 USB
// serial（"市场名+离线"的设备常在语义；前端离线卡副行固定"离线 · <serial>"）。
// 连 serial 都没有的档案 → 副行回退档案 identity 键。
func TestGui29OfflineCardForNoAddrProfileDevice(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"HUAWEI nova 13": {
			Marketname: "HUAWEI nova 13",
			Serials:    []string{"HW12345"},
			Addrs:      []AddrEntry{},
			Profiles:   DefaultProfile(),
		},
		"OLD": {Addrs: []AddrEntry{}, Profiles: DefaultProfile()},
	})
	devs := a.appendProfileOfflineCards(nil)
	if len(devs) != 2 {
		t.Fatalf("无地址档案设备也应显示离线卡: %+v", devs)
	}
	// 键排序确定性："HUAWEI nova 13" < "OLD"
	if devs[0].Serial != "HW12345" || devs[0].ConnType != "usb" || devs[0].State != "offline" ||
		devs[0].Name != "HUAWEI nova 13" {
		t.Fatalf("无地址档案设备离线卡应 市场名+USB serial 副行: %+v", devs[0])
	}
	if devs[1].Serial != "OLD" || devs[1].State != "offline" || devs[1].Name != "OLD" {
		t.Fatalf("无 serial 无 addr 档案应回退 identity 键: %+v", devs[1])
	}
}

// 已有卡（离线残留卡 / 未授权卡 / Wireless 副行身份）→ 不重复补离线卡。
func TestGui29OfflineCardSkipsExistingCards(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Serials:    []string{"601c9f08"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 1750000010, Mode: ModeTcpip},
			},
			Profiles: DefaultProfile(),
		},
	})

	// ① adb 的离线残留卡（foldGhostWireless 保留路径）→ 不补
	devs := a.appendProfileOfflineCards([]adb.Device{
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi"},
	})
	if len(devs) != 1 || gui29FindDev(devs, "192.168.31.197:5555") == nil {
		t.Fatalf("已有离线残留卡不应再补: %+v", devs)
	}

	// ② 未授权卡（USB 插线未授权）→ 不补
	devs = a.appendProfileOfflineCards([]adb.Device{
		{Serial: "601c9f08", State: "unauthorized", ConnType: "usb"},
	})
	if len(devs) != 1 || gui29FindDev(devs, "601c9f08") == nil {
		t.Fatalf("未授权卡在列不应再补离线卡: %+v", devs)
	}

	// ③ 在线 USB 卡 + Wireless 副行（身份经 Wireless 解析）→ 不补
	devs = a.appendProfileOfflineCards([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Wireless: "192.168.31.197:5555"},
	})
	if len(devs) != 1 || gui29FindDev(devs, "601c9f08") == nil {
		t.Fatalf("Wireless 副行已带身份不应再补: %+v", devs)
	}
}

// 待配对卡（mDNS 新设备，非档案设备）不受影响：仍由 buildPending 产出、
// 不出离线卡；档案设备的离线卡照常补齐。
func TestGui29OfflineCardsLeavePendingUntouched(t *testing.T) {
	a, _ := newWirelessApp()
	setMdns(a, []discovery.MdnsService{
		{Type: "_adb-tls-connect._tcp", Name: "adb-HW12345-Xy9zQ2", Addr: "192.168.31.99:33895", Mode: discovery.MdnsModeTls},
	})
	a.buildPending(nil)
	if len(a.Snapshot().Pending) != 1 || a.Snapshot().Pending[0].Addr != "192.168.31.99:33895" {
		t.Fatalf("buildPending 应产出待配对卡: %+v", a.Snapshot().Pending)
	}

	// 档案只有 K80：补 K80 离线卡；待配对设备不是档案设备 → 不补卡、不清卡
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Serials:    []string{"601c9f08"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 1750000010, Mode: ModeTcpip},
			},
			Profiles: DefaultProfile(),
		},
	})
	devs := a.appendProfileOfflineCards(nil)
	if len(devs) != 1 || devs[0].Serial != "192.168.31.197:5555" || devs[0].State != "device" {
		t.Fatalf("应只补 K80 在线合成卡（档案含 active）: %+v", devs)
	}
	p := a.Snapshot().Pending
	if len(p) != 1 || p[0].Addr != "192.168.31.99:33895" {
		t.Fatalf("待配对卡应原样保留（不受离线卡补齐影响）: %+v", p)
	}
}

// 顺序确定性：map 迭代无序，离线卡追加顺序必须按档案键排序（-count=10 无抖动）。
func TestGui29OfflineCardOrderDeterministic(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"Zebra": {
			Marketname: "Zebra", Serials: []string{"Z-1"}, Addrs: []AddrEntry{
				{Addr: "10.0.0.3:5555", State: AddrStateActive, LastOk: 3, Mode: ModeTcpip},
			}, Profiles: DefaultProfile(),
		},
		"Apple": {
			Marketname: "Apple", Serials: []string{"A-1"}, Addrs: []AddrEntry{
				{Addr: "10.0.0.1:5555", State: AddrStateActive, LastOk: 1, Mode: ModeTcpip},
			}, Profiles: DefaultProfile(),
		},
		"Mango": {
			Marketname: "Mango", Serials: []string{"M-1"}, Addrs: []AddrEntry{
				{Addr: "10.0.0.2:5555", State: AddrStateActive, LastOk: 2, Mode: ModeTcpip},
			}, Profiles: DefaultProfile(),
		},
	})
	want := []string{"Apple", "Mango", "Zebra"} // 档案键升序
	for i := 0; i < 20; i++ {
		devs := a.appendProfileOfflineCards(nil)
		if len(devs) != len(want) {
			t.Fatalf("第 %d 轮离线卡数错误: %+v", i, devs)
		}
		for j := range want {
			if devs[j].Name != want[j] {
				t.Fatalf("第 %d 轮顺序抖动: 第 %d 张=%q 期望=%q（全部 %+v）", i, j, devs[j].Name, want[j], devs)
			}
		}
	}
}

// pollOnce 全链路（Linux only，假 adb 脚本）：
// 在线（USB）→ 正常在线卡；拔线（adb devices 干净空列表）→ K80 离线卡仍在
// （市场名 + 最近地址），且不触发新设备弹窗；再插线 → 恢复在线卡。
func TestPollOnceGui29OfflineCardWhenDeviceGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n%s\\n' \"$FAKE_DEV\"; exit 0; fi\n" +
		"if [ \"$3\" = \"shell\" ]; then case \"$5\" in ro.product.marketname) echo 'REDMI K80';; ro.product.manufacturer) echo 'Xiaomi';; ro.product.model) echo '24117RK2CC';; esac; exit 0; fi\n" +
		"if [ \"$1\" = \"mdns\" ]; then printf 'List of discovered mdns services\\n'; exit 0; fi\n" +
		"if [ \"$1\" = \"connect\" ]; then exit 1; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": {
			Marketname: "REDMI K80",
			Serials:    []string{"601c9f08"},
			Addrs: []AddrEntry{
				{Addr: "192.168.31.197:5555", State: AddrStateActive, LastOk: 1750000000, Mode: ModeTcpip},
			},
			Profiles: DefaultProfile(),
		},
	})

	// 在线：正常在线卡（无离线补卡）
	os.Setenv("FAKE_DEV", "601c9f08\tdevice")
	defer os.Unsetenv("FAKE_DEV")
	a.pollOnce(context.Background())
	devs := a.Snapshot().Devices
	if len(devs) != 1 || devs[0].Serial != "601c9f08" || devs[0].State != "device" {
		t.Fatalf("在线轮应单在线卡: %+v", devs)
	}

	// 拔线：adb devices 干净消失 → 档案 active 驱动补在线合成卡；若插线遮罩
	// 仍置位（plugging）则显示为 USB 连接中卡（不凭空消失）；不弹窗。
	os.Setenv("FAKE_DEV", "")
	a.pollOnce(context.Background())
	devs = a.Snapshot().Devices
	if len(devs) != 1 {
		t.Fatalf("拔线后 K80 卡应仍在列表（不再凭空消失）: %+v", devs)
	}
	d := devs[0]
	// 当前实现路径：学习遮罩把合成在线卡转成 USB 连接中卡（USB serial + 副行 5555）
	if d.State != "device" || d.Name != "REDMI K80" || d.Serial != "601c9f08" ||
		d.ConnType != "usb" || d.Wireless != "192.168.31.197:5555" || !d.Connecting {
		t.Fatalf("拔线后应保留 K80 卡（当前为 USB 连接中遮罩）: %+v", d)
	}
	if a.Snapshot().NewDevice != nil {
		t.Fatalf("档案设备离线卡不得触发新设备弹窗: %+v", a.Snapshot().NewDevice)
	}

	// 再插 USB：恢复在线卡（离线卡随之消失，不重复）
	os.Setenv("FAKE_DEV", "601c9f08\tdevice")
	a.pollOnce(context.Background())
	devs = a.Snapshot().Devices
	if len(devs) != 1 || devs[0].State != "device" || devs[0].Serial != "601c9f08" {
		t.Fatalf("再插线应恢复单在线卡: %+v", devs)
	}
}
