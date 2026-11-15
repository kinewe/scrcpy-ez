package adb

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type mdnsTestPacketConn struct {
	mu           sync.Mutex
	local        net.Addr
	packets      [][]byte
	readErr      error
	writeErr     error
	reads        int
	queries      []string
	destinations []string
	closed       bool
	wake         chan struct{}
}

func newMdnsTestPacketConn(bind string) *mdnsTestPacketConn {
	addr, _ := net.ResolveUDPAddr("udp4", bind)
	return &mdnsTestPacketConn{local: addr, wake: make(chan struct{}, 1)}
}

func (c *mdnsTestPacketConn) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *mdnsTestPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	for {
		c.mu.Lock()
		if c.readErr != nil {
			err := c.readErr
			c.mu.Unlock()
			return 0, nil, err
		}
		if c.closed {
			c.mu.Unlock()
			return 0, nil, net.ErrClosed
		}
		if len(c.packets) > 0 {
			packet := c.packets[0]
			c.packets = c.packets[1:]
			c.mu.Unlock()
			return copy(b, packet), mdnsUDPAddr, nil
		}
		c.mu.Unlock()
		<-c.wake
	}
}

func (c *mdnsTestPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	msg := new(dns.Msg)
	if err := msg.Unpack(b); err != nil {
		return 0, err
	}
	destination, valid := addr.(*net.UDPAddr)
	if !valid || destination.Port != 5353 || destination.IP.To4() == nil || len(msg.Question) != 1 || msg.Question[0].Qclass&0x8000 == 0 {
		return 0, errors.New("invalid QU destination or question")
	}
	c.queries = append(c.queries, msg.Question[0].Name)
	c.destinations = append(c.destinations, addr.String())
	return len(b), nil
}

func (c *mdnsTestPacketConn) directDestinations() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, destination := range c.destinations {
		if destination != mdnsUDPAddr.String() {
			out = append(out, destination)
		}
	}
	return out
}

func (c *mdnsTestPacketConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.signal()
	return nil
}
func (c *mdnsTestPacketConn) LocalAddr() net.Addr              { return c.local }
func (c *mdnsTestPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *mdnsTestPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *mdnsTestPacketConn) SetWriteDeadline(time.Time) error { return nil }

func (c *mdnsTestPacketConn) stats() (int, []string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads, append([]string(nil), c.queries...), c.closed
}

func (c *mdnsTestPacketConn) failRead(err error) {
	c.mu.Lock()
	c.readErr = err
	c.mu.Unlock()
	c.signal()
}

func (c *mdnsTestPacketConn) enqueue(b []byte) {
	c.mu.Lock()
	c.packets = append(c.packets, b)
	c.mu.Unlock()
	c.signal()
}

func mdnsEventually(t *testing.T, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !pred() {
		if time.Now().After(deadline) {
			t.Fatal("mDNS condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func mdnsTestTarget(ip string, index int) mdnsQueryTarget {
	mi := mdnsTestIface("Ethernet", "00:11:22:33:44:55", ip, net.FlagUp|net.FlagMulticast)
	mi.iface.Index = index
	return mdnsQueryTarget{bind: net.JoinHostPort(ip, "0"), iface: mi.iface,
		network: &net.IPNet{IP: net.ParseIP(ip).To4(), Mask: net.CIDRMask(24, 32)}}
}

func mdnsTestQueries(t *testing.T, opts mdnsBrowseOptions) *mdnsQuerySockets {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	state := newMdnsListenState(nil, nil, nil)
	q := newMdnsQuerySockets(ctx, state, opts)
	t.Cleanup(func() { cancel(); state.stop(); q.close() })
	return q
}

func mdnsAssertInitialQueries(t *testing.T, c *mdnsTestPacketConn) {
	t.Helper()
	_, queries, closed := c.stats()
	if closed || len(queries) != len(mdnsListenServiceTypes) {
		t.Fatalf("new socket should immediately query every service: queries=%v closed=%v", queries, closed)
	}
	for i, serviceType := range mdnsListenServiceTypes {
		if queries[i] != serviceType+".local." {
			t.Fatalf("missing connection or pairing query: %v", queries)
		}
	}
}

func TestMdnsQUReaderFailureSameAddressRepairs(t *testing.T) {
	target := mdnsTestTarget("192.0.2.10", 10)
	opts := defaultMdnsBrowseOptions()
	opts.retryMin, opts.retryMax = 10*time.Millisecond, 40*time.Millisecond
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{target}, nil }
	var sockets []*mdnsTestPacketConn
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		c := newMdnsTestPacketConn(target.bind)
		sockets = append(sockets, c)
		return c, nil
	}
	q := mdnsTestQueries(t, opts)
	now := time.Now()
	q.refreshTargets()
	q.reconcile(now)
	old := q.active[target.key()]
	mdnsAssertInitialQueries(t, sockets[0])
	sockets[0].failRead(errors.New("injected read failure"))
	mdnsEventually(t, old.failed.Load)
	q.reconcile(now)
	if q.active[target.key()] != nil {
		t.Fatal("failed receiver must leave the send set")
	}
	q.reconcile(now.Add(9 * time.Millisecond))
	if q.active[target.key()] != nil {
		t.Fatal("repair bypassed retry backoff")
	}
	q.reconcile(now.Add(10 * time.Millisecond))
	replacement := q.active[target.key()]
	if replacement == nil || replacement == old {
		t.Fatal("unchanged address must not retain a failed receiver")
	}
	mdnsAssertInitialQueries(t, replacement.conn.(*mdnsTestPacketConn))
	// A late error from a retired receiver cannot invalidate its replacement.
	q.failure(old, errors.New("late old failure"))
	q.reconcile(now.Add(20 * time.Millisecond))
	if q.active[target.key()] != replacement || replacement.failed.Load() {
		t.Fatal("old reader failure affected the replacement")
	}
}

func TestMdnsQUWriteFailureClosesReaderAndRepairs(t *testing.T) {
	target := mdnsTestTarget("192.0.2.10", 10)
	opts := defaultMdnsBrowseOptions()
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{target}, nil }
	calls := 0
	var first *mdnsTestPacketConn
	opts.listenQuery = func(_ context.Context, t mdnsQueryTarget) (net.PacketConn, error) {
		c := newMdnsTestPacketConn(t.bind)
		if t.key() == target.key() {
			calls++
			if calls == 1 {
				c.writeErr = errors.New("injected send failure")
				first = c
			}
		}
		return c, nil
	}
	q := mdnsTestQueries(t, opts)
	now := time.Now()
	q.refreshTargets()
	q.reconcile(now)
	q.reconcile(now)
	_, _, closed := first.stats()
	if !closed {
		t.Fatal("write failure must close and unblock the old receiver")
	}
	q.reconcile(now.Add(opts.retryMin))
	if calls != 2 {
		t.Fatalf("send failure did not retry: attempts=%d", calls)
	}
	mdnsAssertInitialQueries(t, q.active[target.key()].conn.(*mdnsTestPacketConn))
}

func TestMdnsQUPartialBindFailureKeepsHealthySocketAndBacksOff(t *testing.T) {
	a, b := mdnsTestTarget("192.0.2.10", 10), mdnsTestTarget("198.51.100.10", 20)
	opts := defaultMdnsBrowseOptions()
	opts.retryMin, opts.retryMax = 10*time.Millisecond, 20*time.Millisecond
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{a, b}, nil }
	attempts := map[string]int{}
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		attempts[target.key()]++
		if target.key() == b.key() && attempts[target.key()] < 4 {
			return nil, errors.New("adapter temporarily unavailable")
		}
		return newMdnsTestPacketConn(target.bind), nil
	}
	q := mdnsTestQueries(t, opts)
	now := time.Now()
	q.refreshTargets()
	q.reconcile(now)
	healthy := q.active[a.key()]
	for _, elapsed := range []time.Duration{0, 9, 10, 29, 30, 49, 50} {
		q.reconcile(now.Add(elapsed * time.Millisecond))
	}
	if attempts[a.key()] != 1 || attempts[b.key()] != 4 || len(q.active) != 2 || q.active[a.key()] != healthy {
		t.Fatalf("partial repair replaced healthy socket or ignored backoff: attempts=%v active=%d", attempts, len(q.active))
	}
	mdnsAssertInitialQueries(t, healthy.conn.(*mdnsTestPacketConn))
	mdnsAssertInitialQueries(t, q.active[b.key()].conn.(*mdnsTestPacketConn))
}

func TestMdnsQUAddressAndInterfaceChangesQueryImmediately(t *testing.T) {
	for _, change := range []string{"IP", "interface-index", "MAC"} {
		t.Run(change, func(t *testing.T) {
			oldTarget := mdnsTestTarget("192.0.2.10", 10)
			newTarget := oldTarget
			switch change {
			case "IP":
				newTarget.bind = "192.0.2.20:0"
			case "interface-index":
				newTarget.iface.Index = 20
			case "MAC":
				newTarget.iface.HardwareAddr, _ = net.ParseMAC("00:11:22:33:44:66")
			}
			targets := []mdnsQueryTarget{oldTarget}
			opts := defaultMdnsBrowseOptions()
			opts.queryTargets = func() ([]mdnsQueryTarget, error) { return targets, nil }
			opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
				return newMdnsTestPacketConn(target.bind), nil
			}
			q := mdnsTestQueries(t, opts)
			q.refreshTargets()
			q.reconcile(time.Now())
			old := q.active[oldTarget.key()].conn.(*mdnsTestPacketConn)
			targets = []mdnsQueryTarget{newTarget}
			if !q.refreshTargets() {
				t.Fatal("adapter change was not detected")
			}
			q.reconcile(time.Now())
			_, _, closed := old.stats()
			if !closed || len(q.active) != 1 {
				t.Fatal("removed adapter socket was not retired")
			}
			mdnsAssertInitialQueries(t, q.active[newTarget.key()].conn.(*mdnsTestPacketConn))
		})
	}
}

func TestMdnsQUTargetOrderAndEnumerationFailurePreserveSockets(t *testing.T) {
	a, b := mdnsTestTarget("192.0.2.10", 10), mdnsTestTarget("198.51.100.10", 20)
	targets := []mdnsQueryTarget{a, b}
	var enumerateErr error
	opts := defaultMdnsBrowseOptions()
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return targets, enumerateErr }
	count := 0
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		count++
		return newMdnsTestPacketConn(target.bind), nil
	}
	q := mdnsTestQueries(t, opts)
	q.refreshTargets()
	q.reconcile(time.Now())
	targets = []mdnsQueryTarget{b, a}
	if q.refreshTargets() {
		t.Fatal("enumeration order alone must not replace sockets")
	}
	q.reconcile(time.Now())
	enumerateErr = errors.New("transient enumeration failure")
	targets = nil
	q.refreshTargets()
	q.reconcile(time.Now())
	if count != 2 || len(q.active) != 2 {
		t.Fatal("enumeration failure discarded known working adapters")
	}
}

func TestMdnsQUOfflineFallbackThenLANRecovery(t *testing.T) {
	target := mdnsTestTarget("192.0.2.10", 10)
	var targets []mdnsQueryTarget
	bindFails := false
	opts := defaultMdnsBrowseOptions()
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return targets, nil }
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		if bindFails && target.bind != mdnsQueryFallback {
			return nil, errors.New("concrete bind failure")
		}
		return newMdnsTestPacketConn(target.bind), nil
	}
	q := mdnsTestQueries(t, opts)
	now := time.Now()
	q.refreshTargets()
	q.reconcile(now)
	fallbackKey := (mdnsQueryTarget{bind: mdnsQueryFallback}).key()
	fallback := q.active[fallbackKey]
	mdnsAssertInitialQueries(t, fallback.conn.(*mdnsTestPacketConn))
	targets, bindFails = []mdnsQueryTarget{target}, true
	q.refreshTargets()
	q.reconcile(now)
	if len(q.active) != 1 || q.active[fallbackKey] != fallback {
		t.Fatal("failed LAN bind must preserve the working wildcard fallback")
	}
	bindFails = false
	q.reconcile(now.Add(opts.retryMin))
	_, _, closed := fallback.conn.(*mdnsTestPacketConn).stats()
	if !closed || len(q.active) != 1 || q.active[target.key()] == nil {
		t.Fatal("LAN recovery must replace wildcard fallback")
	}
	mdnsAssertInitialQueries(t, q.active[target.key()].conn.(*mdnsTestPacketConn))
	// Disconnect again: keep the loop alive and install a fresh fallback.
	targets = nil
	q.refreshTargets()
	q.reconcile(now.Add(2 * opts.retryMin))
	if len(q.active) != 1 || q.active[fallbackKey] == nil {
		t.Fatal("disconnect did not return to wildcard discovery")
	}
}

func TestMdnsQueryTargetsMultipleIPsAndFiltering(t *testing.T) {
	flags := net.FlagUp | net.FlagMulticast
	lan := mdnsTestIface("Ethernet", "00:11:22:33:44:55", "192.0.2.10", flags)
	lan.iface.Index = 10
	for _, ip := range []string{"192.0.2.11", "192.0.2.10", "169.254.1.1", "0.0.0.0", "224.0.0.251", "127.0.0.1", "2001:db8::1"} {
		lan.addrs = append(lan.addrs, &net.IPNet{IP: net.ParseIP(ip)})
	}
	virtual := mdnsTestIface("ZeroTier One", "02:00:00:aa:bb:cc", "10.0.0.1", flags)
	targets := mdnsQueryTargetsFrom([]mdnsIface{virtual, lan, lan})
	if len(targets) != 2 || targets[0].bind != "192.0.2.10:0" || targets[1].bind != "192.0.2.11:0" {
		t.Fatalf("invalid, virtual or duplicate bind targets: %+v", targets)
	}
	if targets[0].iface.Index != 10 {
		t.Fatal("multicast egress interface identity was lost")
	}
}

func TestMdnsQUSilentSocketRenewalKeepsOldOnFailure(t *testing.T) {
	target := mdnsTestTarget("192.0.2.10", 10)
	opts := defaultMdnsBrowseOptions()
	opts.querySocketMaxAge = time.Minute
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{target}, nil }
	attempts := 0
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		attempts++
		if attempts == 2 {
			return nil, errors.New("renewal bind failure")
		}
		return newMdnsTestPacketConn(target.bind), nil
	}
	q := mdnsTestQueries(t, opts)
	now := time.Now()
	q.refreshTargets()
	q.reconcile(now)
	old := q.active[target.key()]
	q.reconcile(now.Add(time.Minute))
	_, _, closed := old.conn.(*mdnsTestPacketConn).stats()
	if closed || q.active[target.key()] != old {
		t.Fatal("renewal failure discarded the still-working old receiver")
	}
	q.reconcile(now.Add(time.Minute + opts.retryMin/2))
	if attempts != 2 {
		t.Fatal("renewal ignored retry backoff")
	}
	q.reconcile(now.Add(time.Minute + opts.retryMin))
	_, _, closed = old.conn.(*mdnsTestPacketConn).stats()
	if attempts != 3 || !closed || q.active[target.key()] == old {
		t.Fatal("silent socket did not renew after the backoff")
	}
	mdnsAssertInitialQueries(t, q.active[target.key()].conn.(*mdnsTestPacketConn))
}

func TestMdnsQUOversizedAndMalformedDatagrams(t *testing.T) {
	state := newMdnsListenState(nil, nil, nil)
	defer state.stop()
	c := newMdnsTestPacketConn("192.0.2.10:40001")
	defer c.Close()
	msg := mdnsTestAdvertise("adb-large", "_adb._tcp")
	for i := 0; i < 12; i++ {
		msg.Extra = append(msg.Extra, &dns.TXT{Hdr: dns.RR_Header{Name: "large.local.", Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 120}, Txt: []string{string(make([]byte, 200))}})
	}
	packet, err := msg.Pack()
	if err != nil || len(packet) <= 1500 {
		t.Fatalf("invalid large DNS fixture: size=%d err=%v", len(packet), err)
	}
	c.enqueue([]byte("malformed DNS"))
	c.enqueue(packet)
	done := make(chan error, 1)
	go func() { done <- readMdnsDatagrams(c, func(msg *dns.Msg) { state.processMessage(msg, true) }) }()
	waitMdnsStateEntries(t, state, 1)
	c.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("unexpected read exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read loop leaked after closing socket")
	}
}
