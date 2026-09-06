package adb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseDevices(t *testing.T) {
	out := `List of devices attached
24117RK2CC	device
192.168.31.45:5555	device
24117RK2CC._adb-tls._tcp.local.	device
emulator-5554	offline
ABCDEF0123456789	unauthorized

`
	devs := ParseDevices(out)
	if len(devs) != 5 {
		t.Fatalf("解析数量错误: %d", len(devs))
	}
	want := map[string]struct {
		state string
		conn  string
	}{
		"24117RK2CC":                      {"device", "usb"},
		"192.168.31.45:5555":              {"device", "wifi"},
		"24117RK2CC._adb-tls._tcp.local.": {"device", "other"},
		"emulator-5554":                   {"offline", "other"},
		"ABCDEF0123456789":                {"unauthorized", "usb"},
	}
	for _, d := range devs {
		w, ok := want[d.Serial]
		if !ok {
			t.Fatalf("意外设备: %s", d.Serial)
		}
		if d.State != w.state || d.ConnType != w.conn {
			t.Errorf("%s: state=%s conn=%s, want %s %s", d.Serial, d.State, d.ConnType, w.state, w.conn)
		}
	}
}

// --- adb devices -l 解析与去重 ---

func TestParseDevicesL(t *testing.T) {
	out := `List of devices attached
a743e1df               device product:nabu model:Xiaomi_Pad_8_Pro device:nabu transport_id:3
192.168.31.162:5555    device product:nabu model:Xiaomi_Pad_8_Pro device:nabu transport_id:4
ZYX987                 device product:other model:Some_Other_Phone device:x transport_id:5
OLDUSB                 unauthorized
`
	devs := ParseDevicesL(out)
	if len(devs) != 4 {
		t.Fatalf("解析数量错误: %d", len(devs))
	}
	if devs[0].Serial != "a743e1df" || devs[0].ConnType != "usb" || devs[0].Model != "Xiaomi_Pad_8_Pro" {
		t.Fatalf("USB 条目解析错误: %+v", devs[0])
	}
	if devs[1].Serial != "192.168.31.162:5555" || devs[1].ConnType != "wifi" || devs[1].Model != "Xiaomi_Pad_8_Pro" {
		t.Fatalf("无线条目解析错误: %+v", devs[1])
	}
	if devs[3].Model != "" || devs[3].State != "unauthorized" {
		t.Fatalf("无 model 条目解析错误: %+v", devs[3])
	}
}

// 同 model 的多 transport 合并为一组；不同 model 分组；无 model 各自成组。
func TestGroupDevices(t *testing.T) {
	raw := []RawDevice{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Model: "Xiaomi_Pad_8_Pro"},
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Model: "Xiaomi_Pad_8_Pro"},
		{Serial: "ZYX987", State: "device", ConnType: "usb", Model: "Other_Phone"},
		{Serial: "OLDUSB", State: "device", ConnType: "usb"},
		{Serial: "10.0.0.9:5555", State: "device", ConnType: "wifi"},
	}
	g := GroupDevices(raw)
	if len(g) != 4 {
		t.Fatalf("应 4 组（同 model 合并）: %d", len(g))
	}
	if len(g[0]) != 2 {
		t.Fatalf("同 model 未合并: %+v", g[0])
	}
	if len(g[1]) != 1 || g[1][0].Serial != "ZYX987" {
		t.Fatalf("不同 model 不应合并: %+v", g[1])
	}
}

// 一台设备两个 transport：主 transport 取 USB，无线地址并入 Wireless。
// 用 offline 状态避免 enrich 执行真实 adb。
func TestBuildDeviceMergeTransports(t *testing.T) {
	m := New("", "")
	d := m.buildDevice(context.Background(), []RawDevice{
		{Serial: "192.168.31.162:5555", State: "offline", ConnType: "wifi", Model: "Xiaomi_Pad_8_Pro"},
		{Serial: "a743e1df", State: "offline", ConnType: "usb", Model: "Xiaomi_Pad_8_Pro"},
	})
	if d.Serial != "a743e1df" || d.ConnType != "usb" {
		t.Fatalf("应 USB 优先: %+v", d)
	}
	if d.Wireless != "192.168.31.162:5555" {
		t.Fatalf("无线地址未并入: %+v", d)
	}

	// 仅无线：单栏无线
	d = m.buildDevice(context.Background(), []RawDevice{
		{Serial: "192.168.31.162:5555", State: "offline", ConnType: "wifi", Model: "Xiaomi_Pad_8_Pro"},
	})
	if d.Serial != "192.168.31.162:5555" || d.ConnType != "wifi" || d.Wireless != "" {
		t.Fatalf("仅无线设备错误: %+v", d)
	}

	// USB 离线 + 无线在线：以在线无线为主
	d = m.buildDevice(context.Background(), []RawDevice{
		{Serial: "a743e1df", State: "offline", ConnType: "usb", Model: "Xiaomi_Pad_8_Pro"},
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Model: "Xiaomi_Pad_8_Pro"},
	})
	if d.Serial != "192.168.31.162:5555" || d.ConnType != "wifi" {
		t.Fatalf("USB 离线应回退无线: %+v", d)
	}

	// 双无线 transport（一个 offline 残留 + 一个 device 在线）：以在线无线为主
	d = m.buildDevice(context.Background(), []RawDevice{
		{Serial: "192.168.31.162:5555", State: "offline", ConnType: "wifi", Model: "Xiaomi_Pad_8_Pro"},
		{Serial: "192.168.31.183:5555", State: "device", ConnType: "wifi", Model: "Xiaomi_Pad_8_Pro"},
	})
	if d.Serial != "192.168.31.183:5555" || d.State != "device" || d.ConnType != "wifi" {
		t.Fatalf("双无线应选在线者: %+v", d)
	}
}

// 无 model 条目按市场名二次合并：USB 优先，无线地址并入。
func TestMergeDevice(t *testing.T) {
	a := Device{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Name: "Xiaomi Pad 8 Pro"}
	mergeDevice(&a, Device{Serial: "a743e1df", State: "device", ConnType: "usb", Name: "Xiaomi Pad 8 Pro"})
	if a.Serial != "a743e1df" || a.ConnType != "usb" {
		t.Fatalf("合并应 USB 优先: %+v", a)
	}
	if a.Wireless != "192.168.31.162:5555" {
		t.Fatalf("无线地址未并入: %+v", a)
	}
}

func TestParseBatteryLevel(t *testing.T) {
	out := `Current Battery Service state:
  AC powered: false
  USB powered: true
  status: 5
  health: 2
  level: 98
  scale: 100
`
	if n := ParseBatteryLevel(out); n != 98 {
		t.Fatalf("电量解析错误: %d", n)
	}
	if n := ParseBatteryLevel("level: notfound"); n != 0 {
		t.Fatalf("无 level 应返回 0, got %d", n)
	}
}

func TestParseDisplaySize(t *testing.T) {
	w, h, ok := ParseDisplaySize("Physical size: 2560x1708\nOverride size: 1600x1067")
	if !ok || w != 2560 || h != 1708 {
		t.Fatalf("Physical size 解析错误: %d %d %v", w, h, ok)
	}
	// Override 优先场景（部分老 ROM 只有 Override）
	w, h, ok = ParseDisplaySize("Override size: 1080x2340")
	if !ok || w != 1080 || h != 2340 {
		t.Fatalf("Override size 解析错误: %d %d %v", w, h, ok)
	}
	if _, _, ok := ParseDisplaySize(""); ok {
		t.Fatal("空输出应失败")
	}
}

func TestParsePeakRefresh(t *testing.T) {
	if n := ParsePeakRefresh("120"); n != 120 {
		t.Fatalf("120 -> %d", n)
	}
	if n := ParsePeakRefresh("120.0\r\n"); n != 120 {
		t.Fatalf("120.0 -> %d", n)
	}
	if n := ParsePeakRefresh("null"); n != 0 {
		t.Fatalf("null -> %d", n)
	}
	if n := ParsePeakRefresh(""); n != 0 {
		t.Fatalf("空 -> %d", n)
	}
}

// --- 无线自恢复 ---

func TestReadConfigAddr(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.txt")

	// bat 写入格式：单行 IP:port + CRLF
	if err := os.WriteFile(p, []byte("192.168.31.162:5555\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadConfigAddr(p); got != "192.168.31.162:5555" {
		t.Fatalf("地址解析错误: %q", got)
	}

	// 空文件 → ""
	if err := os.WriteFile(p, []byte("\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadConfigAddr(p); got != "" {
		t.Fatalf("空内容应返回空: %q", got)
	}

	// 非法内容（非 IP:port）→ ""
	if err := os.WriteFile(p, []byte("not-an-address"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadConfigAddr(p); got != "" {
		t.Fatalf("非法内容应返回空: %q", got)
	}

	// 文件缺失 → ""
	if got := ReadConfigAddr(filepath.Join(dir, "missing.txt")); got != "" {
		t.Fatalf("缺文件应返回空: %q", got)
	}
	// 空路径 → ""
	if got := ReadConfigAddr(""); got != "" {
		t.Fatalf("空路径应返回空: %q", got)
	}
}

// 0 台 + config 有记忆地址 → 触发 connect；30s 内节流不重试。
func TestRecoverIfEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(cfg, []byte("192.168.31.162:5555\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []string
	m := New("adb", cfg)
	m.connectFn = func(ctx context.Context, addr string) error {
		calls = append(calls, addr)
		return nil
	}

	if !m.RecoverIfEmpty(context.Background()) {
		t.Fatal("首次应发起 connect")
	}
	if len(calls) != 1 || calls[0] != "192.168.31.162:5555" {
		t.Fatalf("connect 未按记忆地址调用: %v", calls)
	}
	// 30s 节流：立即再调应被跳过
	if m.RecoverIfEmpty(context.Background()) {
		t.Fatal("30s 内不应重试 connect")
	}
	if len(calls) != 1 {
		t.Fatalf("节流期内 connect 被重复调用: %v", calls)
	}
	// 超过 30s 后恢复
	m.lastConnect = time.Now().Add(-31 * time.Second)
	if !m.RecoverIfEmpty(context.Background()) {
		t.Fatal("节流期过后应再次发起 connect")
	}
	if len(calls) != 2 {
		t.Fatalf("应再次 connect: %v", calls)
	}
}

// 无 config / 空地址：不发起 connect。
func TestRecoverNoConfig(t *testing.T) {
	m := New("adb", filepath.Join(t.TempDir(), "missing.txt"))
	m.connectFn = func(ctx context.Context, addr string) error {
		t.Fatal("无记忆地址不应 connect")
		return nil
	}
	if m.RecoverIfEmpty(context.Background()) {
		t.Fatal("无 config 不应发起 connect")
	}
}

// 无线投屏分辨率 = 当前 wm size 归一化宽≥高后，长边（宽）>1920 时等比缩到 1920
// （短边整数截断），输出始终大数字在前。不做方向检测（主人拍板）。
func TestScaleForWireless(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 竖屏定义 2136x3200 → 归一化 3200x2136 → 长边 1920、短边 2136*1920/3200=1281
		{"2136x3200", "1920x1281"},
		{"2560x1708", "1920x1281"}, // 横屏定义：1708*1920/2560=1281
		{"3200x1440", "1920x864"},  // 超宽屏：1440*1920/3200=864
		{"1920x1080", "1920x1080"}, // 长边 ≤ 1920 不缩放
		{"1280x800", "1280x800"},
		{"", ""},
		{"abc", ""},
		{"1920x", ""},
		{"0x0", ""},
		{"-1x100", ""},
	}
	for _, c := range cases {
		if got := ScaleForWireless(c.in); got != c.want {
			t.Errorf("ScaleForWireless(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 分辨率统一大数字在前（宽≥高），不做方向检测。
func TestSortWideFirst(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2136x3200", "3200x2136"},
		{"2560x1708", "2560x1708"},
		{"3200x1440", "3200x1440"},
		{"1440x3200", "3200x1440"},
		{"abc", "abc"},
		{"", ""},
		{"1920x", "1920x"},
		{"0x0", "0x0"},
	}
	for _, c := range cases {
		if got := SortWideFirst(c.in); got != c.want {
			t.Errorf("SortWideFirst(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- 无线调试配对（gui12） ---

// adb pair 输出分类：成功/码错/端口不通/地址非法（纯函数）。
func TestPairErrKind(t *testing.T) {
	cases := []struct {
		out  string
		want string
	}{
		{"Successfully paired to 192.168.31.99:37033 [guid=adb-a743e1df-Ab12Cd]", ""},
		{"Failed: Wrong password or connection was dropped.", "code"},
		{"Failed: Unable to start pairing client.", "port"},
		{"Failed to parse address for pairing: bad", "addr"},
		{"Failed: Successfully paired but server returned unknown response=x", "other"},
	}
	for _, c := range cases {
		if got := PairErrKind(c.out); got != c.want {
			t.Errorf("PairErrKind(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

// Manager.Pair 命令构造：adb pair ip:port code（fake adb 脚本记录参数）。
func TestManagerPairCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "calls.txt")
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\necho \"$@\" >> \"" + rec + "\"\n" +
		"echo 'Successfully paired to 192.168.31.99:37033 [guid=adb-a743e1df-Ab12Cd]'\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := New(fake, "")
	out, err := m.Pair(context.Background(), "192.168.31.99", "37033", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if PairErrKind(out) != "" {
		t.Fatalf("配对输出应判成功: %q", out)
	}
	b, _ := os.ReadFile(rec)
	if got := strings.TrimSpace(string(b)); got != "pair 192.168.31.99:37033 123456" {
		t.Fatalf("pair 命令行错误: %q", got)
	}
}

// Manager.Getprop 命令构造与返回清理（配对接入后市场名验证）。
func TestManagerGetprop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-only：依赖可执行的假 adb 脚本")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "calls.txt")
	fake := filepath.Join(dir, "adb")
	script := "#!/bin/sh\necho \"$@\" >> \"" + rec + "\"\n" +
		"if [ \"$3\" = \"shell\" ]; then echo 'Xiaomi Pad 8 Pro'; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := New(fake, "")
	got, err := m.Getprop(context.Background(), "192.168.31.99:33895", "ro.product.marketname")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Xiaomi Pad 8 Pro" {
		t.Fatalf("getprop 返回值错误: %q", got)
	}
	b, _ := os.ReadFile(rec)
	if want := "-s 192.168.31.99:33895 shell getprop ro.product.marketname"; strings.TrimSpace(string(b)) != want {
		t.Fatalf("getprop 命令行错误: %q", strings.TrimSpace(string(b)))
	}
}

// --- 设备档案 identity 规则 ---

// marketname 非空优先；无市场名 → manufacturer+model；都无 → 首个 serial。
// 同一设备 USB/无线 transport 由此归并（IP 变化不分裂设备的前提）。
func TestIdentityKey(t *testing.T) {
	cases := []struct {
		name string
		man  string
		mod  string
		ser  string
		want string
	}{
		{"Xiaomi Pad 8 Pro", "Xiaomi", "25091RP04C", "a743e1df", "Xiaomi Pad 8 Pro"},
		{"  ", "Xiaomi", "25091RP04C", "a743e1df", "Xiaomi 25091RP04C"},
		{"", "Xiaomi", "", "a743e1df", "a743e1df"},                 // 只有厂商无型号 → serial
		{"", "", "25091RP04C", "a743e1df", "a743e1df"},             // 只有型号无厂商 → serial
		{"", "", "", "a743e1df", "a743e1df"},                       // 都无 → serial
		{"", "", "", "192.168.31.162:5555", "192.168.31.162:5555"}, // 无线设备回退到 IP:port
		{" Mi 11 ", "", "", "abc", "Mi 11"},                        // marketname 去空白
	}
	for _, c := range cases {
		if got := IdentityKey(c.name, c.man, c.mod, c.ser); got != c.want {
			t.Errorf("IdentityKey(%q,%q,%q,%q) = %q, want %q", c.name, c.man, c.mod, c.ser, got, c.want)
		}
	}
}
