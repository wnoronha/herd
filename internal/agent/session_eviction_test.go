package agent

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/adk/v2/session"
)

// TestPersistedSessionCapEviction verifies overflow beyond
// maxPersistedSessions evicts least-recently-updated first, keeping the
// shared service bounded on long-running daemons.
func TestPersistedSessionCapEviction(t *testing.T) {
	isolateAgentEnv(t)
	mockCtx := &mockClusterContext{nodeName: "test-node"}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)
	svc := engine.sessionService()
	ctx := context.Background()

	for i := 0; i < maxPersistedSessions+5; i++ {
		id := fmt.Sprintf("cap-session-%d", i)
		if _, err := svc.Create(ctx, &session.CreateRequest{
			AppName:   herdADKAppName,
			UserID:    herdADKUserID,
			SessionID: id,
		}); err != nil {
			t.Fatalf("create %s failed: %v", id, err)
		}
	}

	engine.evictStaleSessions(ctx, svc)

	list, err := svc.List(ctx, &session.ListRequest{AppName: herdADKAppName, UserID: herdADKUserID})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list.Sessions) != maxPersistedSessions {
		t.Errorf("expected %d sessions after cap eviction, got %d", maxPersistedSessions, len(list.Sessions))
	}
}
