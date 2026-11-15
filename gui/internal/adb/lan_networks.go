package adb

import "net"

// LanIPv4Networks shares mDNS's physical-adapter policy, retaining each local
// address and its actual mask for bounded, directly attached LAN discovery.
func LanIPv4Networks() ([]net.IPNet, error) {
	all, err := collectMdnsIfaceAddrs()
	if err != nil {
		return nil, err
	}
	return lanIPv4NetworksFrom(all), nil
}

func lanIPv4NetworksFrom(all []mdnsIface) []net.IPNet {
	var out []net.IPNet
	seen := map[string]bool{}
	for _, mi := range filterLanIfaceAddrs(all) {
		for _, addr := range mi.addrs {
			n, ok := addr.(*net.IPNet)
			if !ok || !usableMdnsIPv4(n.IP) {
				continue
			}
			bits, size := n.Mask.Size()
			if size != 32 || bits < 8 || bits > 31 {
				continue
			}
			key := n.IP.String() + "/" + n.Mask.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, net.IPNet{IP: append(net.IP(nil), n.IP.To4()...), Mask: append(net.IPMask(nil), n.Mask...)})
		}
	}
	return out
}
