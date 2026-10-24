package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/wnoronha/herd/internal/kv"
)

// TestConfirmationRequiredFor verifies the destructive tool set. Mutating
// tools pause for approval; read-only tools run directly.
func TestConfirmationRequiredFor(t *testing.T) {
	destructive := []string{
		"cluster_exec", "cluster_cp", "file_write",
		"artifact_store", "cluster_forward", "cluster_manage",
	}
	for _, name := range destructive {
		if !ConfirmationRequiredFor(name) {
			t.Errorf("expected %s to require confirmation", name)
		}
		if !ConfirmationRequiredFor("  " + strings.ToUpper(name) + " ") {
			t.Errorf("expected matching to ignore case and padding for %s", name)
		}
	}
	readonly := []string{
		"cluster_roster", "shared_kv", "file_read", "fetch_url",
		"google_search", "dns_lookup", "run_agent", "agent_read_mailbox",
		"", "unknown_tool",
	}
	for _, name := range readonly {
		if ConfirmationRequiredFor(name) {
			t.Errorf("expected %s to run without confirmation", name)
		}
	}
}

// TestConfirmationGateOneShot verifies automated callers keep direct
// execution: without a chat session the gate always proceeds.
func TestConfirmationGateOneShot(t *testing.T) {
	isolateAgentEnv(t)
	engine := NewEngine(Config{NodeName: "test-node"}, &mockClusterContext{nodeName: "test-node"})
	ctx := contextWithConfirmationMode(context.Background(), "")
	proceed, blocked := engine.confirmationGate(ctx, "cluster_exec", map[string]any{"command": "uptime"})
	if !proceed || blocked != nil {
		t.Fatalf("expected one-shot cluster_exec to proceed, got %v %v", proceed, blocked)
	}
	if _, ok := engine.pendingPrompt(""); ok {
		t.Errorf("expected no pending entry for one-shot calls")
	}
}

// TestConfirmationGateBlocksAndTakes verifies the yes path through the
// gate: block, record pending, take the entry once, then block again.
func TestConfirmationGateBlocksAndTakes(t *testing.T) {
	isolateAgentEnv(t)
	engine := NewEngine(Config{NodeName: "test-node"}, &mockClusterContext{nodeName: "test-node"})
	ctx := contextWithConfirmationMode(context.Background(), "session-1")
	args := map[string]any{"command": "uptime", "target": "all"}

	proceed, blocked := engine.confirmationGate(ctx, "cluster_exec", args)
	if proceed || blocked == nil {
		t.Fatalf("expected chat cluster_exec to block, got %v %v", proceed, blocked)
	}
	if blocked["confirmation_required"] != true {
		t.Errorf("expected confirmation_required marker, got %v", blocked)
	}

	prompt, ok := engine.pendingPrompt("session-1")
	if !ok {
		t.Fatalf("expected pending prompt after block")
	}
	if prompt.ToolName != "cluster_exec" {
		t.Errorf("expected tool cluster_exec, got %q", prompt.ToolName)
	}
	if !strings.Contains(prompt.Arguments, "uptime") {
		t.Errorf("expected arguments to carry command, got %q", prompt.Arguments)
	}
	if prompt.Hint == "" {
		t.Errorf("expected non-empty hint")
	}

	// Repeat call before the answer blocks again without consuming.
	if proceed, _ := engine.confirmationGate(ctx, "cluster_exec", args); proceed {
		t.Errorf("expected repeat call to block before the answer")
	}

	// Taking consumes the entry a single time.
	entry, ok := engine.takePending("session-1")
	if !ok || entry.tool != "cluster_exec" {
		t.Fatalf("expected take to return cluster_exec, got %+v %v", entry, ok)
	}
	if _, ok := engine.takePending("session-1"); ok {
		t.Errorf("expected take to work a single time")
	}
	if _, ok := engine.pendingPrompt("session-1"); ok {
		t.Errorf("expected take to clear the prompt")
	}
}

// TestConfirmationGateDecline verifies a no answer drops the entry and a
// different call never consumes another call's approval.
func TestConfirmationGateDecline(t *testing.T) {
	isolateAgentEnv(t)
	engine := NewEngine(Config{NodeName: "test-node"}, &mockClusterContext{nodeName: "test-node"})
	ctx := contextWithConfirmationMode(context.Background(), "session-9")

	argsExec := map[string]any{"command": "uptime", "target": "all"}
	if proceed, _ := engine.confirmationGate(ctx, "cluster_exec", argsExec); proceed {
		t.Fatalf("expected block")
	}
	toolName, ok := engine.denyPending("session-9")
	if !ok || toolName != "cluster_exec" {
		t.Fatalf("expected deny to report cluster_exec, got %q %v", toolName, ok)
	}
	if _, ok := engine.pendingPrompt("session-9"); ok {
		t.Errorf("expected deny to clear the entry")
	}

	// Approval binds to the exact call.
	argsCp := map[string]any{"source": "a", "destination": "b"}
	if proceed, _ := engine.confirmationGate(ctx, "cluster_cp", argsCp); proceed {
		t.Fatalf("expected block")
	}
	entry, ok := engine.takePending("session-9")
	if !ok || entry.tool != "cluster_cp" || !strings.Contains(entry.args, `"destination":"b"`) {
		t.Fatalf("expected take to return the exact blocked call, got %+v %v", entry, ok)
	}
}

// TestChatConfirmationEndToEnd runs chat turns through the deterministic
// fallback: the destructive call blocks without executing, a no answer
// returns without a model turn, and a yes answer executes the recorded
// call directly. Each phase uses a fresh session because the deterministic
// fallback emits at most one tool call per session.
func TestChatConfirmationEndToEnd(t *testing.T) {
	isolateAgentEnv(t)
	mockCtx := &mockClusterContext{nodeName: "test-node"}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)
	ctx := context.Background()

	blocked, err := engine.ExecutePrompt(ctx, &PromptRequest{
		Prompt:    "run uptime",
		SessionID: "chat-e2e-decline",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt error: %v", err)
	}
	if blocked.Confirmation == nil {
		t.Fatalf("expected confirmation prompt, got response %q tools %v", blocked.Response, blocked.ToolsCalled)
	}
	if blocked.Confirmation.ToolName != "cluster_exec" {
		t.Errorf("expected cluster_exec prompt, got %q", blocked.Confirmation.ToolName)
	}
	for _, tc := range blocked.ToolsCalled {
		if strings.Contains(tc.Result, "mock output") {
			t.Errorf("blocked call must not execute: %v", tc)
		}
	}

	declined, err := engine.ExecutePrompt(ctx, &PromptRequest{
		Prompt:       "declined",
		SessionID:    "chat-e2e-decline",
		ToolDecision: &ToolDecision{Approved: false},
	})
	if err != nil {
		t.Fatalf("decline error: %v", err)
	}
	if declined.Confirmation != nil {
		t.Errorf("expected no pending prompt after decline")
	}
	if !strings.Contains(strings.ToLower(declined.Response), "declined") {
		t.Errorf("expected decline note, got %q", declined.Response)
	}

	approveBlocked, err := engine.ExecutePrompt(ctx, &PromptRequest{
		Prompt:    "run uptime",
		SessionID: "chat-e2e-approve",
	})
	if err != nil {
		t.Fatalf("block error: %v", err)
	}
	if approveBlocked.Confirmation == nil {
		t.Fatalf("expected confirmation prompt, got %q", approveBlocked.Response)
	}

	approvedRun, err := engine.ExecutePrompt(ctx, &PromptRequest{
		Prompt:       "run uptime",
		SessionID:    "chat-e2e-approve",
		ToolDecision: &ToolDecision{Approved: true},
	})
	if err != nil {
		t.Fatalf("approve error: %v", err)
	}
	if approvedRun.Confirmation != nil {
		t.Errorf("expected approval to clear the prompt, got %+v", approvedRun.Confirmation)
	}
	executed := false
	for _, tc := range approvedRun.ToolsCalled {
		if tc.Name == "cluster_exec" && strings.Contains(tc.Result, "mock output") {
			executed = true
		}
	}
	if !executed {
		t.Errorf("expected approved cluster_exec to execute, got %+v", approvedRun.ToolsCalled)
	}
	if !strings.Contains(strings.ToLower(approvedRun.Response), "approved") {
		t.Errorf("expected approval note, got %q", approvedRun.Response)
	}
}

// TestChatConfirmationPolicyStillFailsClosed verifies the exec whitelist
// rejects before any prompt: a rejected command emits no call and records
// no pending entry.
func TestChatConfirmationPolicyStillFailsClosed(t *testing.T) {
	isolateAgentEnv(t)
	kvStore := kv.NewStore("test-node")
	kvStore.Set("agent:policy:allowed_exec", []byte(`["uname -a"]`), 0)
	mockCtx := &mockClusterContext{nodeName: "test-node", kvStore: kvStore}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt:    "run uptime",
		SessionID: "chat-pol-1",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt error: %v", err)
	}
	if len(resp.ToolsCalled) != 0 {
		t.Errorf("expected rejected command to emit no calls, got %+v", resp.ToolsCalled)
	}
	if resp.Confirmation != nil {
		t.Errorf("expected no confirmation prompt for rejected commands, got %+v", resp.Confirmation)
	}
	if _, ok := engine.pendingPrompt("chat-pol-1"); ok {
		t.Errorf("expected no pending entry for rejected commands")
	}
}
