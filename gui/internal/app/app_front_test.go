package app

import (
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- 标签点击 → 投屏窗口浮前：候选 serial 计算（纯函数） ---

func idOfSelf(d *adb.Device) string { return d.Identity }

// entryOfMap 测试用档案查询（对齐 ProfileStore.resolveLocked 语义）：
// identity 键直查 → serials 集合 → addrs 集合。
func entryOfMap(m map[string]*DeviceEntry) func(string) (DeviceEntry, bool) {
	return func(key string) (DeviceEntry, bool) {
		if e, ok := m[key]; ok {
			return *e, true
		}
		for _, e := range m {
			if contains(e.Serials, key) {
				return *e, true
			}
			for i := range e.Addrs {
				if e.Addrs[i].Addr == key {
					return *e, true
				}
			}
		}
		return DeviceEntry{}, false
	}
}

func noEntry(string) (DeviceEntry, bool) { return DeviceEntry{}, false }

// 会话键串命中卡片 → 卡片 Serial/Wireless 入候选；同 identity 在线卡并集入候选
// （USB/无线切换卡片重键后旧会话键仍能命中 scrcpy 进程）。
func TestFrontCandidateSerials(t *testing.T) {
	// 合并卡：USB 主 transport + 无线条目
	devs := []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Wireless: "192.168.31.162:5555", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Identity: "Redmi K80"},
	}
	got := frontCandidateSerials("a743e1df", devs, idOfSelf, noEntry)
	want := []string{"a743e1df", "192.168.31.162:5555"}
	if len(got) != len(want) {
		t.Fatalf("候选错误: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("候选第 %d 个应为 %q: %+v", i, want[i], got)
		}
	}

	// 卡片重键：会话键是旧无线地址 → 经 Wireless 反查卡片
	got2 := frontCandidateSerials("192.168.31.162:5555", devs, idOfSelf, noEntry)
	want2 := []string{"192.168.31.162:5555", "a743e1df"}
	if len(got2) != len(want2) {
		t.Fatalf("重键候选错误: %+v", got2)
	}
	for i := range want2 {
		if got2[i] != want2[i] {
			t.Fatalf("重键候选第 %d 个应为 %q: %+v", i, want2[i], got2)
		}
	}

	// 同 identity 双卡（USB 卡 + 独立无线卡）：两卡 serial 都入候选
	devs2 := []adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Identity: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Identity: "Redmi K80"},
	}
	got3 := frontCandidateSerials("a743e1df", devs2, idOfSelf, noEntry)
	want3 := []string{"a743e1df", "192.168.31.162:5555"}
	if len(got3) != len(want3) || got3[0] != want3[0] || got3[1] != want3[1] {
		t.Fatalf("双卡候选错误: %+v", got3)
	}

	// 无匹配卡片：只有会话键自身
	got4 := frontCandidateSerials("nope", devs, idOfSelf, noEntry)
	if len(got4) != 1 || got4[0] != "nope" {
		t.Fatalf("无匹配应仅含会话键: %+v", got4)
	}

	// 空串号：空候选（调用方防御）
	if got := frontCandidateSerials("", devs, idOfSelf, noEntry); len(got) != 0 {
		t.Fatalf("空串号应空候选: %+v", got)
	}
}

// v3 修复（实况）：平板会话键=a743e1df（USB serial），但 scrcpy 实际命令行
// `--serial 192.168.31.162:5555`（无线）——候选必须从档案补上全部 serials+addrs
// 才能命中。档案是权威：a743e1df 的档案 addrs 有 162:5555 → 候选含 162:5555
// → 与 scrcpy 命令行匹配。
func TestFrontCandidateSerialsProfileAddrs(t *testing.T) {
	profiles := map[string]*DeviceEntry{
		"Xiaomi Pad 8 Pro": mkEntry("Xiaomi Pad 8 Pro", "25091RP04C",
			[]string{"a743e1df"}, []string{"192.168.31.162:5555"}),
	}
	entryOf := entryOfMap(profiles)
	// 设备列表里只有无线卡（USB 卡本轮未出现：会话键查不到卡片）
	devs := []adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi", Identity: "Xiaomi Pad 8 Pro"},
	}
	got := frontCandidateSerials("a743e1df", devs, idOfSelf, entryOf)
	// 档案补全：会话键 + 档案 serials + 档案 addrs
	if len(got) != 2 || got[0] != "a743e1df" || got[1] != "192.168.31.162:5555" {
		t.Fatalf("候选应含档案 addrs: %+v", got)
	}
	// 组合验证（与 bridge 匹配逻辑同判据）：scrcpy 实际命令行命中候选
	cmdline := `"C:\x\scrcpy.exe" --serial 192.168.31.162:5555 --max-size 1920`
	hit := false
	for _, c := range got {
		tok := "--serial " + c
		if strings.HasSuffix(cmdline, tok) || strings.Contains(cmdline, tok+" ") {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatalf("平板场景应命中 scrcpy 命令行 %q（候选 %v）", cmdline, got)
	}
	// 会话键直接按档案 identity 解析（键=无线地址时同样补全）
	got2 := frontCandidateSerials("192.168.31.162:5555", devs, idOfSelf, entryOf)
	if len(got2) != 2 || got2[0] != "192.168.31.162:5555" || got2[1] != "a743e1df" {
		t.Fatalf("无线会话键也应补全档案 serials: %+v", got2)
	}
}

// App.BringCastToFront：非 Windows 空实现安全返回（无 panic、无错误）；
// 会话存在与否都幂等。
func TestBringCastToFrontSafeOnNonWindows(t *testing.T) {
	a, _ := multiTestApp()
	setDevices(a, []adb.Device{{Serial: "A", State: "device", ConnType: "usb", Identity: "devA"}})
	if err := a.BringCastToFront("A"); err != nil {
		t.Fatalf("非 Windows 空实现应返回 nil: %v", err)
	}
	if err := a.BringCastToFront("NOPE"); err != nil {
		t.Fatalf("无会话也应安全返回: %v", err)
	}
	if err := a.BringCastToFront(""); err != nil {
		t.Fatalf("空串号也应安全返回: %v", err)
	}
}
