package agent

import (
	"context"
	"fmt"
	"testing"
)

type mockAgentRunner struct {
	lastRole   string
	lastPrompt string
	resp       *PromptResponse
	err        error
}

func (m *mockAgentRunner) ExecutePrompt(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
	m.lastRole = req.Role
	m.lastPrompt = req.Prompt
	if m.err != nil {
		return nil, m.err
	}
	return m.resp, nil
}

func (m *mockAgentRunner) ListAgents() []*AgentDefinition {
	return []*AgentDefinition{
		{Name: "ops", Role: "ops"},
		{Name: "researcher", Role: "researcher"},
	}
}

func TestRunAgentTool(t *testing.T) {
	mockRunner := &mockAgentRunner{
		resp: &PromptResponse{
			Response:    "Sub-agent completed task successfully",
			ModelUsed:   "gemini-3.8-flash",
			ExecutionMs: 150,
			ToolsCalled: []ToolCall{
				{Name: "cluster_roster", Arguments: "{}", Result: "[]"},
			},
		},
	}

	tool := buildRunAgentTool(mockRunner)
	ctx := context.Background()

	// 1. Successful execution
	res, err := tool.Handler(ctx, map[string]any{
		"agent":  "researcher",
		"prompt": "Find all stale nodes in the cluster",
	})
	if err != nil {
		t.Fatalf("expected run_agent to succeed: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["agent"] != "researcher" {
		t.Errorf("expected agent 'researcher', got: %v", resMap["agent"])
	}
	if resMap["response"] != "Sub-agent completed task successfully" {
		t.Errorf("unexpected response: %v", resMap["response"])
	}
	if resMap["model_used"] != "gemini-3.8-flash" {
		t.Errorf("expected model 'gemini-3.8-flash', got: %v", resMap["model_used"])
	}
	if mockRunner.lastRole != "researcher" {
		t.Errorf("expected lastRole 'researcher', got: %v", mockRunner.lastRole)
	}
	if mockRunner.lastPrompt != "Find all stale nodes in the cluster" {
		t.Errorf("expected lastPrompt 'Find all stale nodes in the cluster', got: %v", mockRunner.lastPrompt)
	}

	// 2. Execution with context
	_, err = tool.Handler(ctx, map[string]any{
		"agent":   "ops",
		"prompt":  "Restart service",
		"context": "Node alpha-1 is unresponsive",
	})
	if err != nil {
		t.Fatalf("expected run_agent with context to succeed: %v", err)
	}
	expectedPrompt := "Context:\nNode alpha-1 is unresponsive\n\nTask:\nRestart service"
	if mockRunner.lastPrompt != expectedPrompt {
		t.Errorf("expected contextual prompt %q, got: %q", expectedPrompt, mockRunner.lastPrompt)
	}

	// 3. Runner failure handling
	mockRunner.err = fmt.Errorf("agent not found")
	_, err = tool.Handler(ctx, map[string]any{
		"agent":  "unknown",
		"prompt": "do something",
	})
	if err == nil {
		t.Fatalf("expected error when runner fails, got nil")
	}

	// 4. Missing required parameters
	mockRunner.err = nil
	_, err = tool.Handler(ctx, map[string]any{
		"agent": "ops",
	})
	if err == nil {
		t.Fatalf("expected error when prompt is missing, got nil")
	}

	_, err = tool.Handler(ctx, map[string]any{
		"prompt": "hello",
	})
	if err == nil {
		t.Fatalf("expected error when agent is missing, got nil")
	}

	// 5. Tool without runner
	nilRunnerTool := buildRunAgentTool(nil)
	_, err = nilRunnerTool.Handler(ctx, map[string]any{
		"agent":  "ops",
		"prompt": "test",
	})
	if err == nil {
		t.Fatalf("expected error when runner is nil, got nil")
	}
}
