package adb

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"

	"scrcpy-ez/gui/internal/bridge"
)

const mdnsQueryFallback = "0.0.0.0:0"

// Include interface identity: a new adapter can reuse the same IPv4 address.
type mdnsQueryTarget struct {
	bind    string
	iface   net.Interface
	network *net.IPNet
}

func (t mdnsQueryTarget) key() string {
	return strconv.Itoa(t.iface.Index) + "|" + t.iface.HardwareAddr.String() + "|" + t.bind
}

func mdnsQueryTargetsFrom(all []mdnsIface) []mdnsQueryTarget {
	byKey := map[string]mdnsQueryTarget{}
	for _, mi := range filterLanIfaceAddrs(all) {
		for _, a := range mi.addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || !usableMdnsIPv4(ipn.IP) {
				continue
			}
			t := mdnsQueryTarget{bind: net.JoinHostPort(ipn.IP.To4().String(), "0"), iface: mi.iface,
				network: &net.IPNet{IP: append(net.IP(nil), ipn.IP.To4()...), Mask: append(net.IPMask(nil), ipn.Mask...)}}
			byKey[t.key()] = t
		}
	}
	out := make([]mdnsQueryTarget, 0, len(byKey))
	for _, t := range byKey {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func collectMdnsQueryTargets() ([]mdnsQueryTarget, error) {
	all, err := collectMdnsIfaceAddrs()
	if err != nil {
		return nil, err
	}
	return mdnsQueryTargetsFrom(all), nil
}

func listenMdnsQuerySocket(ctx context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
	lc := net.ListenConfig{}
	pc, err := lc.ListenPacket(ctx, "udp4", target.bind)
	if err != nil {
		return nil, err
	}
	p4 := ipv4.NewPacketConn(pc)
	// Binding a source IP and selecting a multicast egress interface are separate
	// operations. Do both, so VPN route metrics cannot choose the QU egress.
	if target.iface.Index != 0 {
		if err := p4.SetMulticastInterface(&target.iface); err != nil {
			pc.Close()
			return nil, fmt.Errorf("mDNS multicast interface %s: %w", target.iface.Name, err)
		}
	}
	if err := p4.SetMulticastTTL(255); err != nil {
		pc.Close()
		return nil, err
	}
	return pc, nil
}

type mdnsRetry struct {
	delay time.Duration
	next  time.Time
}

func (r *mdnsRetry) fail(now time.Time, min, max time.Duration) {
	if r.delay == 0 {
		r.delay = min
	} else if r.delay < max {
		r.delay *= 2
	}
	if r.delay > max {
		r.delay = max
	}
	r.next = now.Add(r.delay)
}

type mdnsQuerySocket struct {
	target    mdnsQueryTarget
	conn      net.PacketConn
	opened    time.Time
	failed    atomic.Bool
	retired   atomic.Bool
	received  atomic.Bool
	closeOnce sync.Once
}

func (s *mdnsQuerySocket) close() {
	s.closeOnce.Do(func() { _ = s.conn.Close() })
}

func (s *mdnsQuerySocket) retire() {
	s.retired.Store(true)
	s.close()
}

// All maps belong to the browse goroutine. Readers only mark their own socket
// failed and signal wake; no error can be lost when the wake channel is full.
type mdnsQuerySockets struct {
	ctx                 context.Context
	state               *mdnsListenState
	targets             func() ([]mdnsQueryTarget, error)
	listen              func(context.Context, mdnsQueryTarget) (net.PacketConn, error)
	desired             map[string]mdnsQueryTarget
	active              map[string]*mdnsQuerySocket
	retry               map[string]*mdnsRetry
	wake                chan struct{}
	readers             sync.WaitGroup
	retryMin            time.Duration
	retryMax            time.Duration
	maxAge              time.Duration
	directListen        func(context.Context, MdnsRefreshTarget) (net.Conn, error)
	directTimeout       time.Duration
	directRetryInterval time.Duration
	directSlots         chan struct{}
	directReaders       sync.WaitGroup
}

func newMdnsQuerySockets(ctx context.Context, state *mdnsListenState, opts mdnsBrowseOptions) *mdnsQuerySockets {
	fallback := mdnsQueryTarget{bind: mdnsQueryFallback}
	return &mdnsQuerySockets{
		ctx: ctx, state: state, targets: opts.queryTargets, listen: opts.listenQuery,
		desired: map[string]mdnsQueryTarget{fallback.key(): fallback},
		active:  map[string]*mdnsQuerySocket{}, retry: map[string]*mdnsRetry{},
		wake: make(chan struct{}, 1), retryMin: opts.retryMin, retryMax: opts.retryMax,
		maxAge:       opts.querySocketMaxAge,
		directListen: opts.listenDirect, directTimeout: opts.directTimeout, directSlots: make(chan struct{}, mdnsRefreshHostBatch),
		directRetryInterval: opts.directRetryInterval,
	}
}

func (q *mdnsQuerySockets) refreshTargets() bool {
	targets, err := q.targets()
	if err != nil {
		// An enumeration error is not evidence that the old adapters disappeared.
		bridge.DebugLog("[app] mdns 网卡枚举失败（保留旧连接）：%v", err)
		return false
	}
	if len(targets) == 0 {
		targets = []mdnsQueryTarget{{bind: mdnsQueryFallback}}
	}
	desired := make(map[string]mdnsQueryTarget, len(targets))
	for _, target := range targets {
		desired[target.key()] = target
	}
	changed := len(desired) != len(q.desired)
	for key := range desired {
		if _, ok := q.desired[key]; !ok {
			changed = true
		}
	}
	q.desired = desired
	return changed
}

func (q *mdnsQuerySockets) failure(s *mdnsQuerySocket, err error) {
	if s.retired.Load() || s.failed.Swap(true) {
		return
	}
	bridge.DebugLog("[app] mdns QU socket 失效：local=%v err=%v", s.conn.LocalAddr(), err)
	s.close() // Unblock a reader after a write failure, too.
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *mdnsQuerySockets) start(s *mdnsQuerySocket) {
	q.readers.Add(1)
	go func() {
		defer q.readers.Done()
		err := readMdnsDatagrams(s.conn, func(msg *dns.Msg) {
			if !s.retired.Load() && !s.failed.Load() && q.ctx.Err() == nil {
				s.received.Store(true)
				q.state.processMessage(msg, true)
			}
		})
		if err != nil {
			q.failure(s, err)
		}
	}()
}

func (q *mdnsQuerySockets) retryFor(key string) *mdnsRetry {
	r := q.retry[key]
	if r == nil {
		r = &mdnsRetry{}
		q.retry[key] = r
	}
	return r
}

func (q *mdnsQuerySockets) open(target mdnsQueryTarget, now time.Time) {
	key := target.key()
	old := q.active[key]
	if q.ctx.Err() != nil || (old != nil && now.Sub(old.opened) < q.maxAge) {
		return
	}
	r := q.retryFor(key)
	if now.Before(r.next) {
		return
	}
	pc, err := q.listen(q.ctx, target)
	if err != nil {
		r.fail(now, q.retryMin, q.retryMax)
		bridge.DebugLog("[app] mdns QU socket 创建失败：%s retry=%v err=%v", target.bind, r.delay, err)
		return
	}
	s := &mdnsQuerySocket{target: target, conn: pc, opened: now}
	q.active[key] = s
	q.start(s) // Install the receiver before sending, including on a replacement.
	bridge.DebugLog("[app] mdns QU socket 就绪：local=%v iface=%s", pc.LocalAddr(), target.iface.Name)
	for _, serviceType := range mdnsListenServiceTypes {
		q.sendTo(s, serviceType)
	}
	if old != nil {
		// Periodically renew even a silent, apparently healthy UDP socket after
		// suspend/NIC resets. Keep the old one if creating/sending on the new
		// socket failed, and honor backoff before trying another replacement.
		if s.failed.Load() && !old.failed.Load() {
			q.active[key] = old
			s.retire()
			r.fail(now, q.retryMin, q.retryMax)
		} else {
			old.retire()
		}
	}
}

func (q *mdnsQuerySockets) reconcile(now time.Time) {
	fallback := mdnsQueryTarget{bind: mdnsQueryFallback}
	for key, s := range q.active {
		_, wanted := q.desired[key]
		if s.failed.Load() {
			r := q.retryFor(key)
			if s.received.Load() || now.Sub(s.opened) >= q.retryMax {
				*r = mdnsRetry{}
			}
			r.fail(now, q.retryMin, q.retryMax)
			s.retire()
			delete(q.active, key)
		} else if !wanted && key != fallback.key() {
			s.retire()
			delete(q.active, key)
		} else if now.Sub(s.opened) < q.maxAge && (s.received.Load() || now.Sub(s.opened) >= q.retryMax) {
			delete(q.retry, key)
		}
	}
	keys := make([]string, 0, len(q.desired))
	for key := range q.desired {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		q.open(q.desired[key], now)
	}
	physicalActive := false
	for _, s := range q.active {
		if s.target.bind != mdnsQueryFallback && !s.failed.Load() {
			physicalActive = true
		}
	}
	if physicalActive {
		if s := q.active[fallback.key()]; s != nil {
			s.retire()
			delete(q.active, fallback.key())
		}
		delete(q.retry, fallback.key())
	} else {
		// Keep discovery available when every concrete bind temporarily fails.
		q.open(fallback, now)
	}
	for key := range q.retry {
		if _, wanted := q.desired[key]; !wanted && key != fallback.key() {
			delete(q.retry, key)
		}
	}
}

func writeMdnsQuery(conn net.PacketConn, serviceType string) error {
	return writeMdnsQueryTo(conn, serviceType, mdnsUDPAddr)
}

func writeMdnsQueryTo(conn net.PacketConn, serviceType string, addr *net.UDPAddr) error {
	b, err := mdnsPtrQuery(serviceType).Pack()
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		return err
	}
	_, err = conn.WriteTo(b, addr)
	return err
}

// False asks the owner to retain this request for a later batch.
func (q *mdnsQuerySockets) sendHost(host mdnsHostRefresh) bool {
	if host.ctx.Err() != nil || q.ctx.Err() != nil {
		return true
	}
	ip := net.ParseIP(host.target.IP).To4()
	for _, target := range q.desired {
		if target.network != nil && ip.Equal(target.network.IP) {
			return true // Another local interface is not a remote device.
		}
	}
	for _, target := range q.desired {
		if target.bind != net.JoinHostPort(host.target.Source, "0") || target.network == nil || !target.network.Contains(ip) {
			continue
		}
		ones, bits := target.network.Mask.Size()
		if bits != 32 || ones < 8 || ones > 31 {
			continue
		}
		networkIP := ip.Mask(target.network.Mask)
		broadcast := append(net.IP(nil), networkIP...)
		for i := range broadcast {
			broadcast[i] |= ^target.network.Mask[i]
		}
		if ones <= 30 && (ip.Equal(networkIP) || ip.Equal(broadcast)) {
			continue
		}
		select {
		case q.directSlots <- struct{}{}:
		default:
			return false
		}
		q.directReaders.Add(1)
		go func() {
			defer q.directReaders.Done()
			defer func() { <-q.directSlots }()
			q.queryHost(host)
		}()
		return true
	}
	return true
}

func (q *mdnsQuerySockets) sendTo(s *mdnsQuerySocket, serviceType string) {
	if !s.failed.Load() && !s.retired.Load() && q.ctx.Err() == nil {
		if err := writeMdnsQuery(s.conn, serviceType); err != nil {
			q.failure(s, err)
		}
	}
}

func (q *mdnsQuerySockets) send(serviceType string) {
	for _, s := range q.active {
		q.sendTo(s, serviceType)
	}
}

func (q *mdnsQuerySockets) close() {
	for _, s := range q.active {
		s.retire()
	}
	q.readers.Wait()
	q.directReaders.Wait()
}

// Keep the small sender helper for tests and callers with fixed socket sets.
func mdnsQueryFnFor(conns ...net.PacketConn) func(string) {
	return func(serviceType string) {
		for _, conn := range conns {
			if conn != nil {
				_ = writeMdnsQuery(conn, serviceType)
			}
		}
	}
}

func readMdnsDatagrams(pc net.PacketConn, onMessage func(*dns.Msg)) error {
	// A valid DNS UDP datagram may exceed the Ethernet MTU. In particular,
	// Windows reports WSAEMSGSIZE when a 1500-byte receive buffer truncates it.
	buf := make([]byte, 65535)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		msg := new(dns.Msg)
		if err := msg.Unpack(buf[:n]); err != nil {
			continue
		}
		onMessage(msg)
	}
}
