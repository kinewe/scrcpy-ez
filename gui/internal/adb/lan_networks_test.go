package adb

import (
	"net"
	"testing"
)

func TestLanIPv4NetworksUsePhysicalInterfacesAndPreserveMask(t *testing.T) {
	flags := net.FlagUp | net.FlagMulticast
	lan := mdnsTestIface("Wi-Fi", "10:20:30:40:50:60", "192.0.2.10", flags)
	lan.addrs = []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(23, 32)}}
	vpn := mdnsTestIface("Tailscale", "10:20:30:40:50:61", "100.64.0.1", flags)
	down := mdnsTestIface("Ethernet", "10:20:30:40:50:62", "192.0.2.11", net.FlagMulticast)
	got := lanIPv4NetworksFrom([]mdnsIface{vpn, down, lan, lan})
	if len(got) != 1 || got[0].IP.String() != "192.0.2.10" || got[0].Mask.String() != net.CIDRMask(23, 32).String() {
		t.Fatalf("unexpected LAN networks: %v", got)
	}
}
