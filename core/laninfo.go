package core

import (
	"net"
	"sort"
)

// ─── LAN address discovery (Settings pairing card) ────────────────
//
// The Settings page's LAN pairing card shows the addresses a phone on the same
// network can use to reach this machine's llama-server. The enumeration is a
// new functional domain (network-interface discovery), so it lives in its own
// file per the repo's per-domain file rule.

// lanInterfaces is the seam for tests; production uses net.Interfaces.
var lanInterfaces = net.Interfaces

// GetLanAddresses lists non-loopback IPv4 addresses of this machine
// (e.g. "192.168.1.5") for the Settings pairing card. Returns an empty
// slice (never nil semantics contract: JSON []) when none are found;
// enumeration errors degrade to an empty list, not an error.
func (a *App) GetLanAddresses() []string {
	ifaces, err := lanInterfaces()
	if err != nil {
		return []string{}
	}
	return filterLanAddresses(ifaces, func(iface net.Interface) ([]net.Addr, error) {
		return iface.Addrs()
	})
}

// filterLanAddresses extracts the unicast IPv4 addresses of the UP,
// non-loopback interfaces in ifaces, deduplicated and in ascending string
// order. Pure: the address lookup goes through the injectable addrs function
// (production passes net.Interface.Addrs). Per-interface lookup errors skip
// that interface only — a flapping NIC must not hide the healthy ones.
func filterLanAddresses(ifaces []net.Interface, addrs func(net.Interface) ([]net.Addr, error)) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrList, err := addrs(iface)
		if err != nil {
			continue
		}
		for _, addr := range addrList {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			// To4 reports nil for IPv6 addresses, keeping them out of the
			// pairing list (phones connect by the IPv4 LAN address today).
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			s := ip.String()
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
