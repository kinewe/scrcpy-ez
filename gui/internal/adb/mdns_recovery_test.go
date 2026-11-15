package adb

import (
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestMdnsRecoverySameEndpointAndTrailingPresence(t *testing.T) {
	s := newMdnsListenState(nil, nil, nil)
	defer s.stop()
	events := collectEvents(s)
	tcp := MdnsService{Type: "_adb._tcp", Name: "adb-PHONE", Addr: "192.0.2.2:5555", Mode: MdnsModeTcpip}
	tls := MdnsService{Type: "_adb-tls-connect._tcp", Name: "adb-PHONE-Ab12Cd", Addr: "192.0.2.2:37001", Mode: MdnsModeTls}
	now := time.Now()
	s.upsert(tls, now, false)
	s.upsert(tcp, now, false)
	s.upsert(tcp, now.Add(30*time.Second), false) // return to Wi-Fi, identical cache
	if len(*events) != 3 || !reflect.DeepEqual((*events)[2].Added, []MdnsService{tcp}) {
		t.Fatalf("same-endpoint multicast did not wake only its channel: %+v", *events)
	}
	for i := 1; i <= 100; i++ {
		s.upsert(tcp, now.Add(30*time.Second+time.Duration(i)*time.Millisecond), false)
	}
	if len(*events) != 3 {
		t.Fatalf("burst flooded checks: %d", len(*events))
	}
	s.flushPresence(now.Add(32 * time.Second))
	if len(*events) != 4 || !reflect.DeepEqual((*events)[3].Added, []MdnsService{tcp}) {
		t.Fatal("last burst signal was lost")
	}
	s.gone(tcp)
	s.flushPresence(now.Add(time.Minute))
	if len(*events) != 5 {
		t.Fatal("removed entry replayed a pending signal")
	}
	s.stop()
	s.upsert(tcp, now.Add(time.Hour), true)
	s.flushPresence(now.Add(time.Hour))
	if len(*events) != 5 {
		t.Fatal("presence emitted after shutdown")
	}
}

func recoveryRecords(name, host, ip string, port uint16, ttl uint32) (dns.RR, dns.RR, dns.RR) {
	instance := name + "._adb-tls-connect._tcp.local."
	return &dns.PTR{Hdr: dns.RR_Header{Name: "_adb-tls-connect._tcp.local.", Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: ttl}, Ptr: instance},
		&dns.SRV{Hdr: dns.RR_Header{Name: instance, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: ttl}, Target: host, Port: port},
		&dns.A{Hdr: dns.RR_Header{Name: host, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: net.ParseIP(ip)}
}

func recoverySnapshot(s *mdnsListenState) []MdnsService {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func TestMdnsRecoverySplitRecordsAllOrders(t *testing.T) {
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			s := newMdnsListenState(nil, nil, nil)
			defer s.stop()
			ptr, srv, a := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.2", 37001, 120)
			rrs := []dns.RR{ptr, srv, a}
			for _, i := range order {
				s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{rrs[i]}}, false)
			}
			got := recoverySnapshot(s)
			if len(got) != 1 || got[0].Addr != "192.0.2.2:37001" || got[0].Name != "adb-PHONE-Ab12Cd" {
				t.Fatalf("split records lost: %+v", got)
			}
			// Incomplete announcements cannot erase a usable address.
			s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{srv}}, false)
			if !reflect.DeepEqual(got, recoverySnapshot(s)) {
				t.Fatal("SRV-only renewal damaged complete candidate")
			}
		})
	}
}

func TestMdnsRecoveryMultipleDevicesAndGoodbye(t *testing.T) {
	s := newMdnsListenState(nil, nil, nil)
	defer s.stop()
	p1, s1, a1 := recoveryRecords("adb-ONE-Ab12Cd", "one.local.", "192.0.2.2", 37001, 120)
	p2, s2, a2 := recoveryRecords("adb-TWO-Ab12Cd", "two.local.", "192.0.2.3", 37002, 120)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{p1, p2}, Extra: []dns.RR{s1, s2, a1, a2}}, false)
	if got := recoverySnapshot(s); len(got) != 2 {
		t.Fatalf("multiple PTRs lost a device: %+v", got)
	}
	bye, _, _ := recoveryRecords("adb-ONE-Ab12Cd", "one.local.", "192.0.2.2", 37001, 0)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{bye}}, false)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{a1}}, true)
	if got := recoverySnapshot(s); len(got) != 1 || got[0].Addr != "192.0.2.3:37002" {
		t.Fatalf("A-only packet resurrected a goodbye: %+v", got)
	}
	s.expireRecords(time.Now().Add(2 * time.Hour))
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{a2}}, true)
	if got := recoverySnapshot(s); len(got) != 0 {
		t.Fatalf("expired SRV resurrected: %+v", got)
	}
}

func TestMdnsRecoveryPortChangeAndOldGoodbye(t *testing.T) {
	s := newMdnsListenState(nil, nil, nil)
	defer s.stop()
	p, srv, a := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.2", 37001, 120)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{p, srv, a}}, false)
	_, newSRV, _ := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.2", 38002, 120)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{newSRV}}, false)
	_, oldBye, _ := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.2", 37001, 0)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{oldBye}}, false)
	got := recoverySnapshot(s)
	if len(got) != 1 || got[0].Addr != "192.0.2.2:38002" {
		t.Fatalf("old port goodbye erased replacement: %+v", got)
	}
	_, _, newA := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.4", 38002, 120)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{newA}}, false)
	if got = recoverySnapshot(s); len(got) != 1 || got[0].Addr != "192.0.2.4:38002" {
		t.Fatalf("A-only IP change missed: %+v", got)
	}
}

func TestMdnsRecoveryInvalidEndpointsAndCase(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "127.0.0.1", "224.0.0.251", "255.255.255.255"} {
		s := newMdnsListenState(nil, nil, nil)
		p, srv, a := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", ip, 37001, 120)
		s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{p, srv, a}}, false)
		if len(recoverySnapshot(s)) != 0 {
			t.Errorf("invalid address published: %s", ip)
		}
		s.stop()
	}
	s := newMdnsListenState(nil, nil, nil)
	defer s.stop()
	p, srv, a := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.2", 0, 120)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{p, srv, a}}, false)
	if len(recoverySnapshot(s)) != 0 {
		t.Fatal("port zero published")
	}
	_, srv, _ = recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.2", 37001, 120)
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{srv}}, false)
	ptr := p.(*dns.PTR)
	ptr.Hdr.Ttl = 0
	ptr.Ptr = "ADB-PHONE-AB12CD._ADB-TLS-CONNECT._TCP.LOCAL."
	s.processMessage(&dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{ptr}}, false)
	if len(recoverySnapshot(s)) != 0 {
		t.Fatal("case variant goodbye missed the existing instance")
	}
}

func TestMdnsRecoveryRejectsQueryKnownAnswers(t *testing.T) {
	s := newMdnsListenState(nil, nil, nil)
	defer s.stop()
	p, srv, a := recoveryRecords("adb-PHONE-Ab12Cd", "phone.local.", "192.0.2.2", 37001, 120)
	q := mdnsPtrQuery("_adb-tls-connect._tcp")
	q.Answer = []dns.RR{p, srv, a}
	s.processMessage(q, false)
	q.Question = nil
	s.processMessage(q, false)
	if len(recoverySnapshot(s)) != 0 || s.sawAnySignal() {
		t.Fatal("another querier's cache was mistaken for a device response")
	}
}
