package app

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"scrcpy-ez/gui/internal/adb"
)

// --- gui48-teachfix2：物理接口候选 + 并行探测裁决 + 遮罩事件链 ---

type teachfix2Ops struct {
	mu         sync.Mutex
	port       string
	addrOut    string
	routeOut   string
	probeOK    map[string]bool
	probeCalls []string
	tcpipCalls int
}

func (o *teachfix2Ops) install(a *App) {
	a.teachOps.getpropFn = func(ctx context.Context, serial, prop string) (string, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.port, nil
	}
	a.teachOps.shellFn = func(ctx context.Context, serial string, args ...string) (string, error) {
		if len(args) == 0 {
			return "", nil
		}
		switch args[0] {
		case "settings":
			return "1\n", nil
		case "ip":
			if len(args) >= 2 {
				switch args[1] {
				case "addr":
					o.mu.Lock()
					defer o.mu.Unlock()
					return o.addrOut, nil
				case "route":
					o.mu.Lock()
					defer o.mu.Unlock()
					return o.routeOut, nil
				}
			}
		}
		return "", nil
	}
	a.teachOps.tcpipFn = func(ctx context.Context, serial, port string) error {
		o.mu.Lock()
		o.tcpipCalls++
		o.mu.Unlock()
		return nil
	}
	a.teachOps.probeFn = func(ctx context.Context, addr string) bool {
		o.mu.Lock()
		o.probeCalls = append(o.probeCalls, addr)
		ok := o.probeOK[addr]
		o.mu.Unlock()
		return ok
	}
}

func (o *teachfix2Ops) probes() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.probeCalls...)
}

func teachfix2SeedK80(a *App) {
	seedProfiles(a, map[string]*DeviceEntry{
		"REDMI K80": mkEntry("REDMI K80", "24117RK2CC", []string{"601c9f08"},
			[]string{"192.168.31.197:5555"}),
	})
}

func teachfix2Usb(state string) adb.Device {
	return adb.Device{Serial: "601c9f08", State: state, ConnType: "usb",
		Name: "REDMI K80", Marketname: "REDMI K80", Identity: "REDMI K80"}
}

func teachfix2PlugActive(a *App, id string) bool {
	a.teachMu.Lock()
	defer a.teachMu.Unlock()
	_, ok := a.plugging[id]
	return ok
}

// 用例①：候选列表接口过滤纯函数——保留 wlan/eth，排除 rmnet/tun/usb/ppp/ifb。
func TestTeachfix2IPCandidatesExcludeNonPhysical(t *testing.T) {
	out := "" +
		"1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536\n" +
		"    inet 127.0.0.1/8 scope host lo\n" +
		"2: rmnet_data2: <UP,LOWER_UP> mtu 1500\n" +
		"    inet 10.2.190.105/30 scope global rmnet_data2\n" +
		"3: tun0: <POINTOPOINT> mtu 1500\n" +
		"    inet 10.8.0.2/32 scope global tun0\n" +
		"4: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n" +
		"    inet 192.168.31.100/24 scope global eth0\n" +
		"5: usb0: <UP,LOWER_UP> mtu 1500\n" +
		"    inet 192.168.42.129/24 scope global usb0\n" +
		"6: wlan0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n" +
		"    inet 192.168.31.197/24 scope global wlan0\n" +
		"7: ppp0: <POINTOPOINT> mtu 1500\n" +
		"    inet 10.64.64.64/32 scope global ppp0\n" +
		"8: ifb0: <BROADCAST> mtu 1500\n" +
		"    inet 169.254.3.4/24 scope global ifb0\n"
	ips := teachCandidateIPs(teachIPCandidatesFromAddr(out))
	want := []string{"192.168.31.197", "192.168.31.100"} // wlan0 > eth0
	if len(ips) != len(want) {
		t.Fatalf("候选应只含物理接口（wlan/eth）: %v", ips)
	}
	for i := range want {
		if ips[i] != want[i] {
			t.Fatalf("候选顺序/内容错误: got=%v want=%v", ips, want)
		}
	}
	if strings.Contains(strings.Join(ips, ","), "10.2.190.105") ||
		strings.Contains(strings.Join(ips, ","), "10.8.0.2") {
		t.Fatalf("蜂窝/隧道 IP 不得进入候选: %v", ips)
	}
}

// 用例②③：多候选并行探测——并发标记 + 第一个通者胜 + 总耗时≈单次上限。
func TestTeachfix2ProbeCandidatesParallelFirstSuccess(t *testing.T) {
	a, _ := newWirelessApp()
	var mu sync.Mutex
	active, maxActive := 0, 0
	start := time.Now()
	a.teachOps.probeFn = func(ctx context.Context, addr string) bool {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return addr == "192.168.31.100:5555" // 第二个候选（eth）通
	}
	got := a.probeWirelessCandidates(context.Background(), "601c9f08",
		[]string{"192.168.31.197", "192.168.31.100"})
	elapsed := time.Since(start)
	if got != "192.168.31.100" {
		t.Fatalf("第一个探测通的候选应写入: %q", got)
	}
	if maxActive != 2 {
		t.Fatalf("多候选应并行探测（最大并发=%d）", maxActive)
	}
	if elapsed >= 380*time.Millisecond {
		t.Fatalf("并行总耗时不应≈串行（2×200ms），实际 %s", elapsed)
	}
}

// 全不通 → 返回空（诚实不写档案由 teachTcpipSerial 保证）。
func TestTeachfix2ProbeAllFailReturnsEmpty(t *testing.T) {
	a, _ := newWirelessApp()
	a.teachOps.probeFn = func(ctx context.Context, addr string) bool { return false }
	if got := a.probeWirelessCandidates(context.Background(), "601c9f08",
		[]string{"192.168.31.197", "192.168.31.100"}); got != "" {
		t.Fatalf("全不通应返回空: %q", got)
	}
}

// 全链路：ip addr 含 wlan+rmnet（蜂窝）+eth，探测只对物理候选发起；
// 第一个通者（wlan）写档案。
func TestTeachfix2LearnPicksPhysicalReachableCandidate(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix2SeedK80(a)
	ops := &teachfix2Ops{
		port: "5555",
		addrOut: "2: wlan0: <UP> mtu 1500\n    inet 192.168.31.197/24 scope global wlan0\n" +
			"3: rmnet_data2: <UP> mtu 1500\n    inet 10.2.190.105/30 scope global rmnet_data2\n" +
			"4: eth0: <UP> mtu 1500\n    inet 192.168.31.100/24 scope global eth0\n",
		probeOK: map[string]bool{
			"192.168.31.197:5555": true,
			"192.168.31.100:5555": false,
		},
	}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{teachfix2Usb("device")})
	calls := ops.probes()
	if len(calls) != 2 {
		t.Fatalf("应对 wlan+eth 两个物理候选并行探测（蜂窝不参与）: %v", calls)
	}
	ae := teachfixAddrState(a, "REDMI K80", "192.168.31.197:5555")
	if ae == nil || ae.State != AddrStateActive {
		t.Fatalf("应写入探测通的 wlan IP: %+v", ae)
	}
	if teachfixAddrState(a, "REDMI K80", "10.2.190.105:5555") != nil {
		t.Fatal("蜂窝 IP 不得写档案")
	}
}

// 用例④（gui48-teachfix4 适配）：removed+2s 防抖确认不再清遮罩（拔线确认
// 已删除——adbd 重启断档 >2s 时不可与真拔线区分）；真拔线由 10s 兜底清。
func TestTeachfix2RemovedNoLongerClearsMask(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix2SeedK80(a)
	a.profiles.AddrSuccess("REDMI K80", "192.168.31.197:5555", ModeTcpip)

	a.applyTrackUpdate([]adb.Device{teachfix2Usb("offline")})
	if !teachfix2PlugActive(a, "REDMI K80") {
		t.Fatal("插线后遮罩应启动")
	}
	a.applyTrackUpdate(nil) // removed → 2s 防抖
	a.onDropped("REDMI K80")
	if !teachfix2PlugActive(a, "REDMI K80") {
		t.Fatal("removed 防抖确认不得清遮罩（拔线确认已删除）")
	}
	// 10s 兜底：加速模拟超时点。
	a.teachMu.Lock()
	a.plugging["REDMI K80"] = time.Now().Add(-(plugShieldTimeout + time.Second))
	a.teachMu.Unlock()
	a.plugTimeout("REDMI K80")
	if teachfix2PlugActive(a, "REDMI K80") {
		t.Fatal("10s 兜底应清除遮罩")
	}
}

// 用例⑤：无事件链时的 10s 兜底语义保留（gui48-teachfix4：15s→10s）。
func TestTeachfix2PlugTimeoutFallbackStillWorks(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix2SeedK80(a)
	gui47fixPlug(a, "REDMI K80", time.Now().Add(-(plugShieldTimeout + time.Second)))
	a.plugTimeout("REDMI K80")
	if teachfix2PlugActive(a, "REDMI K80") {
		t.Fatal("10s 兜底应清除遮罩")
	}
}

// 遮罩生死绑定 getprop 判定：added 起一次；端口非 5555 保持；就绪 5555 退一次。
func TestTeachfix2MaskLifecycleBindsTcpipReady(t *testing.T) {
	a, _ := newWirelessApp()
	teachfix2SeedK80(a)
	ops := &teachfix2Ops{port: "0"}
	ops.install(a)
	gui49fix6FastStable(t)
	logPath := mdns8StartLogCapture(t)

	a.applyTrackUpdate([]adb.Device{teachfix2Usb("device")})
	if !teachfix2PlugActive(a, "REDMI K80") {
		t.Fatal("tcpip 未就绪时遮罩应持续")
	}
	if ops.tcpipCalls != 1 {
		t.Fatalf("端口非 5555 应执行一次 tcpip: %d", ops.tcpipCalls)
	}

	// gui49-fix6：getprop==5555 不再清遮罩；清因①=USB 连续 device 满稳定窗口。
	ops.mu.Lock()
	ops.port = "5555"
	ops.mu.Unlock()
	a.plugCheckTcpipReady(context.Background(), "601c9f08")
	if !teachfix2PlugActive(a, "REDMI K80") {
		t.Fatal("getprop==5555 不得清遮罩（清因改为稳定 device）")
	}
	gui49fix6WaitPlugClear(t, a)

	mdns8LogContains(t, logPath, "[app] 插线遮罩结束：REDMI K80")
	b, _ := os.ReadFile(logPath)
	logs := string(b)
	if strings.Count(logs, "[app] 插线遮罩开始：REDMI K80") != 1 {
		t.Fatalf("一次插线周期 plugStart 应恰一次:\n%s", logs)
	}
	if strings.Count(logs, "[app] 插线遮罩结束：REDMI K80") != 1 {
		t.Fatalf("一次插线周期 plugClear 应恰一次:\n%s", logs)
	}
}

// 用例⑥：华为/平板回归——added 即 device，物理候选学习一次成功。
func TestTeachfix2HuaweiStyleRegression(t *testing.T) {
	a, _ := newWirelessApp()
	ops := &teachfix2Ops{
		port:    "5555",
		addrOut: "2: wlan0: <UP> mtu 1500\n    inet 192.168.31.77/24 scope global wlan0\n",
		probeOK: map[string]bool{"192.168.31.77:5555": true},
	}
	ops.install(a)

	a.applyTrackUpdate([]adb.Device{{
		Serial: "FLA123", State: "device", ConnType: "usb",
		Name: "HUAWEI FLA-TL10", Marketname: "HUAWEI FLA-TL10",
		Identity: "HUAWEI FLA-TL10",
	}})
	ae := teachfixAddrState(a, "HUAWEI FLA-TL10", "192.168.31.77:5555")
	if ae == nil || ae.State != AddrStateActive {
		t.Fatalf("华为型 added device 应学习成功: %+v", ae)
	}
}
