package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/adk/v2/session"
)

// TestPersistedSessionsSurviveServiceRecreation verifies the ADK database
// session service at StateDir/sessions.db keeps chat sessions across engine
// restarts (daemon restarts), replacing the old in-memory TTL eviction.
func TestPersistedSessionsSurviveServiceRecreation(t *testing.T) {
	isolateAgentEnv(t)
	stateDir := t.TempDir()
	mockCtx := &mockClusterContext{nodeName: "test-node"}
	engine := NewEngine(Config{NodeName: "test-node", StateDir: stateDir}, mockCtx)
	svc := engine.sessionService()
	ctx := context.Background()

	if _, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   herdADKAppName,
		UserID:    herdADKUserID,
		SessionID: "chat-persist-1",
	}); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, sessionsDBFile)); err != nil {
		t.Fatalf("expected %s under StateDir, got: %v", sessionsDBFile, err)
	}

	// Recreate the engine (daemon restart) pointing at the same StateDir.
	engine2 := NewEngine(Config{NodeName: "test-node", StateDir: stateDir}, mockCtx)
	listed, err := engine2.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	found := false
	for _, s := range listed {
		if s.ID == "chat-persist-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected chat-persist-1 to survive service recreation, got %+v", listed)
	}
}

// TestListSessionsEmptyState verifies ListSessions works with no sessions.
func TestListSessionsEmptyState(t *testing.T) {
	isolateAgentEnv(t)
	mockCtx := &mockClusterContext{nodeName: "test-node"}
	engine := NewEngine(Config{NodeName: "test-node", StateDir: t.TempDir()}, mockCtx)
	listed, err := engine.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("expected no sessions, got %+v", listed)
	}
}
