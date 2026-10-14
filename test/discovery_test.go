package test

import (
	"net"
	"testing"
	"time"

	"github.com/wnoronha/herd/internal/identity"
	"github.com/wnoronha/herd/internal/testutil"
)

// probeMulticast skips the test when LAN multicast is unavailable
// (CI sandboxes). Where multicast works — including this machine —
// the real discovery path runs.
func probeMulticast(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping multicast discovery in -short mode")
	}
	conn, err := net.ListenPacket("udp4", "224.0.0.251:0")
	if err != nil {
		t.Skipf("multicast unavailable: %v", err)
	}
	_ = conn.Close()
}

// enableDiscovery opts back into mDNS for one test (the hermetic
// environment disables it) with loopback advertisement for test daemons.
func enableDiscovery(t *testing.T) {
	t.Helper()
	testutil.SetupHermeticEnvironment(t)
	t.Setenv("HERD_MDNS", "1")
	t.Setenv("HERD_ADVERTISE_ADDR", "127.0.0.1")
}

func verifyDistinctIdentities(t *testing.T, nodes []*clusterNode, names []string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, n := range nodes {
			// Daemon-local store carries identity metadata (TailcatAddr);
			// the IPC view does not.
			members := n.Daemon.Store.GetMembers()
			seenNames := make(map[string]bool)
			seenKeys := make(map[string]bool)
			for _, m := range members {
				if m.Status != "alive" {
					continue
				}
				seenNames[m.Name] = true
				if m.Meta != nil {
					if seenKeys[m.Meta.TailcatAddr] {
						ok = false
						break
					}
					seenKeys[m.Meta.TailcatAddr] = true
				}
			}
			for _, want := range names {
				if !seenNames[want] {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("members %v did not converge distinctly", names)
}

// TestDiscoveryLateJoiner boots B after A with no --join anywhere:
// B must hop on via mDNS alone.
func TestDiscoveryLateJoiner(t *testing.T) {
	probeMulticast(t)
	enableDiscovery(t)

	tmpDir := t.TempDir()
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}
	cluster := newLocalCluster(t, tmpDir, psk)
	defer cluster.Close()

	nodeA := cluster.StartNode("disc-late-a", "")
	time.Sleep(2 * time.Second)
	nodeB := cluster.StartNode("disc-late-b", "")

	verifyClusterConvergence(t, []*clusterNode{nodeA, nodeB}, 2)
	verifyDistinctIdentities(t, []*clusterNode{nodeA, nodeB}, []string{"disc-late-a", "disc-late-b"})
}

// TestDiscoveryRestartRejoin stops B, restarts it with the same identity,
// and requires rejoin with no duplicate members.
func TestDiscoveryRestartRejoin(t *testing.T) {
	probeMulticast(t)
	enableDiscovery(t)

	tmpDir := t.TempDir()
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}
	cluster := newLocalCluster(t, tmpDir, psk)
	defer cluster.Close()

	nodeA := cluster.StartNode("disc-re-a", "")
	nodeB := cluster.StartNode("disc-re-b", "")
	verifyClusterConvergence(t, []*clusterNode{nodeA, nodeB}, 2)

	cluster.StopNode("disc-re-b")
	time.Sleep(3 * time.Second)

	nodeB2 := cluster.StartNode("disc-re-b", "")
	verifyClusterConvergence(t, []*clusterNode{nodeA, nodeB2}, 2)
	verifyDistinctIdentities(t, []*clusterNode{nodeA, nodeB2}, []string{"disc-re-a", "disc-re-b"})
}

// TestDiscoverySimultaneousBoot starts two daemons back-to-back with no
// --join anywhere: mutual discovery must still converge the mesh.
func TestDiscoverySimultaneousBoot(t *testing.T) {
	probeMulticast(t)
	enableDiscovery(t)

	tmpDir := t.TempDir()
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}
	cluster := newLocalCluster(t, tmpDir, psk)
	defer cluster.Close()

	nodeA := cluster.StartNode("disc-sim-a", "")
	nodeB := cluster.StartNode("disc-sim-b", "")

	verifyClusterConvergence(t, []*clusterNode{nodeA, nodeB}, 2)
	verifyDistinctIdentities(t, []*clusterNode{nodeA, nodeB}, []string{"disc-sim-a", "disc-sim-b"})
}
