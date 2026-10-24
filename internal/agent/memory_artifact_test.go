package agent

import (
	"context"
	"testing"

	"github.com/wnoronha/herd/internal/kv"
)

// TestMemoryRecallDoesNotWriteKV verifies the ADK memory wrapper reads prior
// sessions without KV gossip writes; KV keeps only shared cluster facts.
func TestMemoryRecallDoesNotWriteKV(t *testing.T) {
	isolateAgentEnv(t)
	kvStore := kv.NewStore("test-node")
	mockCtx := &mockClusterContext{nodeName: "test-node", kvStore: kvStore}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	tool, ok := engine.tools["memory_recall"]
	if !ok {
		t.Fatalf("memory_recall tool not registered")
	}
	before := len(kvStore.List(""))
	res, err := tool.Handler(context.Background(), map[string]any{"query": "prior deployment notes"})
	if err != nil {
		t.Fatalf("memory_recall failed: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	if _, ok := m["memories"]; !ok {
		t.Errorf("expected memories key, got %v", res)
	}
	if got := len(kvStore.List("")); got != before {
		t.Errorf("memory_recall wrote %d KV keys; expected no gossip writes", got-before)
	}
}

// TestArtifactStoreVersions verifies the artifact wrapper lists versioned
// entries via the ADK artifact service.
func TestArtifactStoreVersions(t *testing.T) {
	isolateAgentEnv(t)
	mockCtx := &mockClusterContext{nodeName: "artifact-node"}
	engine := NewEngine(Config{NodeName: "artifact-node"}, mockCtx)
	tool, ok := engine.tools["artifact_store"]
	if !ok {
		t.Fatalf("artifact_store tool not registered")
	}
	ctx := context.Background()

	if _, err := tool.Handler(ctx, map[string]any{
		"action": "store", "name": "notes.md", "content": "v1",
	}); err != nil {
		t.Fatalf("store v1 failed: %v", err)
	}
	storeRes, err := tool.Handler(ctx, map[string]any{
		"action": "store", "name": "notes.md", "content": "v2",
	})
	if err != nil {
		t.Fatalf("store v2 failed: %v", err)
	}
	if storeRes.(map[string]any)["version"] == int64(0) {
		t.Logf("store version not tracked (ADK service missing); response %v", storeRes)
	}

	versRes, err := tool.Handler(ctx, map[string]any{"action": "versions", "name": "notes.md"})
	if err != nil {
		t.Fatalf("versions failed: %v", err)
	}
	versMap := versRes.(map[string]any)
	if versMap["count"] == 0 {
		t.Errorf("expected versioned entries, got %v", versRes)
	}

	listRes, err := tool.Handler(ctx, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if listRes.(map[string]any)["count"] == 0 {
		t.Errorf("expected listed artifacts, got %v", listRes)
	}
}
