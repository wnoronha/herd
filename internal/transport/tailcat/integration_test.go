package tailcat

import (
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/hashicorp/memberlist"
	"herd/internal/identity"
	"herd/internal/roster"
)

type testNode struct {
	Name      string
	Identity  *identity.Identity
	Transport *Transport
	Store     *roster.Store
	Delegate  *roster.Delegate
	ML        *memberlist.Memberlist
}

func createTestNode(t *testing.T, name string, psk identity.Key) *testNode {
	t.Helper()

	id, err := identity.NewIdentity(name)
	if err != nil {
		t.Fatalf("failed to create identity for %s: %v", name, err)
	}
	id.PreSharedKey = psk

	tr, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id,
	})
	if err != nil {
		t.Fatalf("failed to create transport for %s: %v", name, err)
	}

	meta := &roster.NodeMeta{
		NodeName:    name,
		TailcatAddr: id.TailcatAddr(),
		Version:     "1.0.0",
		StartTime:   time.Now(),
	}
	store := roster.NewStore(meta)
	del := roster.NewDelegate(store, meta)

	mcfg := memberlist.DefaultLocalConfig()
	mcfg.Name = name
	mcfg.BindPort = tr.GetPort()
	mcfg.Transport = tr
	mcfg.Delegate = del
	mcfg.Events = del
	mcfg.Ping = del
	mcfg.Logger = log.New(io.Discard, "", 0)
	mcfg.ProbeInterval = 50 * time.Millisecond
	mcfg.ProbeTimeout = 100 * time.Millisecond
	mcfg.GossipInterval = 50 * time.Millisecond
	mcfg.PushPullInterval = 200 * time.Millisecond

	ml, err := memberlist.Create(mcfg)
	if err != nil {
		_ = tr.Shutdown()
		t.Fatalf("failed to create memberlist for %s: %v", name, err)
	}

	return &testNode{
		Name:      name,
		Identity:  id,
		Transport: tr,
		Store:     store,
		Delegate:  del,
		ML:        ml,
	}
}

func (n *testNode) Close() {
	if n.ML != nil {
		_ = n.ML.Leave(1 * time.Second)
		_ = n.ML.Shutdown()
	}
	if n.Transport != nil {
		_ = n.Transport.Shutdown()
	}
}

func TestTwoNodeGossipSync(t *testing.T) {
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("failed to generate PSK: %v", err)
	}

	node1 := createTestNode(t, "node-1", psk)
	defer node1.Close()

	node2 := createTestNode(t, "node-2", psk)
	defer node2.Close()

	// Join node2 to node1
	joinAddr := fmt.Sprintf("127.0.0.1:%d", node1.Transport.GetPort())
	num, err := node2.ML.Join([]string{joinAddr})
	if err != nil {
		t.Fatalf("node2 failed to join node1: %v", err)
	}
	if num < 1 {
		t.Fatalf("expected at least 1 node joined, got %d", num)
	}

	// Wait for gossip roster convergence
	deadline := time.Now().Add(5 * time.Second)
	converged := false
	for time.Now().Before(deadline) {
		if node1.ML.NumMembers() == 2 && node2.ML.NumMembers() == 2 &&
			len(node1.Store.GetMembers()) == 2 && len(node2.Store.GetMembers()) == 2 {
			converged = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !converged {
		t.Fatalf("nodes failed to converge in 5s. Node1 members: %d, Node2 members: %d",
			node1.ML.NumMembers(), node2.ML.NumMembers())
	}

	// Check metadata exchange
	m, ok := node1.Store.GetMember("node-2")
	if !ok || m.Meta == nil || m.Meta.TailcatAddr != node2.Identity.TailcatAddr() {
		t.Errorf("node-2 metadata not properly exchanged to node-1: %+v", m)
	}
}

func TestThreeNodeMeshConvergence(t *testing.T) {
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("failed to generate PSK: %v", err)
	}

	node1 := createTestNode(t, "node-a", psk)
	defer node1.Close()

	node2 := createTestNode(t, "node-b", psk)
	defer node2.Close()

	node3 := createTestNode(t, "node-c", psk)
	defer node3.Close()

	// node2 joins node1
	joinAddr1 := fmt.Sprintf("127.0.0.1:%d", node1.Transport.GetPort())
	if _, err := node2.ML.Join([]string{joinAddr1}); err != nil {
		t.Fatalf("node-b failed to join node-a: %v", err)
	}

	// node3 joins node2
	joinAddr2 := fmt.Sprintf("127.0.0.1:%d", node2.Transport.GetPort())
	if _, err := node3.ML.Join([]string{joinAddr2}); err != nil {
		t.Fatalf("node-c failed to join node-b: %v", err)
	}

	// All 3 nodes should discover each other via gossip mesh
	deadline := time.Now().Add(6 * time.Second)
	converged := false
	for time.Now().Before(deadline) {
		if node1.ML.NumMembers() == 3 && node2.ML.NumMembers() == 3 && node3.ML.NumMembers() == 3 {
			converged = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !converged {
		t.Fatalf("3-node mesh failed to converge. NumMembers: n1=%d, n2=%d, n3=%d",
			node1.ML.NumMembers(), node2.ML.NumMembers(), node3.ML.NumMembers())
	}

	// Verify all 3 nodes see all members in their Store
	for _, n := range []*testNode{node1, node2, node3} {
		members := n.Store.GetMembers()
		if len(members) < 3 {
			t.Errorf("node %s store has %d members, expected 3", n.Name, len(members))
		}
	}
}
