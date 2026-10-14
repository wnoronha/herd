package discover

import (
	"fmt"
	"net"
)

// LANIP returns the machine's preferred outbound LAN IP without sending
// traffic: connecting a UDP socket only selects the source address.
// It falls back to the first usable interface address.
func LANIP() (net.IP, error) {
	if ip := udpDialIP(); ip != nil {
		return ip, nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("discover lan ip: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || !ip.IsGlobalUnicast() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4, nil
			}
		}
	}
	return nil, fmt.Errorf("discover lan ip: no usable interface address")
}

// udpDialIP selects a source IP via UDP connect (no packets are sent).
func udpDialIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && usableIP(addr.IP) {
		return addr.IP
	}
	return nil
}
