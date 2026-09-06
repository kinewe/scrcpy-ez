package adb

import (
	"context"
	"net"
	"reflect"
	"sort"
	"strconv"
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
// 监听注册通告/Goodbye/周期通告；TTL=0（Goodbye）立即 stale。
//
// Windows 组播监听长时间运行可能「掉组」（socket 仍在但收不到组播包）——低频
// 兜底用 QU（unicast response）查询：查询 QCLASS 带 0x8000 位，设备单播应答，
// 不依赖组播组；60s 周期查询 < 90s 无信号窗口，掉组时也不会误 stale。
const (
	mdnsColdStartQueryOnce = true
	// mdnsSelfCheckTimeout 是启动自检等待应答窗口。
	mdnsSelfCheckTimeout = 2 * time.Second
)

var (
	mdnsIdleProbeAfter = 90 * time.Second
	// mdnsQueryInterval 是周期 QU 查询间隔（< 90s 无信号窗口）。
	mdnsQueryInterval = 60 * time.Second
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

// mdnsPeriodicQueryTypes 是 60s 周期 QU 查询的服务类型（只查连接类服务）。
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

// selectLanInterfaces 筛出物理局域网接口；候选为空返回空（上层回退全接口）。
func selectLanInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	all := make([]mdnsIface, 0, len(ifaces))
	for _, ifi := range ifaces {
		addrs, _ := ifi.Addrs()
		all = append(all, mdnsIface{iface: ifi, addrs: addrs})
	}
	return filterLanInterfaces(all)
}

func filterLanInterfaces(all []mdnsIface) []net.Interface {
	var out []net.Interface
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
		out = append(out, ifi)
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
		ip := ipn.IP.To4()
		if ip == nil || ip.IsLoopback() || (ip[0] == 169 && ip[1] == 254) {
			continue
		}
		return true
	}
	return false
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
	ifaces := selectLanInterfaces()
	if len(ifaces) == 0 {
		ifaces = listMulticastInterfaces()
	}
	for i := range ifaces {
		_ = p4.JoinGroup(&ifaces[i], mdnsUDPAddr)
	}
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
	fq := strings.TrimSuffix(strings.TrimSuffix(name, "."), ".local")
	suffix := "." + serviceType
	if strings.HasSuffix(strings.ToLower(fq), strings.ToLower(suffix)) {
		return fq[:len(fq)-len(suffix)]
	}
	return fq
}

// mdnsRecordKey 是条目活性 key：type|instance。
func mdnsRecordKey(s MdnsService) string {
	return s.Type + "|" + s.Name
}

// --- 事件状态机 ---

type mdnsListenEntry struct {
	svc       MdnsService
	idleTimer *time.Timer // 90s 无信号 → onIdleProbe；upsert 重置
}

type mdnsListenState struct {
	mu          sync.Mutex
	entries     map[string]*mdnsListenEntry
	last        []MdnsService
	firstSent   bool
	coldDone    bool
	sawSignal   bool // 启动自检/诊断：是否收到过任何有效 mDNS 信号
	onEvents    func(MdnsTrackEvents)
	onIdleProbe func(addr string)
	queryFn     func(serviceType string)
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

// periodicQuery 每 60s 对连接类服务发 QU 查询（掉组兜底；单播应答不依赖组播组）。
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
func (s *mdnsListenState) selfCheck() {
	time.Sleep(mdnsSelfCheckTimeout)
	if s.sawAnySignal() {
		bridge.DebugLog("[app] mDNS 自检：ok")
		return
	}
	bridge.DebugLog("[app] mDNS 自检：limited（2s 内无应答——防火墙/网段限制可能导致 mDNS 不可用）")
}

func (s *mdnsListenState) initial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstSent {
		return
	}
	s.firstSent = true
	s.last = nil
	if s.onEvents != nil {
		s.onEvents(MdnsTrackEvents{First: true, Snapshot: nil})
	}
}

func (s *mdnsListenState) upsert(svc MdnsService, now time.Time, force bool) {
	_ = now
	s.mu.Lock()
	key := mdnsRecordKey(svc)
	e := s.entries[key]
	if e == nil {
		e = &mdnsListenEntry{}
		s.entries[key] = e
	}
	if e.idleTimer != nil {
		e.idleTimer.Stop()
	}
	e.svc = svc
	// 每次收到该服务信号 → 重置 90s timer（重新计数）
	e.idleTimer = time.AfterFunc(mdnsIdleProbeAfter, func() { s.idleProbe(key) })
	s.emitLocked(force)
	s.mu.Unlock()
}

func (s *mdnsListenState) gone(svc MdnsService) {
	s.mu.Lock()
	key := mdnsRecordKey(svc)
	e := s.entries[key]
	if e == nil {
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
func (s *mdnsListenState) idleProbe(key string) {
	s.mu.Lock()
	e := s.entries[key]
	if e == nil {
		s.mu.Unlock()
		return
	}
	addr := e.svc.Addr
	if addr == "" {
		addr = e.svc.Name
	}
	// 触发后立即重计数；即使 connect 探测还未返回也不额外推断。
	if e.idleTimer != nil {
		e.idleTimer.Stop()
	}
	e.idleTimer = time.AfterFunc(mdnsIdleProbeAfter, func() { s.idleProbe(key) })
	s.mu.Unlock()

	if s.onIdleProbe != nil {
		s.onIdleProbe(addr)
	}
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

func (s *mdnsListenState) emitLocked(force bool) {
	snap := s.snapshotLocked()
	if !force && reflect.DeepEqual(s.last, snap) {
		return
	}
	added, removed := DiffMdnsServices(s.last, snap)
	if force {
		// QU 应答=「服务确认在」：内容未变也必须通知 app 层翻 active。
		added = snap
		removed = nil
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
				ptrs[strings.ToLower(v.Hdr.Name)] = v
			case *dns.SRV:
				srvs[strings.ToLower(v.Hdr.Name)] = v
			case *dns.A:
				as[strings.ToLower(v.Hdr.Name)] = v.A
			}
		}
	}
	return
}

func (s *mdnsListenState) processMessage(msg *dns.Msg, fromQuery bool) {
	ptrs, srvs, as := mdnsCollectRecords(msg)
	if len(ptrs) > 0 || len(srvs) > 0 {
		s.mu.Lock()
		s.sawSignal = true
		s.mu.Unlock()
	}
	now := time.Now()

	seen := map[string]bool{}
	for _, ptr := range ptrs {
		svcType, ok := mdnsServiceTypeForName(ptr.Hdr.Name)
		if !ok {
			continue
		}
		instance := mdnsInstanceForName(ptr.Ptr, svcType)
		if instance == "" {
			continue
		}
		key := svcType + "|" + instance
		if seen[key] {
			continue
		}
		seen[key] = true
		if ptr.Hdr.Ttl == 0 {
			s.gone(MdnsService{Type: svcType, Name: instance, Mode: MdnsModeOf(svcType)})
			continue
		}
		srv, ok := srvs[strings.ToLower(ptr.Ptr)]
		if !ok {
			continue
		}
		s.upsert(mdnsServiceFromRecords(svcType, instance, srv, as), now, fromQuery)
	}
	// SRV 直接兜底（无 PTR 的周期通告/应答）
	for _, srv := range srvs {
		svcType, ok := mdnsServiceTypeForName(srv.Hdr.Name)
		if !ok {
			continue
		}
		instance := mdnsInstanceForName(srv.Hdr.Name, svcType)
		if instance == "" {
			continue
		}
		key := svcType + "|" + instance
		if seen[key] {
			continue
		}
		seen[key] = true
		if srv.Hdr.Ttl == 0 {
			s.gone(MdnsService{Type: svcType, Name: instance, Mode: MdnsModeOf(svcType)})
			continue
		}
		s.upsert(mdnsServiceFromRecords(svcType, instance, srv, as), now, fromQuery)
	}
}

func mdnsServiceFromRecords(svcType, instance string, srv *dns.SRV, as map[string]net.IP) MdnsService {
	s := MdnsService{Type: svcType, Name: instance, Mode: MdnsModeOf(svcType)}
	if ip, ok := as[strings.ToLower(srv.Target)]; ok && ip.To4() != nil {
		s.Addr = net.JoinHostPort(ip.To4().String(), strconv.Itoa(int(srv.Port)))
	}
	return s
}

// --- 主循环 ---

// BrowseMdns 监听 mDNS 组播（自建监听），阻塞直到 ctx 取消。
// 冷启动发一轮 PTR 查询（一次）后旁听；对外仍发 MdnsTrackEvents 全量快照。
// onIdleProbe：条目 90s 无新信号时回调（地址或实例名；由上层 connect 硬判定）。
func (m *Manager) BrowseMdns(ctx context.Context, onEvents func(MdnsTrackEvents), onIdleProbe func(string)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p4, _, err := listenMdnsSocket()
	if err != nil {
		return err
	}

	// QU 查询走专用高位 socket（0.0.0.0:0 独占端口）：Windows 上多个进程绑定
	// 5353 时单播应答只投递给其中一个——从 5353 发 QU 会收不到应答。专用
	// socket 的单播应答独占接收，不依赖组播组。
	queryConn, err := listenMdnsQuerySocket()
	if err != nil {
		_ = p4.Close()
		return err
	}
	defer queryConn.Close()

	queryFn := mdnsQueryFnFor(queryConn)
	state := newMdnsListenState(onEvents, onIdleProbe, queryFn)
	state.coldStartQuery()
	state.initial()
	go state.selfCheck() // 异步自检，不阻塞启动

	owner := newMdnsLoopOwner(p4)
	errCh := make(chan error, 1)
	startMdnsReadLoop(owner, p4, owner.currentGen(), state, errCh)
	go func() {
		if err := readMdnsUDPLoop(queryConn, state); err != nil {
			bridge.DebugLog("[app] mDNS queryConn 读取退出: %v", err)
		}
	}()
	queryTick := time.NewTicker(mdnsQueryInterval)
	defer queryTick.Stop()
	rebuildTick := time.NewTicker(mdnsRebuildInterval)
	defer rebuildTick.Stop()
	for {
		select {
		case <-ctx.Done():
			owner.closeCurrent()
			_ = queryConn.Close()
			return ctx.Err()
		case err := <-errCh:
			owner.closeCurrent() // 当前活跃 socket 已异常退出；保留 defer 语义清理。
			return err
		case <-queryTick.C:
			state.periodicQuery()
		case <-rebuildTick.C:
			mdnsRebuildMainSocket(owner, state, errCh, listenMdnsMainSocketForRebuild)
		}
	}
}

// listenMdnsQuerySocket 创建 QU 查询专用 UDP socket（内核分配高位端口，独占）。
func listenMdnsQuerySocket() (net.PacketConn, error) {
	lc := net.ListenConfig{Control: reuseAddrControl}
	return lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:0")
}

// mdnsQueryFnFor 构造查询发送函数：QU 包固定从 queryConn（高位独占 socket）发出。
func mdnsQueryFnFor(queryConn net.PacketConn) func(serviceType string) {
	return func(serviceType string) {
		msg := mdnsPtrQuery(serviceType)
		b, err := msg.Pack()
		if err != nil {
			return
		}
		_, _ = queryConn.WriteTo(b, mdnsUDPAddr)
	}
}

func readMdnsUDPLoop(pc net.PacketConn, state *mdnsListenState) error {
	buf := make([]byte, 1500)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		msg := new(dns.Msg)
		if err := msg.Unpack(buf[:n]); err != nil {
			continue
		}
		state.processMessage(msg, true) // QU 单播应答 → forced emit
	}
}

// mdnsReadConn 是主组播 socket 的读/关接口；*ipv4.PacketConn 天然满足，
// 测试可用 fake 实现（重建交接语义单测）。
type mdnsReadConn interface {
	ReadFrom(b []byte) (n int, cm *ipv4.ControlMessage, src net.Addr, err error)
	Close() error
}

// mdnsLoopOwner 跟踪「当前活跃主 socket」的代数：重建成功时先 adopt 新 socket
// 再关旧 socket；旧 loop 退出时因代数不匹配被忽略，只有当前活跃 loop 意外退出
// 才会上报（保持 A 层意外退出语义）。
type mdnsLoopOwner struct {
	mu      sync.Mutex
	current mdnsReadConn
	gen     int64
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
	return old, o.gen
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
	go func() {
		err := readMdnsLoop(conn, state)
		if err != nil && owner.isCurrent(gen) {
			errCh <- err
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
	buf := make([]byte, 1500)
	for {
		n, _, _, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		msg := new(dns.Msg)
		if err := msg.Unpack(buf[:n]); err != nil {
			continue
		}
		state.processMessage(msg, false) // 组播事件 → DeepEqual 短路保持
	}
}
