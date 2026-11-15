package discovery

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sort"
	"sync"
	"time"
)

const (
	LANScanLimit    = 1024
	lanWorkers      = 48
	lanProbeTimeout = 750 * time.Millisecond
	adbConnect      = uint32(0x4e584e43)
	adbAuth         = uint32(0x48545541)
)

type LANProbeTarget struct{ Addr, Source string }

// LANScanCursor advances wide-subnet coverage between rounds, while revisiting
// the local /24 promptly. All targets stay inside the actual interface mask.
type LANScanCursor struct{ near, rest uint64 }

func LANScanTargets(networks []net.IPNet, cursors map[string]*LANScanCursor, limit int) []LANProbeTarget {
	if limit <= 0 || len(networks) == 0 {
		return nil
	}
	if cursors == nil {
		cursors = map[string]*LANScanCursor{}
	}
	local := map[uint32]bool{}
	for _, n := range networks {
		if ip := n.IP.To4(); ip != nil {
			local[binary.BigEndian.Uint32(ip)] = true
		}
	}
	networks = append([]net.IPNet(nil), networks...)
	sort.Slice(networks, func(i, j int) bool { return networks[i].IP.String() < networks[j].IP.String() })
	seen := map[uint32]bool{}
	var out []LANProbeTarget
	for ni, n := range networks {
		ip := n.IP.To4()
		ones, bits := n.Mask.Size()
		if ip == nil || bits != 32 || ones < 8 || ones > 31 {
			continue
		}
		start := uint64(binary.BigEndian.Uint32(ip.Mask(n.Mask)))
		span := uint64(1) << uint(32-ones)
		nearStart := uint64(binary.BigEndian.Uint32(ip) & 0xffffff00)
		if nearStart < start {
			nearStart = start
		}
		nearEnd := nearStart + 256
		if nearEnd > start+span {
			nearEnd = start + span
		}
		nearSize := nearEnd - nearStart
		key := n.IP.String() + "/" + n.Mask.String()
		cursor := cursors[key]
		if cursor == nil {
			cursor = &LANScanCursor{}
			cursors[key] = cursor
		}
		quota := (limit - len(out)) / (len(networks) - ni)
		nearQuota := quota
		if span > nearSize {
			nearQuota = (quota + 1) / 2
		}
		appendIP := func(v uint64) bool {
			if v < start || v >= start+span || ones <= 30 && (v == start || v == start+span-1) || local[uint32(v)] || seen[uint32(v)] {
				return false
			}
			seen[uint32(v)] = true
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(v))
			out = append(out, LANProbeTarget{Addr: net.IP(b[:]).String() + ":5555", Source: n.IP.String()})
			return true
		}
		added := 0
		for tried := uint64(0); tried < nearSize && added < nearQuota; tried++ {
			v := nearStart + cursor.near%nearSize
			cursor.near++
			if appendIP(v) {
				added++
			}
		}
		restSize := span - nearSize
		// At most quota plus boundary skips: never allocate/enumerate an entire
		// /8 just to search one bounded page of its hosts.
		for tried := uint64(0); restSize > 0 && tried < restSize && added < quota && tried < uint64(quota+len(local)+4); tried++ {
			rank := cursor.rest % restSize
			cursor.rest++
			v := start + rank
			if v >= nearStart {
				v += nearSize
			}
			if appendIP(v) {
				added++
			}
		}
	}
	return out
}

// ProbeADB5555 identifies adbd without using the shared ADB server, exchanging
// keys or opening a shell. AUTH TOKEN already proves an unpaired device exists.
// Protocol: https://android.googlesource.com/platform/system/core/+/android10-release/adb/protocol.txt
func ProbeADB5555(ctx context.Context, target LANProbeTarget) bool {
	dctx, cancel := context.WithTimeout(ctx, lanProbeTimeout)
	defer cancel()
	dialer := net.Dialer{}
	if target.Source != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(target.Source)}
	}
	conn, err := dialer.DialContext(dctx, "tcp4", target.Addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	stop := context.AfterFunc(dctx, func() { conn.Close() })
	defer stop()
	deadline, _ := dctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return false
	}
	payload := []byte("host::\x00")
	packet := make([]byte, 24+len(payload))
	words := []uint32{adbConnect, 0x01000000, 4096, uint32(len(payload)), adbChecksum(payload), ^adbConnect}
	for i, w := range words {
		binary.LittleEndian.PutUint32(packet[i*4:], w)
	}
	copy(packet[24:], payload)
	for len(packet) > 0 {
		n, err := conn.Write(packet)
		if err != nil || n == 0 {
			return false
		}
		packet = packet[n:]
	}
	var header [24]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return false
	}
	cmd := binary.LittleEndian.Uint32(header[:4])
	arg0 := binary.LittleEndian.Uint32(header[4:8])
	arg1 := binary.LittleEndian.Uint32(header[8:12])
	length := binary.LittleEndian.Uint32(header[12:16])
	sum := binary.LittleEndian.Uint32(header[16:20])
	if binary.LittleEndian.Uint32(header[20:]) != ^cmd || length > 4096 {
		return false
	}
	if cmd == adbAuth {
		if arg0 != 1 || arg1 != 0 || length != 20 {
			return false
		}
	} else if cmd == adbConnect {
		if arg0 < 0x01000000 || arg1 == 0 || length == 0 {
			return false
		}
	} else {
		return false
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return false
	}
	if sum != 0 && sum != adbChecksum(data) {
		return false
	}
	if cmd == adbConnect && !bytes.HasPrefix(data, []byte("device::")) && !bytes.HasPrefix(data, []byte("recovery::")) {
		return false
	}
	return dctx.Err() == nil
}

func adbChecksum(data []byte) uint32 {
	var sum uint32
	for _, v := range data {
		sum += uint32(v)
	}
	return sum
}

// ScanLAN5555 streams both positive and negative results as they arrive. Workers
// are joined before returning; cancellation interrupts dials and protocol reads.
func ScanLAN5555(ctx context.Context, targets []LANProbeTarget, probe func(context.Context, LANProbeTarget) bool, result func(LANProbeTarget, bool)) {
	if len(targets) == 0 || ctx.Err() != nil {
		return
	}
	count := lanWorkers
	if len(targets) < count {
		count = len(targets)
	}
	jobs := make(chan LANProbeTarget)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				if ctx.Err() != nil {
					return
				}
				ok := probe(ctx, target)
				if ctx.Err() == nil {
					result(target, ok)
				}
			}
		}()
	}
send:
	for _, target := range targets {
		select {
		case <-ctx.Done():
			break send
		case jobs <- target:
		}
	}
	close(jobs)
	wg.Wait()
}
