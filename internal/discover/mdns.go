package discover

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
)

// TXT record keys for herd instances. mesh_fp is reserved for the future
// mesh-key epic and stays empty in v1 (open mesh). addr pins one dialable
// IP (from --advertise-addr or LAN detection) so multihomed hosts with
// noisy interfaces (docker0, VPNs) still advertise a working address;
// browsers prefer it over mDNS-resolved IPs.
const (
	txtNode    = "node"
	txtID      = "id"
	txtAddr    = "addr"
	txtPort    = "port"
	txtVersion = "ver"
	txtMeshFP  = "mesh_fp"
)

// MDNSDomain is the default DNS-SD domain for local discovery.
const MDNSDomain = "local."

// MDNSProvider implements Provider via mDNS/DNS-SD.
type MDNSProvider struct {
	domain string

	mu     sync.Mutex
	server *zeroconf.Server
}

// NewMDNSProvider builds a provider for domain ("" selects MDNSDomain).
func NewMDNSProvider(domain string) *MDNSProvider {
	if strings.TrimSpace(domain) == "" {
		domain = MDNSDomain
	}
	return &MDNSProvider{domain: domain}
}

// Announce advertises this node on the LAN.
func (m *MDNSProvider) Announce(info LocalInfo) error {
	text := buildTXT(info)
	server, err := zeroconf.Register(info.NodeName, ServiceName, m.domain, info.Port, text, nil)
	if err != nil {
		return fmt.Errorf("mdns announce: %w", err)
	}
	m.mu.Lock()
	if m.server != nil {
		m.server.Shutdown()
	}
	m.server = server
	m.mu.Unlock()
	return nil
}

// Withdraw revokes the LAN advertisement.
func (m *MDNSProvider) Withdraw() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		m.server.Shutdown()
		m.server = nil
	}
	return nil
}

// Browse emits sightings for herd instances until ctx ends.
func (m *MDNSProvider) Browse(ctx context.Context, emit func(DiscoveredPeer)) error {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return fmt.Errorf("mdns browse: %w", err)
	}
	entries := make(chan *zeroconf.ServiceEntry)
	go func() {
		_ = resolver.Browse(ctx, ServiceName, m.domain, entries)
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case entry, ok := <-entries:
			if !ok {
				return nil
			}
			if peer, good := parsePeer(entry); good {
				emit(peer)
			}
		}
	}
}

// buildTXT encodes node info as DNS-SD TXT records.
func buildTXT(info LocalInfo) []string {
	return []string{
		txtNode + "=" + info.NodeName,
		txtID + "=" + info.IdentityKey,
		txtAddr + "=" + info.Addr,
		txtPort + "=" + strconv.Itoa(info.Port),
		txtVersion + "=" + info.Version,
		txtMeshFP + "=" + info.MeshFP,
	}
}

// parsePeer converts a zeroconf entry to a pipeline sighting.
// It returns false for entries that cannot identify or reach a peer.
func parsePeer(entry *zeroconf.ServiceEntry) (DiscoveredPeer, bool) {
	if entry == nil || entry.Port <= 0 {
		return DiscoveredPeer{}, false
	}
	txt := parseTXT(entry.Text)
	id := txt[txtID]
	if id == "" {
		return DiscoveredPeer{}, false
	}
	var addrs []string
	seen := make(map[string]bool)
	// A pinned addr (operator override or LAN detection) is tried first;
	// resolved IPs follow as fallback for stale/partial configurations.
	if pinned := txt[txtAddr]; pinned != "" {
		if ip := net.ParseIP(pinned); ip != nil && usableIP(ip) {
			a := net.JoinHostPort(ip.String(), strconv.Itoa(entry.Port))
			addrs = append(addrs, a)
			seen[a] = true
		}
	}
	for _, ip := range entry.AddrIPv4 {
		if usableIP(ip) {
			if a := net.JoinHostPort(ip.String(), strconv.Itoa(entry.Port)); !seen[a] {
				addrs = append(addrs, a)
				seen[a] = true
			}
		}
	}
	for _, ip := range entry.AddrIPv6 {
		if usableIP(ip) && ip.IsGlobalUnicast() {
			if a := net.JoinHostPort(ip.String(), strconv.Itoa(entry.Port)); !seen[a] {
				addrs = append(addrs, a)
				seen[a] = true
			}
		}
	}
	if len(addrs) == 0 {
		return DiscoveredPeer{}, false
	}
	return DiscoveredPeer{
		NodeName:    peerName(entry, txt),
		IdentityKey: id,
		Addrs:       addrs,
		Version:     txt[txtVersion],
		MeshFP:      txt[txtMeshFP],
	}, true
}

// peerName prefers the TXT node name, falling back to the DNS hostname.
func peerName(entry *zeroconf.ServiceEntry, txt map[string]string) string {
	if name := txt[txtNode]; name != "" {
		return name
	}
	return entry.HostName
}

// parseTXT decodes "k=v" TXT records; malformed entries are skipped.
func parseTXT(text []string) map[string]string {
	out := make(map[string]string, len(text))
	for _, kv := range text {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// usableIP rejects unspecified, multicast, and link-local addresses.
// Link-local (169.254/10, fe80::/10) is segment-ambiguous without a zone
// and never the right answer for mesh dialing.
func usableIP(ip net.IP) bool {
	return !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}
