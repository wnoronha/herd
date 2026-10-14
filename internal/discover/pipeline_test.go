package discover

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func testPeer(id string, addrs ...string) DiscoveredPeer {
	return DiscoveredPeer{NodeName: "node-" + id, IdentityKey: "key-" + id, Addrs: addrs}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func failuresOf(p *Pipeline, id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.seen[id]; ok {
		return st.failures
	}
	return -1
}

func TestBackoffFor(t *testing.T) {
	p := NewPipeline(nil, nil)
	p.BaseBackoff = time.Second
	p.MaxBackoff = 5 * time.Second
	cases := map[int]time.Duration{
		0:  time.Second,
		1:  2 * time.Second,
		2:  4 * time.Second,
		3:  5 * time.Second,
		10: 5 * time.Second,
	}
	for n, want := range cases {
		if got := p.backoffFor(n); got != want {
			t.Errorf("backoffFor(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestHandleJoinsExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	p := NewPipeline(func(ctx context.Context, addr string) error {
		calls.Add(1)
		return nil
	}, nil)

	peer := testPeer("a", "10.0.0.2:7946")
	p.Handle(context.Background(), peer)
	p.Handle(context.Background(), peer)
	p.Handle(context.Background(), peer)

	waitFor(t, "join", func() bool { return calls.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected exactly 1 join, got %d", n)
	}
}

func TestHandleSkipsMembers(t *testing.T) {
	var calls atomic.Int32
	p := NewPipeline(func(ctx context.Context, addr string) error {
		calls.Add(1)
		return nil
	}, func(id string) bool { return id == "key-a" })

	p.Handle(context.Background(), testPeer("a", "10.0.0.2:7946"))
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected 0 joins for existing member, got %d", n)
	}
	if !p.seen["key-a"].joined {
		t.Fatal("expected member recorded as joined")
	}
}

func TestHandleIgnoresEmpty(t *testing.T) {
	var calls atomic.Int32
	p := NewPipeline(func(ctx context.Context, addr string) error {
		calls.Add(1)
		return nil
	}, nil)

	p.Handle(context.Background(), DiscoveredPeer{Addrs: []string{"10.0.0.2:7946"}})
	p.Handle(context.Background(), DiscoveredPeer{IdentityKey: "key-b"})
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("expected 0 joins for empty sightings, got %d", n)
	}
}

func TestHandleBackoffAcrossSightings(t *testing.T) {
	var calls atomic.Int32
	p := NewPipeline(func(ctx context.Context, addr string) error {
		calls.Add(1)
		return context.DeadlineExceeded
	}, nil)
	p.BaseBackoff = 300 * time.Millisecond
	p.JoinTimeout = 50 * time.Millisecond

	p.Handle(context.Background(), testPeer("a", "10.0.0.2:7946"))
	waitFor(t, "first failure", func() bool { return failuresOf(p, "key-a") == 1 })

	// Immediate re-sighting stays inside backoff: no new attempt.
	p.Handle(context.Background(), testPeer("a", "10.0.0.2:7946"))
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected backoff to suppress retry, calls = %d", n)
	}

	// After backoff elapses, the next sighting retries.
	// (failures=1 with 300ms base backs off 600ms; already waited ~100ms.)
	time.Sleep(600 * time.Millisecond)
	p.Handle(context.Background(), testPeer("a", "10.0.0.2:7946"))
	waitFor(t, "second failure", func() bool { return failuresOf(p, "key-a") == 2 })
}

func TestHandleTriesNextAddr(t *testing.T) {
	var calls atomic.Int32
	p := NewPipeline(func(ctx context.Context, addr string) error {
		calls.Add(1)
		if addr == "10.0.0.2:7946" {
			return context.DeadlineExceeded
		}
		return nil
	}, nil)

	p.Handle(context.Background(), testPeer("a", "10.0.0.2:7946", "10.0.0.3:7946"))
	waitFor(t, "join success", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.seen["key-a"].joined
	})
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected 2 attempts (failover), got %d", n)
	}
}

func TestHandleSeesLateMembership(t *testing.T) {
	var calls atomic.Int32
	member := atomic.Bool{}
	p := NewPipeline(func(ctx context.Context, addr string) error {
		calls.Add(1)
		member.Store(true) // gossip beat us: now a member
		return nil
	}, func(id string) bool { return member.Load() })

	p.Handle(context.Background(), testPeer("a", "10.0.0.2:7946"))
	waitFor(t, "join", func() bool { return calls.Load() == 1 })
	// Gossip converged mid-flight: further sightings must not rejoin.
	p.Handle(context.Background(), testPeer("a", "10.0.0.2:7946"))
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected no rejoin after membership, calls = %d", n)
	}
}
