package core

import (
	"net"
	"sort"
	"strings"
	"time"
)

// ─── LAN address discovery (Settings pairing card) ────────────────
//
// The Settings page's LAN pairing card shows the addresses a phone on the same
// network can use to reach this machine's llama-server. The enumeration is a
// new functional domain (network-interface discovery), so it lives in its own
// file per the repo's per-domain file rule.

// lanInterfaces is the seam for tests; production uses net.Interfaces.
var lanInterfaces = net.Interfaces

// interfaceAddrs is the seam for the per-interface address lookup (tests
// inject canned lists so fake interfaces need no real kernel index);
// production uses net.Interface.Addrs.
var interfaceAddrs = func(iface net.Interface) ([]net.Addr, error) {
	return iface.Addrs()
}

// outboundDial is the seam for the default-route probe (tests inject a stub);
// production dials a real UDP socket.
var outboundDial = func(network, addr string) (net.Conn, error) {
	return net.DialTimeout(network, addr, 2*time.Second)
}

// GetLanAddresses lists non-loopback IPv4 addresses of this machine
// (e.g. "192.168.1.5") for the Settings pairing card: filtered against
// virtual-NIC / link-local noise, then ranked by LAN-routeability with the
// default-route outbound address first. Returns an empty slice (never nil
// semantics contract: JSON []) when none are found; enumeration errors
// degrade to an empty list, not an error.
func (a *App) GetLanAddresses() []string {
	ifaces, err := lanInterfaces()
	if err != nil {
		return []string{}
	}
	list := filterLanAddresses(ifaces, interfaceAddrs)
	return rankLanAddresses(list, preferredOutboundIP())
}

// filterLanAddresses extracts the unicast IPv4 addresses of the UP,
// non-loopback interfaces in ifaces, deduplicated and in ascending string
// order. Pure: the address lookup goes through the injectable addrs function
// (production passes net.Interface.Addrs). Per-interface lookup errors skip
// that interface only — a flapping NIC must not hide the healthy ones.
//
// Two noise classes are dropped on top of the interface flags:
//   - IPv4 link-local (169.254.0.0/16): APIPA self-assigned, never routable
//     to a LAN peer;
//   - known virtual-switch interfaces (by lowercased name substring: Hyper-V /
//     WSL "vethernet", "VirtualBox Host-Only"-style "virtualbox host", VMware
//     "vmware network adapter"). This is a best-effort pruning of Windows
//     virtual switches: a physical machine routinely carries unreachable
//     addresses (e.g. the 172.20.16.1 WSL/Hyper-V host vNIC that QR-code
//     consumers cannot reach), but 172.16/12 is also used by real corporate
//     LANs, so a blanket CIDR ban would be wrong — the name match is the
//     surgical cut.
func filterLanAddresses(ifaces []net.Interface, addrs func(net.Interface) ([]net.Addr, error)) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualIfaceName(iface.Name) {
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
			// Link-local (169.254.x.x, APIPA): self-assigned when no DHCP
			// answers; a peer can never route to it.
			if ip.IsLinkLocalUnicast() {
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

// virtualIfaceNameHints are lowercased substrings of Windows virtual-switch
// adapter names ("vEthernet (WSL)", "VirtualBox Host-Only Network", "VMware
// Network Adapter VMnet8" — case-insensitive contains). Kept narrow on
// purpose: see the filterLanAddresses comment for why CIDR-based pruning
// would overreach.
var virtualIfaceNameHints = []string{
	"vethernet",              // Hyper-V / WSL2 virtual switch
	"virtualbox host",        // VirtualBox Host-Only Network
	"vmware network adapter", // VMware Network Adapter VMnet1/VMnet8
}

// isVirtualIfaceName reports whether ifaceName names a known virtual-switch
// adapter (case-insensitive substring match over virtualIfaceNameHints).
func isVirtualIfaceName(ifaceName string) bool {
	lower := strings.ToLower(ifaceName)
	for _, hint := range virtualIfaceNameHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// lanCIDRGroups lists the common private LAN ranges in QR-preference order:
// the home-router 192.168/16 first, then the large 10/8 corporate range, then
// the 172.16/12 range (also used by virtualization hosts — hence its low
// rank, never a ban). Anything else sorts last.
var lanCIDRGroups = []*net.IPNet{
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
}

// mustParseCIDR parses a compile-time-constant CIDR, panicking on a typo (the
// inputs above are literals, so a panic can only be a programming error).
func mustParseCIDR(cidr string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic("laninfo: bad built-in CIDR " + cidr)
	}
	return ipnet
}

// rankLanAddresses orders the filtered address list for the pairing card.
// preferred (the default-route outbound address, preferredOutboundIP) moves
// to the front — that is the address a LAN peer can actually route to; the
// rest are grouped by CIDR preference (lanCIDRGroups: 192.168/16, then 10/8,
// then 172.16/12, then everything else), ascending by string within a group.
// An empty preferred falls back to grouping alone. Pure. Segment membership
// uses net.ParseIP + IPNet.Contains (stdlib classic, works directly on the
// string list without a re-marshal round-trip).
func rankLanAddresses(addrs []string, preferred string) []string {
	groupOf := func(addr string) int {
		ip := net.ParseIP(addr)
		if ip == nil {
			return len(lanCIDRGroups)
		}
		for i, ipnet := range lanCIDRGroups {
			if ipnet.Contains(ip) {
				return i
			}
		}
		return len(lanCIDRGroups)
	}

	out := make([]string, 0, len(addrs))
	rest := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		// Only a preferred value that IS in the list moves to the front; a
		// foreign preferred (route probe raced the enumeration) adds nothing.
		if preferred != "" && addr == preferred {
			out = append(out, addr)
			continue
		}
		rest = append(rest, addr)
	}
	// Within-group ascending string order preserves filterLanAddresses'
	// deterministic output.
	sort.Strings(rest)
	for group := 0; group <= len(lanCIDRGroups); group++ {
		for _, addr := range rest {
			if groupOf(addr) == group {
				out = append(out, addr)
			}
		}
	}
	return out
}

// preferredOutboundIP returns the local address the default route would use
// to reach the internet (e.g. the physical NIC's 192.168.x.x), or "" when it
// cannot be determined. Classic trick: net.DialTimeout("udp", …) performs a
// connect WITHOUT sending any packet — the kernel only consults the routing
// table to bind the socket, so nothing leaves the machine; 8.8.8.8:80 is
// merely a routing decision trigger (any public IP would do) and carries no
// data. Errors (no route, sandboxed network) degrade to "" and the ranking
// falls back to CIDR grouping alone.
func preferredOutboundIP() string {
	conn, err := outboundDial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	udp, ok := conn.LocalAddr().(*net.UDPAddr)
	_ = conn.Close()
	if !ok {
		return ""
	}
	return udp.IP.String()
}
