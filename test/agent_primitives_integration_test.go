package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"herd/internal/agent"
	"herd/internal/identity"
	"herd/internal/testutil"
)

func TestAgentPrimitivesEndToEnd(t *testing.T) {
	testutil.SetupHermeticEnvironment(t)

	tmpDir := t.TempDir()
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}

	cluster := newLocalCluster(t, tmpDir, psk)
	defer cluster.Close()

	// 1. Start node-alpha (seed) and node-beta
	nodeAlpha := cluster.StartNode("node-alpha", "")
	alphaAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(nodeAlpha.Daemon.Transport.GetPort()))
	nodeBeta := cluster.StartNode("node-beta", alphaAddr)

	verifyClusterConvergence(t, []*clusterNode{nodeAlpha, nodeBeta}, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tools := agent.BuildDefaultTools(nodeAlpha.Daemon)
	toolMap := make(map[string]agent.ToolDefinition)
	for _, td := range tools {
		toolMap[td.Name] = td
	}

	t.Run("FilePrimitives", func(t *testing.T) {
		verifyFilePrimitives(t, ctx, toolMap, tmpDir)
	})

	t.Run("ArtifactStore", func(t *testing.T) {
		verifyArtifactPrimitives(t, ctx, toolMap)
	})

	t.Run("ClusterManage", func(t *testing.T) {
		verifyClusterManagePrimitives(t, ctx, toolMap)
	})

	t.Run("ClusterForward", func(t *testing.T) {
		verifyClusterForwardPrimitives(t, ctx, toolMap, nodeBeta)
	})

	t.Run("AutonomousToolLoop", func(t *testing.T) {
		verifyAutonomousToolLoop(t, ctx, nodeAlpha, tmpDir)
	})
}

func verifyFilePrimitives(t *testing.T, ctx context.Context, toolMap map[string]agent.ToolDefinition, tmpDir string) {
	t.Helper()
	fileWriteTool := toolMap["file_write"]
	fileReadTool := toolMap["file_read"]

	testFilePath := filepath.Join(tmpDir, "workspace", "output.txt")
	writeRes, err := fileWriteTool.Handler(ctx, map[string]any{
		"path":    testFilePath,
		"content": "line 1: cluster test\nline 2: second entry\n",
	})
	if err != nil || writeRes.(map[string]any)["status"] != "created" {
		t.Fatalf("file_write failed: %v", err)
	}

	readRes, err := fileReadTool.Handler(ctx, map[string]any{
		"path":         testFilePath,
		"offset_lines": float64(2),
		"max_lines":    float64(1),
	})
	if err != nil {
		t.Fatalf("file_read failed: %v", err)
	}
	if readRes.(map[string]any)["content"] != "line 2: second entry" {
		t.Errorf("unexpected read content: %v", readRes)
	}
}

func verifyArtifactPrimitives(t *testing.T, ctx context.Context, toolMap map[string]agent.ToolDefinition) {
	t.Helper()
	artifactTool := toolMap["artifact_store"]

	storeRes, err := artifactTool.Handler(ctx, map[string]any{
		"action":  "store",
		"name":    "cluster_report.md",
		"content": "# Live Cluster Report\nAll 2 nodes healthy.",
		"metadata": map[string]any{
			"cluster": "test-mesh",
		},
	})
	if err != nil || storeRes.(map[string]any)["status"] != "stored" {
		t.Fatalf("artifact_store 'store' failed: %v", err)
	}

	getRes, err := artifactTool.Handler(ctx, map[string]any{
		"action": "get",
		"name":   "cluster_report.md",
	})
	if err != nil || getRes.(map[string]any)["name"] != "cluster_report.md" {
		t.Fatalf("artifact_store 'get' failed: %v", err)
	}
}

func verifyClusterManagePrimitives(t *testing.T, ctx context.Context, toolMap map[string]agent.ToolDefinition) {
	t.Helper()
	manageTool := toolMap["cluster_manage"]

	statusRes, err := manageTool.Handler(ctx, map[string]any{
		"action": "status",
	})
	if err != nil {
		t.Fatalf("cluster_manage status failed: %v", err)
	}
	statusMap := statusRes.(map[string]any)
	if statusMap["node_name"] != "node-alpha" || statusMap["member_count"].(int) != 2 {
		t.Errorf("unexpected status result: %v", statusMap)
	}
}

func verifyClusterForwardPrimitives(t *testing.T, ctx context.Context, toolMap map[string]agent.ToolDefinition, nodeBeta *clusterNode) {
	t.Helper()
	forwardTool := toolMap["cluster_forward"]

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	var freePort float64
	for _, c := range portStr {
		freePort = freePort*10 + float64(c-'0')
	}

	fwdRes, err := forwardTool.Handler(ctx, map[string]any{
		"action":      "start",
		"local_port":  freePort,
		"target_node": "node-beta",
		"target_port": float64(nodeBeta.Daemon.Transport.GetPort()),
	})
	if err != nil || fwdRes.(map[string]any)["status"] != "started" {
		t.Fatalf("cluster_forward start failed: %v", err)
	}

	listRes, err := forwardTool.Handler(ctx, map[string]any{"action": "list"})
	if err != nil || listRes.(map[string]any)["count"].(int) < 1 {
		t.Errorf("expected at least 1 tunnel in list")
	}

	stopRes, err := forwardTool.Handler(ctx, map[string]any{"action": "stop", "local_port": freePort})
	if err != nil || stopRes.(map[string]any)["status"] != "stopped" {
		t.Fatalf("cluster_forward stop failed: %v", err)
	}
}

func verifyAutonomousToolLoop(t *testing.T, ctx context.Context, nodeAlpha *clusterNode, tmpDir string) {
	t.Helper()
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if turn == 1 {
			resp := agent.OpenAIChatResponse{
				Choices: []struct {
					Message struct {
						Role      string                 `json:"role"`
						Content   string                 `json:"content"`
						ToolCalls []agent.OpenAIToolCall `json:"tool_calls,omitempty"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{
					{
						Message: struct {
							Role      string                 `json:"role"`
							Content   string                 `json:"content"`
							ToolCalls []agent.OpenAIToolCall `json:"tool_calls,omitempty"`
						}{
							Role: "assistant",
							ToolCalls: []agent.OpenAIToolCall{
								{
									ID:   "call_manage",
									Type: "function",
									Function: agent.OpenAIFunctionCall{
										Name:      "cluster_manage",
										Arguments: `{"action":"status"}`,
									},
								},
								{
									ID:   "call_write",
									Type: "function",
									Function: agent.OpenAIFunctionCall{
										Name:      "file_write",
										Arguments: fmt.Sprintf(`{"path":%q,"content":"autonomous model write"}`, filepath.Join(tmpDir, "model.txt")),
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
			resp := agent.OpenAIChatResponse{
				Choices: []struct {
					Message struct {
						Role      string                 `json:"role"`
						Content   string                 `json:"content"`
						ToolCalls []agent.OpenAIToolCall `json:"tool_calls,omitempty"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{
					{
						Message: struct {
							Role      string                 `json:"role"`
							Content   string                 `json:"content"`
							ToolCalls []agent.OpenAIToolCall `json:"tool_calls,omitempty"`
						}{
							Role:    "assistant",
							Content: "Executed cluster_manage and file_write autonomously.",
						},
						FinishReason: "stop",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	liveCfg := agent.Config{
		NodeName:      "node-alpha",
		OpenAIBaseURL: ts.URL,
		OpenAIAPIKey:  "mock-test-key",
		OpenAIModel:   "gpt-4o-mini",
	}
	liveEngine := agent.NewEngine(liveCfg, nodeAlpha.Daemon)

	llmResp, err := liveEngine.ExecutePrompt(ctx, &agent.PromptRequest{
		Prompt: "Check cluster status and save report to disk",
	})
	if err != nil {
		t.Fatalf("autonomous tool execution failed: %v", err)
	}
	if len(llmResp.ToolsCalled) != 2 {
		t.Fatalf("expected 2 tools called, got %d", len(llmResp.ToolsCalled))
	}

	modelFileBytes, err := os.ReadFile(filepath.Join(tmpDir, "model.txt"))
	if err != nil || string(modelFileBytes) != "autonomous model write" {
		t.Fatalf("file written by autonomous model invalid or missing: %v", err)
	}
}
