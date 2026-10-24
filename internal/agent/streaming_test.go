package agent

import (
	"context"
	"strings"
	"testing"
)

func TestExecutePromptStreamingParity(t *testing.T) {
	eng := NewEngine(Config{NodeName: "test-node"}, &mockClusterContext{nodeName: "test-node"})
	req := &PromptRequest{Prompt: "status check", SessionID: "stream-test-session"}

	var streamed strings.Builder
	respStream, err := eng.ExecutePromptStreaming(context.Background(), req, func(ev StreamEvent) bool {
		streamed.WriteString(ev.TextDelta)
		return true
	})
	if err != nil {
		t.Fatalf("ExecutePromptStreaming error: %v", err)
	}
	if streamed.String() != respStream.Response {
		t.Errorf("streamed text %q != final %q", streamed.String(), respStream.Response)
	}

	respUnary, err := eng.ExecutePrompt(context.Background(), &PromptRequest{Prompt: "status check"})
	if err != nil {
		t.Fatalf("ExecutePrompt error: %v", err)
	}
	if respUnary.Response == "" {
		t.Errorf("unary deterministic response empty")
	}
	if respStream.ModelUsed != deterministicModelName {
		t.Errorf("stream model = %q, want deterministic %q", respStream.ModelUsed, deterministicModelName)
	}
}
