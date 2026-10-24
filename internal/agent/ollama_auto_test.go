package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wnoronha/herd/internal/kv"
	"github.com/wnoronha/herd/internal/roster"
)

func TestAutoOllamaOpenAIWhenNoCloudKeys(t *testing.T) {
	// Scrub cloud keys for determinism but keep auto-Ollama enabled:
	// the mock URL is an explicit BaseURL (direct path, no network probe).
	for _, k := range []string{
		"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_API_BASE", "OPENAI_MODEL",
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "GEMINI_MODEL",
		"OLLAMA_HOST", "OLLAMA_MODEL",
	} {
		t.Setenv(k, "")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OpenAIChatResponse{
			Choices: []struct {
				Message struct {
					Role      string           `json:"role"`
					Content   string           `json:"content"`
					ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Role      string           `json:"role"`
						Content   string           `json:"content"`
						ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
					}{
						Role:    "assistant",
						Content: "Hello from local Ollama-compatible endpoint.",
					},
					FinishReason: "stop",
				},
			},
		})
	}))
	defer ts.Close()

	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   roster.NewStore(&roster.NodeMeta{NodeName: "test-node"}),
		kvStore:  kv.NewStore("test-node"),
	}
	engine := NewEngine(Config{
		NodeName:      "test-node",
		OpenAIBaseURL: ts.URL,
		OpenAIModel:   "llama3.2",
	}, mockCtx)

	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "say hello",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt: %v", err)
	}
	if resp.Response != "Hello from local Ollama-compatible endpoint." {
		t.Fatalf("unexpected response: %q", resp.Response)
	}
	if resp.ModelUsed != "llama3.2" {
		t.Fatalf("unexpected model: %s", resp.ModelUsed)
	}
}
