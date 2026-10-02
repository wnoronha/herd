package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"herd/internal/config"
	"herd/internal/ipc"
	"herd/internal/logger"
	"herd/internal/mailbox"
)

func init() {
	_ = os.Setenv("IN_TS_TEST", "true")
	_ = os.Setenv("HERD_DEV_LOCAL_DERP", "1")
	_ = os.Setenv("TS_DISABLE_UPNP", "true")
	_ = os.Setenv("TS_DEBUG_NETCHECK", "0")
}

func TestDaemonLifecycleAndIPC(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := config.Load(config.Options{
		NodeName:   "test-daemon-node",
		ConfigDir:  filepath.Join(tmpDir, "config"),
		DataDir:    filepath.Join(tmpDir, "data"),
		StateDir:   filepath.Join(tmpDir, "state"),
		SocketPath: filepath.Join(tmpDir, "herd.sock"),
		BindAddr:   "127.0.0.1",
		BindPort:   0,
	})
	if err != nil {
		t.Fatalf("config.Load error: %v", err)
	}

	d, err := New(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("daemon.New error: %v", err)
	}

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon.Start error: %v", err)
	}
	defer func() { _ = d.Stop() }()

	// Connect via IPC Client
	client := ipc.NewClient(cfg.SocketPath)

	// 1. Check Status
	status, err := client.GetStatus(ctx)
	if err != nil {
		t.Fatalf("client.GetStatus error: %v", err)
	}
	if status.NodeName != "test-daemon-node" {
		t.Errorf("expected node test-daemon-node, got %s", status.NodeName)
	}
	if status.State != "running" {
		t.Errorf("expected state running, got %s", status.State)
	}

	// 2. Check Roster
	roster, err := client.GetRoster(ctx)
	if err != nil {
		t.Fatalf("client.GetRoster error: %v", err)
	}
	if len(roster.Members) != 1 {
		t.Errorf("expected 1 member in roster, got %d", len(roster.Members))
	}

	// 3. Test Leave
	leaveResp, err := client.LeaveCluster(ctx)
	if err != nil {
		t.Fatalf("client.LeaveCluster error: %v", err)
	}
	if leaveResp == nil {
		t.Errorf("expected leave response")
	}

	// 4. Test Stop
	stopResp, err := client.StopDaemon(ctx)
	if err != nil {
		t.Fatalf("client.StopDaemon error: %v", err)
	}
	if stopResp == nil {
		t.Errorf("expected stop response")
	}

	select {
	case <-d.ShutdownChan():
		// Successfully stopped
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for daemon to stop")
	}
}

func TestTwoDaemonClusterJoin(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	cfg1, _ := config.Load(config.Options{
		NodeName:   "daemon-1",
		DataDir:    tmpDir1,
		SocketPath: filepath.Join(tmpDir1, "d1.sock"),
		BindAddr:   "127.0.0.1",
		BindPort:   0,
	})

	testLog := logger.NewNop()
	d1, err := New(cfg1, testLog)
	if err != nil {
		t.Fatalf("d1 New error: %v", err)
	}
	if err := d1.Start(context.Background()); err != nil {
		t.Fatalf("d1 Start error: %v", err)
	}
	defer func() { _ = d1.Stop() }()

	d1Addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(d1.Transport.GetPort()))

	cfg2, _ := config.Load(config.Options{
		NodeName:   "daemon-2",
		DataDir:    tmpDir2,
		SocketPath: filepath.Join(tmpDir2, "d2.sock"),
		BindAddr:   "127.0.0.1",
		BindPort:   0,
		JoinAddr:   d1Addr,
	})
	// Sync PSK across the test cluster
	d2, err := New(cfg2, testLog)
	if err != nil {
		t.Fatalf("d2 New error: %v", err)
	}
	d2.Identity.PreSharedKey = d1.Identity.PreSharedKey

	if err := d2.Start(context.Background()); err != nil {
		t.Fatalf("d2 Start error: %v", err)
	}
	defer func() { _ = d2.Stop() }()

	client1 := ipc.NewClient(cfg1.SocketPath)
	client2 := ipc.NewClient(cfg2.SocketPath)

	deadline := time.Now().Add(5 * time.Second)
	converged := false
	for time.Now().Before(deadline) {
		r1, err1 := client1.GetRoster(context.Background())
		r2, err2 := client2.GetRoster(context.Background())
		if err1 == nil && err2 == nil && len(r1.Members) == 2 && len(r2.Members) == 2 {
			converged = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !converged {
		t.Fatalf("daemons failed to converge roster over IPC in 5s")
	}

	// Test Join via IPC on running node
	joinResp, err := client2.JoinNode(context.Background(), d1Addr)
	if err != nil {
		t.Fatalf("client2.JoinNode error: %v", err)
	}
	if joinResp.JoinedNodes < 1 {
		t.Errorf("expected at least 1 node joined, got %d", joinResp.JoinedNodes)
	}
}

func TestAgentReactiveInboxWakeup(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := config.Load(config.Options{
		NodeName:   "wake-node",
		DataDir:    tmpDir,
		SocketPath: filepath.Join(tmpDir, "wake.sock"),
		BindAddr:   "127.0.0.1",
		BindPort:   0,
	})
	if err != nil {
		t.Fatalf("config.Load error: %v", err)
	}

	d, err := New(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("daemon.New error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon.Start error: %v", err)
	}
	defer func() { _ = d.Stop() }()

	// Deposit a task message into the node's mailbox
	msgID := "task-autowake-999"
	replyKey := mailbox.KeyFor("sender-node", "reply_task-autowake-999")
	msg := mailbox.Message{
		ID:        msgID,
		From:      "sender-node",
		To:        "wake-node",
		Topic:     "health_check",
		Message:   "Check cluster roster status",
		CreatedAt: time.Now().Unix(),
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	mailboxKey := mailbox.KeyFor("wake-node", msgID)
	d.KVStore.Set(mailboxKey, msgBytes, 5*time.Minute)

	// Wait for reactive watcher to wake up agent, process task, consume mailbox, and post reply
	deadline := time.Now().Add(5 * time.Second)
	replyFound := false
	var replyMsg *mailbox.Message

	for time.Now().Before(deadline) {
		entry, found := d.KVStore.Get(replyKey)
		if found && entry != nil && len(entry.Value) > 0 {
			var rMsg mailbox.Message
			if err := json.Unmarshal(entry.Value, &rMsg); err == nil && rMsg.Message != "" {
				replyFound = true
				replyMsg = &rMsg
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !replyFound {
		t.Fatalf("timed out waiting for reactive agent to wake up and post reply to %s", replyKey)
	}

	if replyMsg.To != "sender-node" {
		t.Errorf("expected reply to 'sender-node', got '%s'", replyMsg.To)
	}
	if replyMsg.Message == "" {
		t.Errorf("expected non-empty reply message")
	}

	// Verify original message was deleted/consumed from mailbox
	_, stillExists := d.KVStore.Get(mailboxKey)
	if stillExists {
		t.Errorf("expected original mailbox message to be consumed/deleted")
	}
}

func TestDaemonAgentAutoSeeding(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cfg, err := config.Load(config.Options{
		NodeName:   "seeding-node",
		ConfigDir:  configDir,
		DataDir:    filepath.Join(tmpDir, "data"),
		StateDir:   filepath.Join(tmpDir, "state"),
		SocketPath: filepath.Join(tmpDir, "herd.sock"),
		BindAddr:   "127.0.0.1",
		BindPort:   0,
	})
	if err != nil {
		t.Fatalf("config.Load error: %v", err)
	}

	d, err := New(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("daemon.New error: %v", err)
	}
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon.Start error: %v", err)
	}
	defer func() { _ = d.Stop() }()

	seededFile := filepath.Join(configDir, "agents", "AGENTS.md")
	if _, err := os.Stat(seededFile); os.IsNotExist(err) {
		t.Fatalf("expected seeded AGENTS.md at %s, but file was not created", seededFile)
	}

	// Verify the agent engine loaded the seeded personas
	if _, ok := d.AgentEngine.GetAgent("coordinator"); !ok {
		t.Errorf("expected coordinator persona to be loaded in daemon AgentEngine")
	}
	if _, ok := d.AgentEngine.GetAgent("ops"); !ok {
		t.Errorf("expected ops persona to be loaded in daemon AgentEngine")
	}
	if _, ok := d.AgentEngine.GetAgent("researcher"); !ok {
		t.Errorf("expected researcher persona to be loaded in daemon AgentEngine")
	}
}

