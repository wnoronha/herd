package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"herd/internal/ipc"
	"herd/internal/kv"
	"herd/internal/roster"
)

type mockClusterContext struct {
	nodeName string
	roster   *roster.Store
	kvStore  *kv.Store
}

func (m *mockClusterContext) GetNodeName() string {
	return m.nodeName
}

func (m *mockClusterContext) GetRosterStore() *roster.Store {
	return m.roster
}

func (m *mockClusterContext) GetKVStore() *kv.Store {
	return m.kvStore
}

func (m *mockClusterContext) GetKVDelegate() *kv.GossipDelegate {
	return nil
}

func (m *mockClusterContext) ExecuteCommand(ctx context.Context, req *ipc.ExecRequest) (*ipc.ExecResponse, error) {
	return &ipc.ExecResponse{
		Results: []ipc.NodeExecResult{
			{
				NodeName: m.nodeName,
				Stdout:   "mock output",
				ExitCode: 0,
			},
		},
	}, nil
}

func (m *mockClusterContext) CopyFile(ctx context.Context, req *ipc.CPRequest) (*ipc.CPResponse, error) {
	return &ipc.CPResponse{
		BytesTransferred: 1234,
		Message:          fmt.Sprintf("Transferred %s to %s (1234 bytes)", req.Source, req.Destination),
	}, nil
}

func (m *mockClusterContext) GetDataDir() string {
	return ""
}

func (m *mockClusterContext) JoinNode(ctx context.Context, addr string) (*ipc.JoinResponse, error) {
	return &ipc.JoinResponse{
		JoinedNodes: 1,
		Message:     fmt.Sprintf("joined %s", addr),
	}, nil
}

func (m *mockClusterContext) LeaveCluster(ctx context.Context) (*ipc.LeaveResponse, error) {
	return &ipc.LeaveResponse{
		Message: "mock left cluster",
	}, nil
}

func (m *mockClusterContext) GetStatus(ctx context.Context) (*ipc.StatusResponse, error) {
	return &ipc.StatusResponse{
		NodeName:      m.nodeName,
		PublicKey:     "mock-pubkey",
		TailcatAddr:   "tcpMockTailcat",
		BindPort:      7946,
		State:         "running",
		MemberCount:   1,
		UptimeSeconds: 42,
	}, nil
}

func TestAgentTools(t *testing.T) {
	meta := &roster.NodeMeta{NodeName: "test-node", TailcatAddr: "tcpTest..."}
	rStore := roster.NewStore(meta)
	kvStore := kv.NewStore("test-node")

	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   rStore,
		kvStore:  kvStore,
	}

	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	t.Run("ClusterRosterTool", func(t *testing.T) {
		tool, ok := engine.tools["cluster_roster"]
		if !ok {
			t.Fatalf("cluster_roster tool not found")
		}
		res, err := tool.Handler(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("tool handler error: %v", err)
		}
		if res == nil {
			t.Fatalf("expected non-nil roster result")
		}
	})

	t.Run("ClusterCPTool", func(t *testing.T) {
		tool, ok := engine.tools["cluster_cp"]
		if !ok {
			t.Fatalf("cluster_cp tool not found")
		}
		res, err := tool.Handler(context.Background(), map[string]any{
			"source":      "/tmp/local.txt",
			"destination": "node-2:/tmp/remote.txt",
		})
		if err != nil {
			t.Fatalf("tool handler error: %v", err)
		}
		resMap, ok := res.(map[string]any)
		if !ok || resMap["status"] != "success" {
			t.Fatalf("expected status 'success', got: %v", res)
		}
		if resMap["bytes_transferred"] != int64(1234) {
			t.Errorf("expected 1234 bytes transferred, got: %v", resMap["bytes_transferred"])
		}
	})

	t.Run("SharedKVTool", func(t *testing.T) {
		tool, ok := engine.tools["shared_kv"]
		if !ok {
			t.Fatalf("shared_kv tool not found")
		}

		// Set
		_, err := tool.Handler(context.Background(), map[string]any{
			"action": "set",
			"key":    "agent/status",
			"value":  "active",
		})
		if err != nil {
			t.Fatalf("set failed: %v", err)
		}

		// Get
		res, err := tool.Handler(context.Background(), map[string]any{
			"action": "get",
			"key":    "agent/status",
		})
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		resMap, ok := res.(map[string]any)
		if !ok || resMap["value"] != "active" {
			t.Fatalf("expected value 'active', got: %v", res)
		}
	})

	t.Run("DeterministicFallbackPrompt", func(t *testing.T) {
		resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
			Prompt: "Show cluster roster",
		})
		if err != nil {
			t.Fatalf("prompt execution error: %v", err)
		}
		if len(resp.ToolsCalled) == 0 {
			t.Errorf("expected cluster_roster tool call in trace")
		}
	})

	t.Run("DynamicKVConfigResolution", func(t *testing.T) {
		// Seed global agent config in KV
		globalJSON := `{"model":"llama3.2-custom","base_url":"http://10.0.0.1:11434/v1"}`
		kvStore.Set("agent:config:global", []byte(globalJSON), 0)

		eff := engine.resolveEffectiveConfig()
		if eff.OpenAIModel != "llama3.2-custom" {
			t.Errorf("expected model 'llama3.2-custom', got: %s", eff.OpenAIModel)
		}
		if eff.OpenAIBaseURL != "http://10.0.0.1:11434/v1" {
			t.Errorf("expected base_url 'http://10.0.0.1:11434/v1', got: %s", eff.OpenAIBaseURL)
		}

		// Seed node-specific override in KV
		nodeJSON := `{"model":"qwen2.5-coder:1.5b"}`
		kvStore.Set("agent:config:nodes/test-node", []byte(nodeJSON), 0)

		effNode := engine.resolveEffectiveConfig()
		if effNode.OpenAIModel != "qwen2.5-coder:1.5b" {
			t.Errorf("expected node override model 'qwen2.5-coder:1.5b', got: %s", effNode.OpenAIModel)
		}
	})

	t.Run("ExecPolicyValidation", func(t *testing.T) {
		// Set allowed policy whitelist
		policyJSON := `["uptime", "df -h", "systemctl status *"]`
		kvStore.Set("agent:policy:allowed_exec", []byte(policyJSON), 0)

		if err := engine.validateExecPolicy("uptime"); err != nil {
			t.Errorf("expected 'uptime' to be permitted, got: %v", err)
		}
		if err := engine.validateExecPolicy("systemctl status herd"); err != nil {
			t.Errorf("expected 'systemctl status herd' wildcard to be permitted, got: %v", err)
		}
		if err := engine.validateExecPolicy("rm -rf /"); err == nil {
			t.Errorf("expected 'rm -rf /' to be rejected by policy")
		}
	})
}

func TestAgentWebTools(t *testing.T) {
	meta := &roster.NodeMeta{NodeName: "test-node", TailcatAddr: "tcpTest..."}
	rStore := roster.NewStore(meta)
	kvStore := kv.NewStore("test-node")

	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   rStore,
		kvStore:  kvStore,
	}

	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	t.Run("FetchURLTool", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body><h1>Herd Mesh</h1><p>Decentralized cluster runtime.</p></body></html>"))
		}))
		defer ts.Close()

		tool, ok := engine.tools["fetch_url"]
		if !ok {
			t.Fatalf("fetch_url tool not found")
		}

		res, err := tool.Handler(context.Background(), map[string]any{
			"url": ts.URL,
		})
		if err != nil {
			t.Fatalf("fetch_url handler error: %v", err)
		}
		resMap, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("expected map result, got: %T", res)
		}
		content, ok := resMap["content"].(string)
		if !ok || !strings.Contains(content, "Herd Mesh") || !strings.Contains(content, "Decentralized cluster runtime.") {
			t.Errorf("unexpected content: %v", resMap["content"])
		}
	})

	t.Run("GoogleSearchTool", func(t *testing.T) {
		tool, ok := engine.tools["google_search"]
		if !ok {
			t.Fatalf("google_search tool not found")
		}

		// Validation on empty query
		_, err := tool.Handler(context.Background(), map[string]any{})
		if err == nil {
			t.Errorf("expected error on empty query")
		}

		// Test HTML parsing logic directly
		mockHTML := `<h2 class="result__title"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org&rut=1">Go Language</a></h2><a class="result__snippet" href="#">An open source programming language.</a>`
		parsed := parseDuckDuckGoHTML(mockHTML, 5)
		if len(parsed) != 1 {
			t.Fatalf("expected 1 parsed result, got %d", len(parsed))
		}
		if parsed[0].Title != "Go Language" || parsed[0].URL != "https://golang.org" {
			t.Errorf("unexpected parsed result: %+v", parsed[0])
		}
	})
}

func TestOpenAIBackendToolLoop(t *testing.T) {
	meta := &roster.NodeMeta{NodeName: "test-node", TailcatAddr: "tcpTest..."}
	rStore := roster.NewStore(meta)
	kvStore := kv.NewStore("test-node")

	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   rStore,
		kvStore:  kvStore,
	}

	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// First turn: model returns tool call
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
							Role: "assistant",
							ToolCalls: []OpenAIToolCall{
								{
									ID:   "call_123",
									Type: "function",
									Function: OpenAIFunctionCall{
										Name:      "cluster_roster",
										Arguments: `{"filter_status":"all"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			// Second turn: model returns final answer
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
							Role:    "assistant",
							Content: "Found 1 alive node: test-node.",
						},
						FinishReason: "stop",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	engine := NewEngine(Config{
		NodeName:      "test-node",
		OpenAIBaseURL: ts.URL,
		OpenAIModel:   "mock-gpt",
		Provider:      "openai",
	}, mockCtx)

	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "What nodes are in the cluster?",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt error: %v", err)
	}

	if len(resp.ToolsCalled) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolsCalled))
	}
	if resp.ToolsCalled[0].Name != "cluster_roster" {
		t.Errorf("expected tool name 'cluster_roster', got %s", resp.ToolsCalled[0].Name)
	}
	if resp.Response != "Found 1 alive node: test-node." {
		t.Errorf("unexpected response: %s", resp.Response)
	}
}

func TestInterAgentMessagingTools(t *testing.T) {
	meta := &roster.NodeMeta{NodeName: "node-alpha", TailcatAddr: "tcpAlpha..."}
	rStore := roster.NewStore(meta)
	kvStore := kv.NewStore("node-alpha")

	mockCtxAlpha := &mockClusterContext{
		nodeName: "node-alpha",
		roster:   rStore,
		kvStore:  kvStore,
	}

	engineAlpha := NewEngine(Config{NodeName: "node-alpha"}, mockCtxAlpha)

	// 1. Send Message from node-alpha to node-beta
	sendTool, ok := engineAlpha.tools["agent_send_mail"]
	if !ok {
		t.Fatalf("agent_send_mail tool not registered")
	}

	sendRes, err := sendTool.Handler(context.Background(), map[string]any{
		"to":        "node-beta",
		"topic":     "telemetry_request",
		"message":   "report CPU and GPU temperature",
		"ttl":       "10m",
		"thread_id": "thread-telemetry-1",
	})
	if err != nil {
		t.Fatalf("sendTool handler error: %v", err)
	}
	sendMap := sendRes.(map[string]any)
	if sendMap["status"] != "queued" {
		t.Errorf("expected status queued, got: %v", sendMap["status"])
	}

	// 2. Broadcast Message from node-alpha
	bcastTool, ok := engineAlpha.tools["agent_broadcast"]
	if !ok {
		t.Fatalf("agent_broadcast tool not registered")
	}
	bcastRes, err := bcastTool.Handler(context.Background(), map[string]any{
		"topic":   "cluster_alert",
		"message": "node maintenance scheduled",
	})
	if err != nil {
		t.Fatalf("bcastTool handler error: %v", err)
	}
	bcastMap := bcastRes.(map[string]any)
	if bcastMap["status"] != "broadcasted" {
		t.Errorf("expected status broadcasted, got: %v", bcastMap["status"])
	}

	// 3. Read Mailbox on node-beta
	mockCtxBeta := &mockClusterContext{
		nodeName: "node-beta",
		roster:   rStore,
		kvStore:  kvStore, // shared in-memory store in test
	}
	engineBeta := NewEngine(Config{NodeName: "node-beta"}, mockCtxBeta)

	readTool, ok := engineBeta.tools["agent_read_mailbox"]
	if !ok {
		t.Fatalf("agent_read_mailbox tool not registered")
	}

	// Read without ack
	readRes, err := readTool.Handler(context.Background(), map[string]any{
		"ack": false,
	})
	if err != nil {
		t.Fatalf("readTool handler error: %v", err)
	}
	readMap := readRes.(map[string]any)
	if readMap["count"] != 1 {
		t.Fatalf("expected 1 message in mailbox, got: %v", readMap["count"])
	}

	// Read with ack (removes message)
	_, err = readTool.Handler(context.Background(), map[string]any{
		"ack": true,
	})
	if err != nil {
		t.Fatalf("readTool ack handler error: %v", err)
	}

	// Verify mailbox is empty after ack
	emptyRes, _ := readTool.Handler(context.Background(), map[string]any{})
	emptyMap := emptyRes.(map[string]any)
	if emptyMap["count"] != 0 {
		t.Errorf("expected mailbox to be 0 after ack, got: %v", emptyMap["count"])
	}
}

func TestAgentMailboxTools(t *testing.T) {
	meta := &roster.NodeMeta{NodeName: "node-gamma", TailcatAddr: "tcpGamma..."}
	rStore := roster.NewStore(meta)
	kvStore := kv.NewStore("node-gamma")

	mockCtxGamma := &mockClusterContext{
		nodeName: "node-gamma",
		roster:   rStore,
		kvStore:  kvStore,
	}
	mockCtxDelta := &mockClusterContext{
		nodeName: "node-delta",
		roster:   rStore,
		kvStore:  kvStore,
	}

	engineGamma := NewEngine(Config{NodeName: "node-gamma"}, mockCtxGamma)
	engineDelta := NewEngine(Config{NodeName: "node-delta"}, mockCtxDelta)

	// 1. Verify tools are registered
	sendMailTool, ok := engineGamma.tools["agent_send_mail"]
	if !ok {
		t.Fatalf("agent_send_mail tool not registered")
	}
	readMailTool, ok := engineDelta.tools["agent_read_mailbox"]
	if !ok {
		t.Fatalf("agent_read_mailbox tool not registered")
	}

	// 2. Send mail from gamma to delta
	sendRes, err := sendMailTool.Handler(context.Background(), map[string]any{
		"to":        "mailbox:node-delta",
		"topic":     "work_request",
		"message":   "process dataset A",
		"thread_id": "thread-101",
	})
	if err != nil {
		t.Fatalf("sendMailTool failed: %v", err)
	}
	sendMap := sendRes.(map[string]any)
	if sendMap["status"] != "queued" || sendMap["to"] != "node-delta" {
		t.Errorf("unexpected sendMail response: %+v", sendMap)
	}

	// 3. Read mailbox on delta without ack
	readRes, err := readMailTool.Handler(context.Background(), map[string]any{
		"ack": false,
	})
	if err != nil {
		t.Fatalf("readMailTool failed: %v", err)
	}
	readMap := readRes.(map[string]any)
	if readMap["count"] != 1 {
		t.Fatalf("expected count=1, got: %v", readMap["count"])
	}

	// 4. Read mailbox on delta with ack
	readAckRes, err := readMailTool.Handler(context.Background(), map[string]any{
		"ack": true,
	})
	if err != nil {
		t.Fatalf("readMailTool ack failed: %v", err)
	}
	readAckMap := readAckRes.(map[string]any)
	if readAckMap["count"] != 1 || !readAckMap["acked"].(bool) {
		t.Errorf("unexpected readAck response: %+v", readAckMap)
	}

	// 5. Verify mailbox is empty after ack
	emptyRes, _ := readMailTool.Handler(context.Background(), map[string]any{})
	emptyMap := emptyRes.(map[string]any)
	if emptyMap["count"] != 0 {
		t.Errorf("expected 0 messages after ack, got: %v", emptyMap["count"])
	}
}

func TestParallelToolExecution(t *testing.T) {
	meta := &roster.NodeMeta{NodeName: "parallel-node"}
	rStore := roster.NewStore(meta)
	kvStore := kv.NewStore("parallel-node")

	mockCtx := &mockClusterContext{
		nodeName: "parallel-node",
		roster:   rStore,
		kvStore:  kvStore,
	}

	turnCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turnCount++
		w.Header().Set("Content-Type", "application/json")

		if turnCount == 1 {
			// First turn: return 3 parallel tool calls
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
							Role: "assistant",
							ToolCalls: []OpenAIToolCall{
								{
									ID:   "call-1",
									Type: "function",
									Function: struct {
										Name      string `json:"name"`
										Arguments string `json:"arguments"`
									}{
										Name:      "cluster_roster",
										Arguments: `{"filter_status":"all"}`,
									},
								},
								{
									ID:   "call-2",
									Type: "function",
									Function: struct {
										Name      string `json:"name"`
										Arguments string `json:"arguments"`
									}{
										Name:      "shared_kv",
										Arguments: `{"action":"set","key":"test/parallel","value":"ok"}`,
									},
								},
								{
									ID:   "call-3",
									Type: "function",
									Function: struct {
										Name      string `json:"name"`
										Arguments string `json:"arguments"`
									}{
										Name:      "shared_kv",
										Arguments: `{"action":"get","key":"test/parallel"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			// Second turn: return completed answer
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
							Role:    "assistant",
							Content: "All 3 parallel tools finished successfully.",
						},
						FinishReason: "stop",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	engine := NewEngine(Config{
		NodeName:      "parallel-node",
		OpenAIBaseURL: ts.URL,
		OpenAIModel:   "mock-gpt",
		Provider:      "openai",
	}, mockCtx)

	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "Run 3 parallel actions",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt error: %v", err)
	}

	if len(resp.ToolsCalled) != 3 {
		t.Fatalf("expected 3 tools called, got %d", len(resp.ToolsCalled))
	}

	// Verify exact deterministic order of results
	if resp.ToolsCalled[0].Name != "cluster_roster" {
		t.Errorf("expected first tool 'cluster_roster', got %s", resp.ToolsCalled[0].Name)
	}
	if resp.ToolsCalled[1].Name != "shared_kv" {
		t.Errorf("expected second tool 'shared_kv', got %s", resp.ToolsCalled[1].Name)
	}
	if resp.ToolsCalled[2].Name != "shared_kv" {
		t.Errorf("expected third tool 'shared_kv', got %s", resp.ToolsCalled[2].Name)
	}
	if resp.Response != "All 3 parallel tools finished successfully." {
		t.Errorf("unexpected final response: %s", resp.Response)
	}
}

func TestEngineAgentRegistration(t *testing.T) {
	mockCtx := &mockClusterContext{nodeName: "test-node"}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	def := &AgentDefinition{
		Name:         "Cluster-Researcher",
		Role:         "researcher",
		Description:  "Research specialist",
		AllowedTools: []string{"google_search", "fetch_url"},
	}

	engine.RegisterAgent(def)

	// Lookup by name
	foundByName, ok := engine.GetAgent("cluster-researcher")
	if !ok || foundByName.Role != "researcher" {
		t.Fatalf("expected to find agent by lowercase name")
	}

	// Lookup by role
	foundByRole, ok := engine.GetAgent("RESEARCHER")
	if !ok || foundByRole.Name != "Cluster-Researcher" {
		t.Fatalf("expected to find agent by uppercase role")
	}

	agents := engine.ListAgents()
	var hasResearcher bool
	for _, a := range agents {
		if a.Role == "researcher" {
			hasResearcher = true
			break
		}
	}
	if !hasResearcher {
		t.Errorf("expected researcher in ListAgents")
	}
}

func TestRoleBasedScopedToolsets_FallbackAndNotFound(t *testing.T) {
	meta := &roster.NodeMeta{NodeName: "test-node", TailcatAddr: "tcpTest..."}
	rStore := roster.NewStore(meta)
	mockCtx := &mockClusterContext{nodeName: "test-node", roster: rStore}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	engine.RegisterAgent(&AgentDefinition{
		Name:         "monitor",
		Role:         "monitor",
		AllowedTools: []string{"cluster_roster"},
	})

	// 1. Role not found
	_, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "who are you?",
		Role:   "nonexistent-role",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected role not found error, got: %v", err)
	}

	// 2. Fallback with scoped toolset (only cluster_roster allowed)
	// Prompt requesting exec/uptime should NOT call cluster_exec because it's not allowed
	resp, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "check uptime of node",
		Role:   "monitor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolsCalled) != 0 {
		t.Errorf("expected 0 tools called for disallowed cluster_exec, got %d", len(resp.ToolsCalled))
	}

	// Prompt requesting roster should call cluster_roster because it is allowed
	respRoster, err := engine.ExecutePrompt(context.Background(), &PromptRequest{
		Prompt: "show cluster roster nodes",
		Role:   "monitor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(respRoster.ToolsCalled) != 1 || respRoster.ToolsCalled[0].Name != "cluster_roster" {
		t.Errorf("expected cluster_roster to be called, got: %+v", respRoster.ToolsCalled)
	}
}

func TestRoleBasedScopedToolsets_OpenAI(t *testing.T) {
	mockCtx := &mockClusterContext{nodeName: "test-node"}
	turn := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		turn++
		if turn == 1 {
			// Verify that the OpenAI tools sent in the request only include allowed tools
			for _, tool := range req.Tools {
				if tool.Function.Name != "google_search" && tool.Function.Name != "fetch_url" {
					t.Errorf("disallowed tool found in OpenAI request schema: %s", tool.Function.Name)
				}
			}

			// Simulate model attempting to invoke a disallowed tool
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
							Role: "assistant",
							ToolCalls: []OpenAIToolCall{
								{
									ID:   "call_forbidden",
									Type: "function",
									Function: OpenAIFunctionCall{
										Name:      "cluster_exec",
										Arguments: `{"target":"node-1","command":"rm -rf /"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Turn 2: verify the assistant received the error that tool is not allowed
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role != "tool" || !strings.Contains(fmt.Sprintf("%v", lastMsg.Content), "not allowed") {
			t.Errorf("expected tool rejection message, got: %+v", lastMsg)
		}

		finalResp := OpenAIChatResponse{
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
						Content: "Understood, cluster_exec is not permitted for this role.",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(finalResp)
	}))
	defer ts.Close()

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
		Prompt: "Perform research task",
		Role:   "researcher",
	})
	if err != nil {
		t.Fatalf("ExecutePrompt error: %v", err)
	}

	if resp.Response != "Understood, cluster_exec is not permitted for this role." {
		t.Errorf("unexpected response: %s", resp.Response)
	}
}





