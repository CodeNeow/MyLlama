package core

import (
	"io"
	"net"
	"testing"
	"time"
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
// To4-failing / link-local (169.254.x.x) / virtual-switch-interface addresses
// are filtered out, duplicates collapse, lookup errors skip only the failing
// interface, and the result is deduplicated ascending.
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
			name:   "link-local 169.254 filtered out",
			ifaces: []net.Interface{fakeIface("en0", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"en0": {ipNetAddr("169.254.10.20/16"), ipNetAddr("192.168.1.5/24")},
			},
			want: []string{"192.168.1.5"},
		},
		{
			// The reported real-world case: the WSL/Hyper-V host vNIC
			// ("vEthernet (WSL)") carries an unroutable 172.20.16.1 that the
			// QR code must not default to.
			name:   "Hyper-V/WSL vethernet interface filtered out",
			ifaces: []net.Interface{fakeIface("vEthernet (WSL)", upNonLoop), fakeIface("Ethernet", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"vEthernet (WSL)": {ipNetAddr("172.20.16.1/20")},
				"Ethernet":        {ipNetAddr("192.168.1.5/24")},
			},
			want: []string{"192.168.1.5"},
		},
		{
			name:   "virtualbox and vmware host adapters filtered out",
			ifaces: []net.Interface{fakeIface("VirtualBox Host-Only Network", upNonLoop), fakeIface("VMware Network Adapter VMnet8", upNonLoop), fakeIface("en0", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"VirtualBox Host-Only Network":  {ipNetAddr("192.168.56.1/24")},
				"VMware Network Adapter VMnet8": {ipNetAddr("192.168.111.1/24")},
				"en0":                           {ipNetAddr("10.0.0.7/8")},
			},
			want: []string{"10.0.0.7"},
		},
		{
			// 172.16/12 stays legitimate when the NIC is physical: the filter
			// prunes by NAME, never by network segment (corporate LANs use
			// 172.x too).
			name:   "physical 172.x interface kept",
			ifaces: []net.Interface{fakeIface("Ethernet", upNonLoop)},
			addrMap: map[string][]net.Addr{
				"Ethernet": {ipNetAddr("172.20.16.1/20")},
			},
			want: []string{"172.20.16.1"},
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

// TestRankLanAddresses covers the pairing-card ordering: the default-route
// outbound address moves to the front, the rest group by CIDR preference
// (192.168/16 → 10/8 → 172.16/12 → other) ascending within a group, an empty
// preferred falls back to grouping alone, and the empty list stays empty.
func TestRankLanAddresses(t *testing.T) {
	cases := []struct {
		name      string
		addrs     []string
		preferred string
		want      []string
	}{
		{
			// The reported bug scenario: the WSL 172.20.16.1 must not lead the
			// QR list when a home-LAN 192.168 address exists.
			name:      "wsl 172.20.16.1 ranks behind the 192.168 lan address",
			addrs:     []string{"172.20.16.1", "192.168.1.5"},
			preferred: "",
			want:      []string{"192.168.1.5", "172.20.16.1"},
		},
		{
			name:      "preferred moves to the front even from a lower group",
			addrs:     []string{"192.168.1.5", "10.0.0.3", "172.20.16.1"},
			preferred: "172.20.16.1",
			want:      []string{"172.20.16.1", "192.168.1.5", "10.0.0.3"},
		},
		{
			name:      "groups ordered 192.168 then 10 then 172.16 then other, ascending inside each",
			addrs:     []string{"172.20.16.1", "10.0.0.20", "192.168.0.9", "8.8.8.8", "10.0.0.2", "192.168.1.5", "172.31.255.1"},
			preferred: "",
			want:      []string{"192.168.0.9", "192.168.1.5", "10.0.0.2", "10.0.0.20", "172.20.16.1", "172.31.255.1", "8.8.8.8"},
		},
		{
			// No route probe result: ranking degrades to CIDR groups alone.
			name:      "empty preferred falls back to grouping only",
			addrs:     []string{"10.0.0.2", "192.168.1.5"},
			preferred: "",
			want:      []string{"192.168.1.5", "10.0.0.2"},
		},
		{
			// preferred not present in the list (route probe raced the
			// enumeration): a foreign value must NOT be prepended — only
			// grouped ordering applies.
			name:      "preferred absent from the list is not prepended",
			addrs:     []string{"10.0.0.2", "192.168.1.5"},
			preferred: "203.0.113.7",
			want:      []string{"192.168.1.5", "10.0.0.2"},
		},
		{
			name:      "empty list stays empty non-nil",
			addrs:     []string{},
			preferred: "192.168.1.5",
			want:      []string{},
		},
		{
			name:      "unparseable entry lands in the tail group",
			addrs:     []string{"not-an-ip", "192.168.1.5"},
			preferred: "",
			want:      []string{"192.168.1.5", "not-an-ip"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rankLanAddresses(c.addrs, c.preferred)
			if len(got) != len(c.want) {
				t.Fatalf("rankLanAddresses(%v, %q) = %v, want %v", c.addrs, c.preferred, got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("rankLanAddresses[%d] = %q, want %q (full: %v)", i, got[i], c.want[i], got)
				}
			}
			if got == nil {
				t.Error("result must be non-nil for the JSON [] contract")
			}
		})
	}
}

// TestGetLanAddressesOutboundProbe wiring: GetLanAddresses feeds
// preferredOutboundIP's answer into rankLanAddresses — a succeeding probe
// seam value moves the route-chosen address to the front even when it is in
// a lower CIDR group; a failing probe degrades to grouped ordering (the WSL
// address still lands behind the home-LAN one, as the TestRankLanAddresses
// row covers).
func TestGetLanAddressesOutboundProbe(t *testing.T) {
	origDial := outboundDial
	origIfaces := lanInterfaces
	origAddrs := interfaceAddrs
	t.Cleanup(func() {
		outboundDial = origDial
		lanInterfaces = origIfaces
		interfaceAddrs = origAddrs
	})

	lanInterfaces = func() ([]net.Interface, error) {
		// Both names are physical-looking: the virtual-NIC name pruning is a
		// FILTER concern (covered in TestFilterLanAddresses); this wiring test
		// needs both addresses to survive filtering so only the ranking is
		// under test.
		return []net.Interface{fakeIface("Ethernet 2", net.FlagUp), fakeIface("Ethernet", net.FlagUp)}, nil
	}
	// Canned per-interface addresses: fake interfaces carry no real kernel
	// index, so the production net.Interface.Addrs lookup must not run.
	interfaceAddrs = func(iface net.Interface) ([]net.Addr, error) {
		if iface.Name == "Ethernet 2" {
			return []net.Addr{ipNetAddr("172.20.16.1/20")}, nil
		}
		return []net.Addr{ipNetAddr("192.168.1.5/24")}, nil
	}

	t.Run("failing probe degrades to CIDR grouping", func(t *testing.T) {
		outboundDial = func(network, addr string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Err: net.UnknownNetworkError("injected")}
		}
		got := (&App{}).GetLanAddresses()
		want := []string{"192.168.1.5", "172.20.16.1"}
		if len(got) != len(want) {
			t.Fatalf("GetLanAddresses = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("GetLanAddresses[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
			}
		}
	})

	t.Run("succeeding probe pins the outbound address first", func(t *testing.T) {
		// A connected UDP conn whose LocalAddr reports the physical NIC: only
		// LocalAddr/Close are exercised by preferredOutboundIP.
		outboundDial = func(network, addr string) (net.Conn, error) {
			return &fakeUDPConn{local: &net.UDPAddr{IP: net.ParseIP("172.20.16.1")}}, nil
		}
		got := (&App{}).GetLanAddresses()
		// The route probe says peers should use 172.20.16.1 (e.g. the WSL
		// switch IS the default route in this synthetic environment): it
		// leads despite the lower group.
		want := []string{"172.20.16.1", "192.168.1.5"}
		if len(got) != len(want) {
			t.Fatalf("GetLanAddresses = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("GetLanAddresses[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
			}
		}
	})
}

// fakeUDPConn is the minimal net.Conn preferredOutboundIP needs (LocalAddr
// + Close; every other method is a no-op panic guard).
type fakeUDPConn struct {
	local *net.UDPAddr
}

func (c *fakeUDPConn) LocalAddr() net.Addr { return c.local }
func (c *fakeUDPConn) Close() error        { return nil }

func (c *fakeUDPConn) Read(b []byte) (int, error)       { return 0, io.EOF }
func (c *fakeUDPConn) Write(b []byte) (int, error)      { return 0, io.EOF }
func (c *fakeUDPConn) RemoteAddr() net.Addr             { return c.local }
func (c *fakeUDPConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeUDPConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeUDPConn) SetWriteDeadline(time.Time) error { return nil }

// TestPreferredOutboundIPLiveSmoke runs the real UDP route probe once (CI
// hosts without a default route legally return ""); it must never panic and
// only ever yields a parseable IPv4 or "".
func TestPreferredOutboundIPLiveSmoke(t *testing.T) {
	orig := outboundDial
	outboundDial = func(network, addr string) (net.Conn, error) {
		return net.DialTimeout(network, addr, 2*time.Second)
	}
	t.Cleanup(func() { outboundDial = orig })

	got := preferredOutboundIP()
	if got == "" {
		return // no route in this environment: legal degradation
	}
	ip := net.ParseIP(got)
	if ip == nil || ip.To4() == nil {
		t.Errorf("preferredOutboundIP returned a non-IPv4 value: %q", got)
	}
}
