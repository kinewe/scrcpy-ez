package app

// gui31「插线不闪现无线」测试：USB 身份优先呈现（tcpip 学习窗口的显示次序）。
// 覆盖：USB 幽灵条目（model 未就绪）+ 同身份无线条目 → 按档案 identity 立即
// 归并为 USB 卡（USB 优先、Wireless 副行保留无线地址、富化字段从无线卡补缺）；
// 独立 USB 幽灵卡名称档案回补；纯 WiFi 不回归；无档案/异身份绝不跨设备归并；
// 无线卡离线不参与 USB 主 transport 合并；adbd 重启窗口全链路时序——
// 恢复轮即 USB 卡，无「仅无线卡」闪现。

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// gui31K80Profiles 种入 K80 档案（USB serial 601c9f08 + 无线 192.168.31.197:5555）。
func gui31K80Profiles(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
	})
}

// TestGui31FoldGhostUsbMergesUsbFirst：USB 幽灵条目（无 model、Name=裸 serial）
// + 同身份无线条目（已富化）→ 归并为单卡，USB 作主 transport（ConnType=usb、
// Serial=USB），无线地址并入 Wireless 副行，名称/型号/电量/规格/无线标注从
// 无线卡补缺——不出现「仅无线卡」。
func TestGui31FoldGhostUsbMergesUsbFirst(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "601c9f08"}, // model 未就绪
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Model: "24117RK2CC", Marketname: "REDMI K80", Identity: "REDMI K80",
			Battery: 94, Res: "2560x1600", FPS: 120, WirelessRes: "1920x1200",
			Tls: true, WirelessForm: ModeTcpip},
	})
	if len(devs) != 1 {
		t.Fatalf("USB 幽灵应与同身份无线卡归并为单卡: %+v", devs)
	}
	d := devs[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "device" {
		t.Fatalf("USB 应作主 transport（USB 优先）: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线地址应并入 Wireless 副行: %+v", d)
	}
	if d.Name != "REDMI K80" || d.Model != "24117RK2CC" || d.Marketname != "REDMI K80" || d.Identity != "REDMI K80" {
		t.Fatalf("名称/型号/身份应从无线卡补缺: %+v", d)
	}
	if d.Battery != 94 || d.Res != "2560x1600" || d.FPS != 120 || d.WirelessRes != "1920x1200" ||
		!d.Tls || d.WirelessForm != ModeTcpip {
		t.Fatalf("电量/规格/无线标注应从无线卡补缺（不等 USB getprop）: %+v", d)
	}
}

// TestGui31FoldGhostUsbWifiCardListedFirst：无线卡排在 USB 幽灵卡之前（adb
// 输出顺序不定）→ 仍归并为单 USB 卡（两遍式处理，顺序敏感不炸）。
func TestGui31FoldGhostUsbWifiCardListedFirst(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80",
			Marketname: "REDMI K80", Identity: "REDMI K80"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "601c9f08"},
	})
	if len(devs) != 1 || devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" ||
		devs[0].Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线卡在前也应归并为单 USB 卡: %+v", devs)
	}
}

// TestGui31FoldGhostUsbBackfillsProfileName：无同身份无线卡在场的独立 USB
// 幽灵卡（getprop 未就绪、Name=裸 serial）→ 原样保留为 USB 卡，名称由档案
// 市场名回补（K80 档案 serial 601c9f08 命中）。
func TestGui31FoldGhostUsbBackfillsProfileName(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "601c9f08"},
	})
	if len(devs) != 1 || devs[0].ConnType != "usb" || devs[0].Name != "REDMI K80" {
		t.Fatalf("独立 USB 幽灵卡应保留为 USB 卡且名称档案回补: %+v", devs)
	}
	if devs[0].Wireless != "" {
		t.Fatalf("无无线卡在场时 Wireless 应保持空: %+v", devs[0])
	}
}

// TestGui31FoldGhostUsbBackfillsNameWhenBothEnrichFail：USB 与无线两侧富化
// 都未就绪（Name 都是裸 serial）→ 归并后名称仍由档案市场名回补。
func TestGui31FoldGhostUsbBackfillsNameWhenBothEnrichFail(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "601c9f08"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "192.168.31.197:5555"},
	})
	if len(devs) != 1 {
		t.Fatalf("应归并为单卡: %+v", devs)
	}
	if devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" ||
		devs[0].Wireless != "192.168.31.197:5555" || devs[0].Name != "REDMI K80" {
		t.Fatalf("归并卡应 USB 主 + 无线副行 + 档案名回补: %+v", devs[0])
	}
}

// TestGui31FoldGhostUsbLeavesPureWifi：纯 WiFi 设备（无 USB 条目）→ 无线卡
// 原样保留（不回归）。
func TestGui31FoldGhostUsbLeavesPureWifi(t *testing.T) {
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

// TestGui31FoldGhostUsbNoCrossDeviceMerge：USB 幽灵卡与无线卡档案身份不同
// （K80 USB vs 平板无线）→ 绝不跨设备归并，两张卡原样保留。
func TestGui31FoldGhostUsbNoCrossDeviceMerge(t *testing.T) {
	a, _ := newWirelessApp()
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80":        mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"}, []string{"192.168.31.197:5555"}),
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C", []string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	})
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "601c9f08"},
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro"},
	})
	if len(devs) != 2 {
		t.Fatalf("异身份卡不得归并: %+v", devs)
	}
	if devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" || devs[0].Wireless != "" {
		t.Fatalf("K80 USB 幽灵卡应原样保留: %+v", devs[0])
	}
	if devs[1].ConnType != "wifi" {
		t.Fatalf("平板无线卡应原样保留: %+v", devs[1])
	}
}

// TestGui31FoldGhostUsbNoProfileNoMerge：USB 幽灵 serial 无档案身份（首见
// 设备）→ 不归并（无身份不归并原则），USB 卡不丢、无线卡原样。
func TestGui31FoldGhostUsbNoProfileNoMerge(t *testing.T) {
	a, _ := newWirelessApp()
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "abc123", State: "device", ConnType: "usb", Name: "abc123"},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80"},
	})
	if len(devs) != 2 {
		t.Fatalf("无档案身份不应归并: %+v", devs)
	}
	if devs[0].Serial != "abc123" || devs[0].Name != "abc123" {
		t.Fatalf("无档案 USB 幽灵卡应原样保留: %+v", devs[0])
	}
}

// TestGui31FoldGhostUsbSkipsOfflineWifi：同身份无线卡是 offline → 不做 USB
// 主 transport 归并（离线无线幽灵由 foldGhostWireless 归并入 Wireless 副行，
// 本函数只处理无线在线卡，避免把在线 USB 卡状态带坏）。
func TestGui31FoldGhostUsbSkipsOfflineWifi(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80"},
		{Serial: "192.168.31.197:5555", State: "offline", ConnType: "wifi"},
	})
	if len(devs) != 2 {
		t.Fatalf("无线离线卡不参与 USB 主归并: %+v", devs)
	}
	if devs[0].Serial != "601c9f08" || devs[0].State != "device" || devs[0].ConnType != "usb" {
		t.Fatalf("USB 在线卡应原样保留: %+v", devs[0])
	}
}

// TestGui31FoldGhostUsbUsbWithModelStillMerges：对称分裂方向——USB 卡 model
// 已就绪但 adb 层仍分卡（无线条目 model 缺失）→ 同样按身份归并为 USB 卡，
// USB 侧已富化字段保留（USB 优先覆盖）。
func TestGui31FoldGhostUsbUsbWithModelStillMerges(t *testing.T) {
	a, _ := newWirelessApp()
	gui31K80Profiles(a)
	devs := a.foldGhostUsb([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb", Name: "REDMI K80",
			Model: "24117RK2CC", Marketname: "REDMI K80", Identity: "REDMI K80",
			Battery: 94, Res: "2560x1600", FPS: 120},
		{Serial: "192.168.31.197:5555", State: "device", ConnType: "wifi", Name: "REDMI K80"},
	})
	if len(devs) != 1 {
		t.Fatalf("对称分裂方向同样应归并: %+v", devs)
	}
	d := devs[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("应 USB 主 + 无线副行: %+v", d)
	}
	if d.Name != "REDMI K80" || d.Battery != 94 || d.Res != "2560x1600" || d.FPS != 120 {
		t.Fatalf("USB 侧已富化字段应保留（USB 优先）: %+v", d)
	}
}

// TestPollOnceGui31UsbFirstNoWirelessFlash（Linux-only，假 adb 脚本全链路）：
// 时序模拟 adbd 重启窗口——① 插线前 USB 消失：离线卡（gui29）；② adbd 恢复：
// USB 条目（无 model）+ 5555 无线条目（有 model）同轮出现、USB getprop 未就绪
// → 恢复轮即单 USB 卡（无线地址并入副行），全程不出现「仅无线卡」；③ model
// 就绪轮：adb 层归并稳定单 USB 卡；④ 拔线：无线卡照常（gui30 学习生效）。
// 全程不执行 adb tcpip（端口已是 5555 / getprop 未就绪时跳过）。
func TestPollOnceGui31UsbFirstNoWirelessFlash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec.txt")
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$G31_REC\"\n" +
		"if [ \"$1\" = \"devices\" ]; then printf 'List of devices attached\\n%b\\n' \"$G31_DEV\"; exit 0; fi\n" +
		"if [ \"$1\" = \"mdns\" ]; then printf 'List of discovered mdns services\\n'; exit 0; fi\n" +
		"if [ \"$1\" = \"connect\" ]; then exit 1; fi\n" +
		"if [ \"$3\" = \"shell\" ]; then\n" +
		"  if [ \"$2\" = \"601c9f08\" ] && [ \"$G31_USB_READY\" != \"1\" ]; then exit 1; fi\n" +
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
	t.Setenv("G31_REC", rec)
	t.Setenv("G31_DEV", "")
	t.Setenv("G31_USB_READY", "0")

	a := New(Config{AdbPath: fake, ConfigPath: "", ProfilesPath: filepath.Join(dir, "profiles.json"), Version: "test"})
	gui31K80Profiles(a)
	gui49fix6FastStable(t)
	a.disc.ConnectFn = func(ctx context.Context, addr string) error { return nil }

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

	// ① 插线前（adbd 重启窗口，USB 消失）：档案含 active → online 合成卡
	a.pollOnce(context.Background())
	devs := a.Snapshot().Devices
	if len(devs) != 1 || devs[0].State != "device" || devs[0].Name != "REDMI K80" {
		t.Fatalf("插线前应为 K80 在线合成卡（档案 active）: %+v", devs)
	}

	// ② adbd 恢复：USB 回来（无 model、getprop 未就绪）+ 5555 已连（有 model）
	// → 恢复轮即单 USB 卡（无无线闪现）
	t.Setenv("G31_DEV", "601c9f08\tdevice\n192.168.31.197:5555\tdevice model:24117RK2CC")
	a.pollOnce(context.Background())
	devs = a.Snapshot().Devices
	noWifiOnly("恢复", devs)
	if len(devs) != 1 {
		t.Fatalf("恢复轮应单卡（无线卡被归并）: %+v", devs)
	}
	d := devs[0]
	if d.Serial != "601c9f08" || d.ConnType != "usb" || d.State != "device" {
		t.Fatalf("恢复轮应为 USB 主卡（USB 身份优先）: %+v", d)
	}
	if d.Wireless != "192.168.31.197:5555" {
		t.Fatalf("无线地址应并入 Wireless 副行: %+v", d)
	}
	// gui49-fix10：插线遮罩活跃期无条件盖遮罩卡（无规格/无电量，Connecting）。
	if !d.Connecting || d.Battery != 0 || d.Res != "" || d.FPS != 0 {
		t.Fatalf("恢复轮遮罩期不应露全规格真卡: %+v", d)
	}

	// ③ model 就绪轮：USB/无线条目都带 model → adb 层归并稳定单 USB 卡
	t.Setenv("G31_USB_READY", "1")
	t.Setenv("G31_DEV", "601c9f08\tdevice model:24117RK2CC\n192.168.31.197:5555\tdevice model:24117RK2CC")
	a.pollOnce(context.Background())
	devs = a.Snapshot().Devices
	noWifiOnly("稳定", devs)
	if len(devs) != 1 || devs[0].Serial != "601c9f08" || devs[0].ConnType != "usb" ||
		devs[0].Wireless != "192.168.31.197:5555" || devs[0].State != "device" {
		t.Fatalf("model 就绪轮应稳定单 USB 卡: %+v", devs)
	}
	gui49fix6WaitPlugClear(t, a) // USB 连续 device 满稳定窗口 → 插线遮罩清

	// ④ 拔线：仅无线 5555 → 无线卡照常（gui30 学习生效，不回归）
	t.Setenv("G31_DEV", "192.168.31.197:5555\tdevice model:24117RK2CC")
	a.pollOnce(context.Background())
	waitForMdns(t, "拔线遮罩 connect 成功后应恢复无线卡", func() bool {
		devs = a.Snapshot().Devices
		return len(devs) == 1 && devs[0].ConnType == "wifi" && devs[0].Serial == "192.168.31.197:5555" &&
			devs[0].State == "device" && !devs[0].Connecting
	})

	// 全程不应执行 adb tcpip（端口已 5555 或 getprop 未就绪时跳过）
	if n := tcpipCount(); n != 0 {
		t.Fatalf("全程不应执行 adb tcpip: %d 次", n)
	}
}
