package discover

import (
	"net"
	"reflect"
	"testing"

	"github.com/grandcat/zeroconf"
)

func testEntry() *zeroconf.ServiceEntry {
	return &zeroconf.ServiceEntry{
		Instance: "node-b",
		Service:  ServiceName,
		Domain:   MDNSDomain,
		HostName: "node-b.local.",
		Port:     7946,
		Text: []string{
			"node=node-b",
			"id=pubkey-b",
			"port=7946",
			"ver=dev",
			"mesh_fp=",
		},
		AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")},
		AddrIPv6: []net.IP{net.ParseIP("fe80::1"), net.ParseIP("2001:db8::1")},
	}
}

func TestBuildTXTParseRoundTrip(t *testing.T) {
	info := LocalInfo{NodeName: "node-b", IdentityKey: "pubkey-b", Port: 7946, Version: "dev"}
	peer, ok := parsePeer(&zeroconf.ServiceEntry{
		HostName: "node-b.local.",
		Port:     7946,
		Text:     buildTXT(info),
		AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")},
	})
	if !ok {
		t.Fatal("expected parseable peer")
	}
	want := DiscoveredPeer{
		NodeName:    "node-b",
		IdentityKey: "pubkey-b",
		Addrs:       []string{"192.168.1.20:7946"},
		Version:     "dev",
	}
	if !reflect.DeepEqual(peer, want) {
		t.Fatalf("got %+v, want %+v", peer, want)
	}
}

func TestParsePeerFilters(t *testing.T) {
	peer, ok := parsePeer(testEntry())
	if !ok {
		t.Fatal("expected parseable peer")
	}
	// Link-local IPv6 must be dropped (undialable without a zone);
	// global IPv6 and IPv4 are kept.
	want := []string{"192.168.1.20:7946", "[2001:db8::1]:7946"}
	if !reflect.DeepEqual(peer.Addrs, want) {
		t.Fatalf("got addrs %v, want %v", peer.Addrs, want)
	}
}

func TestParsePeerRejects(t *testing.T) {
	cases := map[string]*zeroconf.ServiceEntry{
		"nil":      nil,
		"zeroPort": {Port: 0, Text: []string{"id=x"}, AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")}},
		"noID":     {Port: 7946, Text: []string{"node=x"}, AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")}},
		"noAddrs":  {Port: 7946, Text: []string{"id=x"}},
		"garbage":  {Port: 7946, Text: []string{"not-a-record", "=x", "id="}, AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")}},
		"badIP":    {Port: 7946, Text: []string{"id=x"}, AddrIPv4: []net.IP{net.ParseIP("0.0.0.0")}},
	}
	for name, entry := range cases {
		if _, ok := parsePeer(entry); ok {
			t.Errorf("expected rejection for %s", name)
		}
	}
}

func TestParsePeerNameFallback(t *testing.T) {
	peer, ok := parsePeer(&zeroconf.ServiceEntry{
		HostName: "node-c.local.",
		Port:     7946,
		Text:     []string{"id=pubkey-c"},
		AddrIPv4: []net.IP{net.ParseIP("192.168.1.21")},
	})
	if !ok || peer.NodeName != "node-c.local." {
		t.Fatalf("expected hostname fallback, got %+v", peer)
	}
}
