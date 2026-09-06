package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui52fix3：建档后事件驱动合卡（运行期孤儿清理，非 Load） ---

// TestGui52Fix3RuntimeCleanMergesOrphan：运行期直接调用事件入口，
// IP:port 孤儿并入同 IP 主档案、孤儿键与 deviceOrder 清理、日志留痕、幂等。
func TestGui52Fix3RuntimeCleanMergesOrphan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	logPath := mdns8StartLogCapture(t)

	s := NewProfileStore(path)
	s.mu.Lock()
	s.data.Devices["192.168.31.162:42595"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.168.31.162:42595", State: AddrStateActive, Mode: ModeTls}},
		Profiles: DefaultProfile(),
	}
	s.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	}
	s.data.DeviceOrder = []string{"192.168.31.162:42595", "Xiaomi Pad 8 Pro"}
	s.mu.Unlock()

	if !s.CleanOrphanIPPort() {
		t.Fatal("同 IP 存在主档案时事件入口应合并孤儿")
	}
	entries := s.Entries()
	if _, ok := entries["192.168.31.162:42595"]; ok {
		t.Fatalf("事件驱动合卡后孤儿键应删除: %v", entries)
	}
	main, ok := entries["Xiaomi Pad 8 Pro"]
	if !ok {
		t.Fatalf("主档案应保留: %v", entries)
	}
	// gui52-fix12：孤儿零合入——42595 不得进入主档案（IP 由 mdns 覆盖，不再搬运）。
	if !gui50Fix45EntryHasAddr(main, "192.168.31.162:5555", ModeTcpip) {
		t.Fatalf("主档案权威地址应保留: %+v", main.Addrs)
	}
	for i := range main.Addrs {
		if main.Addrs[i].Addr == "192.168.31.162:42595" {
			t.Fatalf("孤儿地址零合入: %+v", main.Addrs)
		}
	}
	if got := s.DeviceOrder(); len(got) != 1 || got[0] != "Xiaomi Pad 8 Pro" {
		t.Fatalf("deviceOrder 应移除孤儿键: %v", got)
	}
	mdns8LogContains(t, logPath, "孤儿档案清除（零合入）：192.168.31.162:42595 → Xiaomi Pad 8 Pro（事件驱动）")

	// 幂等：再次调用无改动、文件字节不变
	b1, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.CleanOrphanIPPort() {
		t.Fatal("无孤儿时事件入口应返回 false（零写盘）")
	}
	b2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("幂等调用不得改写档案:\n---1---\n%s\n---2---\n%s", b1, b2)
	}
}

// TestGui52Fix3RuntimeCleanKeepsOrphanWithoutMain：不同 IP 无主档案 → 保留。
func TestGui52Fix3RuntimeCleanKeepsOrphanWithoutMain(t *testing.T) {
	s := NewProfileStore("")
	s.mu.Lock()
	s.data.Devices["192.168.31.184:38167"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.168.31.184:38167", State: AddrStateActive, Mode: ModeTls}},
		Profiles: DefaultProfile(),
	}
	s.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	}
	s.mu.Unlock()

	if s.CleanOrphanIPPort() {
		t.Fatal("无同 IP 主档案时不应有改动")
	}
	if len(s.Entries()) != 2 {
		t.Fatalf("不同 IP 孤儿应保留: %v", s.Entries())
	}
}

// TestGui52Fix3PairArchiveTriggersMerge：PairArchive 建档后立即合卡——
// identity/serial 全空的过渡 IP:port 档不残留，同 IP 主档案吸收。
func TestGui52Fix3PairArchiveTriggersMerge(t *testing.T) {
	dir := t.TempDir()
	s := NewProfileStore(filepath.Join(dir, "profiles.json"))
	s.mu.Lock()
	s.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
			{Addr: "192.168.31.162:44125", State: AddrStateActive, Mode: ModeTls},
		},
		Profiles: DefaultProfile(),
	}
	s.mu.Unlock()

	// 模拟 fix1 之前的坏路径：identity/serial 都空 → PairArchive 建 IP:port 过渡档
	s.PairArchive("", "", "192.168.31.162:42595", "", "", "")

	entries := s.Entries()
	if _, ok := entries["192.168.31.162:42595"]; ok {
		t.Fatalf("PairArchive 后应立即合卡，不得残留过渡键: %v", entries)
	}
	if len(entries) != 1 {
		t.Fatalf("应只剩主档案: %v", entries)
	}
	main, ok := entries["Xiaomi Pad 8 Pro"]
	if !ok {
		t.Fatalf("主档案应保留: %v", entries)
	}
	// gui52-fix11：孤儿合并不得让过渡地址赢过主档案同形态 active——
	// 主档案已有 mdns/配对权威 TLS active 时，过渡 42595 降级淘汰（不抢 active）。
	if !gui50Fix45EntryHasAddr(main, "192.168.31.162:44125", ModeTls) {
		t.Fatalf("主档案同形态 active 应保留: %+v", main.Addrs)
	}
	for i := range main.Addrs {
		if main.Addrs[i].Addr == "192.168.31.162:42595" && main.Addrs[i].State == AddrStateActive {
			t.Fatalf("过渡地址不得为主 active: %+v", main.Addrs)
		}
	}
	raw := readFileString(t, filepath.Join(dir, "profiles.json"))
	if strings.Contains(raw, `"192.168.31.162:42595":`) {
		t.Fatalf("落盘不得残留 IP:port 孤儿键:\n%s", raw)
	}
}

// TestGui52Fix3TrackUpdateTriggerMerges：设备流新建 IP:port 档案后立即合卡，
// 显示层单卡（fix2 身份回退 + fix3 事件合并完整闭环）。
func TestGui52Fix3TrackUpdateTriggerMerges(t *testing.T) {
	a, _ := newWirelessApp()
	fix2SeedPad(a) // Pad 主档案：同 IP 5555 active + TLS 46051 active

	// 无市场名/身份：SyncDevices 会先建 IP:port 过渡档，fix3 应立即合并。
	d := adb.Device{Serial: "192.168.31.162:42595", State: "device", ConnType: "wifi"}
	a.applyTrackUpdate([]adb.Device{d})

	entries := a.profiles.Entries()
	if _, ok := entries["192.168.31.162:42595"]; ok {
		t.Fatalf("设备流建档后过渡 IP:port 键应被事件合卡清理: %v", entries)
	}
	if len(entries) != 1 {
		t.Fatalf("应只剩 Pad 主档案: %v", entries)
	}
	if np := a.Snapshot().NewDevice; np != nil {
		t.Fatalf("同 IP 已知档案不得弹新设备窗: %+v", np)
	}
	devs := a.Snapshot().Devices
	if len(devs) != 1 {
		t.Fatalf("显示层应单卡: %+v", devs)
	}
}

// TestGui52Fix3UnifyDisplayDefense：显示提交前事件合卡——过渡档在 unify 前被吸收，
// 不为此档案单独出卡。
func TestGui52Fix3UnifyDisplayDefense(t *testing.T) {
	a, _ := newWirelessApp()
	a.profiles.mu.Lock()
	a.profiles.data.Devices["192.168.31.162:42595"] = &DeviceEntry{
		Addrs:    []AddrEntry{{Addr: "192.168.31.162:42595", State: AddrStateActive, Mode: ModeTls}},
		Profiles: DefaultProfile(),
	}
	a.profiles.data.Devices["Xiaomi Pad 8 Pro"] = &DeviceEntry{
		Marketname: "Xiaomi Pad 8 Pro",
		Serials:    []string{"a743e1df"},
		TlsGuid:    "adb-a743e1df-KWqpio",
		Addrs: []AddrEntry{
			{Addr: "192.168.31.162:5555", State: AddrStateActive, Mode: ModeTcpip},
		},
		Profiles: DefaultProfile(),
	}
	a.profiles.mu.Unlock()

	out := a.unifyProfileCards([]adb.Device{
		{Serial: "192.168.31.162:42595", State: "device", ConnType: "wifi"},
	})
	if len(out) != 1 {
		t.Fatalf("过渡档不得独立成卡: %+v", out)
	}
	if _, ok := a.profiles.Entries()["192.168.31.162:42595"]; ok {
		t.Fatal("unify 前应完成事件合卡（孤儿键删除）")
	}
	// gui52-fix12：孤儿零合入——过渡地址不再进入主档案，显示用主档案 active（5555）。
	if out[0].Serial != "192.168.31.162:5555" || out[0].Tls {
		t.Fatalf("孤儿零合入后应显示主档案 active 地址: %+v", out[0])
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
