package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"herd/internal/agent"
	"herd/internal/config"
	"herd/internal/identity"
	"herd/internal/kv"
	"herd/internal/logger"
	"herd/internal/mailbox"
)

func newTestDaemonWithMocks(t *testing.T, nodeName string) (*Daemon, *kv.Store) {
	t.Helper()
	id, err := identity.NewIdentity(nodeName)
	if err != nil {
		t.Fatalf("failed to create identity: %v", err)
	}

	kvStore := kv.NewStore(nodeName)
	cfg := &config.Config{
		NodeName: nodeName,
	}

	d := &Daemon{
		Config:     cfg,
		Identity:   id,
		KVStore:    kvStore,
		Logger:     logger.NewNop(),
		state:      "running",
		shutdownCh: make(chan struct{}),
	}

	return d, kvStore
}

func TestProcessMailboxEntry_SymmetricDelivery(t *testing.T) {
	d, kvStore := newTestDaemonWithMocks(t, "node-1")
	d.AgentEngine = agent.NewEngine(agent.Config{NodeName: "node-1"}, d)

	// Modern actor-style mailbox message with thread ID
	msg := mailbox.Message{
		ID:        "msg-modern-100",
		From:      "node-2",
		To:        "node-1",
		ThreadID:  "thread-incident-42",
		Topic:     "system_audit",
		Message:   "Check cluster disk usage",
		CreatedAt: time.Now().Unix(),
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	mailboxKey := mailbox.KeyFor("node-1", msg.ID) // "mailbox:node-1/msg-modern-100"
	entry := kvStore.Set(mailboxKey, msgBytes, 1*time.Hour)

	// Process message
	d.processMailboxEntry(context.Background(), entry)

	// 1. Verify message was auto-acknowledged (cleared from node-1 mailbox)
	_, found := kvStore.Get(mailboxKey)
	if found {
		t.Errorf("expected mailbox entry %s to be deleted after processing", mailboxKey)
	}

	// 2. Verify symmetric reply was delivered directly to sender's mailbox: mailbox:node-2/reply_msg-modern-100
	expectedReplyKey := mailbox.KeyFor("node-2", "reply_"+msg.ID)
	replyEntry, found := kvStore.Get(expectedReplyKey)
	if !found {
		t.Fatalf("expected symmetric reply at %s, but none was found", expectedReplyKey)
	}

	var replyMsg mailbox.Message
	if err := json.Unmarshal(replyEntry.Value, &replyMsg); err != nil {
		t.Fatalf("failed to unmarshal reply message: %v", err)
	}

	if replyMsg.To != "node-2" {
		t.Errorf("expected reply To='node-2', got %q", replyMsg.To)
	}
	if replyMsg.From != "node-1" {
		t.Errorf("expected reply From='node-1', got %q", replyMsg.From)
	}
	if replyMsg.ThreadID != "thread-incident-42" {
		t.Errorf("expected reply ThreadID='thread-incident-42', got %q", replyMsg.ThreadID)
	}
	if replyMsg.ID != "reply_msg-modern-100" {
		t.Errorf("expected reply ID 'reply_msg-modern-100', got %q", replyMsg.ID)
	}
}

func TestProcessMailboxEntry_Broadcast(t *testing.T) {
	d, kvStore := newTestDaemonWithMocks(t, "node-1")
	d.AgentEngine = agent.NewEngine(agent.Config{NodeName: "node-1"}, d)

	broadcastMsg := mailbox.Message{
		ID:        "bcast-99",
		From:      "coordinator",
		To:        "*",
		Topic:     "announcement",
		Message:   "Mesh rebalancing in 5m",
		CreatedAt: time.Now().Unix(),
	}
	msgBytes, _ := json.Marshal(broadcastMsg)
	bcastKey := mailbox.KeyFor("*", broadcastMsg.ID)
	entry := kvStore.Set(bcastKey, msgBytes, 1*time.Hour)

	d.processMailboxEntry(context.Background(), entry)

	if _, loaded := d.processedMsgs.Load("bcast-99"); !loaded {
		t.Errorf("expected broadcast message to be marked processed in node-1")
	}
}

func TestProcessMailboxEntry_Deduplication(t *testing.T) {
	d, kvStore := newTestDaemonWithMocks(t, "node-1")
	d.AgentEngine = agent.NewEngine(agent.Config{NodeName: "node-1"}, d)

	msg := mailbox.Message{
		ID:        "dedup-msg-1",
		From:      "node-2",
		To:        "node-1",
		Topic:     "ping",
		Message:   "hello",
		CreatedAt: time.Now().Unix(),
	}
	msgBytes, _ := json.Marshal(msg)
	mailboxKey := mailbox.KeyFor("node-1", "dedup-msg-1")
	entry := kvStore.Set(mailboxKey, msgBytes, 1*time.Hour)

	// First execution should process
	d.processMailboxEntry(context.Background(), entry)

	// Re-add and process second time with same message ID
	entry2 := kvStore.Set(mailboxKey, msgBytes, 1*time.Hour)
	d.processMailboxEntry(context.Background(), entry2)

	// Verify deduplication map contains ID
	if _, ok := d.processedMsgs.Load("dedup-msg-1"); !ok {
		t.Errorf("expected dedup-msg-1 to be recorded in processedMsgs")
	}
}

func TestProcessMailboxEntry_IgnoresWrongTarget(t *testing.T) {
	d, kvStore := newTestDaemonWithMocks(t, "node-1")
	d.AgentEngine = agent.NewEngine(agent.Config{NodeName: "node-1"}, d)

	msg := mailbox.Message{
		ID:        "wrong-target-msg",
		From:      "node-2",
		To:        "node-3", // Not for node-1
		Topic:     "task",
		Message:   "do work",
		CreatedAt: time.Now().Unix(),
	}
	msgBytes, _ := json.Marshal(msg)
	mailboxKey := mailbox.KeyFor("node-3", "wrong-target-msg")
	entry := kvStore.Set(mailboxKey, msgBytes, 1*time.Hour)

	d.processMailboxEntry(context.Background(), entry)

	// Should not have acknowledged or processed
	if _, loaded := d.processedMsgs.Load("wrong-target-msg"); loaded {
		t.Errorf("expected wrong target message not to be processed")
	}
}
