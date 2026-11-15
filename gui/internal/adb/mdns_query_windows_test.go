//go:build windows

package adb

import (
	"context"
	"encoding/binary"
	"net"
	"syscall"
	"testing"
)

func TestMdnsQUWindowsSelectsMulticastInterface(t *testing.T) {
	targets, err := collectMdnsQueryTargets()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) == 0 {
		t.Skip("no active physical IPv4 LAN adapter")
	}
	for _, target := range targets {
		t.Run(target.bind, func(t *testing.T) {
			pc, err := listenMdnsQuerySocket(context.Background(), target)
			if err != nil {
				t.Fatal(err)
			}
			defer pc.Close()
			local := pc.LocalAddr().(*net.UDPAddr)
			host, _, _ := net.SplitHostPort(target.bind)
			if !local.IP.Equal(net.ParseIP(host)) || local.Port == 0 || local.Port == 5353 {
				t.Fatalf("invalid exclusive query bind: %v", local)
			}
			raw, err := pc.(*net.UDPConn).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var iface, ttl int
			var sockErr error
			err = raw.Control(func(fd uintptr) {
				iface, sockErr = syscall.GetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF)
				if sockErr == nil {
					ttl, sockErr = syscall.GetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL)
				}
			})
			if err != nil || sockErr != nil {
				t.Fatalf("read multicast socket options: %v / %v", err, sockErr)
			}
			// Winsock versions/providers return either the selected local IPv4
			// address bytes or an interface index for IP_MULTICAST_IF.
			selected := iface == target.iface.Index
			addresses, err := target.iface.Addrs()
			if err != nil {
				t.Fatal(err)
			}
			for _, address := range addresses {
				if ipn, ok := address.(*net.IPNet); ok && ipn.IP.To4() != nil {
					// SetMulticastInterface may select the adapter's primary IP
					// while this query socket is bound to another IP on it.
					selected = selected || uint32(iface) == binary.NativeEndian.Uint32(ipn.IP.To4())
				}
			}
			if !selected || ttl != 255 {
				t.Fatalf("multicast egress/TTL: got interface=%d ttl=%d; want interface=%d ttl=255", iface, ttl, target.iface.Index)
			}
		})
	}
}
