package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
	"scrcpy-ez/gui/internal/discovery"
)

// --- gui23：广播优先 + 档案兜底（横跳+缓存过期场景不扑空） ---
//
// 现场（2026-08-26 晚实测）：平板 DHCP 高频横跳 183↔162（一小时数次），
// mDNS 广播记录有 TTL 缓存——平板已跳 162 后旧广播 183 仍在缓存内被扫到。
// gui19"广播权威"只试 183 → 10060 → notfound 一轮（用户点刷新扑空）；
// 新鲜广播 162 出现后才恢复。gui23 修正为"广播=优先（第一）"：广播地址
// 层内排前先试，失败后同设备健康档案地址兜底——本轮即可找回 162。

// seedGui23Stale183Archive 播种平板档案：183:5555 旧地址（fail28 死档案）、
// 162:5555 当前地址（active fail0 健康档案——横跳后 connect 成功入档）。
func seedGui23Stale183Archive(a *App) {
	a.profiles.SyncDevices([]adb.Device{
		{Serial: "T7000PAD", State: "device", ConnType: "usb", Marketname: "Xiaomi Pad 8 Pro"},
	})
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.183:5555", ModeTcpip)
	for i := 0; i < 28; i++ {
		a.profiles.AddrFail("Xiaomi Pad 8 Pro", "192.168.31.183:5555")
	}
	a.profiles.AddrSuccessWithMode("Xiaomi Pad 8 Pro", "192.168.31.162:5555", ModeTcpip)
}

// gui23StaleCands 手工构建探测候选（tiers 构建是本测试的单元，不经
// OfflineCandidateAddrs 的 gui15 健康过滤——验证 tiers 构建处的健康过滤）。
func gui23StaleCands() map[string][]AddrEntry {
	return map[string][]AddrEntry{
		"Xiaomi Pad 8 Pro": {
			{Addr: "192.168.31.183:5555", Mode: ModeTcpip, State: AddrStateHistory, Fail: 28, LastOk: 100},
			{Addr: "192.168.31.162:5555", Mode: ModeTcpip, State: AddrStateActive, Fail: 0, LastOk: 200},
		},
	}
}

// gui23StaleMdns 旧广播缓存：183 已过期（平板实际已跳 162），TTL 内仍被扫到。
func gui23StaleMdns() []discovery.MdnsService {
	return []discovery.MdnsService{
		{Type: "_adb._tcp", Name: "adb-T7000PAD", Addr: "192.168.31.183:5555", Mode: discovery.MdnsModeTcpip},
	}
}

// 横跳现场回归：广播 183（过期缓存）connect 失败 → 健康档案 162 兜底成功 →
// found(162)。ConnectFn 调用序列 183 先、162 后（广播地址层内排前）。
func TestGui23StaleBroadcastArchiveFallbackFound(t *testing.T) {
	a, _ := newWirelessApp()
	seedGui23Stale183Archive(a)

	var mu sync.Mutex
	var calls []string
	// ConnectAny 首成功即返回（并行取消其余探测）——为确定性断言"183 先试、
	// 162 后兜底"，162 的成功等到 183 的 connect 已发起之后（门闩模式）。
	gate := make(chan struct{})
	var once sync.Once
	a.disc.ConnectFn = func(ctx context.Context, addr string) error {
		if addr == "192.168.31.162:5555" {
			<-gate
		}
		mu.Lock()
		calls = append(calls, addr)
		mu.Unlock()
		if addr == "192.168.31.183:5555" {
			once.Do(func() { close(gate) })
			return context.DeadlineExceeded // 旧广播已死（10060 等价）
		}
		return nil
	}
	a.disc.MdnsScanFn = func(ctx context.Context, maxWait time.Duration) ([]discovery.MdnsService, error) {
		return gui23StaleMdns(), nil
	}

	a.runDiscovery(context.Background(), gui23StaleCands())

	st := waitDiscStatus(t, a, "found")
	if st.Found != "192.168.31.162:5555" {
		t.Fatalf("广播失败后健康档案应兜底找到 162: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	// 调用序列：广播 183 先试，档案 162 兜底后试（层内 [广播..., 档案...] 顺序）
	if len(calls) != 2 || calls[0] != "192.168.31.183:5555" || calls[1] != "192.168.31.162:5555" {
		t.Fatalf("应 183 先试、162 兜底（got %v）", calls)
	}
	// Tried = [广播..., 档案...]（广播层内排前）
	if len(st.Tried) != 2 || st.Tried[0] != "192.168.31.183:5555" || st.Tried[1] != "192.168.31.162:5555" {
		t.Fatalf("Tried 应为 [183 162]（广播优先）: %+v", st)
	}
	// 档案：gui41 单记忆下 183 已删除；162 兜底成功保持 active+fail=0
	e, _ := a.profiles.Entry("Xiaomi Pad 8 Pro")
	if gui24FindAddr(e, "192.168.31.183:5555") != nil {
		t.Fatalf("183 死档案应按单记忆删除: %+v", e.Addrs)
	}
	a162 := gui24FindAddr(e, "192.168.31.162:5555")
	if a162 == nil || a162.Fail != 0 || a162.State != AddrStateActive {
		t.Fatalf("162 兜底成功应保持 active+fail=0: %+v", e.Addrs)
	}
}
