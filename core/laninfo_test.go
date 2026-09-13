package core

import (
	"net"
	"testing"
)

// fakeIface builds one injected net.Interface for the filterLanAddresses
// table tests (production passes net.Interface.Addrs per interface).
func fakeIface(name string, flags net.Flags) net.Interface {
	return net.Interface{
		Index:        1,
		MTU:          1500,
		Name:         name,
		Flags:        flags,
		HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
	}
}

// ipNetAddr wraps an IPv4 CIDR as the *net.IPNet the stdlib returns.
func ipNetAddr(cidr string) net.Addr {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic("bad test CIDR " + cidr)
	}
	// Mirror net.Interface.Addrs: the addr carries the interface IP, the
	// IPNet's IP field replaced with the parsed address.
	return &net.IPNet{IP: ip, Mask: ipnet.Mask}
}

// TestFilterLanAddresses covers the injected table: loopback / down / IPv6 /
// To4-failing addresses are filtered out, duplicates collapse, lookup errors
// skip only the failing interface, and the result is deduplicated ascending.
func TestFilterLanAddresses(t *testing.T) {
	up := net.FlagUp
	upNonLoop := up // UP, not loopback
	upLoop := up | net.FlagLoopback
	down := net.Flags(0)

	ipv6Addr := &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}
	// A 16-byte garbage IP that cannot convert to IPv4 (To4 == nil), distinct
	// from a real IPv6 net address.
	unconvertible := &net.IPNet{IP: net.IP{0xde, 0xad, 0xbe, 0xef, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}, Mask: net.CIDRMask(64, 128)}

	cases := []struct {
		name    string
		ifaces  []net.Interface
		addrMap map[string][]net.Addr
		want    []string
	}{
		{
			name:   "loopback filtered out",
			ifaces: []net.Interface{fakeIface("lo0", upLoop), fakeIface("en0", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"lo0": {ipNetAddr("127.0.0.1/8")},
				"en0": {ipNetAddr("192.168.1.5/24")},
			},
			want: []string{"192.168.1.5"},
		},
		{
			name:   "down interface filtered out",
			ifaces: []net.Interface{fakeIface("en1", down), fakeIface("en0", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"en1": {ipNetAddr("10.0.0.2/8")},
				"en0": {ipNetAddr("192.168.1.5/24")},
			},
			want: []string{"192.168.1.5"},
		},
		{
			name:   "ipv6 addresses filtered out",
			ifaces: []net.Interface{fakeIface("en0", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"en0": {ipv6Addr, ipNetAddr("192.168.1.5/24")},
			},
			want: []string{"192.168.1.5"},
		},
		{
			name:   "unconvertible 16-byte ip filtered out (To4 fails)",
			ifaces: []net.Interface{fakeIface("en0", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"en0": {unconvertible, ipNetAddr("10.1.2.3/24")},
			},
			want: []string{"10.1.2.3"},
		},
		{
			name:   "duplicate addresses across interfaces deduplicated",
			ifaces: []net.Interface{fakeIface("en0", upNonLoop), fakeIface("en1", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"en0": {ipNetAddr("192.168.1.5/24")},
				"en1": {ipNetAddr("192.168.1.5/24")},
			},
			want: []string{"192.168.1.5"},
		},
		{
			name:   "results sorted ascending",
			ifaces: []net.Interface{fakeIface("en0", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"en0": {ipNetAddr("192.168.0.9/24"), ipNetAddr("10.0.0.1/8"), ipNetAddr("172.16.0.1/12")},
			},
			want: []string{"10.0.0.1", "172.16.0.1", "192.168.0.9"},
		},
		{
			name:    "no usable addresses yields empty non-nil result",
			ifaces:  []net.Interface{fakeIface("lo0", upLoop)},
			addrMap: map[string][]net.Addr{},
			want:    []string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filterLanAddresses(c.ifaces, func(iface net.Interface) ([]net.Addr, error) {
				return c.addrMap[iface.Name], nil
			})
			if len(got) != len(c.want) {
				t.Fatalf("filterLanAddresses = %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("filterLanAddresses[%d] = %q, want %q (full: %v)", i, got[i], c.want[i], got)
				}
			}
			if got == nil {
				t.Error("result must be a non-nil empty slice for the JSON [] contract")
			}
		})
	}
}

// TestFilterLanAddressesLookupErrorSkipsIface verifies a per-interface address
// lookup failure skips only that interface; healthy ones still report.
func TestFilterLanAddressesLookupErrorSkipsIface(t *testing.T) {
	ifaces := []net.Interface{
		fakeIface("broken", net.FlagUp),
		fakeIface("healthy", net.FlagUp),
	}
	got := filterLanAddresses(ifaces, func(iface net.Interface) ([]net.Addr, error) {
		if iface.Name == "broken" {
			return nil, &net.OpError{Op: "route", Err: net.UnknownNetworkError("injected")}
		}
		return []net.Addr{ipNetAddr("192.168.1.5/24")}, nil
	})
	if len(got) != 1 || got[0] != "192.168.1.5" {
		t.Errorf("healthy interface address lost after sibling lookup error: %v", got)
	}
}

// TestGetLanAddressesDegradedToEmpty verifies the binding contract: an
// interface enumeration failure degrades to an empty (non-nil) list, never an
// error or nil.
func TestGetLanAddressesDegradedToEmpty(t *testing.T) {
	orig := lanInterfaces
	lanInterfaces = func() ([]net.Interface, error) {
		return nil, &net.OpError{Op: "route", Err: net.UnknownNetworkError("injected")}
	}
	t.Cleanup(func() { lanInterfaces = orig })

	got := (&App{}).GetLanAddresses()
	if got == nil {
		t.Fatal("GetLanAddresses must return a non-nil slice (JSON [] contract)")
	}
	if len(got) != 0 {
		t.Errorf("GetLanAddresses on enumeration failure = %v, want empty", got)
	}
}

// TestGetLanAddressesLiveSmoke runs the real net.Interfaces enumeration once:
// it only asserts the non-nil JSON [] contract (a CI host always has at least
// a loopback interface, which the filter must exclude, so empty is legal).
func TestGetLanAddressesLiveSmoke(t *testing.T) {
	orig := lanInterfaces
	lanInterfaces = net.Interfaces
	t.Cleanup(func() { lanInterfaces = orig })

	got := (&App{}).GetLanAddresses()
	if got == nil {
		t.Fatal("GetLanAddresses must never return nil")
	}
	for _, addr := range got {
		ip := net.ParseIP(addr)
		if ip == nil || ip.To4() == nil {
			t.Errorf("non-IPv4 address in result: %q", addr)
			continue
		}
		if ip.IsLoopback() {
			t.Errorf("loopback address in result: %q", addr)
		}
	}
}
