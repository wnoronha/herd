package daemon

import (
	"context"
	"errors"
	"net"

	"github.com/wnoronha/herd/internal/discover"
)

// discoverVersion is announced in TXT records (informational only).
const discoverVersion = "dev"

// startDiscovery announces this node on the LAN and browses for peers,
// feeding sightings into the join pipeline. It degrades gracefully:
// without multicast (CI sandboxes) it logs and leaves manual --join working.
func (d *Daemon) startDiscovery() {
	if d.Config == nil || !d.Config.Mdns {
		return
	}

	provider := discover.NewMDNSProvider(discover.MDNSDomain)
	pipeline := discover.NewPipeline(d.discoveryJoin, d.isMeshMember)

	info := discover.LocalInfo{
		NodeName:    d.Config.NodeName,
		IdentityKey: d.Identity.PublicKey.String(),
		Addr:        d.advertiseIP(),
		Port:        d.Transport.GetPort(),
		Version:     discoverVersion,
	}
	if err := provider.Announce(info); err != nil {
		d.Logger.Named("discover").Warnf("mDNS announce failed (continuing without advertising): %v", err)
	}

	dctx, cancel := context.WithCancel(d.getRunCtx())
	d.mu.Lock()
	d.discoverProvider = provider
	d.discoverPipeline = pipeline
	d.discoverCancel = cancel
	if d.warnedClashes == nil {
		d.warnedClashes = make(map[string]bool)
	}
	d.mu.Unlock()

	go func() {
		defer cancel()
		if err := provider.Browse(dctx, d.discoveryEmit); err != nil && !errors.Is(err, context.Canceled) {
			d.Logger.Named("discover").Warnf("mDNS browse ended: %v", err)
		}
	}()
	d.Logger.Named("discover").Infof("mDNS discovery active as '%s' (%s)", info.NodeName, discover.ServiceName)
}

// stopDiscovery revokes the LAN advertisement and stops browsing.
func (d *Daemon) stopDiscovery() {
	d.mu.Lock()
	provider := d.discoverProvider
	cancel := d.discoverCancel
	d.discoverProvider = nil
	d.discoverPipeline = nil
	d.discoverCancel = nil
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if provider != nil {
		if err := provider.Withdraw(); err != nil {
			d.Logger.Named("discover").Warnf("mDNS withdraw failed: %v", err)
		}
	}
}

// discoveryEmit filters one sighting: self, name clashes, then pipeline.
func (d *Daemon) discoveryEmit(peer discover.DiscoveredPeer) {
	if peer.IdentityKey == "" || peer.IdentityKey == d.Identity.PublicKey.String() {
		return
	}
	d.warnOnNameClash(peer)
	d.mu.RLock()
	pipeline := d.discoverPipeline
	d.mu.RUnlock()
	if pipeline == nil {
		return
	}
	pipeline.Handle(d.getRunCtx(), peer)
}

// warnOnNameClash logs once per identity when a peer reuses a member name.
func (d *Daemon) warnOnNameClash(peer discover.DiscoveredPeer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.warnedClashes == nil {
		d.warnedClashes = make(map[string]bool)
	}
	if d.warnedClashes[peer.IdentityKey] {
		return
	}
	if d.Store == nil {
		return
	}
	for _, m := range d.Store.GetMembers() {
		if m.Name == peer.NodeName && (m.Meta == nil || m.Meta.TailcatAddr != peer.IdentityKey) {
			d.warnedClashes[peer.IdentityKey] = true
			d.Logger.Named("discover").Warnf(
				"hostname clash: discovered '%s' but roster member '%s' has a different identity; not joining (rename one node)",
				peer.IdentityKey, peer.NodeName)
			return
		}
	}
}

// isMeshMember reports whether an identity key is already a member.
func (d *Daemon) isMeshMember(identityKey string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.Store == nil {
		return false
	}
	for _, m := range d.Store.GetMembers() {
		if m.Meta != nil && m.Meta.TailcatAddr == identityKey {
			return true
		}
	}
	return false
}

// discoveryJoin joins the mesh via one discovered address.
func (d *Daemon) discoveryJoin(ctx context.Context, addr string) error {
	_, err := d.JoinNode(ctx, addr)
	if err == nil {
		d.Logger.Named("discover").Infof("joined mesh via discovered peer %s", addr)
		return nil
	}
	// One line per failed round (already backed off by the pipeline).
	d.Logger.Named("discover").Warnf("discovery join via %s failed: %v", addr, err)
	return err
}

// advertiseIP resolves the LAN IP announced for discovery: explicit
// --advertise-addr wins, else auto-detection, else empty (browsers fall
// back to mDNS-resolved IPs).
func (d *Daemon) advertiseIP() string {
	if d.Config != nil && d.Config.AdvertiseAddr != "" {
		return advertiseHost(d.Config.AdvertiseAddr)
	}
	if ip, err := discover.LANIP(); err == nil {
		return ip.String()
	}
	return ""
}

// advertiseHost normalizes --advertise-addr: a bare IP/host is used as-is,
// host:port is reduced to its host (the gossip port travels separately).
func advertiseHost(s string) string {
	if host, _, err := net.SplitHostPort(s); err == nil && host != "" {
		return host
	}
	return s
}
