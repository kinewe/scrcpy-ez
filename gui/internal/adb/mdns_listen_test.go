package adb

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

func mdnsTestIface(name, mac, ip string, flags net.Flags) mdnsIface {
	hw, _ := net.ParseMAC(mac)
	var addrs []net.Addr
	if ip != "" {
		addrs = append(addrs, &net.IPNet{IP: net.ParseIP(ip), Mask: net.CIDRMask(24, 32)})
	}
	return mdnsIface{iface: net.Interface{Name: name, HardwareAddr: hw, Flags: flags}, addrs: addrs}
}

func TestFilterLanInterfaces(t *testing.T) {
	upMulti := net.FlagUp | net.FlagMulticast
	cases := []struct {
		name string
		in   mdnsIface
		keep bool
	}{
		{"物理以太网", mdnsTestIface("Ethernet", "00:11:22:33:44:55", "192.168.31.174", upMulti), true},
		{"vEthernet 排除", mdnsTestIface("vEthernet (WSL)", "00:15:5d:aa:bb:cc", "172.17.208.1", upMulti), false},
		{"ZeroTier 排除", mdnsTestIface("ZeroTier One [xxxx]", "02:00:00:aa:bb:cc", "10.151.76.18", upMulti), false},
		{"虚拟 MAC 排除", mdnsTestIface("Ethernet 2", "00:50:56:aa:bb:cc", "192.168.31.175", upMulti), false},
		{"无 IPv4 排除", mdnsTestIface("Ethernet 3", "00:11:22:33:44:66", "", upMulti), false},
		{"链路本地排除", mdnsTestIface("Ethernet 4", "00:11:22:33:44:77", "169.254.1.1", upMulti), false},
		{"回环排除", mdnsTestIface("Loopback", "", "127.0.0.1", upMulti), false},
		{"FlagDown 排除", mdnsTestIface("Ethernet 5", "00:11:22:33:44:88", "192.168.31.176", 0), false},
		{"无 Multicast 排除", mdnsTestIface("Ethernet 6", "00:11:22:33:44:99", "192.168.31.177", net.FlagUp), false},
		{"空 MAC 排除", mdnsTestIface("Ethernet 7", "", "192.168.31.178", upMulti), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filterLanInterfaces([]mdnsIface{c.in})
			if c.keep && (len(got) != 1 || got[0].Name != c.in.iface.Name) {
				t.Fatalf("应保留该接口: %+v", got)
			}
			if !c.keep && len(got) != 0 {
				t.Fatalf("应排除该接口: %+v", got)
			}
		})
	}
	if got := filterLanInterfaces(nil); len(got) != 0 {
		t.Fatalf("空输入应返回空: %+v", got)
	}
}

func TestMdnsNameParsing(t *testing.T) {
	for name, want := range map[string]string{
		"_adb-tls-connect._tcp.local.":                     "_adb-tls-connect._tcp",
		"_adb._tcp.local.":                                 "_adb._tcp",
		"_adb-tls-pairing._tcp.local.":                     "_adb-tls-pairing._tcp",
		"adb-a743e1df-On9v2R._adb-tls-connect._tcp.local.": "_adb-tls-connect._tcp",
	} {
		got, ok := mdnsServiceTypeForName(name)
		if !ok || got != want {
			t.Fatalf("mdnsServiceTypeForName(%q) = %q,%v want %q", name, got, ok, want)
		}
	}
	if _, ok := mdnsServiceTypeForName("_http._tcp.local."); ok {
		t.Fatal("非 adb 服务不应识别")
	}
	if got := mdnsInstanceForName("adb-a743e1df-On9v2R._adb-tls-connect._tcp.local.", "_adb-tls-connect._tcp"); got != "adb-a743e1df-On9v2R" {
		t.Fatalf("实例名剥离错误: %q", got)
	}
}

func collectEvents(st *mdnsListenState) *[]MdnsTrackEvents {
	var out []MdnsTrackEvents
	st.onEvents = func(ev MdnsTrackEvents) { out = append(out, ev) }
	return &out
}

func TestMdnsListenStateFirstAndUpsertGone(t *testing.T) {
	st := newMdnsListenState(nil, nil, nil)
	evs := collectEvents(st)
	st.initial()
	st.upsert(MdnsService{Type: "_adb._tcp", Name: "adb-a", Addr: "192.168.31.2:5555", Mode: MdnsModeTcpip}, time.Now(), false)
	st.gone(MdnsService{Type: "_adb._tcp", Name: "adb-a"})
	if len(*evs) != 3 {
		t.Fatalf("事件数错误: %+v", *evs)
	}
	if !(*evs)[0].First || len((*evs)[0].Snapshot) != 0 {
		t.Fatalf("首块应 First=true 空快照: %+v", (*evs)[0])
	}
	if (*evs)[1].First || len((*evs)[1].Added) != 1 || len((*evs)[1].Snapshot) != 1 {
		t.Fatalf("upsert 事件错误: %+v", (*evs)[1])
	}
	if len((*evs)[2].Removed) != 1 || len((*evs)[2].Snapshot) != 0 {
		t.Fatalf("gone 事件错误: %+v", (*evs)[2])
	}
}

func TestMdnsListenStateIdleProbe(t *testing.T) {
	old := mdnsIdleProbeAfter
	mdnsIdleProbeAfter = 30 * time.Millisecond
	defer func() { mdnsIdleProbeAfter = old }()

	var probesMu sync.Mutex
	var probes []string
	probeDone := make(chan struct{})
	var probeDoneOnce sync.Once
	svc := MdnsService{Type: "_adb-tls-connect._tcp", Name: "adb-a-Xy9zQ2", Addr: "192.168.31.2:36329", Mode: MdnsModeTls}
	var st *mdnsListenState
	st = newMdnsListenState(nil, func(addr string) {
		probesMu.Lock()
		probes = append(probes, addr)
		n := len(probes)
		probesMu.Unlock()
		if n >= 2 {
			// 第二次触发后停掉条目 timer，让测试在结束前收敛（-race 下
			// 不与 defer 恢复 mdnsIdleProbeAfter 竞态）。
			st.gone(svc)
			probeDoneOnce.Do(func() { close(probeDone) })
		}
	}, nil)
	st.initial()
	st.upsert(svc, time.Now(), false)

	waitProbe := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			probesMu.Lock()
			n := len(probes)
			addr := ""
			if n > 0 {
				addr = probes[n-1]
			}
			all := append([]string(nil), probes...)
			probesMu.Unlock()
			if n > 0 {
				if addr != "192.168.31.2:36329" {
					t.Fatalf("onIdleProbe 地址错误: %v", all)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("90s 无信号（测试注入 30ms）应触发 onIdleProbe: %v", all)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitProbe()

	// 触发后立即重置 timer 重新计数：再等一个周期应再次触发。
	probesMu.Lock()
	n := len(probes)
	probesMu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		probesMu.Lock()
		cur := len(probes)
		all := append([]string(nil), probes...)
		probesMu.Unlock()
		if cur > n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("触发后应重置 90s timer 再次触发: probes=%v", all)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("第二次 probe 后应停止条目 timer")
	}
}

func TestMdnsListenStateUpsertResetsIdleTimer(t *testing.T) {
	old := mdnsIdleProbeAfter
	mdnsIdleProbeAfter = 60 * time.Millisecond
	defer func() { mdnsIdleProbeAfter = old }()

	var probesMu sync.Mutex
	var probes []string
	probeDone := make(chan struct{})
	svc := MdnsService{Type: "_adb._tcp", Name: "adb-a", Addr: "192.168.31.2:5555", Mode: MdnsModeTcpip}
	var st *mdnsListenState
	st = newMdnsListenState(nil, func(addr string) {
		probesMu.Lock()
		probes = append(probes, addr)
		n := len(probes)
		probesMu.Unlock()
		if n == 1 {
			// 首次触发后停掉条目 timer，测试结束前收敛（避免 -race
			// 与 defer 恢复 mdnsIdleProbeAfter 竞态）。
			st.gone(svc)
			close(probeDone)
		}
	}, nil)
	st.upsert(svc, time.Now(), false)
	time.Sleep(30 * time.Millisecond) // 未到 60ms
	st.upsert(svc, time.Now(), false) // 重置计时
	time.Sleep(40 * time.Millisecond) // 距上次 upsert 仅 40ms（<60ms）
	probesMu.Lock()
	n := len(probes)
	all := append([]string(nil), probes...)
	probesMu.Unlock()
	if n != 0 {
		t.Fatalf("upsert 应重置 90s timer（测试注入 60ms），不应提前触发: %v", all)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		probesMu.Lock()
		cur := len(probes)
		all = append([]string(nil), probes...)
		probesMu.Unlock()
		if cur > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("重置后到点应触发一次 onIdleProbe: %v", all)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("首次 probe 后应停止条目 timer")
	}
}

func TestMdnsListenStateGoodbyeStopsIdleTimer(t *testing.T) {
	old := mdnsIdleProbeAfter
	mdnsIdleProbeAfter = 30 * time.Millisecond
	defer func() { mdnsIdleProbeAfter = old }()

	var probes []string
	st := newMdnsListenState(nil, func(addr string) { probes = append(probes, addr) }, nil)
	svc := MdnsService{Type: "_adb-tls-connect._tcp", Name: "adb-a-Xy9zQ2", Addr: "192.168.31.2:36329", Mode: MdnsModeTls}
	st.upsert(svc, time.Now(), false)
	st.gone(svc)
	time.Sleep(80 * time.Millisecond)
	if len(probes) != 0 {
		t.Fatalf("Goodbye 后应停止 idle timer，不再 probe: %v", probes)
	}
}

func TestMdnsColdStartQueryOnce(t *testing.T) {
	var queries []string
	st := newMdnsListenState(nil, nil, func(st string) { queries = append(queries, st) })
	st.coldStartQuery()
	st.coldStartQuery()
	if len(queries) != 3 {
		t.Fatalf("冷启动应只发一轮 3 类查询: %v", queries)
	}
	joined := strings.Join(queries, "|")
	if !strings.Contains(joined, "_adb-tls-connect._tcp") ||
		!strings.Contains(joined, "_adb._tcp") ||
		!strings.Contains(joined, "_adb-tls-pairing._tcp") {
		t.Fatalf("冷启动查询类型不全: %v", queries)
	}
}

func TestMdnsProcessMessageGoodbyeAndUpdate(t *testing.T) {
	st := newMdnsListenState(nil, nil, nil)
	evs := collectEvents(st)
	st.initial()

	build := func(ttl uint32) *dns.Msg {
		m := new(dns.Msg)
		svc := "_adb-tls-connect._tcp.local."
		inst := "adb-a-Xy9zQ2._adb-tls-connect._tcp.local."
		host := "a.local."
		ptr := &dns.PTR{Hdr: dns.RR_Header{Name: svc, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: ttl}, Ptr: inst}
		srv := &dns.SRV{Hdr: dns.RR_Header{Name: inst, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: ttl}, Port: 36329, Target: host}
		a := &dns.A{Hdr: dns.RR_Header{Name: host, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: net.ParseIP("192.168.31.2")}
		m.Answer = append(m.Answer, ptr)
		m.Extra = append(m.Extra, srv, a)
		return m
	}

	st.processMessage(build(120), false)
	if len(*evs) != 2 || len((*evs)[1].Snapshot) != 1 {
		t.Fatalf("注册通告应产生 appeared 快照: %+v", *evs)
	}
	if s := (*evs)[1].Snapshot[0]; s.Name != "adb-a-Xy9zQ2" || s.Addr != "192.168.31.2:36329" || s.Mode != MdnsModeTls {
		t.Fatalf("解析错误: %+v", s)
	}

	st.processMessage(build(0), false)
	if len(*evs) != 3 || len((*evs)[2].Removed) != 1 {
		t.Fatalf("TTL=0 Goodbye 应产生 gone 快照: %+v", *evs)
	}
}

func TestMdnsPtrQueryHasQU(t *testing.T) {
	q := mdnsPtrQuery("_adb._tcp")
	if len(q.Question) != 1 {
		t.Fatalf("查询应只有 1 个 Question: %+v", q.Question)
	}
	if q.Question[0].Qclass&0x8000 == 0 {
		t.Fatalf("PTR 查询应带 QU 位（QCLASS 含 0x8000）: %#x", q.Question[0].Qclass)
	}
}

func TestMdnsPeriodicQueryOnlyConnectTypes(t *testing.T) {
	var queries []string
	st := newMdnsListenState(nil, nil, func(st string) { queries = append(queries, st) })
	st.periodicQuery()
	if len(queries) != 2 {
		t.Fatalf("周期查询应只查两类连接服务: %v", queries)
	}
	joined := strings.Join(queries, "|")
	if !strings.Contains(joined, "_adb-tls-connect._tcp") || !strings.Contains(joined, "_adb._tcp") {
		t.Fatalf("周期查询类型错误: %v", queries)
	}
	if strings.Contains(joined, "_adb-tls-pairing") {
		t.Fatalf("周期查询不应包含 pairing: %v", queries)
	}
}

func TestMdnsQueryResponseUpsertResetsIdleTimer(t *testing.T) {
	old := mdnsIdleProbeAfter
	mdnsIdleProbeAfter = 60 * time.Millisecond
	defer func() { mdnsIdleProbeAfter = old }()

	var probesMu sync.Mutex
	var probes []string
	probeDone := make(chan struct{})
	svc := MdnsService{Type: "_adb-tls-connect._tcp", Name: "adb-a-Xy9zQ2", Addr: "192.168.31.2:36329", Mode: MdnsModeTls}
	var st *mdnsListenState
	st = newMdnsListenState(nil, func(addr string) {
		probesMu.Lock()
		probes = append(probes, addr)
		n := len(probes)
		probesMu.Unlock()
		if n == 1 {
			// 首次触发后停掉条目 timer，测试结束前收敛（避免 -race
			// 与 defer 恢复 mdnsIdleProbeAfter 竞态）。
			st.gone(svc)
			close(probeDone)
		}
	}, nil)
	st.upsert(svc, time.Now(), false)
	time.Sleep(30 * time.Millisecond)

	// QU 查询的应答 → 与广播同等处理：upsert + 重置 idle timer。
	build := func() *dns.Msg {
		m := new(dns.Msg)
		svcName := "_adb-tls-connect._tcp.local."
		inst := "adb-a-Xy9zQ2._adb-tls-connect._tcp.local."
		host := "a.local."
		ptr := &dns.PTR{Hdr: dns.RR_Header{Name: svcName, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 120}, Ptr: inst}
		srv := &dns.SRV{Hdr: dns.RR_Header{Name: inst, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 120}, Port: 36329, Target: host}
		a := &dns.A{Hdr: dns.RR_Header{Name: host, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120}, A: net.ParseIP("192.168.31.2")}
		m.Answer = append(m.Answer, ptr)
		m.Extra = append(m.Extra, srv, a)
		return m
	}
	st.processMessage(build(), true)
	time.Sleep(40 * time.Millisecond) // 距应答仅 40ms（<60ms）
	probesMu.Lock()
	n := len(probes)
	all := append([]string(nil), probes...)
	probesMu.Unlock()
	if n != 0 {
		t.Fatalf("QU 应答应重置 90s（测试 60ms）idle timer，不应提前触发: %v", all)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		probesMu.Lock()
		cur := len(probes)
		all = append([]string(nil), probes...)
		probesMu.Unlock()
		if cur > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("重置后到点应触发一次 onIdleProbe: %v", all)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("首次 probe 后应停止条目 timer")
	}
}

func TestMdnsQueryNoAnswerNoUpsert(t *testing.T) {
	st := newMdnsListenState(nil, nil, nil)
	st.initial()
	// 只含 Question 的查询回显（我们自己的查询）不应被处理成 upsert。
	q := mdnsPtrQuery("_adb._tcp")
	st.processMessage(q, false)
	st.mu.Lock()
	n := len(st.entries)
	st.mu.Unlock()
	if n != 0 {
		t.Fatalf("查询回显（无 Answer）不应 upsert: %+v", st.entries)
	}
	if st.sawAnySignal() {
		t.Fatal("查询回显不应算作收到 mDNS 信号")
	}
}

type fakeQueryConn struct {
	local *net.UDPAddr
	last  []byte
	to    net.Addr
}

func (f *fakeQueryConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	f.last = append([]byte(nil), b...)
	f.to = addr
	return len(b), nil
}
func (f *fakeQueryConn) ReadFrom(b []byte) (int, net.Addr, error) { return 0, nil, nil }
func (f *fakeQueryConn) Close() error                             { return nil }
func (f *fakeQueryConn) LocalAddr() net.Addr                      { return f.local }
func (f *fakeQueryConn) SetDeadline(time.Time) error              { return nil }
func (f *fakeQueryConn) SetReadDeadline(time.Time) error          { return nil }
func (f *fakeQueryConn) SetWriteDeadline(time.Time) error         { return nil }

func TestMdnsQuerySentFromDedicatedSocket(t *testing.T) {
	conn := &fakeQueryConn{local: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}}
	fn := mdnsQueryFnFor(conn)
	fn("_adb._tcp")

	if len(conn.last) == 0 || conn.to.String() != mdnsUDPAddr.String() {
		t.Fatalf("查询应从专用 socket 发往 224.0.0.251:5353: last=%d to=%v", len(conn.last), conn.to)
	}
	m := new(dns.Msg)
	if err := m.Unpack(conn.last); err != nil {
		t.Fatalf("查询包解析失败: %v", err)
	}
	if len(m.Question) != 1 || m.Question[0].Qclass&0x8000 == 0 {
		t.Fatalf("查询应为 QU 单问句（QDCOUNT=1 + 0x8000）: %+v", m.Question)
	}
	if conn.LocalAddr().(*net.UDPAddr).Port == 5353 {
		t.Fatalf("专用查询 socket 不应绑定 5353: %v", conn.LocalAddr())
	}
}

func TestMdnsQueryResponseForcedEmit(t *testing.T) {
	st := newMdnsListenState(nil, nil, nil)
	evs := collectEvents(st)
	st.initial()
	svc := MdnsService{Type: "_adb._tcp", Name: "adb-a", Addr: "192.168.31.2:5555", Mode: MdnsModeTcpip}
	st.upsert(svc, time.Now(), false)
	n := len(*evs)

	// QU 应答内容与上次完全相同 → force emit，Added=当前快照全量。
	build := func() *dns.Msg {
		m := new(dns.Msg)
		svcName := "_adb._tcp.local."
		inst := "adb-a._adb._tcp.local."
		host := "a.local."
		ptr := &dns.PTR{Hdr: dns.RR_Header{Name: svcName, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 120}, Ptr: inst}
		srv := &dns.SRV{Hdr: dns.RR_Header{Name: inst, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 120}, Port: 5555, Target: host}
		a := &dns.A{Hdr: dns.RR_Header{Name: host, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120}, A: net.ParseIP("192.168.31.2")}
		m.Answer = append(m.Answer, ptr)
		m.Extra = append(m.Extra, srv, a)
		return m
	}
	st.processMessage(build(), true)
	if len(*evs) != n+1 {
		t.Fatalf("QU 同内容应答应强制 emit 一次: %+v", *evs)
	}
	last := (*evs)[len(*evs)-1]
	if len(last.Added) != 1 || len(last.Removed) != 0 || len(last.Snapshot) != 1 || last.First {
		t.Fatalf("强制 emit 事件错误（Added=快照全量，Removed=空）: %+v", last)
	}
}

func TestBrowseMdnsContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got []MdnsTrackEvents
	err := (&Manager{}).BrowseMdns(ctx, func(ev MdnsTrackEvents) { got = append(got, ev) }, nil)
	if err != context.Canceled {
		t.Fatalf("已取消 ctx 应返回 context.Canceled: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("已取消 ctx 不应发事件: %+v", got)
	}
}

// --- gui48-mdns7：主 socket 周期重建（先建后关，新旧重叠交接） ---

type fakeMdnsReadConn struct {
	mu      sync.Mutex
	packets [][]byte
	readErr error
	closed  bool
	wake    chan struct{}
}

func newFakeMdnsReadConn() *fakeMdnsReadConn {
	return &fakeMdnsReadConn{wake: make(chan struct{}, 1)}
}

func (f *fakeMdnsReadConn) signalLocked() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *fakeMdnsReadConn) enqueuePacket(b []byte) {
	f.mu.Lock()
	f.packets = append(f.packets, b)
	f.signalLocked()
	f.mu.Unlock()
}

func (f *fakeMdnsReadConn) enqueueMsg(t *testing.T, m *dns.Msg) {
	t.Helper()
	b, err := m.Pack()
	if err != nil {
		t.Fatalf("打包测试 mDNS 消息失败: %v", err)
	}
	f.enqueuePacket(b)
}

func (f *fakeMdnsReadConn) setReadError(err error) {
	f.mu.Lock()
	f.readErr = err
	f.signalLocked()
	f.mu.Unlock()
}

func (f *fakeMdnsReadConn) ReadFrom(b []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	for {
		f.mu.Lock()
		if f.readErr != nil {
			err := f.readErr
			f.readErr = nil
			f.mu.Unlock()
			return 0, nil, nil, err
		}
		if f.closed {
			f.mu.Unlock()
			return 0, nil, nil, net.ErrClosed
		}
		if len(f.packets) > 0 {
			p := f.packets[0]
			f.packets = f.packets[1:]
			f.mu.Unlock()
			return copy(b, p), nil, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}, nil
		}
		f.mu.Unlock()
		<-f.wake
	}
}

func (f *fakeMdnsReadConn) Close() error {
	f.mu.Lock()
	if !f.closed {
		f.closed = true
		f.signalLocked()
	}
	f.mu.Unlock()
	return nil
}

func (f *fakeMdnsReadConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func mdnsTestAdvertise(inst, svcType string) *dns.Msg {
	m := new(dns.Msg)
	svcFQDN := svcType + ".local."
	instFQDN := inst + "." + svcFQDN
	host := inst + ".local."
	m.Answer = append(m.Answer, &dns.PTR{
		Hdr: dns.RR_Header{Name: svcFQDN, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 120},
		Ptr: instFQDN,
	})
	m.Extra = append(m.Extra,
		&dns.SRV{Hdr: dns.RR_Header{Name: instFQDN, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 120}, Port: 5555, Target: host},
		&dns.A{Hdr: dns.RR_Header{Name: host, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120}, A: net.ParseIP("192.168.31.2")},
	)
	return m
}

func waitMdnsStateEntries(t *testing.T, st *mdnsListenState, want int) []MdnsService {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		st.mu.Lock()
		snap := st.snapshotLocked()
		st.mu.Unlock()
		if len(snap) >= want {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %d 个 mDNS 条目超时，当前快照=%+v", want, snap)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMdnsLoopOwnerAdopt(t *testing.T) {
	oldC := newFakeMdnsReadConn()
	o := newMdnsLoopOwner(oldC)
	gen1 := o.currentGen()
	if !o.isCurrent(gen1) {
		t.Fatalf("初始代应为当前活跃代: gen=%d", gen1)
	}

	newC := newFakeMdnsReadConn()
	old, gen2 := o.adopt(newC)
	if old != oldC || gen2 == gen1 {
		t.Fatalf("adopt 应返回旧 socket 并递增代数: old==oldC=%v gen2=%d gen1=%d", old == oldC, gen2, gen1)
	}
	if o.isCurrent(gen1) || !o.isCurrent(gen2) {
		t.Fatalf("adopt 后旧代应失效、新代应生效: gen1=%d gen2=%d", gen1, gen2)
	}

	o.closeCurrent()
	if !newC.isClosed() {
		t.Fatal("closeCurrent 应关闭当前新 socket")
	}
	if oldC.isClosed() {
		t.Fatal("closeCurrent 不应误关已被替换的旧 socket")
	}
}

func TestMdnsReadLoopOldExitIgnoredCurrentExitReported(t *testing.T) {
	oldC := newFakeMdnsReadConn()
	newC := newFakeMdnsReadConn()
	o := newMdnsLoopOwner(oldC)
	errCh := make(chan error, 1)
	st := newMdnsListenState(nil, nil, nil)
	startMdnsReadLoop(o, oldC, o.currentGen(), st, errCh)

	_, gen2 := o.adopt(newC)
	startMdnsReadLoop(o, newC, gen2, st, errCh)

	oldC.Close() // 旧 loop 退出：代数不匹配 → 不能上报，A 层不退出。
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-errCh:
		t.Fatalf("旧 loop 退出不应触发 A 层退出: %v", err)
	default:
	}

	wantErr := errors.New("new socket broken")
	newC.setReadError(wantErr) // 当前活跃代 loop 意外退出 → 必须上报，A 层退出。
	select {
	case err := <-errCh:
		if !errors.Is(err, wantErr) {
			t.Fatalf("当前 loop 退出上报错误不符: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("当前活跃 loop 意外退出未上报")
	}
}

func TestMdnsRebuildSuccessKeepsStateAndNewSocketFeedsSameState(t *testing.T) {
	oldC := newFakeMdnsReadConn()
	newC := newFakeMdnsReadConn()
	o := newMdnsLoopOwner(oldC)
	errCh := make(chan error, 1)
	var evs []MdnsTrackEvents
	st := newMdnsListenState(func(ev MdnsTrackEvents) { evs = append(evs, ev) }, nil, nil)
	st.initial()
	startMdnsReadLoop(o, oldC, o.currentGen(), st, errCh)

	// 旧 socket 先收到一个服务，建立 state 条目。
	oldC.enqueueMsg(t, mdnsTestAdvertise("adb-old", "_adb._tcp"))
	waitMdnsStateEntries(t, st, 1)

	// 重建成功：先建新（adopt+新 loop），后关旧。
	old, ifaces, err := mdnsRebuildMainSocket(o, st, errCh, func() (mdnsReadConn, []net.Interface, error) {
		return newC, []net.Interface{{Name: "Ethernet", Flags: net.FlagUp | net.FlagMulticast}}, nil
	})
	if err != nil {
		t.Fatalf("重建不应失败: %v", err)
	}
	if old != oldC {
		t.Fatal("重建应返回被替换的旧 socket")
	}
	if ifaces != 1 {
		t.Fatalf("重建成功应上报接口数 1，得到 %d", ifaces)
	}
	if !oldC.isClosed() {
		t.Fatal("重建成功必须先建新后关旧：旧 socket 应已关闭")
	}
	if newC.isClosed() {
		t.Fatal("重建后新 socket 应保持打开")
	}

	// state 条目保留：旧服务仍在。
	snap := waitMdnsStateEntries(t, st, 1)
	if snap[0].Name != "adb-old" {
		t.Fatalf("重建后旧 socket 之前解析的条目应保留: %+v", snap)
	}

	// 新 socket 收到的包进入同一个 state：两个条目并存。
	newC.enqueueMsg(t, mdnsTestAdvertise("adb-new", "_adb._tcp"))
	snap = waitMdnsStateEntries(t, st, 2)
	names := map[string]bool{}
	for _, s := range snap {
		names[s.Name] = true
	}
	if !names["adb-old"] || !names["adb-new"] {
		t.Fatalf("新旧 socket 的条目应共享同一 state: %+v", snap)
	}

	select {
	case err := <-errCh:
		t.Fatalf("重建成功过程中 A 层不应退出: %v", err)
	default:
	}
	o.closeCurrent()
	if len(evs) == 0 {
		t.Fatal("state 应正常产生事件")
	}
}

func TestMdnsRebuildFailureKeepsOldSocket(t *testing.T) {
	oldC := newFakeMdnsReadConn()
	o := newMdnsLoopOwner(oldC)
	errCh := make(chan error, 1)
	st := newMdnsListenState(nil, nil, nil)
	startMdnsReadLoop(o, oldC, o.currentGen(), st, errCh)

	wantErr := errors.New("listen 5353 failed")
	old, ifaces, err := mdnsRebuildMainSocket(o, st, errCh, func() (mdnsReadConn, []net.Interface, error) {
		return nil, nil, wantErr
	})
	if !errors.Is(err, wantErr) || old != nil || ifaces != 0 {
		t.Fatalf("重建失败返回不符: old=%v ifaces=%d err=%v", old, ifaces, err)
	}
	if oldC.isClosed() {
		t.Fatal("重建失败必须保留旧 socket")
	}
	o.mu.Lock()
	stillCurrent := o.current == oldC
	o.mu.Unlock()
	if !stillCurrent {
		t.Fatal("重建失败后 owner 当前 socket 应仍是旧 socket")
	}
	select {
	case err := <-errCh:
		t.Fatalf("重建失败不应触发 A 层退出: %v", err)
	default:
	}

	// 旧 socket 仍是当前活跃 socket：其意外退出仍必须上报（A 层退出语义保留）。
	readErr := errors.New("old socket broken")
	oldC.setReadError(readErr)
	select {
	case err := <-errCh:
		if !errors.Is(err, readErr) {
			t.Fatalf("保留的旧 socket 意外退出上报错误不符: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("保留的旧 socket 作为当前活跃 socket，意外退出未上报")
	}
}
