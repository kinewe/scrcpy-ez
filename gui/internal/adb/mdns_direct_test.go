package adb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestMdnsRefreshRetainsEveryHostAndNewWindowContext(t *testing.T) {
	ctx := context.Background()
	r := &mdnsRefreshRequests{owner: ctx, wake: make(chan struct{}, 1)}
	old, cancel := context.WithCancel(ctx)
	target := MdnsRefreshTarget{IP: "192.0.2.100", Source: "192.0.2.1"}
	r.request(old, target)
	r.request(ctx, target)
	cancel()
	for i := 2; i < 22; i++ {
		if !r.request(ctx, MdnsRefreshTarget{IP: fmt.Sprintf("192.0.2.%d", i), Source: target.Source}) {
			t.Fatal("live host rejected")
		}
	}
	seen := map[MdnsRefreshTarget]bool{}
	for r.pending() {
		ok, hosts := r.take()
		if !ok || len(hosts) == 0 || len(hosts) > mdnsRefreshHostBatch {
			t.Fatalf("invalid batch: accepted=%v hosts=%d", ok, len(hosts))
		}
		for _, host := range hosts {
			if host.ctx.Err() != nil || seen[host.target] {
				t.Fatal("old cancellation leaked into new request or duplicate host")
			}
			seen[host.target] = true
		}
	}
	if len(seen) != 21 || !seen[target] {
		t.Fatalf("burst lost hosts: %v", seen)
	}
	for _, invalid := range []MdnsRefreshTarget{
		{IP: "bad", Source: "192.0.2.1"}, {IP: "192.0.2.2:5555", Source: "192.0.2.1"},
		{IP: "127.0.0.1", Source: "192.0.2.1"}, {IP: "224.0.0.251", Source: "192.0.2.1"},
		{IP: "192.0.2.1", Source: "192.0.2.1"}, {IP: "192.0.2.2", Source: "0.0.0.0"},
	} {
		if r.request(ctx, invalid) {
			t.Fatalf("invalid target accepted: %+v", invalid)
		}
	}
	closed, closeWindow := context.WithCancel(ctx)
	r.request(closed, target)
	closeWindow()
	if ok, hosts := r.take(); ok || len(hosts) != 0 {
		t.Fatal("closed window retained directed requests")
	}
}

func TestMdnsDirectedQueryUsesCurrentPhysicalSourceAndSubnet(t *testing.T) {
	ctx := context.Background()
	a, b := mdnsTestTarget("192.0.2.1", 1), mdnsTestTarget("198.51.100.1", 2)
	opts := defaultMdnsBrowseOptions()
	opts.directTimeout = 20 * time.Millisecond
	var targets = []mdnsQueryTarget{a, b}
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return targets, nil }
	conns := map[string]*mdnsTestPacketConn{}
	opts.listenQuery = func(_ context.Context, target mdnsQueryTarget) (net.PacketConn, error) {
		conn := newMdnsTestPacketConn(target.bind)
		conns[target.bind] = conn
		return conn, nil
	}
	var mu sync.Mutex
	var dialed []MdnsRefreshTarget
	opts.listenDirect = func(_ context.Context, host MdnsRefreshTarget) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, host)
		mu.Unlock()
		return &mdnsDirectResponder{mdnsTestPacketConn: newMdnsTestPacketConn(net.JoinHostPort(host.Source, "0")), remote: &net.UDPAddr{IP: net.ParseIP(host.IP), Port: 5353}}, nil
	}
	dialCount := func() int { mu.Lock(); defer mu.Unlock(); return len(dialed) }
	q := mdnsTestQueries(t, opts)
	q.refreshTargets()
	q.reconcile(time.Now())
	send := func(ip, source string) {
		q.sendHost(mdnsHostRefresh{ctx: ctx, target: MdnsRefreshTarget{IP: ip, Source: source}})
	}
	send("192.0.2.159", "192.0.2.1")
	mdnsEventually(t, func() bool { return dialCount() == 1 })
	mu.Lock()
	first := dialed[0]
	mu.Unlock()
	if first.IP != "192.0.2.159" || first.Source != "192.0.2.1" {
		t.Fatalf("wrong directed route: %+v", first)
	}
	send("192.0.2.159", "198.51.100.1")
	send("192.0.2.255", "192.0.2.1")
	send("192.0.2.0", "192.0.2.1")
	send("192.0.2.1", "192.0.2.1")
	// Same interface/IP with a narrower mask: use current metadata, not the
	// mask stored when the healthy socket was opened.
	a.network = &net.IPNet{IP: net.ParseIP("192.0.2.1").To4(), Mask: net.CIDRMask(25, 32)}
	targets = []mdnsQueryTarget{a, b}
	q.refreshTargets()
	send("192.0.2.159", "192.0.2.1")
	if dialCount() != 1 {
		t.Fatal("off-subnet, local, broadcast or wrong-source query escaped")
	}
	// Old source disappears: the still-open socket cannot send a stale request.
	targets = []mdnsQueryTarget{b}
	q.refreshTargets()
	send("192.0.2.20", "192.0.2.1")
	if dialCount() != 1 {
		t.Fatal("retired source used for directed query")
	}
}

func TestMdnsRefreshBoundsQueuedHosts(t *testing.T) {
	ctx := context.Background()
	r := &mdnsRefreshRequests{owner: ctx, wake: make(chan struct{}, 1)}
	var hosts []MdnsRefreshTarget
	for i := 0; i < mdnsRefreshHostLimit; i++ {
		hosts = append(hosts, MdnsRefreshTarget{IP: fmt.Sprintf("10.1.%d.%d", i/256, i%256), Source: "192.0.2.1"})
	}
	if !r.request(ctx, hosts...) {
		t.Fatal("bounded full batch rejected")
	}
	extra := MdnsRefreshTarget{IP: "10.2.1.1", Source: "192.0.2.1"}
	if r.request(ctx, extra) {
		t.Fatal("queue exceeded host limit")
	}
	r.take()
	if !r.request(ctx, extra) {
		t.Fatal("drained queue did not accept a new host")
	}
}

func TestMdnsDirectedWindowCancelClosesPeerWithoutStoppingBrowse(t *testing.T) {
	opts := defaultMdnsBrowseOptions()
	opts.directTimeout = time.Hour
	target := mdnsTestTarget("192.0.2.1", 1)
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{target}, nil }
	peer := newMdnsTestPacketConn(target.bind)
	opts.listenDirect = func(context.Context, MdnsRefreshTarget) (net.Conn, error) {
		return &mdnsDirectResponder{mdnsTestPacketConn: peer, remote: &net.UDPAddr{IP: net.ParseIP("192.0.2.159"), Port: 5353}}, nil
	}
	q := mdnsTestQueries(t, opts)
	q.refreshTargets() // Directed discovery also works while QU sockets are down.
	window, closeWindow := context.WithCancel(context.Background())
	if !q.sendHost(mdnsHostRefresh{ctx: window, target: MdnsRefreshTarget{IP: "192.0.2.159", Source: "192.0.2.1"}}) {
		t.Fatal("request not dispatched")
	}
	mdnsEventually(t, func() bool { _, queries, _ := peer.stats(); return len(queries) == 3 })
	closeWindow()
	mdnsEventually(t, func() bool { _, _, closed := peer.stats(); return closed && len(q.directSlots) == 0 })
	if q.ctx.Err() != nil {
		t.Fatal("closing the window canceled the global browse")
	}
}

type mdnsDirectResponder struct {
	*mdnsTestPacketConn
	onWrite func([]byte, net.Addr) error
	remote  *net.UDPAddr
}

type mdnsReadTimeout struct{}

func (mdnsReadTimeout) Error() string   { return "injected UDP timeout" }
func (mdnsReadTimeout) Timeout() bool   { return true }
func (mdnsReadTimeout) Temporary() bool { return true }

type mdnsFirstTimeoutConn struct {
	*mdnsDirectResponder
	first bool
}

func (c *mdnsFirstTimeoutConn) Read(b []byte) (int, error) {
	if !c.first {
		c.first = true
		time.Sleep(10 * time.Millisecond)
		return 0, mdnsReadTimeout{}
	}
	return c.mdnsDirectResponder.Read(b)
}

func TestMdnsDirectedRetriesDroppedFirstQuestions(t *testing.T) {
	opts := defaultMdnsBrowseOptions()
	opts.directTimeout = 100 * time.Millisecond
	opts.directRetryInterval = 5 * time.Millisecond
	peer := newMdnsTestPacketConn("192.0.2.1:0")
	writes := 0
	responder := &mdnsDirectResponder{mdnsTestPacketConn: peer, remote: &net.UDPAddr{IP: net.ParseIP("192.0.2.159"), Port: 5353}}
	responder.onWrite = func(b []byte, _ net.Addr) error {
		writes++
		if writes == 5 {
			packet, _ := mdnsTestAdvertise("adb-RETRY", "_adb._tcp").Pack()
			peer.enqueue(packet)
		}
		return nil
	}
	opts.listenDirect = func(context.Context, MdnsRefreshTarget) (net.Conn, error) {
		return &mdnsFirstTimeoutConn{mdnsDirectResponder: responder}, nil
	}
	q := mdnsTestQueries(t, opts)
	q.queryHost(mdnsHostRefresh{ctx: context.Background(), target: MdnsRefreshTarget{IP: "192.0.2.159", Source: "192.0.2.1"}})
	q.state.mu.Lock()
	snapshot := q.state.snapshotLocked()
	q.state.mu.Unlock()
	if writes != 6 || len(snapshot) != 1 || snapshot[0].Name != "adb-RETRY" {
		t.Fatalf("dropped first round didn't recover: writes=%d snapshot=%v", writes, snapshot)
	}
	_, _, closed := peer.stats()
	if !closed {
		t.Fatal("retry socket leaked")
	}
}

func (c *mdnsDirectResponder) Read(b []byte) (int, error)  { n, _, err := c.ReadFrom(b); return n, err }
func (c *mdnsDirectResponder) Write(b []byte) (int, error) { return c.WriteTo(b, c.remote) }
func (c *mdnsDirectResponder) RemoteAddr() net.Addr        { return c.remote }

func (c *mdnsDirectResponder) WriteTo(b []byte, addr net.Addr) (int, error) {
	if c.onWrite != nil {
		if err := c.onWrite(b, addr); err != nil {
			return 0, err
		}
	}
	return c.mdnsTestPacketConn.WriteTo(b, addr)
}

func TestBrowseMdnsDirectedResponseAndPeerFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	opts := fastMdnsBrowseOptions()
	opts.refreshMinInterval = 20 * time.Millisecond
	opts.directTimeout = 30 * time.Millisecond
	target := mdnsTestTarget("192.0.2.1", 1)
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{target}, nil }
	opts.listenMain = func() (mdnsReadConn, []net.Interface, error) { return newFakeMdnsReadConn(), nil, nil }
	conn := newMdnsTestPacketConn(target.bind)
	var opens, peerFailures atomic.Int32
	var directWrites atomic.Int32
	var directMu sync.Mutex
	var directConns []*mdnsTestPacketConn
	opts.listenDirect = func(_ context.Context, host MdnsRefreshTarget) (net.Conn, error) {
		direct := newMdnsTestPacketConn(net.JoinHostPort(host.Source, "0"))
		directMu.Lock()
		directConns = append(directConns, direct)
		directMu.Unlock()
		responder := &mdnsDirectResponder{mdnsTestPacketConn: direct, remote: &net.UDPAddr{IP: net.ParseIP(host.IP), Port: 5353}}
		responder.onWrite = func(b []byte, addr net.Addr) error {
			if addr.String() == "192.0.2.136:5353" {
				peerFailures.Add(1)
				return errors.New("peer unreachable")
			}
			directWrites.Add(1)
			if addr.String() != "192.0.2.159:5353" {
				return nil // No response to multicast: the field failure from rc.2.
			}
			query := new(dns.Msg)
			query.Unpack(b)
			if query.Question[0].Name == "_adb._tcp.local." {
				msg := mdnsTestAdvertise("adb-PHONE", "_adb._tcp")
				for _, rr := range msg.Extra {
					if a, ok := rr.(*dns.A); ok {
						a.A = net.ParseIP("192.0.2.159")
					}
				}
				packet, _ := msg.Pack()
				direct.enqueue(packet)
			}
			return nil
		}
		return responder, nil
	}
	opts.listenQuery = func(context.Context, mdnsQueryTarget) (net.PacketConn, error) { opens.Add(1); return conn, nil }
	m := &Manager{}
	events := make(chan MdnsTrackEvents, 20)
	done := make(chan error, 1)
	go func() { done <- m.browseMdns(ctx, func(ev MdnsTrackEvents) { events <- ev }, nil, opts) }()
	mdnsEventually(t, func() bool { _, queries, _ := conn.stats(); return len(queries) == 3 })
	hosts := []MdnsRefreshTarget{{IP: "192.0.2.136", Source: "192.0.2.1"}, {IP: "192.0.2.159", Source: "192.0.2.1"}}
	for i := 140; i < 158; i++ {
		hosts = append(hosts, MdnsRefreshTarget{IP: fmt.Sprintf("192.0.2.%d", i), Source: "192.0.2.1"})
	}
	if !m.RequestMdnsRefresh(ctx, hosts...) {
		t.Fatal("directed refresh rejected")
	}
	mdnsEventually(t, func() bool {
		select {
		case ev := <-events:
			return len(ev.Snapshot) == 1 && ev.Snapshot[0].Name == "adb-PHONE" && ev.Snapshot[0].Addr == "192.0.2.159:5555"
		default:
			return false
		}
	})
	mdnsEventually(t, func() bool { return directWrites.Load() == int32(3*(len(hosts)-1)) })
	if peerFailures.Load() != 1 || opens.Load() != 1 {
		t.Fatal("one peer failure rebuilt the shared socket or prevented the next host")
	}
	cancel()
	waitMdnsBrowseStopped(t, done)
	directMu.Lock()
	defer directMu.Unlock()
	for _, c := range directConns {
		_, _, closed := c.stats()
		if !closed {
			t.Fatal("directed socket leaked on browse cancellation")
		}
	}
}
