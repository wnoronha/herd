package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"

	"github.com/wnoronha/herd/internal/kv"
	"github.com/wnoronha/herd/internal/roster"
)

// openAIJSONhandler wraps h with the JSON content-type the ADK openai-go
// client requires for chat-completions responses.
func openAIJSONhandler(h func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		h(w, r)
	}
}

// encodeOpenAIResponse writes resp as a chat-completions payload.
func encodeOpenAIResponse(w http.ResponseWriter, content string, toolCalls []OpenAIToolCall) {
	resp := OpenAIChatResponse{
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
					Role:      "assistant",
					Content:   content,
					ToolCalls: toolCalls,
				},
				FinishReason: "stop",
			},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// TestADKInstructionProviderCurlyBraces verifies system prompts containing
// {json_or_template_braces}, ${literal}, and {var?} pass through ADK without
// session.ErrStateKeyNotExist. InstructionProvider must treat braces as raw
// text, unlike Instruction templating.
func TestADKInstructionProviderCurlyBraces(t *testing.T) {
	isolateAgentEnv(t)
	ts := httptest.NewServer(openAIJSONhandler(func(w http.ResponseWriter, r *http.Request) {
		var req OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.Messages) == 0 || !strings.Contains(req.Messages[0].Content.(string), "{json_or_template_braces}") {
			t.Errorf("expected system prompt with raw braces, got: %+v", req.Messages)
		}
		encodeOpenAIResponse(w, "braces handled", nil)
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
		OpenAIModel:   "mock-gpt",
		Provider:      "openai",
		SystemPrompt:  "You are Herd Agent with config {json_or_template_braces} and ${literal} plus {var?} optional.",
	}, mockCtx)

	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt with braced system prompt failed: %v", err)
	}
	if resp.Response != "braces handled" {
		t.Errorf("unexpected response: %q", resp.Response)
	}
}

// TestADKMultiTurnSessionState verifies Turn 2 in the same SessionID sees
// Turn 1 content through the ADK runner + InMemoryService, while a fresh
// SessionID does not.
func TestADKMultiTurnSessionState(t *testing.T) {
	isolateAgentEnv(t)
	ts := httptest.NewServer(openAIJSONhandler(func(w http.ResponseWriter, r *http.Request) {
		var req OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		seen := false
		for _, m := range req.Messages {
			if s, ok := m.Content.(string); ok && strings.Contains(s, "marker-AAA") {
				seen = true
			}
		}
		if seen {
			encodeOpenAIResponse(w, "seen-marker", nil)
		} else {
			encodeOpenAIResponse(w, "marker-AAA", nil)
		}
	}))
	defer ts.Close()

	newEngine := func() *Engine {
		mockCtx := &mockClusterContext{
			nodeName: "test-node",
			roster:   roster.NewStore(&roster.NodeMeta{NodeName: "test-node"}),
			kvStore:  kv.NewStore("test-node"),
		}
		return NewEngine(Config{
			NodeName:      "test-node",
			OpenAIBaseURL: ts.URL,
			OpenAIModel:   "mock-gpt",
			Provider:      "openai",
		}, mockCtx)
	}
	engine := newEngine()

	r1, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt:    "say the marker",
		SessionID: "session-continuity-1",
	})
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}
	if r1.Response != "marker-AAA" {
		t.Fatalf("unexpected turn 1 response: %q", r1.Response)
	}

	r2, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt:    "repeat what you said",
		SessionID: "session-continuity-1",
	})
	if err != nil {
		t.Fatalf("turn 2 failed: %v", err)
	}
	if r2.Response != "seen-marker" {
		t.Errorf("expected turn 2 to see turn 1 state, got: %q", r2.Response)
	}

	r3, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt:    "repeat what you said",
		SessionID: "session-continuity-other",
	})
	if err != nil {
		t.Fatalf("other-session turn failed: %v", err)
	}
	if r3.Response == "seen-marker" {
		t.Errorf("fresh session must not see other session history, got: %q", r3.Response)
	}
}

// TestADKOneShotNoLeakNoBleed verifies one-shot prompts with an empty
// SessionID neither persist sessions on the shared service nor bleed history
// into subsequent one-shot prompts.
func TestADKOneShotNoLeakNoBleed(t *testing.T) {
	isolateAgentEnv(t)
	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   roster.NewStore(&roster.NodeMeta{NodeName: "test-node"}),
		kvStore:  kv.NewStore("test-node"),
	}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	r1, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "show cluster roster nodes zebra-unique-001",
	})
	if err != nil {
		t.Fatalf("one-shot 1 failed: %v", err)
	}
	if r1.Response == "" {
		t.Fatalf("expected non-empty one-shot response")
	}

	r2, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "show cluster roster nodes",
	})
	if err != nil {
		t.Fatalf("one-shot 2 failed: %v", err)
	}
	if strings.Contains(r2.Response, "zebra-unique-001") {
		t.Errorf("one-shot prompt bled history into the next call: %q", r2.Response)
	}

	svc := engine.sessionService()
	list, err := svc.List(context.Background(), &session.ListRequest{AppName: "herd", UserID: "herd-user"})
	if err != nil {
		t.Fatalf("session list failed: %v", err)
	}
	if len(list.Sessions) != 0 {
		t.Errorf("expected no persisted sessions from one-shot prompts, got %d", len(list.Sessions))
	}
}

// TestADKHistoryFoldedIntoPrompt verifies req.History reaches the model even
// for one-shot calls without a persisted session.
func TestADKHistoryFoldedIntoPrompt(t *testing.T) {
	isolateAgentEnv(t)
	ts := httptest.NewServer(openAIJSONhandler(func(w http.ResponseWriter, r *http.Request) {
		var req OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		joined, _ := json.Marshal(req.Messages)
		if !strings.Contains(string(joined), "prior-context-XYZ") {
			t.Errorf("expected folded history in request, got: %s", joined)
		}
		encodeOpenAIResponse(w, "history seen", nil)
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
		OpenAIModel:   "mock-gpt",
		Provider:      "openai",
	}, mockCtx)

	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "continue",
		History: []ChatMessage{
			{Role: "user", Content: "prior-context-XYZ"},
			{Role: "assistant", Content: "acknowledged"},
		},
	})
	if err != nil {
		t.Fatalf("ExecutePrompt with history failed: %v", err)
	}
	if resp.Response != "history seen" {
		t.Errorf("unexpected response: %q", resp.Response)
	}
}

// TestADKDisallowedToolRejection verifies the OnToolErrorCallback contract:
// a model call to a tool outside the role-scoped set resolves to the exact
// Herd rejection string in ToolsCalled.
func TestADKDisallowedToolRejection(t *testing.T) {
	isolateAgentEnv(t)
	turn := 0
	ts := httptest.NewServer(openAIJSONhandler(func(w http.ResponseWriter, r *http.Request) {
		var req OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		turn++
		if turn == 1 {
			encodeOpenAIResponse(w, "", []OpenAIToolCall{
				{
					ID:   "call-bad-1",
					Type: "function",
					Function: OpenAIFunctionCall{
						Name:      "cluster_exec",
						Arguments: `{"target":"node-1","command":"rm -rf /"}`,
					},
				},
			})
			return
		}
		encodeOpenAIResponse(w, "understood, not permitted", nil)
	}))
	defer ts.Close()

	mockCtx := &mockClusterContext{nodeName: "test-node"}
	engine := NewEngine(Config{
		NodeName:      "test-node",
		OpenAIBaseURL: ts.URL,
		OpenAIModel:   "mock-gpt",
		Provider:      "openai",
	}, mockCtx)
	engine.RegisterAgent(&AgentDefinition{
		Name:         "researcher",
		Role:         "researcher",
		AllowedTools: []string{"google_search", "fetch_url"},
	})

	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "do research",
		Role:   "researcher",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt error: %v", err)
	}
	if len(resp.ToolsCalled) != 1 {
		t.Fatalf("expected 1 rejected tool call in trace, got %+v", resp.ToolsCalled)
	}
	if !strings.Contains(resp.ToolsCalled[0].Result, "not allowed or registered") {
		t.Errorf("expected Herd rejection string, got: %q", resp.ToolsCalled[0].Result)
	}
}
