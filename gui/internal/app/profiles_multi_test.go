package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// 旧结构 {serial:{usb,wifi}} → Load 自动迁移为新结构并落盘（旧结构删除）。
// 无设备信息时 identity=旧 key（回退键）；serial 进 serials、IP:port 进 addrs(active)。
func TestProfileStoreLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	legacy := `{
  "a743e1df": {
    "usb": {"res": 2560, "fps": 75, "bitrate": 75, "custom": true, "baseline": {"res": 2560, "fps": 120, "bitrate": 80}},
    "wifi": {"res": 1920, "fps": 60, "bitrate": 15, "custom": false, "baseline": {"res": 1920, "fps": 60, "bitrate": 15}}
  },
  "192.168.31.162:5555": {
    "usb": {"res": 2560, "fps": 120, "bitrate": 60, "custom": false, "baseline": {"res": 2560, "fps": 120, "bitrate": 60}},
    "wifi": {"res": 1920, "fps": 75, "bitrate": 20, "custom": true, "baseline": {"res": 1920, "fps": 60, "bitrate": 15}}
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewProfileStore(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}

	// 迁移后档案可查：参数保留
	if got := s.Get("a743e1df"); !got.Usb.Custom || got.Usb.Res != 2560 || got.Usb.FPS != 75 {
		t.Fatalf("旧 usb 档未迁移: %+v", got)
	}
	if got := s.Get("192.168.31.162:5555"); !got.Wifi.Custom || got.Wifi.FPS != 75 {
		t.Fatalf("旧 wifi 档未迁移: %+v", got)
	}

	// 回退键结构：serial → serials；IP:port → addrs(active)
	e, ok := s.Entry("a743e1df")
	if !ok || len(e.Serials) != 1 || e.Serials[0] != "a743e1df" || len(e.Addrs) != 0 {
		t.Fatalf("serial 回退键结构错误: %+v", e)
	}
	e2, ok := s.Entry("192.168.31.162:5555")
	if !ok || len(e2.Addrs) != 1 || e2.Addrs[0].Addr != "192.168.31.162:5555" ||
		e2.Addrs[0].State != AddrStateActive {
		t.Fatalf("addr 回退键结构错误: %+v", e2)
	}

	// 旧结构已删除：文件为新结构（含 "devices" 键，无旧 serial 顶层键）
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["devices"]; !ok {
		t.Fatalf("落盘应为新结构（devices 键缺失）: %s", b)
	}
	if _, ok := raw["a743e1df"]; ok {
		t.Fatalf("旧结构顶层键未删除: %s", b)
	}
}

// identity 归并（迁移的核心）：旧回退键档案按 adb 轮询的 marketname 重键归并，
// serials 累积、addrs 追加、参数档按模式择优（custom 优先）。
// 覆盖验收场景：平板（USB a743e1df + 无线 192.168.31.162:5555）两键 → 一个 identity。
func TestSyncDevicesMergeByMarketname(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()

	p1 := DefaultProfile()
	p1.Usb = ModeProfile{Res: 2560, FPS: 75, Bitrate: 75, Custom: true}
	if err := s.Save("a743e1df", p1); err != nil {
		t.Fatal(err)
	}
	p2 := DefaultProfile()
	p2.Wifi = ModeProfile{Res: 1920, FPS: 75, Bitrate: 20, Custom: true}
	if err := s.Save("192.168.31.162:5555", p2); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Entries()); got != 2 {
		t.Fatalf("迁移前应 2 个回退键档案: %d", got)
	}

	// 第一次轮询：仅无线在线（marketname 已知）
	changed := s.SyncDevices([]adb.Device{
		{Serial: "192.168.31.162:5555", State: "device", ConnType: "wifi",
			Name: "Xiaomi Pad 8 Pro", Marketname: "Xiaomi Pad 8 Pro", Model: "25091RP04C"},
	})
	if !changed {
		t.Fatal("归并应有改动")
	}
	entries := s.Entries()
	if len(entries) != 2 {
		t.Fatalf("首轮后应仍 2 档案（另一串口键待 USB 出现）: %d", len(entries))
	}
	e, ok := entries["Xiaomi Pad 8 Pro"]
	if !ok {
		t.Fatalf("无线键应重键为 marketname: %v", entries)
	}
	if e.Marketname != "Xiaomi Pad 8 Pro" || len(e.Addrs) != 1 ||
		e.Addrs[0].Addr != "192.168.31.162:5555" || e.Addrs[0].State != AddrStateActive {
		t.Fatalf("重键档案结构错误: %+v", e)
	}
	if !e.Profiles.Wifi.Custom || e.Profiles.Wifi.FPS != 75 {
		t.Fatalf("重键后 wifi 自定义档丢失: %+v", e.Profiles)
	}

	// 第二次轮询：USB 在线（同 marketname）→ 归并进同一档案（不分裂）
	changed = s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb",
			Name: "Xiaomi Pad 8 Pro", Marketname: "Xiaomi Pad 8 Pro", Model: "25091RP04C",
			Wireless: "192.168.31.162:5555"},
	})
	if !changed {
		t.Fatal("USB 归并应有改动")
	}
	entries = s.Entries()
	if len(entries) != 1 {
		t.Fatalf("两键应归并为 1 个 identity: %v", entries)
	}
	e = entries["Xiaomi Pad 8 Pro"]
	if len(e.Serials) != 1 || e.Serials[0] != "a743e1df" {
		t.Fatalf("serials 未累积: %+v", e)
	}
	if !e.Profiles.Usb.Custom || e.Profiles.Usb.FPS != 75 {
		t.Fatalf("归并后 usb 自定义档丢失: %+v", e.Profiles)
	}
	if !e.Profiles.Wifi.Custom || e.Profiles.Wifi.FPS != 75 {
		t.Fatalf("归并后 wifi 自定义档丢失: %+v", e.Profiles)
	}
	// 两个 identity（手机/平板并存）：再同步一个不同市场名设备 → 2 个档案
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
		{Serial: "601c9f08", State: "device", ConnType: "usb", Marketname: "Redmi K80", Model: "25053RT47C"},
	})
	if got := len(s.Entries()); got != 2 {
		t.Fatalf("手机+平板并存应为 2 个 identity: %d", got)
	}
}

// IP 变化不分裂设备：无线 IP 变动 → 同一 marketname 档案记录新 addr（active），
// 旧 addr 按 gui41 单记忆直接删除（history 退役）；addrs 按最近成功排序。
func TestSyncDevicesIPChangeMerges(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	// IP 变了：同一 marketname、同一 serial 集（间隔 >1s 保证 lastOk 秒级区分）
	time.Sleep(1100 * time.Millisecond)
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.99:5555"},
	})
	entries := s.Entries()
	if len(entries) != 1 {
		t.Fatalf("IP 变化不应分裂设备: %v", entries)
	}
	e := entries["Xiaomi Pad 8 Pro"]
	if len(e.Addrs) != 1 {
		t.Fatalf("单记忆下应只保留新地址: %+v", e.Addrs)
	}
	if e.Addrs[0].Addr != "192.168.31.99:5555" || e.Addrs[0].State != AddrStateActive {
		t.Fatalf("addrs 应只有新 active 地址: %+v", e.Addrs)
	}
	if got := s.BestAddr("a743e1df"); got != "192.168.31.99:5555" {
		t.Fatalf("BestAddr 应为最近成功地址: %q", got)
	}
}

// gui52 二态状态管理：成功 → state=active（内存统计清零）；失败 → state=stale
// （内存态 fail++/lastFail 只做 60s 节流，不落盘）；再次成功翻回 active。
func TestAddrFailPromotesToHistory(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	s.AddrFail("a743e1df", "192.168.31.162:5555")
	s.AddrFail("a743e1df", "192.168.31.162:5555")
	e, _ := s.Entry("a743e1df")
	if e.Addrs[0].Fail != 2 || e.Addrs[0].State != AddrStateStale {
		t.Fatalf("失败必须写 state=stale（fail 只作内存节流统计）: %+v", e.Addrs[0])
	}
	s.AddrFail("a743e1df", "192.168.31.162:5555")
	e, _ = s.Entry("a743e1df")
	if e.Addrs[0].Fail != 3 || e.Addrs[0].State != AddrStateStale {
		t.Fatalf("gui52 失败只二态：应保持 stale: %+v", e.Addrs[0])
	}
	if len(e.Addrs) != 1 {
		t.Fatalf("失败不得删除记录: %+v", e.Addrs)
	}
	// 内存态失败节流：刚失败（lastFail=now）→ 60s 内不再作候选；
	// 节流超时后 stale 条目恢复为离线候选（永不拉黑）。
	if got := s.BestAddr("a743e1df"); got != "" {
		t.Fatalf("刚失败（60s 节流内）的地址不应再作首选回退: %q", got)
	}

	// 再次成功 → state=active + 内存统计清零
	s.AddrSuccess("a743e1df", "192.168.31.162:5555")
	e, _ = s.Entry("a743e1df")
	if e.Addrs[0].Fail != 0 || e.Addrs[0].State != AddrStateActive || e.Addrs[0].LastOk == 0 {
		t.Fatalf("成功后应 state=active+fail=0: %+v", e.Addrs[0])
	}
}

// OfflineCandidates：gui52 二态在线判据——serial/addr 在设备流在线 → 无候选；
// 档案任一 state=active 地址 = 在线证据 → 无离线候选；全部 stale → 离线候选。
func TestOfflineCandidates(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})
	s.AddrSuccess("Xiaomi Pad 8 Pro", "192.168.31.99:5555")

	// 在线（USB）→ 无候选
	if got := s.OfflineCandidates([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb"},
	}); len(got) != 0 {
		t.Fatalf("设备在线不应有候选: %v", got)
	}
	// 无线在线 → 无候选
	if got := s.OfflineCandidates([]adb.Device{
		{Serial: "192.168.31.99:5555", State: "device", ConnType: "wifi"},
	}); len(got) != 0 {
		t.Fatalf("无线在线不应有候选: %v", got)
	}
	// 档案仍有 active 地址（99）→ 即使 adb 设备流为空，也是在线证据、无候选
	// （降级由 90s mDNS 静默问询/探测失败把 state 翻成 stale 后再进入候选）。
	if got := s.OfflineCandidates(nil); len(got) != 0 {
		t.Fatalf("active 地址=在线证据，不应有离线候选: %v", got)
	}

	// 全 stale → 离线候选（单记忆只保留最新 99；162 已在成功时被替换删除）
	if !s.MarkAddrStale("Xiaomi Pad 8 Pro", "192.168.31.99:5555") {
		t.Fatal("MarkAddrStale 应有改动")
	}
	got := s.OfflineCandidates(nil)
	if len(got) != 1 {
		t.Fatalf("全 stale 时离线应有候选: %v", got)
	}
	list := got["Xiaomi Pad 8 Pro"]
	if len(list) != 1 || list[0] != "192.168.31.99:5555" {
		t.Fatalf("候选应只含最新 99（162 已出局）: %v", list)
	}
}

// mDNS 匹配：服务名命中 serial → 新 IP 归并入档案（IP 变化归并）；
// 已存在地址 → 候选；无法匹配 → 忽略。
func TestMatchMdns(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	addrs := s.MatchMdns([]MdnsMatch{
		{Name: "a743e1df", Addr: "192.168.31.77:5555"}, // 新 IP：归并并删除旧同形态
		{Name: "unknown", Addr: "10.0.0.9:5555"},       // 无法匹配：忽略
		{Name: "x", Addr: "192.168.31.162:5555"},       // 旧地址已被删除 → 忽略
	})
	if len(addrs) != 1 || addrs[0] != "192.168.31.77:5555" {
		t.Fatalf("mDNS 候选应只有新 IP（旧同形态已删除）: %v", addrs)
	}
	e, _ := s.Entry("a743e1df")
	found := false
	for _, a := range e.Addrs {
		if a.Addr == "192.168.31.77:5555" && a.State == AddrStateActive {
			found = true
		}
	}
	if !found {
		t.Fatalf("mDNS 新 IP 未归并入档案: %+v", e.Addrs)
	}
}

// mDNS 匹配（adb- 前缀）：adb mdns services 实例名形如 "adb-<serial>"，
// 剥离前缀后命中 serial → 新 IP 归并入档案（换 WiFi 换 IP 场景）。
func TestMatchMdnsAdbPrefixMergesNewIP(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	addrs := s.MatchMdns([]MdnsMatch{
		{Name: "adb-a743e1df", Addr: "192.168.31.183:5555"}, // 新 IP（带 adb- 前缀）
	})
	if len(addrs) != 1 || addrs[0] != "192.168.31.183:5555" {
		t.Fatalf("带 adb- 前缀的新 IP 应成为候选: %v", addrs)
	}
	e, _ := s.Entry("a743e1df")
	found := false
	for _, a := range e.Addrs {
		if a.Addr == "192.168.31.183:5555" && a.State == AddrStateActive {
			found = true
		}
	}
	if !found {
		t.Fatalf("带 adb- 前缀的新 IP 未归并入档案: %+v", e.Addrs)
	}
}

// mDNS 匹配回归：现有地址已在档案（无论服务名）→ 仍匹配为候选。
func TestMatchMdnsExistingAddrRegression(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	addrs := s.MatchMdns([]MdnsMatch{
		{Name: "x", Addr: "192.168.31.162:5555"}, // 已存在地址：候选
	})
	if len(addrs) != 1 || addrs[0] != "192.168.31.162:5555" {
		t.Fatalf("已存在地址应匹配为候选: %v", addrs)
	}
}

// mDNS 匹配防误匹配：非 adb- 前缀、非 serial 的名字（如 "local"）不匹配档案。
func TestMatchMdnsIgnoresUnrelatedName(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Wireless: "192.168.31.162:5555"},
	})

	addrs := s.MatchMdns([]MdnsMatch{
		{Name: "local", Addr: "192.168.31.183:5555"}, // 无关名字：忽略
	})
	if len(addrs) != 0 {
		t.Fatalf("无关名字不应匹配: %v", addrs)
	}
	e, _ := s.Entry("a743e1df")
	for _, a := range e.Addrs {
		if a.Addr == "192.168.31.183:5555" {
			t.Fatalf("无关名字的地址不应归并入档案: %+v", e.Addrs)
		}
	}
}

// 档案持久化：gui52 落盘 JSON 干净二态——
// addr 条目只有 addr/mode/state（active/stale），绝不出现
// fail/lastOk/lastFail/stale 布尔/history。
func TestProfileStorePersistedShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	s := NewProfileStore(path)
	_ = s.Load()
	s.SyncDevices([]adb.Device{
		{Serial: "a743e1df", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro",
			Model: "25091RP04C", Wireless: "192.168.31.162:5555"},
	})
	s.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.163:5555")

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b)
	if !strings.Contains(raw, `"devices"`) ||
		!strings.Contains(raw, `"marketname": "Xiaomi Pad 8 Pro"`) ||
		!strings.Contains(raw, `"model": "25091RP04C"`) ||
		!strings.Contains(raw, `"a743e1df"`) ||
		!strings.Contains(raw, `"state": "active"`) ||
		!strings.Contains(raw, `"state": "stale"`) ||
		!strings.Contains(raw, `"usb"`) || !strings.Contains(raw, `"wifi"`) {
		t.Fatalf("落盘结构不符合新 profiles.json 定义:\n%s", b)
	}
	for _, legacy := range []string{`"fail":`, `"lastOk":`, `"lastFail":`, `"stale":`, `"history":`} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("gui52 落盘不得再出现旧字段 %s:\n%s", legacy, b)
		}
	}
}

// 回归（serials 被清空根因）：无线设备换了新 IP（尚未入档）在线时，
// resolveLocked 按新 IP 与 serials/addrs 匹配都失败，但 identity（市场名）键已存在
// （历史 USB 建档）——SyncDevices 的 else 分支必须复用旧条目继续累积，
// 而不是用空壳覆盖（旧档案 serials/参数清空 → mDNS 匹配永久失配）。
func TestSyncDevicesKeepsExistingEntryOnUnknownWifiSerial(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()

	// 预置档案：历史 USB 建档（serials 已累积、参数已自定义、旧无线地址已 history）
	prof := DefaultProfile()
	prof.Wifi = ModeProfile{Res: 1920, FPS: 75, Bitrate: 20, Custom: true}
	s.mu.Lock()
	s.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Model:      "25091RP04C",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{{
			Addr: "192.168.31.162:5555", State: AddrStateHistory, Fail: 3, LastOk: 1750000000,
		}},
		Profiles: prof,
	}
	s.mu.Unlock()

	// 无线设备换 IP 上线（新 IP 不在档案）：按新 IP 解析失败 → else 分支
	changed := s.SyncDevices([]adb.Device{
		{Serial: "192.168.31.183:5555", State: "device", ConnType: "wifi",
			Name: "Xiaomi Pad 8 Pro", Marketname: "Xiaomi Pad 8 Pro", Model: "25091RP04C"},
	})
	if !changed {
		t.Fatal("新地址入档应有改动")
	}

	entries := s.Entries()
	if len(entries) != 1 {
		t.Fatalf("不应分裂/新增档案: %v", entries)
	}
	e, ok := entries["Xiaomi Pad 8 Pro"]
	if !ok {
		t.Fatalf("档案键应保持 marketname identity: %v", entries)
	}
	if e.Marketname != "Xiaomi Pad 8 Pro" {
		t.Fatalf("marketname 丢失: %+v", e)
	}
	if len(e.Serials) != 1 || e.Serials[0] != "a743e1df" {
		t.Fatalf("serials 被清空/篡改（覆盖 bug 回归）: %+v", e.Serials)
	}
	if !e.Profiles.Wifi.Custom || e.Profiles.Wifi.FPS != 75 {
		t.Fatalf("参数记忆被覆盖丢失: %+v", e.Profiles)
	}
	// 新 IP 已追加 active；旧同形态地址按单记忆删除
	var gotNew bool
	for _, a := range e.Addrs {
		if a.Addr == "192.168.31.183:5555" {
			gotNew = true
			if a.State != AddrStateActive || a.Fail != 0 {
				t.Fatalf("新 IP 应为 active+fail=0: %+v", a)
			}
		}
	}
	if !gotNew {
		t.Fatalf("新 IP 未入档: %+v", e.Addrs)
	}
	if gui24FindAddr(e, "192.168.31.162:5555") != nil {
		t.Fatalf("旧地址应按单记忆删除: %+v", e.Addrs)
	}
}

// 新 identity 仍正常新建：resolveLocked 失败且 identity 键不存在 → 新建条目
// （复用修复不得破坏新设备建档路径）。
func TestSyncDevicesNewIdentityKeepsCreating(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	_ = s.Load()

	changed := s.SyncDevices([]adb.Device{
		{Serial: "601c9f08", State: "device", ConnType: "usb",
			Name: "Redmi K80", Marketname: "Redmi K80", Model: "25053RT47C"},
	})
	if !changed {
		t.Fatal("新设备建档应有改动")
	}
	entries := s.Entries()
	e, ok := entries["Redmi K80"]
	if !ok {
		t.Fatalf("新 identity 应建档: %v", entries)
	}
	if len(e.Serials) != 1 || e.Serials[0] != "601c9f08" {
		t.Fatalf("USB serial 未累积: %+v", e.Serials)
	}
	if e.Marketname != "Redmi K80" || e.Model != "25053RT47C" {
		t.Fatalf("marketname/model 未回填: %+v", e)
	}
}
