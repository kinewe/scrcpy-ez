package adb

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestMdnsRefreshLatestCallerAndCancellation(t *testing.T) {
	owner, stop := context.WithCancel(context.Background())
	defer stop()
	r := &mdnsRefreshRequests{owner: owner, wake: make(chan struct{}, 1)}
	old, cancelOld := context.WithCancel(owner)
	if !r.request(old) || !r.request(owner) {
		t.Fatal("live request rejected")
	}
	cancelOld()
	first, _ := r.take()
	second, _ := r.take()
	if !first || second {
		t.Fatal("old window canceled the new window's request, or request repeated")
	}
	if r.request(old) || r.request(nil) {
		t.Fatal("invalid caller accepted")
	}
	if !r.request(owner) {
		t.Fatal("live request rejected")
	}
	stop()
	accepted, _ := r.take()
	if accepted || r.request(context.Background()) {
		t.Fatal("stopped browse accepted a refresh")
	}
}

func TestBrowseMdnsRefreshUsesExistingQUAndCoalesces(t *testing.T) {
	opts := fastMdnsBrowseOptions()
	opts.interfaceInterval = time.Hour
	opts.refreshMinInterval = 100 * time.Millisecond
	target := mdnsTestTarget("192.0.2.10", 10)
	opts.queryTargets = func() ([]mdnsQueryTarget, error) { return []mdnsQueryTarget{target}, nil }
	var mainOpens, queryOpens atomic.Int32
	opts.listenMain = func() (mdnsReadConn, []net.Interface, error) {
		mainOpens.Add(1)
		return newFakeMdnsReadConn(), nil, nil
	}
	conn := newMdnsTestPacketConn(target.bind)
	opts.listenQuery = func(context.Context, mdnsQueryTarget) (net.PacketConn, error) {
		queryOpens.Add(1)
		return conn, nil
	}
	m := &Manager{}
	if m.RequestMdnsRefresh(context.Background()) {
		t.Fatal("request accepted without an active browse")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	events := make(chan MdnsTrackEvents, 20)
	go func() { done <- m.browseMdns(ctx, func(ev MdnsTrackEvents) { events <- ev }, nil, opts) }()
	count := func() int { _, queries, _ := conn.stats(); return len(queries) }
	mdnsEventually(t, func() bool { return count() == 3 })
	if !m.RequestMdnsRefresh(ctx) {
		t.Fatal("active browse rejected request")
	}
	mdnsEventually(t, func() bool { return count() == 5 })
	_, queries, _ := conn.stats()
	for _, service := range queries[3:] {
		if service != "_adb._tcp.local." && service != "_adb-tls-connect._tcp.local." {
			t.Fatalf("refresh used unexpected service: %s", service)
		}
	}
	window, closeWindow := context.WithCancel(ctx)
	for range 100 {
		if !m.RequestMdnsRefresh(window) {
			t.Fatal("burst request rejected")
		}
	}
	closeWindow()
	time.Sleep(2 * opts.refreshMinInterval)
	if count() != 5 {
		t.Fatal("closing the window did not cancel its delayed refresh")
	}
	for range 100 {
		m.RequestMdnsRefresh(ctx)
	}
	mdnsEventually(t, func() bool { return count() >= 7 })
	time.Sleep(2 * opts.refreshMinInterval)
	if count() > 9 || mainOpens.Load() != 1 || queryOpens.Load() != 1 {
		t.Fatalf("burst flooded queries or rebuilt healthy sockets: queries=%d main=%d QU=%d", count(), mainOpens.Load(), queryOpens.Load())
	}
	packet, err := mdnsTestAdvertise("adb-PHONE", "_adb._tcp").Pack()
	if err != nil {
		t.Fatal(err)
	}
	conn.enqueue(packet)
	mdnsEventually(t, func() bool {
		select {
		case ev := <-events:
			return len(ev.Snapshot) == 1 && ev.Snapshot[0].Name == "adb-PHONE"
		default:
			return false
		}
	})
	cancel()
	waitMdnsBrowseStopped(t, done)
	if m.RequestMdnsRefresh(context.Background()) {
		t.Fatal("stopped browse retained its refresh queue")
	}
}
