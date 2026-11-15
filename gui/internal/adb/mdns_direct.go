package adb

import (
	"context"
	"net"
	"time"

	"github.com/miekg/dns"
	"scrcpy-ez/gui/internal/bridge"
)

// A private, peer-bound UDP socket preserves unicast reply delivery on networks
// where reusing the multicast QU socket does not. This is only DNS traffic.
func listenMdnsDirectSocket(ctx context.Context, target MdnsRefreshTarget) (net.Conn, error) {
	// Keep ordinary unicast defaults for this ephemeral-source legacy query.
	local := &net.UDPAddr{IP: net.ParseIP(target.Source), Port: 0}
	dialer := net.Dialer{LocalAddr: local, Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "udp4", net.JoinHostPort(target.IP, "5353"))
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (q *mdnsQuerySockets) queryHost(host mdnsHostRefresh) {
	ctx, cancel := context.WithTimeout(q.ctx, q.directTimeout)
	defer cancel()
	stopCaller := context.AfterFunc(host.ctx, cancel)
	defer stopCaller()
	if host.ctx.Err() != nil || ctx.Err() != nil {
		return
	}
	conn, err := q.directListen(ctx, host.target)
	if err != nil {
		bridge.DebugLog("[app] mdns 定向 socket 失败：source=%s target=%s err=%v", host.target.Source, host.target.IP, err)
		return
	}
	defer conn.Close()
	stopConn := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopConn()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return
	}
	send := func() bool {
		for _, serviceType := range mdnsListenServiceTypes {
			if ctx.Err() != nil {
				return false
			}
			b, err := mdnsPtrQuery(serviceType).Pack()
			if err != nil {
				return false
			}
			if err := conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
				return false
			}
			if _, err := conn.Write(b); err != nil {
				bridge.DebugLog("[app] mdns 定向查询失败：source=%s target=%s err=%v", host.target.Source, host.target.IP, err)
				return false
			}
		}
		return true
	}
	if !send() {
		return
	}
	bridge.DebugLog("[app] mdns 定向查询已发送：source=%s target=%s", host.target.Source, host.target.IP)
	attempts := 1
	delay := q.directRetryInterval
	nextRetry := time.Now().Add(delay)
	gotResponse := false
	readUntil := deadline
	buf := make([]byte, 65535)
	for ctx.Err() == nil {
		until := readUntil
		if !gotResponse && attempts < 3 && nextRetry.Before(until) {
			until = nextRetry
		}
		if err := conn.SetReadDeadline(until); err != nil {
			return
		}
		n, err := conn.Read(buf)
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() && ctx.Err() == nil && !gotResponse && attempts < 3 && time.Now().Before(deadline) {
				if !send() {
					return
				}
				attempts++
				bridge.DebugLog("[app] mdns 定向重查：target=%s attempt=%d", host.target.IP, attempts)
				delay *= 2
				nextRetry = time.Now().Add(delay)
				continue
			}
			if !gotResponse && ctx.Err() == nil {
				bridge.DebugLog("[app] mdns 定向未获服务应答：target=%s attempts=%d err=%v", host.target.IP, attempts, err)
			}
			return
		}
		msg := new(dns.Msg)
		if msg.Unpack(buf[:n]) != nil || ctx.Err() != nil || host.ctx.Err() != nil {
			continue
		}
		ptrs, srvs, _ := mdnsCollectRecords(msg)
		q.state.processMessage(msg, true)
		if len(ptrs) != 0 || len(srvs) != 0 {
			gotResponse = true
			bridge.DebugLog("[app] mdns 定向应答：source=%s target=%s", host.target.Source, host.target.IP)
			// Allow other queried services to arrive, without keeping a slot
			// occupied for the full timeout after a useful response.
			if until := time.Now().Add(time.Second); until.Before(readUntil) {
				readUntil = until
			}
		}
	}
}
