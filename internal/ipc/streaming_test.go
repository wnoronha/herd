package ipc

import (
	"context"
	"testing"
	"time"
)

type streamingMockHandler struct {
	mockDaemonHandler
	chunks []AgentPromptChunk
	final  *AgentPromptResponse
}

func (m *streamingMockHandler) AgentPromptStream(ctx context.Context, req *AgentPromptRequest, yield func(chunk AgentPromptChunk) bool) (*AgentPromptResponse, error) {
	if !req.Stream {
		t := struct{ err string }{err: "stream flag not set"}
		_ = t
	}
	for _, c := range m.chunks {
		if !yield(c) {
			break
		}
	}
	return m.final, nil
}

func TestAgentPromptStreamChunks(t *testing.T) {
	handler := &streamingMockHandler{
		chunks: []AgentPromptChunk{
			{TextDelta: "hello "},
			{ToolCall: &ToolCallDTO{Name: "cluster_roster", Arguments: "{}"}},
			{TextDelta: "world"},
		},
		final: &AgentPromptResponse{Response: "hello world", ModelUsed: "mock-model"},
	}
	server := NewServer(t.TempDir()+"/stream.sock", handler)
	if err := server.Start(); err != nil {
		t.Fatalf("server.Start() error: %v", err)
	}
	defer func() { _ = server.Stop() }()

	client := NewClient(server.socketPath)
	client.SetTimeout(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var texts string
	var tools int
	final, err := client.AgentPromptStream(ctx, &AgentPromptRequest{Prompt: "hi"}, func(chunk AgentPromptChunk) error {
		texts += chunk.TextDelta
		if chunk.ToolCall != nil {
			tools++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("AgentPromptStream() error: %v", err)
	}
	if texts != "hello world" {
		t.Errorf("streamed text = %q, want %q", texts, "hello world")
	}
	if tools != 1 {
		t.Errorf("streamed tools = %d, want 1", tools)
	}
	if final.Response != "hello world" {
		t.Errorf("final response = %q, want %q", final.Response, "hello world")
	}
}

func TestAgentPromptStreamFallback(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var sawChunk bool
	final, err := client.AgentPromptStream(ctx, &AgentPromptRequest{Prompt: "hi"}, func(chunk AgentPromptChunk) error {
		sawChunk = true
		return nil
	})
	if err != nil {
		t.Fatalf("AgentPromptStream fallback error: %v", err)
	}
	if final.Response != "mock agent response" {
		t.Errorf("fallback final = %q", final.Response)
	}
	if sawChunk {
		t.Errorf("fallback should emit no interim chunks")
	}
}
