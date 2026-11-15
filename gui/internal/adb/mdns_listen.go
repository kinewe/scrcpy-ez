package adb

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"

	"scrcpy-ez/gui/internal/bridge"
)

// 自建 mDNS 监听层（gui48-mdns6）：SO_REUSEADDR + JoinGroup(224.0.0.251:5353) +
// 物理网卡绑定（gui48-mdns3 selectLanInterfaces 逻辑并入）。事件驱动为主：
// 监听注册通告/Goodbye/周期通告；Goodbye 移除候选，上层核验实际通道。
//
// Windows 组播监听长时间运行可能「掉组」（socket 仍在但收不到组播包）——低频
// 兜底用 QU（unicast response）查询：查询 QCLASS 带 0x8000 位，设备单播应答，
// 不依赖组播组；5s 查询补偿设备回网通告丢失，在线状态仍由上层验身决定。
const (
	mdnsColdStartQueryOnce = true
	// mdnsSelfCheckTimeout 是启动自检等待应答窗口。
	mdnsSelfCheckTimeout = 2 * time.Second
)

var (
	mdnsIdleProbeAfter = 90 * time.Second
	// mdnsQueryInterval 是周期 QU 查询间隔（补偿通告丢失）。
	mdnsQueryInterval = 5 * time.Second
	// mdnsRebuildInterval 是主组播 socket 周期重建间隔（Windows 掉组自愈）。
	mdnsRebuildInterval = 60 * time.Second
)

var mdnsGroupIPv4 = net.IPv4(224, 0, 0, 251)
var mdnsUDPAddr = &net.UDPAddr{IP: mdnsGroupIPv4, Port: 5353}

var mdnsListenServiceTypes = []string{
	"_adb-tls-connect._tcp",
	"_adb._tcp",
	"_adb-tls-pairing._tcp",
}

// mdnsPeriodicQueryTypes 是周期 QU 查询的服务类型（只查连接类服务）。
var mdnsPeriodicQueryTypes = []string{
	"_adb-tls-connect._tcp",
	"_adb._tcp",
}

// mdnsIface 是 selectLanInterfaces 的测试友好输入。
type mdnsIface struct {
	iface net.Interface
	addrs []net.Addr
}

var mdnsVirtualNameParts = []string{
	"vEthernet", "WSL", "ZeroTier", "Tailscale", "TAP", "TUN", "Bluetooth",
	"Loopback", "vbox", "vmnet", "VMware", "Hyper-V", "Docker", "vpn", "utun",
}

var mdnsVirtualMacPrefixes = []string{
	"00:15:5D", // Hyper-V / WSL
	"00:50:56", // VMware
	"00:0C:29", // VMware
	"00:1C:42", // Parallels
	"02:00:00", // Tailscale
}

// collectLanIfaceAddrs 收集本机接口及其地址（selectLanInterfaces / QU 绑定共用）。
func collectLanIfaceAddrs() []mdnsIface {
	all, _ := collectMdnsIfaceAddrs()
	return all
}

func collectMdnsIfaceAddrs() ([]mdnsIface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	all := make([]mdnsIface, 0, len(ifaces))
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || isMdnsVirtualIface(ifi.Name, ifi.HardwareAddr) {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			return nil, err
		}
		all = append(all, mdnsIface{iface: ifi, addrs: addrs})
	}
	return all, nil
}

// selectLanInterfaces 筛出物理局域网接口；候选为空返回空（上层回退全接口）。
func selectLanInterfaces() []net.Interface {
	return filterLanInterfaces(collectLanIfaceAddrs())
}

// lanQueryBindAddrsFrom 返回 QU 查询/接收应绑定的 LAN IPv4 地址（纯函数，便于单测）：
// 每个物理 LAN 网卡取可用 IPv4（排除 loopback/链路本地），去重保序。
//
// 为什么必须绑定具体地址：QU 查询是从本机发往 224.0.0.251:5353 的组播包。socket 若绑
// 0.0.0.0，Windows 会按组播路由 metric 选出口——多网卡（VPN/ZeroTier 等）主机上可能从
// 错误网卡发出，设备根本收不到查询（实测 ZeroTier metric 291 < WLAN 296 时查询全从
// ZeroTier 出去 → mDNS 自检 2s limited、无线地址学不到）。
func lanQueryBindAddrsFrom(all []mdnsIface) []string {
	seen := map[string]bool{}
	var out []string
	for _, mi := range filterLanIfaceAddrs(all) {
		for _, a := range mi.addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if !usableMdnsIPv4(ipn.IP) {
				continue
			}
			s := ipn.IP.To4().String()
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// filterLanIfaceAddrs 与 filterLanInterfaces 同判据，但保留接口地址（QU 绑定需要）。
func filterLanIfaceAddrs(all []mdnsIface) []mdnsIface {
	var out []mdnsIface
	for _, mi := range all {
		ifi := mi.iface
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		if isMdnsVirtualIface(ifi.Name, ifi.HardwareAddr) {
			continue
		}
		if !hasUsableIPv4(mi.addrs) {
			continue
		}
		out = append(out, mi)
	}
	return out
}

func filterLanInterfaces(all []mdnsIface) []net.Interface {
	kept := filterLanIfaceAddrs(all)
	out := make([]net.Interface, 0, len(kept))
	for _, mi := range kept {
		out = append(out, mi.iface)
	}
	return out
}

func isMdnsVirtualIface(name string, hw net.HardwareAddr) bool {
	for _, p := range mdnsVirtualNameParts {
		if strings.Contains(strings.ToLower(name), strings.ToLower(p)) {
			return true
		}
	}
	mac := strings.ToUpper(hw.String())
	if mac == "" || allZeroBytes(hw) {
		return true
	}
	for _, p := range mdnsVirtualMacPrefixes {
		if strings.HasPrefix(mac, p) {
			return true
		}
	}
	return false
}

func allZeroBytes(hw net.HardwareAddr) bool {
	for _, b := range hw {
		if b != 0 {
			return false
		}
	}
	return true
}

func hasUsableIPv4(addrs []net.Addr) bool {
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if usableMdnsIPv4(ipn.IP) {
			return true
		}
	}
	return false
}

func usableMdnsIPv4(ip net.IP) bool {
	ip = ip.To4()
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast()
}

func listMulticastInterfaces() []net.Interface {
	var out []net.Interface
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagMulticast != 0 {
			out = append(out, ifi)
		}
	}
	return out
}

// --- socket ---

func reuseAddrControl(network, address string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = setSockoptReuseAddr(fd)
	}); err != nil {
		return err
	}
	return sockErr
}

func listenMdnsSocket() (*ipv4.PacketConn, []net.Interface, error) {
	lc := net.ListenConfig{Control: reuseAddrControl}
	pc, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:5353")
	if err != nil {
		return nil, nil, err
	}
	p4 := ipv4.NewPacketConn(pc)
	// 读包路径不使用 ControlMessage（ReadFrom 的 OOB 返回值未被消费）；
	// 旧版 x/net 在 Windows 上 SetControlMessage 是 stub（not implemented），
	// 因此不调用——JoinGroup/ReadFrom 在 Windows 实测正常。
	all, err := collectMdnsIfaceAddrs()
	if err != nil {
		_ = p4.Close()
		return nil, nil, err
	}
	ifaces := filterLanInterfaces(all)
	if len(ifaces) == 0 {
		ifaces = listMulticastInterfaces()
	}
	names := make([]string, 0, len(ifaces))
	joined := 0
	for i := range ifaces {
		names = append(names, ifaces[i].Name)
		if err := p4.JoinGroup(&ifaces[i], mdnsUDPAddr); err != nil {
			bridge.DebugLog("[app] mdns 组播加入失败：iface=%s err=%v", ifaces[i].Name, err)
		} else {
			joined++
		}
	}
	if joined == 0 {
		_ = p4.Close()
		return nil, nil, fmt.Errorf("mDNS could not join any multicast interface (%d candidates)", len(ifaces))
	}
	bridge.DebugLog("[app] mdns 主 socket 就绪：ifaces=%v", names)
	return p4, ifaces, nil
}

// mdnsMainSocketListenFn 是主 socket 重建的监听函数签名；生产用
// listenMdnsMainSocketForRebuild，测试注入 fake 覆盖成功/失败路径。
type mdnsMainSocketListenFn func() (mdnsReadConn, []net.Interface, error)

// listenMdnsMainSocketForRebuild 包装 listenMdnsSocket 以匹配重建监听签名。
func listenMdnsMainSocketForRebuild() (mdnsReadConn, []net.Interface, error) {
	p4, ifaces, err := listenMdnsSocket()
	if err != nil {
		return nil, ifaces, err
	}
	return p4, ifaces, nil
}

// --- DNS helpers（纯函数） ---

// mdnsPtrQuery 构造 QU（unicast response）PTR 查询：
// QCLASS=ClassINET|0x8000——设备单播应答到查询方 socket，不依赖组播组。
func mdnsPtrQuery(serviceType string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(serviceType+".local.", dns.TypePTR)
	m.Question[0].Qclass = dns.ClassINET | 0x8000
	return m
}

// mdnsServiceTypeForName 从 DNS 名（服务类型 FQN 或实例 FQN）识别 adb 服务类型。
func mdnsServiceTypeForName(name string) (string, bool) {
	lower := strings.ToLower(strings.TrimSuffix(name, "."))
	lower = strings.TrimSuffix(lower, ".local")
	for _, st := range mdnsListenServiceTypes {
		if lower == st || strings.HasSuffix(lower, "."+st) {
			return st, true
		}
	}
	return "", false
}

// mdnsInstanceForName 从实例 FQN 剥离服务类型后缀得到实例名。
func mdnsInstanceForName(name, serviceType string) string {
	fq := strings.TrimSuffix(name, ".")
	if strings.HasSuffix(strings.ToLower(fq), ".local") {
		fq = fq[:len(fq)-len(".local")]
	}
	suffix := "." + serviceType
	if strings.HasSuffix(strings.ToLower(fq), strings.ToLower(suffix)) {
		return fq[:len(fq)-len(suffix)]
	}
	return fq
}

// mdnsRecordKey 是条目活性 key：type|instance。
func mdnsRecordKey(s MdnsService) string {
	return strings.ToLower(s.Type + "|" + s.Name)
}

// --- 事件状态机 ---

type mdnsListenEntry struct {
	svc             MdnsService
	idleTimer       *time.Timer // 90s 无信号 → onIdleProbe；upsert 重置
	generation      uint64
	lastEmitted     time.Time
	pendingPresence bool
}

type mdnsListenState struct {
	recordsMu   sync.Mutex // serialize split-packet assembly and candidate publication
	records     mdnsRecordCache
	mu          sync.Mutex
	entries     map[string]*mdnsListenEntry
	last        []MdnsService
	firstSent   bool
	coldDone    bool
	sawSignal   bool // 启动自检/诊断：是否收到过任何有效 mDNS 信号
	onEvents    func(MdnsTrackEvents)
	onIdleProbe func(addr string)
	queryFn     func(serviceType string)
	closed      bool
	callbacks   sync.WaitGroup
}

func newMdnsListenState(onEvents func(MdnsTrackEvents), onIdleProbe func(string), queryFn func(string)) *mdnsListenState {
	return &mdnsListenState{
		entries:     map[string]*mdnsListenEntry{},
		onEvents:    onEvents,
		onIdleProbe: onIdleProbe,
		queryFn:     queryFn,
	}
}

func (s *mdnsListenState) coldStartQuery() {
	s.mu.Lock()
	if s.coldDone {
		s.mu.Unlock()
		return
	}
	s.coldDone = true
	s.mu.Unlock()
	if s.queryFn == nil {
		return
	}
	for _, st := range mdnsListenServiceTypes {
		s.queryFn(st)
	}
}

// periodicQuery 每 5s 对连接类服务发 QU 查询（掉组兜底；单播应答不依赖组播组）。
func (s *mdnsListenState) periodicQuery() {
	if s.queryFn == nil {
		return
	}
	for _, st := range mdnsPeriodicQueryTypes {
		s.queryFn(st)
	}
}

func (s *mdnsListenState) sawAnySignal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawSignal
}

// selfCheck 启动自检（异步，2s 窗口）：QU 有应答或组播有事件 → ok；
// 无任何信号 → limited（防火墙/网段限制可能）。
func (s *mdnsListenState) selfCheck(ctx context.Context) {
	timer := time.NewTimer(mdnsSelfCheckTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	if s.sawAnySignal() {
		bridge.DebugLog("[app] mDNS 自检：ok")
		return
	}
	bridge.DebugLog("[app] mDNS 自检：limited（2s 内无应答——防火墙/网段限制可能导致 mDNS 不可用）")
}

func (s *mdnsListenState) initial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstSent || s.closed {
		return
	}
	s.firstSent = true
	s.last = nil
	if s.onEvents != nil {
		s.onEvents(MdnsTrackEvents{First: true, Snapshot: nil})
	}
}

func (s *mdnsListenState) upsert(svc MdnsService, now time.Time, force bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	key := mdnsRecordKey(svc)
	e := s.entries[key]
	changed := e == nil || e.svc != svc
	if e == nil {
		e = &mdnsListenEntry{}
		s.entries[key] = e
	}
	e.svc = svc
	// 每次收到该服务信号 → 重置 90s timer（重新计数）
	s.resetIdleTimerLocked(key, e)
	// Presence and topology are different evidence: a phone returning with the
	// same endpoint must wake authentication, even though the snapshot is equal.
	// Coalesce bursts, retaining a trailing wakeup instead of dropping it.
	if changed || force || now.Sub(e.lastEmitted) >= mdnsPresenceInterval {
		e.lastEmitted, e.pendingPresence = now, false
		s.emitLocked(true, svc)
	} else {
		e.pendingPresence = true
	}
	s.mu.Unlock()
}

const mdnsPresenceInterval = 2 * time.Second

func (s *mdnsListenState) flushPresence(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	var confirmed []MdnsService
	for _, e := range s.entries {
		if e.pendingPresence && now.Sub(e.lastEmitted) >= mdnsPresenceInterval {
			e.pendingPresence, e.lastEmitted = false, now
			confirmed = append(confirmed, e.svc)
		}
	}
	if len(confirmed) != 0 {
		s.emitLocked(true, confirmed...)
	}
}

func (s *mdnsListenState) gone(svc MdnsService) {
	s.mu.Lock()
	key := mdnsRecordKey(svc)
	e := s.entries[key]
	if e == nil || s.closed {
		s.mu.Unlock()
		return
	}
	if e.idleTimer != nil {
		e.idleTimer.Stop()
	}
	delete(s.entries, key)
	s.emitLocked(false)
	s.mu.Unlock()
}

// idleProbe 是 90s 无信号事件：触发 onIdleProbe 后立即重置 timer 重新计数。
// 结果回来前档案状态保持不变（上层 connect 成功→active / 失败→stale）。
func (s *mdnsListenState) resetIdleTimerLocked(key string, e *mdnsListenEntry) {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
	}
	e.generation++
	generation := e.generation
	e.idleTimer = time.AfterFunc(mdnsIdleProbeAfter, func() { s.idleProbe(key, e, generation) })
}

func (s *mdnsListenState) idleProbe(key string, expected *mdnsListenEntry, generation uint64) {
	s.mu.Lock()
	e := s.entries[key]
	// Stop cannot retract a timer callback that is already queued. Reject old
	// generations, including a removed and re-added entry with the same key.
	if s.closed || e == nil || e != expected || e.generation != generation {
		s.mu.Unlock()
		return
	}
	addr := e.svc.Addr
	if addr == "" {
		addr = e.svc.Name
	}
	// 触发后立即重计数；即使 connect 探测还未返回也不额外推断。
	s.resetIdleTimerLocked(key, e)
	s.callbacks.Add(1)
	s.mu.Unlock()
	defer s.callbacks.Done()

	if s.onIdleProbe != nil {
		s.onIdleProbe(addr)
	}
}

func (s *mdnsListenState) stop() {
	s.mu.Lock()
	s.closed = true
	for _, e := range s.entries {
		if e.idleTimer != nil {
			e.idleTimer.Stop()
		}
	}
	s.mu.Unlock()
	s.callbacks.Wait()
}

func (s *mdnsListenState) snapshotLocked() []MdnsService {
	out := make([]MdnsService, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e.svc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Addr < out[j].Addr
	})
	return out
}

func (s *mdnsListenState) emitLocked(force bool, confirmed ...MdnsService) {
	snap := s.snapshotLocked()
	if !force && reflect.DeepEqual(s.last, snap) {
		return
	}
	added, removed := DiffMdnsServices(s.last, snap)
	if force {
		// Only this response's service is fresh evidence. Replaying the whole
		// cache would revive a dead TLS entry whenever 5555/another host replies.
		added = append([]MdnsService(nil), confirmed...)
	}
	s.last = snap
	if s.onEvents != nil {
		s.onEvents(MdnsTrackEvents{Added: added, Removed: removed, Snapshot: snap, First: false})
	}
}

// --- 消息解析 ---

func mdnsCollectRecords(msg *dns.Msg) (ptrs map[string]*dns.PTR, srvs map[string]*dns.SRV, as map[string]net.IP) {
	ptrs = map[string]*dns.PTR{}
	srvs = map[string]*dns.SRV{}
	as = map[string]net.IP{}
	sections := [][]dns.RR{msg.Answer, msg.Ns, msg.Extra}
	for _, sec := range sections {
		for _, rr := range sec {
			switch v := rr.(type) {
			case *dns.PTR:
				ptrs[strings.ToLower(v.Hdr.Name+"|"+v.Ptr)] = v
			case *dns.SRV:
				srvs[strings.ToLower(v.Hdr.Name)] = v
			case *dns.A:
				as[strings.ToLower(v.Hdr.Name)] = v.A
			}
		}
	}
	return
}

// mdnsReadConn 是主组播 socket 的读/关接口；*ipv4.PacketConn 天然满足，
// 测试可用 fake 实现（重建交接语义单测）。
type mdnsReadConn interface {
	ReadFrom(b []byte) (n int, cm *ipv4.ControlMessage, src net.Addr, err error)
	Close() error
}

// mdnsLoopOwner 跟踪当前主 socket 的代数和读取健康状态。旧代退出不能污染
// 新代；当前代失败既保存在 owner 中，也发出唤醒通知，供浏览循环退避重建。
type mdnsLoopOwner struct {
	mu      sync.Mutex
	current mdnsReadConn
	gen     int64
	readErr error
	readers sync.WaitGroup
}

func newMdnsLoopOwner(c mdnsReadConn) *mdnsLoopOwner {
	return &mdnsLoopOwner{current: c, gen: 1}
}

func (o *mdnsLoopOwner) adopt(c mdnsReadConn) (old mdnsReadConn, gen int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gen++
	old = o.current
	o.current = c
	o.readErr = nil
	return old, o.gen
}

func (o *mdnsLoopOwner) failure() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.readErr
}

func (o *mdnsLoopOwner) isCurrent(gen int64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.gen == gen
}

func (o *mdnsLoopOwner) currentGen() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.gen
}

func (o *mdnsLoopOwner) closeCurrent() {
	o.mu.Lock()
	c := o.current
	o.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// startMdnsReadLoop 启动主 socket 读循环；当前活跃代退出才写 errCh。
func startMdnsReadLoop(owner *mdnsLoopOwner, conn mdnsReadConn, gen int64, state *mdnsListenState, errCh chan<- error) {
	owner.readers.Add(1)
	go func() {
		defer owner.readers.Done()
		err := readMdnsLoop(conn, state)
		owner.mu.Lock()
		current := owner.gen == gen
		if err != nil && current {
			owner.readErr = err
		}
		owner.mu.Unlock()
		if err != nil && current {
			// The owner retains the failure even if an older notification fills
			// the channel; a retired reader must never block shutdown.
			select {
			case errCh <- err:
			default:
			}
		}
	}()
}

// mdnsRebuildMainSocket 执行一次主 socket 重建：先建新 socket；失败保留旧
// socket（A 层继续）；成功则 adopt 新 socket、启动新读循环后再关旧 socket——
// 新旧重叠交接，旧 loop 退出因代数不匹配被忽略。返回值便于测试断言。
func mdnsRebuildMainSocket(owner *mdnsLoopOwner, state *mdnsListenState, errCh chan<- error, listen mdnsMainSocketListenFn) (old mdnsReadConn, nIfaces int, err error) {
	newP4, listenIfaces, rerr := listen()
	if rerr != nil {
		bridge.DebugLog("[app] mdns 主 socket 重建失败（保留旧 socket）：%v", rerr)
		return nil, 0, rerr
	}
	old, gen := owner.adopt(newP4)
	startMdnsReadLoop(owner, newP4, gen, state, errCh)
	if old != nil {
		_ = old.Close() // 旧 loop 因 ReadFrom err 退出；其退出被 owner 代数忽略
		bridge.DebugLog("[app] mdns 旧 socket 已退（重建替换）")
	}
	nIfaces = len(listenIfaces)
	bridge.DebugLog("[app] mdns 主 socket 重建成功（ifaces=%d）", nIfaces)
	return old, nIfaces, nil
}

func readMdnsLoop(conn mdnsReadConn, state *mdnsListenState) error {
	buf := make([]byte, 65535)
	for {
		n, _, _, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		msg := new(dns.Msg)
		if err := msg.Unpack(buf[:n]); err != nil {
			continue
		}
		state.processMessage(msg, false)
	}
}
