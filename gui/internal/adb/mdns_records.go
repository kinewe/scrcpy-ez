package adb

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// SRV and A can arrive in separate packets, in either order. Keep bounded,
// expiring records across reader/socket generations; never publish an empty
// address over a complete candidate. These records are discovery hints only.
type mdnsCachedSRV struct {
	rr      dns.SRV
	expires time.Time
}
type mdnsCachedA struct {
	ip      net.IP
	expires time.Time
}
type mdnsRecordCache struct {
	srvs      map[string]mdnsCachedSRV
	addresses map[string]mdnsCachedA
}

const mdnsRecordLimit = 1024

func mdnsRecordExpiry(now time.Time, ttl uint32) time.Time {
	return now.Add(min(time.Duration(ttl)*time.Second, time.Hour))
}

func (s *mdnsListenState) processMessage(msg *dns.Msg, fromQuery bool) {
	// Queries may contain another querier's stale known-answer cache or probe
	// authority records. Only responses are authoritative discovery evidence.
	if msg == nil || !msg.Response {
		return
	}
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()
	now := time.Now()
	s.expireRecordsLocked(now)
	r := &s.records
	if r.srvs == nil {
		r.srvs = map[string]mdnsCachedSRV{}
		r.addresses = map[string]mdnsCachedA{}
	}
	ptrs, srvs, _ := mdnsCollectRecords(msg)
	affected, removed := map[string]bool{}, map[string]bool{}
	for _, ptr := range ptrs {
		st, ok := mdnsServiceTypeForName(ptr.Hdr.Name)
		if !ok || ptr.Hdr.Class&0x7fff != dns.ClassINET || mdnsInstanceForName(ptr.Ptr, st) == "" {
			continue
		}
		name := strings.ToLower(ptr.Ptr)
		affected[name] = true
		if ptr.Hdr.Ttl == 0 {
			removed[name] = true
		}
	}
	for name, srv := range srvs {
		st, ok := mdnsServiceTypeForName(name)
		if !ok || srv.Hdr.Class&0x7fff != dns.ClassINET || mdnsInstanceForName(srv.Hdr.Name, st) == "" {
			continue
		}
		affected[name] = true
		if srv.Hdr.Ttl == 0 {
			// A late goodbye for the former port cannot erase its replacement.
			if old, ok := r.srvs[name]; ok && old.rr.Port == srv.Port && strings.EqualFold(old.rr.Target, srv.Target) {
				removed[name] = true
			}
			continue
		}
		if srv.Port == 0 || srv.Target == "." || srv.Target == "" {
			continue
		}
		copy := *srv
		r.srvs[name] = mdnsCachedSRV{copy, mdnsRecordExpiry(now, srv.Hdr.Ttl)}
	}
	for _, section := range [][]dns.RR{msg.Answer, msg.Ns, msg.Extra} {
		for _, rr := range section {
			a, ok := rr.(*dns.A)
			if !ok || a.Hdr.Class&0x7fff != dns.ClassINET || !usableMdnsIPv4(a.A) {
				continue
			}
			host := strings.ToLower(a.Hdr.Name)
			old, existed := r.addresses[host]
			changed := !existed || !old.ip.Equal(a.A)
			if a.Hdr.Ttl == 0 {
				if !existed || !old.ip.Equal(a.A) {
					continue
				}
				delete(r.addresses, host)
				changed = true
			} else {
				r.addresses[host] = mdnsCachedA{append(net.IP(nil), a.A.To4()...), mdnsRecordExpiry(now, a.Hdr.Ttl)}
			}
			if changed {
				for name, srv := range r.srvs {
					if strings.EqualFold(srv.rr.Target, host) {
						affected[name] = true
					}
				}
			}
		}
	}
	for name := range removed {
		delete(r.srvs, name)
	}
	s.trimRecordsLocked()
	for name := range affected {
		st, ok := mdnsServiceTypeForName(name)
		if !ok {
			continue
		}
		svc := MdnsService{Type: st, Name: mdnsInstanceForName(name, st), Mode: MdnsModeOf(st)}
		if removed[name] {
			s.gone(svc)
			continue
		}
		srv, ok := r.srvs[name]
		if !ok {
			continue
		}
		svc.Name = mdnsInstanceForName(srv.rr.Hdr.Name, st)
		a, ok := r.addresses[strings.ToLower(srv.rr.Target)]
		if !ok {
			s.gone(svc)
			continue
		}
		svc.Addr = net.JoinHostPort(a.ip.String(), strconv.Itoa(int(srv.rr.Port)))
		s.mu.Lock()
		s.sawSignal = true
		s.mu.Unlock()
		s.upsert(svc, now, fromQuery)
	}
}

func (s *mdnsListenState) expireRecords(now time.Time) {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()
	s.expireRecordsLocked(now)
}

func (s *mdnsListenState) expireRecordsLocked(now time.Time) {
	for host, a := range s.records.addresses {
		if !now.Before(a.expires) {
			delete(s.records.addresses, host)
		}
	}
	for name, srv := range s.records.srvs {
		_, addressKnown := s.records.addresses[strings.ToLower(srv.rr.Target)]
		if !now.Before(srv.expires) || !addressKnown {
			st, ok := mdnsServiceTypeForName(name)
			if ok {
				s.gone(MdnsService{Type: st, Name: mdnsInstanceForName(name, st)})
			}
		}
		if !now.Before(srv.expires) {
			delete(s.records.srvs, name)
		}
	}
}

func (s *mdnsListenState) trimRecordsLocked() {
	for len(s.records.srvs) > mdnsRecordLimit {
		var oldest string
		for key, value := range s.records.srvs {
			if oldest == "" || value.expires.Before(s.records.srvs[oldest].expires) {
				oldest = key
			}
		}
		srv := s.records.srvs[oldest]
		delete(s.records.srvs, oldest)
		st, _ := mdnsServiceTypeForName(oldest)
		s.gone(MdnsService{Type: st, Name: mdnsInstanceForName(srv.rr.Hdr.Name, st)})
	}
	for len(s.records.addresses) > mdnsRecordLimit {
		var oldest string
		for key, value := range s.records.addresses {
			if oldest == "" || value.expires.Before(s.records.addresses[oldest].expires) {
				oldest = key
			}
		}
		delete(s.records.addresses, oldest)
	}
}
