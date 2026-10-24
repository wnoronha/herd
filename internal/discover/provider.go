package discover

import (
	"context"
	"sync"
	"time"
)

// ServiceName is the mDNS/DNS-SD service type herds announce and browse.
const ServiceName = "_herd._tcp"

// LocalInfo describes this node for announcement.
type LocalInfo struct {
	NodeName    string
	IdentityKey string
	Addr        string
	Port        int
	Version     string
	MeshFP      string
}

// DiscoveredPeer is one sighting of a peer from any Provider.
type DiscoveredPeer struct {
	NodeName    string
	IdentityKey string
	Addrs       []string
	Version     string
	MeshFP      string
}

// Provider sources peer sightings (mDNS now, others later).
type Provider interface {
	// Browse emits sightings until ctx ends, then returns ctx.Err().
	Browse(ctx context.Context, emit func(DiscoveredPeer)) error
	// Announce advertises this node; Withdraw revokes it.
	Announce(info LocalInfo) error
	Withdraw() error
}

// JoinFunc joins the mesh via one dialable address.
type JoinFunc func(ctx context.Context, addr string) error

// MemberFunc reports whether an identity key is already a mesh member.
type MemberFunc func(identityKey string) bool

// peerState tracks join attempts for one identity.
type peerState struct {
	inflight    bool
	joined      bool
	failures    int
	lastAttempt time.Time
}

// Pipeline turns sightings into at-most-once joins with backoff.
// It never imports the daemon: joining and membership are injected.
type Pipeline struct {
	join     JoinFunc
	isMember MemberFunc

	mu   sync.Mutex
	seen map[string]*peerState

	// Tunables; zero values select defaults.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	JoinTimeout time.Duration
}

// NewPipeline builds a Pipeline with the given join and membership funcs.
func NewPipeline(join JoinFunc, isMember MemberFunc) *Pipeline {
	return &Pipeline{
		join:     join,
		isMember: isMember,
		seen:     make(map[string]*peerState),
	}
}

func (p *Pipeline) baseBackoff() time.Duration {
	if p.BaseBackoff > 0 {
		return p.BaseBackoff
	}
	return 5 * time.Second
}

func (p *Pipeline) maxBackoff() time.Duration {
	if p.MaxBackoff > 0 {
		return p.MaxBackoff
	}
	return 5 * time.Minute
}

func (p *Pipeline) joinTimeout() time.Duration {
	if p.JoinTimeout > 0 {
		return p.JoinTimeout
	}
	return 30 * time.Second
}

// backoffFor returns the delay before the next attempt after n failures.
func (p *Pipeline) backoffFor(n int) time.Duration {
	d := p.baseBackoff()
	for i := 0; i < n && d < p.maxBackoff(); i++ {
		d *= 2
		if d > p.maxBackoff() {
			return p.maxBackoff()
		}
	}
	return d
}

// Handle processes one sighting: dedup by identity, skip members and
// in-flight/cooling-down joins, else join asynchronously with backoff.
func (p *Pipeline) Handle(ctx context.Context, peer DiscoveredPeer) {
	if peer.IdentityKey == "" || len(peer.Addrs) == 0 {
		return
	}
	if p.isMember != nil && p.isMember(peer.IdentityKey) {
		p.markJoined(peer.IdentityKey)
		return
	}

	p.mu.Lock()
	st, ok := p.seen[peer.IdentityKey]
	if !ok {
		st = &peerState{}
		p.seen[peer.IdentityKey] = st
	}
	if st.inflight || st.joined {
		p.mu.Unlock()
		return
	}
	if !st.lastAttempt.IsZero() && time.Since(st.lastAttempt) < p.backoffFor(st.failures) {
		p.mu.Unlock()
		return
	}
	st.inflight = true
	p.mu.Unlock()

	go p.joinLoop(ctx, peer)
}

// markJoined records an externally-observed membership (e.g. gossip beat us).
func (p *Pipeline) markJoined(identityKey string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.seen[identityKey]
	if !ok {
		st = &peerState{}
		p.seen[identityKey] = st
	}
	st.joined = true
	st.failures = 0
}

// joinLoop attempts joins until success, membership, or ctx end.
func (p *Pipeline) joinLoop(ctx context.Context, peer DiscoveredPeer) {
	defer func() {
		p.mu.Lock()
		if st, ok := p.seen[peer.IdentityKey]; ok {
			st.inflight = false
		}
		p.mu.Unlock()
	}()

	for _, addr := range peer.Addrs {
		if p.settled(peer.IdentityKey) {
			return
		}
		jctx, cancel := context.WithTimeout(ctx, p.joinTimeout())
		err := p.join(jctx, addr)
		cancel()
		p.mu.Lock()
		st := p.seen[peer.IdentityKey]
		st.lastAttempt = time.Now()
		if err == nil {
			st.joined = true
			st.failures = 0
			p.mu.Unlock()
			return
		}
		st.failures++
		p.mu.Unlock()
	}
}

// settled reports membership or cancellation.
func (p *Pipeline) settled(identityKey string) bool {
	if p.isMember != nil && p.isMember(identityKey) {
		p.markJoined(identityKey)
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[identityKey].joined
}
