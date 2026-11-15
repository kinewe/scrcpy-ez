package discovery

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func lanNet(t *testing.T, address string) net.IPNet {
	t.Helper()
	ip, n, err := net.ParseCIDR(address)
	if err != nil {
		t.Fatal(err)
	}
	n.IP = ip
	return *n
}

func TestLANScanTargetsStayOnNetworkAndRotateWidePrefixes(t *testing.T) {
	for _, address := range []string{"192.0.2.5/25", "192.0.2.5/23", "10.20.30.5/8", "192.0.2.4/31"} {
		n := lanNet(t, address)
		cursors := map[string]*LANScanCursor{}
		first := LANScanTargets([]net.IPNet{n}, cursors, 512)
		if len(first) == 0 || len(first) > 512 {
			t.Fatalf("bad bound for %s: %d", address, len(first))
		}
		seen := map[string]bool{}
		for _, target := range first {
			ip, _, _ := net.SplitHostPort(target.Addr)
			if !n.Contains(net.ParseIP(ip)) || ip == n.IP.String() || seen[ip] || target.Source != n.IP.String() {
				t.Fatalf("invalid/duplicate/self target: %+v", target)
			}
			seen[ip] = true
		}
		if address == "10.20.30.5/8" {
			if !seen["10.20.30.159"] {
				t.Fatal("local /24 must be covered promptly")
			}
			second := LANScanTargets([]net.IPNet{n}, cursors, 512)
			newHost := false
			for _, target := range second {
				ip, _, _ := net.SplitHostPort(target.Addr)
				newHost = newHost || !seen[ip]
			}
			if !newHost {
				t.Fatal("wide subnet never advanced beyond its first page")
			}
		}
	}
}

func TestLANScanTargetsExcludeNetworkBroadcastAllLocalIPsAndDuplicates(t *testing.T) {
	n1, n2 := lanNet(t, "192.0.2.5/24"), lanNet(t, "192.0.2.6/24")
	targets := LANScanTargets([]net.IPNet{n1, n2}, map[string]*LANScanCursor{}, 1024)
	if len(targets) != 252 {
		t.Fatalf("expected 252 unique remote hosts, got %d", len(targets))
	}
	for _, target := range targets {
		if target.Addr == "192.0.2.0:5555" || target.Addr == "192.0.2.255:5555" {
			t.Fatal("network or broadcast was probed")
		}
	}
	if len(LANScanTargets([]net.IPNet{lanNet(t, "192.0.2.5/32")}, nil, 1024)) != 0 {
		t.Fatal("/32 contains no peers")
	}
}

func adbReply(cmd, arg0, arg1 uint32, data []byte) []byte {
	out := make([]byte, 24+len(data))
	for i, w := range []uint32{cmd, arg0, arg1, uint32(len(data)), adbChecksum(data), ^cmd} {
		binary.LittleEndian.PutUint32(out[i*4:], w)
	}
	copy(out[24:], data)
	return out
}

func TestProbeADB5555RecognizesAuthAndRejectsOtherServices(t *testing.T) {
	badMagic := adbReply(adbAuth, 1, 0, make([]byte, 20))
	badMagic[20] ^= 1
	badChecksum := adbReply(adbAuth, 1, 0, make([]byte, 20))
	badChecksum[16] = 1
	oversized := adbReply(adbAuth, 1, 0, make([]byte, 20))
	binary.LittleEndian.PutUint32(oversized[12:16], 0xffffffff)
	for _, tc := range []struct {
		name  string
		reply []byte
		want  bool
	}{
		{"unpaired-auth", adbReply(adbAuth, 1, 0, make([]byte, 20)), true},
		{"device", adbReply(adbConnect, 0x01000001, 4096, []byte("device::ro.product.name=phone;\x00")), true},
		{"wrong-auth-type", adbReply(adbAuth, 3, 0, make([]byte, 20)), false},
		{"wrong-token-length", adbReply(adbAuth, 1, 0, make([]byte, 1)), false},
		{"host-is-not-phone", adbReply(adbConnect, 0x01000001, 4096, []byte("host::\x00")), false},
		{"http", []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"), false},
		{"bad-magic", badMagic, false},
		{"bad-checksum", badChecksum, false},
		{"oversized-packet", oversized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				request := make([]byte, 31)
				_, err = io.ReadFull(conn, request)
				if err == nil && binary.LittleEndian.Uint32(request[:4]) != adbConnect {
					err = io.ErrUnexpectedEOF
				}
				if err == nil {
					for _, b := range tc.reply {
						if _, err = conn.Write([]byte{b}); err != nil {
							break
						}
					}
				}
				if tc.want && err == nil {
					var b [1]byte
					_, err = conn.Read(b[:])
					if err == io.EOF {
						err = nil
					} else if err == nil {
						err = io.ErrUnexpectedEOF
					}
				}
				done <- err
			}()
			got := ProbeADB5555(context.Background(), LANProbeTarget{Addr: listener.Addr().String(), Source: "127.0.0.1"})
			if got != tc.want {
				t.Errorf("probe=%v want=%v", got, tc.want)
			}
			if err := <-done; tc.want && err != nil {
				t.Fatalf("probe sent authentication keys or failed to close: %v", err)
			}
		})
	}
}

func TestProbeADB5555CancellationInterruptsRead(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := listener.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- ProbeADB5555(ctx, LANProbeTarget{Addr: listener.Addr().String()}) }()
	conn := <-accepted
	defer conn.Close()
	var request [31]byte
	conn.SetReadDeadline(time.Now().Add(time.Second))
	io.ReadFull(conn, request[:])
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("canceled probe succeeded")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("protocol read did not cancel")
	}
}

func TestScanLAN5555BoundsWorkersAndSuppressesLateResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, peak, calls, results atomic.Int32
	probe := func(ctx context.Context, _ LANProbeTarget) bool {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for p := peak.Load(); n > p; p = peak.Load() {
			if peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-ctx.Done()
		return true
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ScanLAN5555(ctx, make([]LANProbeTarget, 200), probe, func(LANProbeTarget, bool) { results.Add(1) })
	}()
	deadline := time.Now().Add(time.Second)
	for calls.Load() < lanWorkers && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if peak.Load() != lanWorkers || active.Load() != 0 || results.Load() != 0 {
		t.Fatalf("workers/late result leak: peak=%d active=%d results=%d", peak.Load(), active.Load(), results.Load())
	}
}
