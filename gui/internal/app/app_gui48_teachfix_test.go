package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-teachfix：插线学习事件链补全（offline→device 翻转必学） ---

type teachfixOps struct {
	mu           sync.Mutex
	port         string
	ip           string
	probeOK      bool
	tcpipErr     error
	getpropCalls int
	shellCalls   int
	tcpipCalls   int
	probeCalls   int
	seq          []string
}

func (o *teachfixOps) install(a *App) {
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		o.mu.Lock()
		o.getpropCalls++
		o.seq = append(o.seq, "getprop")
		o.mu.Unlock()
		return o.port, nil
	}
	a.teachOps.shellFn = func(ctx context.Context, serial string, args ...string) (string, error) {
		o.mu.Lock()
		o.shellCalls++
		if len(args) >= 2 && args[0] == "ip" && args[1] == "route" {
			o.seq = append(o.seq, "ip-route")
			ip := o.ip
			o.mu.Unlock()
			return "default via 192.168.31.1 dev wlan0 src " + ip + "\n", nil
		}
		o.seq = append(o.seq, "shell-other")
		o.mu.Unlock()
		return "", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		o.mu.Lock()
		o.tcpipCalls++
		o.seq = append(o.seq, "tcpip")
		err := o.tcpipErr
		o.mu.Unlock()
		return err
	}
	a.teachOps.probeFn = func(ctx context.Context, addr string) bool {
		o.mu.Lock()
		o.probeCalls++
		o.seq = append(o.seq, "probe")
		ok := o.probeOK
		o.mu.Unlock()
		return ok
	}
}

func (o *teachfixOps) counts() (getprop, shell, tcpip, probe int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.getpropCalls, o.shellCalls, o.tcpipCalls, o.probeCalls
}

func (o *teachfixOps) order() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.seq...)
}

func teachfixSeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
}

func teachfixUsb(serial, state string) adb.Device {
	return adb.Device{
		Serial: serial, State: state, ConnType: "usb",
		Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80",
	}
}

func teachfixAddrState(a *App, key, addr string) *AddrEntry {
	e, ok := a.profiles.Entry(key)
	if !ok {
		return nil
	}
	for i := range e.Addrs {
		if e.Addrs[i].Addr == addr {
			return &e.Addrs[i]
		}
	}
	return nil
}

func teachfixTaught(a *App, serial string) bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	return a.taughtTcpip[serial]
}

// 关键用例：added offline（被跳过、不占名额）→ changed 翻转 device → 补学，
// 读到的 IP 经 TCP 探测后写档案（旧 5555 被同形态淘汰）。
func TestTeachfixOfflineAddedThenChangedDeviceLearns(t *testing.T) {
	a, _ := newWirelessApp()
	teachfixSeedK80(a)
	ops := &teachfixOps{port: "5555", ip: "192.168.31.200", probeOK: true}
	ops.install(a)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate([]adb.Device{teachfixUsb("601c9f08", "offline")})
	g, _, _, _ := ops.counts()
	if g != 0 {
		t.Fatalf("offline added 不应执行学习: getprop=%d", g)
	}
	if teachfixTaught(a, "601c9f08") {
		t.Fatal("offline added 不应占 taughtTcpip 名额（否则翻转后无补学机会）")
	}
	mdns8LogContains(t, logPath, "[app] 插线学习待命：601c9f08（offline）")

	a.applyTrackUpdate([]adb.Device{teachfixUsb("601c9f08", "device")})
	g, _, _, p := ops.counts()
	if g != 1 || p != 1 {
		t.Fatalf("翻转 device 应补学一次: getprop=%d probe=%d", g, p)
	}
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.200:5555")
	if ae == nil || ae.State != AddrStateActive || ae.Stale {
		t.Fatalf("学习 IP 应写档案 active: %+v", ae)
	}
	if teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555") != nil {
		t.Fatal("同形态旧 5555 应被淘汰（AddrSuccess 语义）")
	}
	mdns8LogContains(t, logPath, "[app] 插线学习开始：601c9f08")
	mdns8LogContains(t, logPath, "[app] 插线学习 TCP 探测通：601c9f08 -> 192.168.31.200:5555")
}

// 原路径不回归：added 即 device → 立即学习一次。
func TestTeachfixAddedDeviceLearnsImmediately(t *testing.T) {
	a, _ := newWirelessApp()
	teachfixSeedK80(a)
	ops := &teachfixOps{port: "5555", ip: "192.168.31.200", probeOK: true}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{teachfixUsb("601c9f08", "device")})
	g, _, _, p := ops.counts()
	if g != 1 || p != 1 {
		t.Fatalf("added device 应立即学习: getprop=%d probe=%d", g, p)
	}
	if !teachfixTaught(a, "601c9f08") {
		t.Fatal("学习后应占 taughtTcpip 名额")
	}
}

// 一次插线周期只学一次：changed（字段抖动）不重学；拔线清周期后再插重新学。
func TestTeachfixOneTeachPerPlugCycle(t *testing.T) {
	a, _ := newWirelessApp()
	teachfixSeedK80(a)
	ops := &teachfixOps{port: "5555", ip: "192.168.31.200", probeOK: true}
	ops.install(a)

	dev := teachfixUsb("601c9f08", "device")
	a.applyTrackUpdate([]adb.Device{dev})
	g, _, _, p := ops.counts()
	if g != 1 || p != 1 {
		t.Fatalf("首轮应学习一次: getprop=%d probe=%d", g, p)
	}

	// 同周期 changed 事件（如电量变化）不得重复学习。
	dev.Battery = 100
	a.applyTrackUpdate([]adb.Device{dev})
	g, _, _, p = ops.counts()
	if g != 1 || p != 1 {
		t.Fatalf("同周期 changed 不应重学: getprop=%d probe=%d", g, p)
	}

	// 拔线：周期结束清除名额；再插线 = 新周期重新学习。
	a.applyTrackUpdate(nil)
	if teachfixTaught(a, "601c9f08") {
		t.Fatal("拔线后应清除 taughtTcpip 名额")
	}
	a.applyTrackUpdate([]adb.Device{dev})
	g, _, _, p = ops.counts()
	if g != 2 || p != 2 {
		t.Fatalf("再插线应重新学习一次: getprop=%d probe=%d", g, p)
	}
}

// 学习成功档案对齐：AddrSuccess 调用 + 日志（含自定义 IP 覆盖）。
func TestTeachfixLearnAlignsArchive(t *testing.T) {
	a, _ := newWirelessApp()
	teachfixSeedK80(a)
	ops := &teachfixOps{port: "5555", ip: "192.168.31.200", probeOK: true}
	ops.install(a)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate([]adb.Device{teachfixUsb("601c9f08", "device")})
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.200:5555")
	if ae == nil || ae.State != AddrStateActive || ae.Mode != ModeTcpip {
		t.Fatalf("档案应含学习 IP active tcpip: %+v", ae)
	}
	mdns8LogContains(t, logPath, "[app] 插线学习 IP：601c9f08 -> 192.168.31.200:5555（档案已对齐）")
}

// TCP 探测不通 → 不写档案（诚实），本周期名额保留，下次插线再学。
func TestTeachfixProbeFailNoArchiveWrite(t *testing.T) {
	a, _ := newWirelessApp()
	teachfixSeedK80(a)
	ops := &teachfixOps{port: "5555", ip: "192.168.31.200", probeOK: false}
	ops.install(a)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate([]adb.Device{teachfixUsb("601c9f08", "device")})
	if teachfixAddrState(a, "REDMI K80", "192.168.31.200:5555") != nil {
		t.Fatal("探测不通不得写档案")
	}
	old := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
	if old == nil || !old.Stale && old.State != AddrStateActive {
		t.Fatalf("旧档案应保持原样: %+v", old)
	}
	if !teachfixTaught(a, "601c9f08") {
		t.Fatal("探测失败也应保留本周期名额（防轰炸）")
	}
	mdns8LogContains(t, logPath, "[app] 插线学习 TCP 探测不通，不写档案：601c9f08 -> 192.168.31.200:5555")
}

// 华为型：首见即 device 的新设备（档案由 SyncDevices 建立）→ 立即学习。
func TestTeachfixHuaweiStyleAddedDeviceLearns(t *testing.T) {
	a, _ := newWirelessApp()
	ops := &teachfixOps{port: "5555", ip: "192.168.31.77", probeOK: true}
	ops.install(a)

	dev := adb.Device{
		Serial: "FLA123", State: "device", ConnType: "usb",
		Name: "HUAWEI FLA-TL10", Marketname: "HUAWEI FLA-TL10",
		Identity: "HUAWEI FLA-TL10",
	}
	a.applyTrackUpdate([]adb.Device{dev})
	ae := teachfixAddrState(a, "HUAWEI FLA-TL10", "192.168.31.77:5555")
	if ae == nil || ae.State != AddrStateActive {
		t.Fatalf("华为型 added device 应学习并对齐档案: %+v", ae)
	}
	g, _, _, p := ops.counts()
	if g != 1 || p != 1 {
		t.Fatalf("学习调用数不符: getprop=%d probe=%d", g, p)
	}
}

// 端口非 5555：先读 IP → tcpip 5555 → TCP 探测 → 对齐（顺序铁律）。
func TestTeachfixPortNot5555RunsTcpipThenProbe(t *testing.T) {
	a, _ := newWirelessApp()
	teachfixSeedK80(a)
	ops := &teachfixOps{port: "0", ip: "192.168.31.200", probeOK: true}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{teachfixUsb("601c9f08", "device")})
	_, _, tc, pc := ops.counts()
	if tc != 1 || pc != 1 {
		t.Fatalf("非 5555 应 tcpip 一次 + 探测一次: tcpip=%d probe=%d", tc, pc)
	}
	order := strings.Join(ops.order(), ">")
	if !strings.Contains(order, "ip-route>") || strings.Index(order, "ip-route") > strings.Index(order, "tcpip") {
		t.Fatalf("读 IP 必须先于 tcpip（F1' 铁律）: %s", order)
	}
	if strings.Index(order, "tcpip") > strings.Index(order, "probe") {
		t.Fatalf("探测必须在 tcpip 之后: %s", order)
	}
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.200:5555")
	if ae == nil || ae.State != AddrStateActive {
		t.Fatalf("tcpip+探测通后应写档案: %+v", ae)
	}
	// 清理本测试置位的插线遮罩（tcpip 路径 plugStart），避免 15s timer 残留。
	a.plugClear("REDMI K80")
	time.Sleep(10 * time.Millisecond)
}
