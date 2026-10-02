package agent

import (
	"context"
	"net"
	"testing"
)

func TestClusterManageTool(t *testing.T) {
	mockCtx := &mockClusterContext{nodeName: "manage-node"}
	tool := buildClusterManageTool(mockCtx)
	ctx := context.Background()

	// 1. Status action
	statusRes, err := tool.Handler(ctx, map[string]any{
		"action": "status",
	})
	if err != nil {
		t.Fatalf("expected status to succeed: %v", err)
	}
	sMap := statusRes.(map[string]any)
	if sMap["node_name"] != "manage-node" || sMap["state"] != "running" {
		t.Errorf("unexpected status result: %v", sMap)
	}

	// 2. Join action
	joinRes, err := tool.Handler(ctx, map[string]any{
		"action":  "join",
		"address": "127.0.0.1:7946",
	})
	if err != nil {
		t.Fatalf("expected join to succeed: %v", err)
	}
	jMap := joinRes.(map[string]any)
	if jMap["status"] != "joined" || jMap["joined_nodes"] != 1 {
		t.Errorf("unexpected join result: %v", jMap)
	}

	// 3. Leave action
	leaveRes, err := tool.Handler(ctx, map[string]any{
		"action": "leave",
	})
	if err != nil {
		t.Fatalf("expected leave to succeed: %v", err)
	}
	lMap := leaveRes.(map[string]any)
	if lMap["status"] != "left" {
		t.Errorf("unexpected leave result: %v", lMap)
	}

	// 4. Invalid action
	_, err = tool.Handler(ctx, map[string]any{
		"action": "invalid_action",
	})
	if err == nil {
		t.Fatalf("expected error for invalid action, got nil")
	}
}

func TestClusterForwardTool(t *testing.T) {
	// Start a dummy TCP target server to forward to
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start target listener: %v", err)
	}
	defer func() { _ = targetListener.Close() }()

	_, targetPortStr, _ := net.SplitHostPort(targetListener.Addr().String())
	var targetPort float64
	for _, c := range targetPortStr {
		targetPort = targetPort*10 + float64(c-'0')
	}

	// Pick a free local port
	freeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	localAddrStr := freeListener.Addr().String()
	_ = freeListener.Close() // Close so the forwarder can bind it
	_, localPortStr, _ := net.SplitHostPort(localAddrStr)
	var localPort float64
	for _, c := range localPortStr {
		localPort = localPort*10 + float64(c-'0')
	}

	mockCtx := &mockClusterContext{nodeName: "fwd-node"}
	tool := buildClusterForwardTool(mockCtx)
	ctx := context.Background()

	// 1. Start forward
	startRes, err := tool.Handler(ctx, map[string]any{
		"action":      "start",
		"local_port":  localPort,
		"target_node": "127.0.0.1",
		"target_port": targetPort,
	})
	if err != nil {
		t.Fatalf("expected forward start to succeed: %v", err)
	}
	startMap := startRes.(map[string]any)
	if startMap["status"] != "started" {
		t.Errorf("expected status 'started', got %v", startMap["status"])
	}
	tunnelID := startMap["id"].(string)

	// 2. List forwards
	listRes, err := tool.Handler(ctx, map[string]any{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("expected forward list to succeed: %v", err)
	}
	listMap := listRes.(map[string]any)
	if listMap["count"].(int) < 1 {
		t.Errorf("expected at least 1 tunnel in list, got: %v", listMap["count"])
	}

	// 3. Stop forward
	stopRes, err := tool.Handler(ctx, map[string]any{
		"action": "stop",
		"id":     tunnelID,
	})
	if err != nil {
		t.Fatalf("expected forward stop to succeed: %v", err)
	}
	stopMap := stopRes.(map[string]any)
	if stopMap["status"] != "stopped" {
		t.Errorf("expected status 'stopped', got %v", stopMap["status"])
	}

	// 4. Verify tunnel no longer in list
	listAfterStop, _ := tool.Handler(ctx, map[string]any{"action": "list"})
	listAfterMap := listAfterStop.(map[string]any)
	if listAfterMap["count"].(int) != 0 {
		t.Errorf("expected 0 tunnels after stop, got: %v", listAfterMap["count"])
	}
}
