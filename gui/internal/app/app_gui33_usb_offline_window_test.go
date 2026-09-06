package app

// gui33「插线零闪现兜底」测试：foldGhostUsb 放宽 offline + mergeUsbPrimary 状态取优。
// 覆盖：USB 卡 state=offline（握手未完成窗口）+ 无线卡 device → 第一轮即归并为
// USB 卡且 State 取优为 device（不闪「离线/无线」）；未授权 USB 不归并；正常
// device 路径与 gui31 一致（回归）；纯 WiFi 不变；USB offline + 无线 offline
// 不归并；异档案身份绝不跨设备归并；顺序无关；mergeUsbPrimary 状态取优直测。

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// TestGui33FoldGhostUsbMergesUsbOfflineWindow：USB offline（握手未完成）+ 同身份
// 无线 device（已富化）→ 归并为单卡：State 取优=device（设备确实在线）、
// ConnType=usb、Wireless 副行=5555、富化字段从无线卡补缺。
func TestGui33FoldGhostUsbMergesUsbOfflineWindow(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb", Name: "601c9f08"}, // USB 握手窗口
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Model: "24117RK2CC", Marketname: "REDMI K80", Identity: "REDMI K80",
			Battery: 94, Res: "2560x1600", FPS: 120, WirelessRes: "1920x1200",
			Tls: true, WirelessForm: ModeTcpip},
	})
	if len(devs) != 1 {
		t.Fatalf("USB offline 应与同身份无线卡归并为单卡（无无线闪现）: %+v", devs)
	}
	d := devs[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" {
		t.Fatalf("归并卡应 USB 主 transport: %+v", d)
	}
	if d.State != "device" {
		t.Fatalf("状态应取优为 device（无线侧在线，设备确实在线，不闪离线）: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线地址应并入 Wireless 副行: %+v", d)
	}
	if d.Name != "REDMI K80" || d.Model != "24117RK2CC" || d.Marketname != "REDMI K80" || d.Identity != "REDMI K80" {
		t.Fatalf("名称/型号/身份应从无线卡补缺: %+v", d)
	}
	if d.Battery != 94 || d.Res != "2560x1600" || d.FPS != 120 || d.WirelessRes != "1920x1200" ||
		!d.Tls || d.WirelessForm != ModeTcpip {
		t.Fatalf("电量/规格/无线标注应从无线卡补缺: %+v", d)
	}
}

// TestGui33FoldGhostUsbOfflineBackfillsProfileName：USB offline + 无线 device，
// 两侧 Name 都是裸 serial（富化均未就绪）→ 归并后名称由档案市场名回补。
func TestGui33FoldGhostUsbOfflineBackfillsProfileName(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb", Name: "601c9f08"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi",
			Name: "192.168.31.197:5555"},
	})
	if len(devs) != 1 {
		t.Fatalf("应归并为单卡: %+v", devs)
	}
	d := devs[0]
	if d.State != "device" || d.ConnType != "usb" || d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("归并卡应 device+usb+无线副行: %+v", d)
	}
	if d.Name != "REDMI K80" {
		t.Fatalf("两侧无名称时 Name 应由档案市场名回补: %+v", d)
	}
}

// TestGui33FoldGhostUsbUnauthorizedStaysSeparate：USB unauthorized + 无线 device
// → 不归并（未授权卡语义保持独立），无线卡原样。
func TestGui33FoldGhostUsbUnauthorizedStaysSeparate(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "unauthorized", ConnType: "usb", Name: "601c9f08"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80"},
	})
	if len(devs) != 2 {
		t.Fatalf("未授权 USB 不应与无线卡归并: %+v", devs)
	}
	if devs[0].Serial != "601c9f08" || devs[0].State != "unauthorized" || devs[0].ConnType != "usb" {
		t.Fatalf("未授权卡应独立保留: %+v", devs[0])
	}
	if devs[1].ConnType != "wifi" || devs[1].Serial != "192.168.31.197:5555" {
		t.Fatalf("无线卡应原样保留: %+v", devs[1])
	}
}

// TestGui33FoldGhostUsbDevicePathRegression：USB device（正常路径）→ 行为与
// gui31 一致（回归）：归并单卡、USB 主、State=device、无线副行、无线补缺。
func TestGui33FoldGhostUsbDevicePathRegression(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "601c9f08"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
	})
	if len(devs) != 1 {
		t.Fatalf("正常 device 路径应归并为单卡: %+v", devs)
	}
	d := devs[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "device" ||
		d.Wireless != "192.168.31.197:5555" || d.Name != "REDMI K80" {
		t.Fatalf("device 路径归并结果应与 gui31 一致: %+v", d)
	}
}

// TestGui33FoldGhostUsbOfflineUsbNoOnlineWifi：USB offline + 无线 offline →
// 不归并（无线侧必须 device 才参与主 transport 归并），USB 卡保留为离线卡
// （名称档案回补），无线离线卡不丢。
func TestGui33FoldGhostUsbOfflineUsbNoOnlineWifi(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb", Name: "601c9f08"},
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi"},
	})
	if len(devs) != 2 {
		t.Fatalf("无线离线卡不参与 USB 主归并: %+v", devs)
	}
	if devs[0].Serial != "601c9f08" || devs[0].State != "offline" || devs[0].ConnType != "usb" ||
		devs[0].Name != "REDMI K80" {
		t.Fatalf("USB 离线卡应保留（名称档案回补）: %+v", devs[0])
	}
	if devs[1].ConnType != "wifi" {
		t.Fatalf("无线离线卡应原样保留: %+v", devs[1])
	}
}

// TestGui33FoldGhostUsbPureWifiUnchanged：纯 WiFi 设备 → 无线卡原样保留（不回归）。
func TestGui33FoldGhostUsbPureWifiUnchanged(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80"},
	})
	if len(devs) != 1 || devs[0].ConnType != "wifi" || devs[0].Serial != "192.168.31.197:5555" ||
		devs[0].Wireless != "" {
		t.Fatalf("纯 WiFi 卡应原样保留: %+v", devs)
	}
}

// TestGui33FoldGhostUsbOfflineNoCrossDeviceMerge：USB offline（K80）与无线
// device（平板）档案身份不同 → 绝不跨设备归并，两张卡原样保留（放宽 offline
// 不破坏 gui25 防互抢边界）。
func TestGui33FoldGhostUsbOfflineNoCrossDeviceMerge(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80":        mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C", []string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	})
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "offline", ConnType: "usb", Name: "601c9f08"},
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro"},
	})
	if len(devs) != 2 {
		t.Fatalf("异身份卡不得归并: %+v", devs)
	}
	if devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" || devs[0].Wireless != "" {
		t.Fatalf("K80 USB 离线幽灵卡应原样保留: %+v", devs[0])
	}
	if devs[1].ConnType != "wifi" {
		t.Fatalf("平板无线卡应原样保留: %+v", devs[1])
	}
}

// TestGui33FoldGhostUsbOfflineWifiListedFirst：无线卡排在 USB offline 卡之前
// （adb 输出顺序不定）→ 仍归并为单 USB 卡且状态取优（顺序敏感不炸）。
func TestGui33FoldGhostUsbOfflineWifiListedFirst(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
		{Serial: "601c9f08", State: "offline", ConnType: "usb", Name: "601c9f08"},
	})
	if len(devs) != 1 || devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" ||
		devs[0].State != "device" || devs[0].Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线卡在前也应归并为单 USB 卡且状态取优: %+v", devs)
	}
}

// TestPollOnceGui33UsbOfflineWindowNoFlash（Linux-only，假 adb 脚本全链路）：
// 插线握手窗口全链路——① USB offline + 5555 无线 device 同轮出现 → 第一轮即
// 单 USB 卡（State 取优=device、无线地址并入副行），无「仅无线/离线」闪现；
// ② USB unauthorized → 未授权卡独立、无线卡原样；③ USB device（正常路径）
// → gui31 回归（单 USB 卡）。全程不执行 adb tcpip（USB getprop 未就绪/跳过）。
func TestPollOnceGui33UsbOfflineWindowNoFlash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec.txt")
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$G33_REC\"\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n%b\\n' \"$G33_DEV\"; exit 0; fi\n" +
		"if [ \"$1\" = \"mdns\" ]; then printf 'List of discovered mdns services\\n'; exit 0; fi\n" +
		"if [ \"$1\" = \"connect\" ]; then exit 1; fi\n" +
		"if [ \"$3\" = \"shell\" ]; then\n" +
		"  if [ \"$2\" = \"601c9f08\" ]; then exit 1; fi\n" +
		"  case \"$4\" in\n" +
		"    getprop)\n" +
		"      case \"$5\" in\n" +
		"        ro.product.marketname) echo 'REDMI K80';;\n" +
		"        ro.product.manufacturer) echo 'Xiaomi';;\n" +
		"        ro.product.model) echo '24117RK2CC';;\n" +
		"        service.adb.tcp.port) echo '5555';;\n" +
		"      esac\n" +
		"      ;;\n" +
		"    dumpsys) echo 'level: 94';;\n" +
		"    wm) echo 'Physical size: 2560x1600';;\n" +
		"    settings) echo '120';;\n" +
		"  esac\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("G33_REC", rec)
	t.Setenv("G33_DEV", "")

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	gui31K80Profiles(a)
	gui49fix6FastStable(t)

	noWifiOnly := func(round string, devs []adb.Device) {
		t.Helper()
		for i := range devs {
			if devs[i].State == "device" && devs[i].ConnType == "wifi" && devs[i].Wireless == "" {
				t.Fatalf("轮 %s 出现仅无线卡（无线闪现）: %+v", round, devs[i])
			}
		}
	}
	tcpipCount := func() int {
		b, err := os.ReadFile(rec)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "tcpip")
	}

	// ① 握手窗口：USB offline + 5555 无线 device 同轮出现 → 第一轮即单 USB 卡
	// （State 取优=device，无线地址并入副行）——不闪「离线/无线」。
	t.Setenv("G33_DEV", "601c9f08\toffline\n192.168.31.197:5555\tdevice model:24117RK2CC")
	a.pollOnce(context.Background())
	devs := a.Snapshot().Devices
	noWifiOnly("握手窗口", devs)
	if len(devs) != 1 {
		t.Fatalf("握手窗口轮应单卡（无线卡被归并）: %+v", devs)
	}
	d := devs[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "device" {
		t.Fatalf("握手窗口轮应为在线 USB 主卡（状态取优，不闪离线）: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线地址应并入 Wireless 副行: %+v", d)
	}
	if d.Name != "REDMI K80" {
		t.Fatalf("名称应从无线卡补缺（REDMI K80）: %+v", d)
	}
	gui49fix6WaitPlugClear(t, a) // 握手窗口稳定 device → 插线遮罩清

	// ② 未授权：USB unauthorized + 无线 device（gui49-fix4 形态锁定适配）——
	// USB 条目在列即钉有线主卡；State=unauthorized 如实呈现，无线地址并入副行；
	// 不合成连接中卡。
	t.Setenv("G33_DEV", "601c9f08\tunauthorized\n192.168.31.197:5555\tdevice model:24117RK2CC")
	a.pollOnce(context.Background())
	devs = a.Snapshot().Devices
	if len(devs) != 1 {
		t.Fatalf("未授权轮应单张有线主卡（无线并入副行）: %+v", devs)
	}
	d = devs[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "unauthorized" ||
		d.Wireless != "192.168.31.197:5555" || d.Connecting {
		t.Fatalf("未授权轮应钉有线形态并如实显示未授权: %+v", d)
	}

	// ③ USB device（正常路径）→ gui31 回归：单 USB 卡。
	t.Setenv("G33_DEV", "601c9f08\tdevice\n192.168.31.197:5555\tdevice model:24117RK2CC")
	a.pollOnce(context.Background())
	devs = a.Snapshot().Devices
	noWifiOnly("正常", devs)
	if len(devs) != 1 || devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" ||
		devs[0].State != "device" || devs[0].Wireless != "192.168.31.197:5555" {
		t.Fatalf("device 路径应单 USB 卡（gui31 回归）: %+v", devs)
	}

	// 全程不应执行 adb tcpip（USB getprop 未就绪时跳过/无线不在场）
	if n := tcpipCount(); n != 0 {
		t.Fatalf("全程不应执行 adb tcpip: %d 次", n)
	}
}

// TestGui33MergeUsbPrimaryStatePreference：mergeUsbPrimary 状态取优直测——
// USB 侧非 device 且无线侧 device → device；USB 侧 device → 保持 device
// （USB 优先）；两侧都非 device → 保持 USB 侧原状态。
// （unauthorized 由 foldGhostUsb 入口过滤，不会到达 mergeUsbPrimary——
// 见 TestGui33FoldGhostUsbUnauthorizedStaysSeparate。）
func TestGui33MergeUsbPrimaryStatePreference(t *testing.T) {
	cases := []struct {
		name     string
		usbState string
		wifi     string
		want     string
	}{
		{"usb offline 无线 device → device", "offline", "device", "device"},
		{"usb device 无线 offline → device", "device", "offline", "device"},
		{"usb device 无线 device → device", "device", "device", "device"},
		{"两侧 offline → offline（保持 USB 侧）", "offline", "offline", "offline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := adb.Device{Serial: "601c9f08", State: c.usbState, ConnType: "usb"}
			mergeUsbPrimary(&a, adb.Device{Serial: "192.168.31.197:5555", State: c.wifi, ConnType: "wifi"})
			if a.State != c.want || a.Serial != "601c9f08" || a.ConnType != "usb" {
				t.Fatalf("状态取优应为 %q（Serial/ConnType 保持 USB 侧）: %+v", c.want, a)
			}
		})
	}
}
