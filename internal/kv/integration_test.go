package kv_test

import (
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/hashicorp/memberlist"
	"herd/internal/identity"
	"herd/internal/kv"
	"herd/internal/roster"
	"herd/internal/transport/tailcat"
)

type clusterKVNode struct {
	Name       string
	Identity   *identity.Identity
	Transport  *tailcat.Transport
	Roster     *roster.Store
	Delegate   *roster.Delegate
	KVStore    *kv.Store
	KVDelegate *kv.GossipDelegate
	ML         *memberlist.Memberlist
}

func createClusterKVNode(t *testing.T, name string, psk identity.Key) *clusterKVNode {
	t.Helper()

	id, err := identity.NewIdentity(name)
	if err != nil {
		t.Fatalf("failed to create identity for %s: %v", name, err)
	}
	id.PreSharedKey = psk

	tr, err := tailcat.NewTransport(tailcat.Config{
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
	rosterStore := roster.NewStore(meta)
	del := roster.NewDelegate(rosterStore, meta)

	kvStore := kv.NewStore(name)
	var mlRef *memberlist.Memberlist
	kvDel := kv.NewGossipDelegate(kvStore, func() int {
		if mlRef != nil {
			return mlRef.NumMembers()
		}
		return 1
	})
	del.SetCustomDelegate(kvDel)

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
	mlRef = ml

	return &clusterKVNode{
		Name:       name,
		Identity:   id,
		Transport:  tr,
		Roster:     rosterStore,
		Delegate:   del,
		KVStore:    kvStore,
		KVDelegate: kvDel,
		ML:         ml,
	}
}

func (n *clusterKVNode) Close() {
	if n.ML != nil {
		_ = n.ML.Leave(500 * time.Millisecond)
		_ = n.ML.Shutdown()
	}
	if n.Transport != nil {
		_ = n.Transport.Shutdown()
	}
}

func TestThreeNodeKVPropagation(t *testing.T) {
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("failed to generate PSK: %v", err)
	}

	node1 := createClusterKVNode(t, "kv-node-1", psk)
	defer node1.Close()

	node2 := createClusterKVNode(t, "kv-node-2", psk)
	defer node2.Close()

	node3 := createClusterKVNode(t, "kv-node-3", psk)
	defer node3.Close()

	// Form cluster: 2 joins 1, 3 joins 2
	addr1 := fmt.Sprintf("127.0.0.1:%d", node1.Transport.GetPort())
	if _, err := node2.ML.Join([]string{addr1}); err != nil {
		t.Fatalf("node-2 failed to join node-1: %v", err)
	}

	addr2 := fmt.Sprintf("127.0.0.1:%d", node2.Transport.GetPort())
	if _, err := node3.ML.Join([]string{addr2}); err != nil {
		t.Fatalf("node-3 failed to join node-2: %v", err)
	}

	// Write on Node 1
	node1.KVDelegate.SetAndBroadcast("test_prop_key", []byte("hello_from_node_1"), 0)

	// Verify propagation to Node 3
	deadline := time.Now().Add(3 * time.Second)
	propagated := false
	for time.Now().Before(deadline) {
		if entry, ok := node3.KVStore.Get("test_prop_key"); ok {
			if string(entry.Value) == "hello_from_node_1" {
				propagated = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !propagated {
		t.Fatalf("KV update failed to propagate from node-1 to node-3 within deadline")
	}

	// Delete on Node 2 and verify propagation to Node 1 & 3
	node2.KVDelegate.DeleteAndBroadcast("test_prop_key")
	deletePropagated := false
	delDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(delDeadline) {
		_, ok1 := node1.KVStore.Get("test_prop_key")
		_, ok3 := node3.KVStore.Get("test_prop_key")
		if !ok1 && !ok3 {
			deletePropagated = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !deletePropagated {
		t.Fatalf("KV delete failed to propagate to node-1 and node-3 within deadline")
	}
}

func TestSimultaneousConflictResolution(t *testing.T) {
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("failed to generate PSK: %v", err)
	}

	node1 := createClusterKVNode(t, "conflict-node-1", psk)
	defer node1.Close()

	node2 := createClusterKVNode(t, "conflict-node-2", psk)
	defer node2.Close()

	node3 := createClusterKVNode(t, "conflict-node-3", psk)
	defer node3.Close()

	addr1 := fmt.Sprintf("127.0.0.1:%d", node1.Transport.GetPort())
	if _, err := node2.ML.Join([]string{addr1}); err != nil {
		t.Fatalf("failed to join cluster: %v", err)
	}
	if _, err := node3.ML.Join([]string{addr1}); err != nil {
		t.Fatalf("failed to join cluster: %v", err)
	}

	// Write conflicting values from node1 and node2 concurrently
	now := time.Now().UnixNano()
	e1 := &kv.Entry{
		Key:          "conflict_key",
		Value:        []byte("val_node_1"),
		Version:      1,
		Timestamp:    now + 10,
		WriterNodeID: "conflict-node-1",
	}
	e2 := &kv.Entry{
		Key:          "conflict_key",
		Value:        []byte("val_node_2"),
		Version:      1,
		Timestamp:    now + 20, // Higher timestamp -> should win
		WriterNodeID: "conflict-node-2",
	}

	node1.KVStore.MergeEntry(e1)
	node1.KVDelegate.QueueBroadcast(e1)

	node2.KVStore.MergeEntry(e2)
	node2.KVDelegate.QueueBroadcast(e2)

	// Wait for convergence on all 3 nodes
	deadline := time.Now().Add(3 * time.Second)
	converged := false
	for time.Now().Before(deadline) {
		v1, ok1 := node1.KVStore.Get("conflict_key")
		v2, ok2 := node2.KVStore.Get("conflict_key")
		v3, ok3 := node3.KVStore.Get("conflict_key")

		if ok1 && ok2 && ok3 {
			if string(v1.Value) == "val_node_2" && string(v2.Value) == "val_node_2" && string(v3.Value) == "val_node_2" {
				converged = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !converged {
		t.Fatalf("cluster failed to converge deterministically to winner of LWW conflict")
	}
}

func TestAntiEntropyStateRecovery(t *testing.T) {
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("failed to generate PSK: %v", err)
	}

	node1 := createClusterKVNode(t, "ae-node-1", psk)
	defer node1.Close()

	node2 := createClusterKVNode(t, "ae-node-2", psk)
	defer node2.Close()

	addr1 := fmt.Sprintf("127.0.0.1:%d", node1.Transport.GetPort())
	if _, err := node2.ML.Join([]string{addr1}); err != nil {
		t.Fatalf("node2 failed to join node1: %v", err)
	}

	// Node 1 writes multiple keys
	node1.KVDelegate.SetAndBroadcast("ae_key_1", []byte("value_1"), 0)
	node1.KVDelegate.SetAndBroadcast("ae_key_2", []byte("value_2"), 0)
	node1.KVDelegate.SetAndBroadcast("ae_key_3", []byte("value_3"), 0)

	// Create late-joining node 3 (simulating recovery / catch-up)
	node3 := createClusterKVNode(t, "ae-node-3", psk)
	defer node3.Close()

	if _, err := node3.ML.Join([]string{addr1}); err != nil {
		t.Fatalf("node3 failed to join node1: %v", err)
	}

	// Verify Node 3 recovers all 3 keys via Push/Pull anti-entropy
	deadline := time.Now().Add(4 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		k1, ok1 := node3.KVStore.Get("ae_key_1")
		k2, ok2 := node3.KVStore.Get("ae_key_2")
		k3, ok3 := node3.KVStore.Get("ae_key_3")

		if ok1 && ok2 && ok3 {
			if string(k1.Value) == "value_1" && string(k2.Value) == "value_2" && string(k3.Value) == "value_3" {
				recovered = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !recovered {
		t.Fatalf("late-joining node-3 failed to recover keys via anti-entropy sync")
	}
}
