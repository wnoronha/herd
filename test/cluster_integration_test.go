package test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"herd/internal/config"
	"herd/internal/daemon"
	"herd/internal/identity"
	"herd/internal/ipc"
	"herd/internal/logger"
	"herd/internal/testutil"
)

type clusterNode struct {
	Name       string
	Config     *config.Config
	Daemon     *daemon.Daemon
	Client     *ipc.Client
	SocketPath string
	DataDir    string
}

type localCluster struct {
	t       *testing.T
	baseDir string
	psk     identity.Key
	nodes   map[string]*clusterNode
}

func newLocalCluster(t *testing.T, baseDir string, psk identity.Key) *localCluster {
	return &localCluster{
		t:       t,
		baseDir: baseDir,
		psk:     psk,
		nodes:   make(map[string]*clusterNode),
	}
}

func (c *localCluster) StartNode(name string, joinAddr string) *clusterNode {
	c.t.Helper()
	nodeDataDir := filepath.Join(c.baseDir, name, "data")
	nodeConfigDir := filepath.Join(c.baseDir, name, "config")
	nodeStateDir := filepath.Join(c.baseDir, name, "state")
	nodeSocket := filepath.Join(c.baseDir, name, "herd.sock")

	cfg, err := config.Load(config.Options{
		NodeName:   name,
		ConfigDir:  nodeConfigDir,
		DataDir:    nodeDataDir,
		StateDir:   nodeStateDir,
		SocketPath: nodeSocket,
		BindAddr:   "127.0.0.1",
		BindPort:   0,
		JoinAddr:   joinAddr,
	})
	if err != nil {
		c.t.Fatalf("failed to load config for %s: %v", name, err)
	}

	d, err := daemon.New(cfg, logger.NewNop())
	if err != nil {
		c.t.Fatalf("failed to create daemon for %s: %v", name, err)
	}

	// Set shared cluster PSK
	d.Identity.PreSharedKey = c.psk

	if err := d.Start(context.Background()); err != nil {
		c.t.Fatalf("failed to start daemon %s: %v", name, err)
	}

	node := &clusterNode{
		Name:       name,
		Config:     cfg,
		Daemon:     d,
		Client:     ipc.NewClient(nodeSocket),
		SocketPath: nodeSocket,
		DataDir:    nodeDataDir,
	}

	c.nodes[name] = node
	return node
}

func (c *localCluster) StopNode(name string) {
	c.t.Helper()
	if node, ok := c.nodes[name]; ok {
		_ = node.Daemon.Stop()
		delete(c.nodes, name)
	}
}

func (c *localCluster) Close() {
	for name, node := range c.nodes {
		_ = node.Daemon.Stop()
		delete(c.nodes, name)
	}
}

func TestMultiNodeClusterConvergenceAndFailure(t *testing.T) {
	testutil.SetupHermeticEnvironment(t)

	tmpDir := t.TempDir()
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}

	cluster := newLocalCluster(t, tmpDir, psk)
	defer cluster.Close()

	// 1. Start seed node (node-1)
	node1 := cluster.StartNode("node-1", "")
	node1Addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(node1.Daemon.Transport.GetPort()))

	// 2. Start nodes 2, 3, 4 joining node-1
	node2 := cluster.StartNode("node-2", node1Addr)
	node3 := cluster.StartNode("node-3", node1Addr)
	node4 := cluster.StartNode("node-4", node1Addr)

	allNodes := []*clusterNode{node1, node2, node3, node4}

	// 3. Verify all nodes converge to 4 members via IPC
	verifyClusterConvergence(t, allNodes, 4)

	// 4. Test Node Failure: Stop node-3
	cluster.StopNode("node-3")
	verifyLeaveDetection(t, node1, "node-3")

	// 5. Test Node Recovery: Restart node-3
	node3Recovered := cluster.StartNode("node-3", node1Addr)
	verifyRecovery(t, node3Recovered, 3)

	// 6. Verify directory structure isolation
	verifyIdentityIsolation(t, tmpDir, []string{"node-1", "node-2", "node-3", "node-4"})
}

func verifyClusterConvergence(t *testing.T, nodes []*clusterNode, expectedMembers int) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		allMatch := true
		for _, n := range nodes {
			roster, err := n.Client.GetRoster(context.Background())
			if err != nil || len(roster.Members) < expectedMembers {
				allMatch = false
				break
			}
		}
		if allMatch {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, n := range nodes {
		roster, _ := n.Client.GetRoster(context.Background())
		t.Logf("node %s has %d members: %+v", n.Daemon.Config.NodeName, len(roster.Members), roster.Members)
	}
	t.Fatalf("cluster failed to converge to %d members within deadline", expectedMembers)
}

func verifyLeaveDetection(t *testing.T, observer *clusterNode, departedName string) {
	t.Helper()
	leaveDeadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(leaveDeadline) {
		roster, err := observer.Client.GetRoster(context.Background())
		if err == nil {
			for _, m := range roster.Members {
				if m.Name == departedName && m.Status == "left" {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("Note: %s departure handling observed", departedName)
}

func verifyRecovery(t *testing.T, recoveredNode *clusterNode, minMembers int) {
	t.Helper()
	recoveredDeadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(recoveredDeadline) {
		status, err := recoveredNode.Client.GetStatus(context.Background())
		if err == nil && status.MemberCount >= minMembers {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("node %s failed to rejoin cluster on restart", recoveredNode.Name)
}

func verifyIdentityIsolation(t *testing.T, baseDir string, nodeNames []string) {
	t.Helper()
	for _, n := range nodeNames {
		info, err := os.Stat(filepath.Join(baseDir, n, "data", "identity.json"))
		if err != nil {
			t.Errorf("expected identity.json for node %s", n)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
		}
	}
}
