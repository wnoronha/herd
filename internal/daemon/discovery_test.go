package daemon

import (
	"testing"

	"github.com/wnoronha/herd/internal/config"
	"github.com/wnoronha/herd/internal/discover"
	"github.com/wnoronha/herd/internal/logger"
	"github.com/wnoronha/herd/internal/roster"
)

func testDiscoveryDaemon(t *testing.T, nodeName string) *Daemon {
	t.Helper()
	cfg, err := config.Load(config.Options{
		NodeName:   nodeName,
		DataDir:    t.TempDir(),
		SocketPath: t.TempDir() + "/herd.sock",
		BindAddr:   "127.0.0.1",
		BindPort:   0,
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	d, err := New(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	return d
}

func TestWarnOnNameClash(t *testing.T) {
	d := testDiscoveryDaemon(t, "node-a")
	d.Store = roster.NewStore(&roster.NodeMeta{NodeName: "node-a", TailcatAddr: "pubkey-a"})
	d.Store.UpsertMember("node-b", "10.0.0.2", 7946, "alive",
		&roster.NodeMeta{NodeName: "node-b", TailcatAddr: "pubkey-b-real"})

	// Same hostname, different identity: warn once, record it.
	d.warnOnNameClash(discover.DiscoveredPeer{NodeName: "node-b", IdentityKey: "pubkey-b-impostor"})
	d.mu.RLock()
	warned := d.warnedClashes["pubkey-b-impostor"]
	d.mu.RUnlock()
	if !warned {
		t.Fatal("expected clash warning recorded")
	}
	// Idempotent: second sighting is a no-op (no panic, still recorded).
	d.warnOnNameClash(discover.DiscoveredPeer{NodeName: "node-b", IdentityKey: "pubkey-b-impostor"})

	// Same identity, same name: not a clash.
	d.warnOnNameClash(discover.DiscoveredPeer{NodeName: "node-b", IdentityKey: "pubkey-b-real"})
	d.mu.RLock()
	_, ok := d.warnedClashes["pubkey-b-real"]
	d.mu.RUnlock()
	if ok {
		t.Fatal("same identity must not warn")
	}

	// Unknown name: not a clash.
	d.warnOnNameClash(discover.DiscoveredPeer{NodeName: "node-c", IdentityKey: "pubkey-c"})
	d.mu.RLock()
	_, ok = d.warnedClashes["pubkey-c"]
	d.mu.RUnlock()
	if ok {
		t.Fatal("unknown name must not warn")
	}
}

func TestIsMeshMember(t *testing.T) {
	d := testDiscoveryDaemon(t, "node-a")
	d.Store = roster.NewStore(&roster.NodeMeta{NodeName: "node-a", TailcatAddr: "pubkey-a"})
	if !d.isMeshMember("pubkey-a") {
		t.Fatal("local node is in its own roster")
	}
	d.Store.UpsertMember("node-b", "10.0.0.2", 7946, "alive",
		&roster.NodeMeta{NodeName: "node-b", TailcatAddr: "pubkey-b"})
	if !d.isMeshMember("pubkey-b") {
		t.Fatal("expected member by identity key")
	}
	if d.isMeshMember("pubkey-unknown") {
		t.Fatal("unknown identity must not count")
	}
}
