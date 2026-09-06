package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui19 起：mDNS 广播语义（gui19 广播权威 → gui23 广播优先 + 档案兜底；无广播 → 档案经验兜底） ---

// waitDiscStatus 等待探测状态到达指定值（快照轮询；超时失败）。
func waitDiscStatus(t *testing.T, a *App, want string) DiscoveryStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := a.Snapshot().Discovery
		if st.Status == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待探测状态 %s 超时（当前 %+v）", want, st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// addrIn 地址是否在调用序列中。
func addrIn(list []string, addr string) bool {
	for _, a := range list {
		if a == addr {
			return true
		}
	}
	return false
}

// seedGui19TabletArchive 播种平板档案（183→162 跳变现场）：
// 183:5555 档案 fail=28（history），162:5555 档案 fail=3（history）。
func seedGui19TabletArchive(a *App) {
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "T7000PAD", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:5555", ModeTcpip)
	for i := 0; i < 28; i++ {
		a.profiles.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.183:5555")
	}
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	for i := 0; i < 3; i++ {
		a.profiles.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.162:5555")
	}
}

// gui19TabletCands 手工构建探测候选（含死地址，验证 gui19 在 tiers 构建处剔除，
// 不经 gui15 健康过滤——runDiscovery 的 tiers 构建是本测试的单元）。
func gui19TabletCands() map[string][]AddrEntry {
	// gui41 单记忆：旧 183 已退役，档案只剩 162 一条（每形态一条）。
	return map[string][]AddrEntry{
		"Xiaomi Pad 8 Pro": {
			{Addr: "192.168.31.162:5555", Mode: ModeTcpip, State: AddrStateActive, Fail: 3, LastOk: 200},
		},
	}
}

// gui19TabletMdns 平板当前广播：经典 _adb._tcp 162:5555（183→162 跳变后的当前地址）。
func gui19TabletMdns() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb._tcp", Name: "adb-T7000PAD", Addr: "192.168.31.162:5555", Mode: discovery.MdnsModeTcpip},
	}
}

// 平板场景（gui23 语义）：mDNS 广播 162 优先试；档案 183(fail28) 因 gui27
// 失败节流（lastFail=now → 60s 内不重试）不参与兜底 → tiers 只含 162；
// connect 162 成功 → found；183 的 fail 计数不被污染（gui19 旧断言"只试 162
// 无 183"在新语义下由节流保持成立——"节流外档案兜底"见 gui23 新测试）。
func TestGui19MdnsBroadcastAuthoritative(t *testing.T) {
	a, _ := newWirelessApp()
	seedGui19TabletArchive(a)

	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.162:5555" {
			return nil
		}
		return context.DeadlineExceeded
	}
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return gui19TabletMdns(), nil
	}

	a.runDiscovery(context.Background(), gui19TabletCands())

	st := waitDiscStatus(t, a, "found")
	if st.Found != "192.168.31.162:5555" {
		t.Fatalf("应找到广播地址 162: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	// 核心断言：tiers 只含 162——183(fail28) 被 gui15 健康过滤剔除，不参与兜底
	if len(calls) != 1 || calls[0] != "192.168.31.162:5555" {
		t.Fatalf("应只尝试广播地址 162: %v", calls)
	}
	if addrIn(st.Tried, "192.168.31.183:5555") {
		t.Fatalf("Tried 不应含档案死地址 183: %+v", st)
	}
	// 档案：gui41 单记忆下 183 已彻底退役；162 成功复位 active+fail=0
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if gui24FindAddr(e, "192.168.31.183:5555") != nil {
		t.Fatalf("183 应已按单记忆删除: %+v", e.Addrs)
	}
	a162 := gui24FindAddr(e, "192.168.31.162:5555")
	if a162 == nil || a162.Fail != 0 || a162.State != AddrStateActive {
		t.Fatalf("162 成功应复位 active+fail=0: %+v", e.Addrs)
	}
}

// mDNS 无广播（空服务，daemon 挂/设备哑巴）→ 档案经验兜底（gui52 二态）：
// active 地址是"在线证据"不进离线候选；确认 stale 后（90s 缺席/探测失败翻标）
// stale 条目才是离线候选。这里 162 打 stale 后探测通 → 翻回 active；
// 183（已被单记忆替换删除）不参与。
func TestGui19NoBroadcastArchiveFallback(t *testing.T) {
	a, _ := newWirelessApp()
	// 档案：183 先成功、162 后成功（跨 IP 替换 → 183 删除）
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "T7000PAD", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:5555", ModeTcpip)
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
	if !a.profiles.MarkAddrStale("Xiaomi Pad 8 Pro", "192.168.31.162:5555") {
		t.Fatal("MarkAddrStale 应有改动")
	}

	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.162:5555" {
			return nil
		}
		return context.DeadlineExceeded
	}
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return nil, nil // 无广播
	}

	a.runDiscovery(context.Background(), a.profiles.OfflineCandidateAddrs(nil))

	st := waitDiscStatus(t, a, "found")
	if st.Found != "192.168.31.162:5555" {
		t.Fatalf("stale 档案兜底应找到 162: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] != "192.168.31.162:5555" {
		t.Fatalf("无广播时档案兜底应只试 stale 162: %v", calls)
	}
	// 探测成功 → 162 翻回 active；183 已被单记忆删除
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if gui24FindAddr(e, "192.168.31.183:5555") != nil {
		t.Fatalf("183 应已被单记忆删除: %+v", e.Addrs)
	}
	a162 := gui24FindAddr(e, "192.168.31.162:5555")
	if a162 == nil || a162.State != AddrStateActive {
		t.Fatalf("探测成功应翻回 active: %+v", e.Addrs)
	}
}

// gui23 语义：mDNS 广播在但广播地址 connect 失败、且档案地址都在 60s 节流
// 期内（183 fail28 / 162 fail3 的 lastFail 均为 now——seed 刚记的失败）→
// notfound、只试广播 162；fail 记到广播地址（3→4），183 fail 计数不动。
// （gui19 旧名 TestGui19BroadcastFailNotfoundNoArchiveRetry 的"广播失败不回头
// 试档案"已移除——节流外的档案兜底成功场景见 app_gui23_stale_broadcast_fallback_test.go）
func TestGui23BroadcastFailNoHealthyArchiveNotfound(t *testing.T) {
	a, _ := newWirelessApp()
	seedGui19TabletArchive(a)

	var mu sync.Mutex
	var calls []string
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		return context.DeadlineExceeded
	}
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return gui19TabletMdns(), nil
	}

	a.runDiscovery(context.Background(), gui19TabletCands())

	st := waitDiscStatus(t, a, "notfound")
	mu.Lock()
	defer mu.Unlock()
	// 只试广播 162：档案 183(fail28)/162(fail3) 均被健康过滤剔除，无兜底地址
	if len(calls) != 1 || calls[0] != "192.168.31.162:5555" {
		t.Fatalf("广播失败时应只试 162、不试档案 183: %v", calls)
	}
	if addrIn(st.Tried, "192.168.31.183:5555") {
		t.Fatalf("Tried 不应含档案死地址 183: %+v", st)
	}
	// fail 记到广播地址：162 fail++（3→4）；183 已按单记忆删除
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if gui24FindAddr(e, "192.168.31.183:5555") != nil {
		t.Fatalf("183 应已删除: %+v", e.Addrs)
	}
	a162 := gui24FindAddr(e, "192.168.31.162:5555")
	if a162 == nil || a162.Fail != 4 {
		t.Fatalf("广播失败 fail++ 应记到 162（3→4）: %+v", e.Addrs)
	}
}
