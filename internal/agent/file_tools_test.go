package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileReadWriteTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "herd-file-tools-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	writeTool := buildFileWriteTool()
	readTool := buildFileReadTool()
	ctx := context.Background()

	targetFile := filepath.Join(tempDir, "subdir", "test.txt")

	// 1. Write new file (creates parent dir automatically)
	res, err := writeTool.Handler(ctx, map[string]any{
		"path":    targetFile,
		"content": "line 1\nline 2\nline 3\n",
	})
	if err != nil {
		t.Fatalf("expected write to succeed, got: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["status"] != "created" {
		t.Errorf("expected status 'created', got: %v", resMap["status"])
	}

	// 2. Read full file
	readRes, err := readTool.Handler(ctx, map[string]any{
		"path": targetFile,
	})
	if err != nil {
		t.Fatalf("expected read to succeed, got: %v", err)
	}
	readMap := readRes.(map[string]any)
	if readMap["total_lines"] != 3 {
		t.Errorf("expected 3 total lines, got: %v", readMap["total_lines"])
	}
	if readMap["content"] != "line 1\nline 2\nline 3" {
		t.Errorf("unexpected content: %v", readMap["content"])
	}

	// 3. Read with offset and line limit
	readResPart, err := readTool.Handler(ctx, map[string]any{
		"path":         targetFile,
		"offset_lines": float64(2),
		"max_lines":    float64(1),
	})
	if err != nil {
		t.Fatalf("expected partial read to succeed, got: %v", err)
	}
	partMap := readResPart.(map[string]any)
	if partMap["returned_lines"] != 1 {
		t.Errorf("expected 1 returned line, got: %v", partMap["returned_lines"])
	}
	if partMap["content"] != "line 2" {
		t.Errorf("expected 'line 2', got: %v", partMap["content"])
	}

	// 4. Append to file
	appendRes, err := writeTool.Handler(ctx, map[string]any{
		"path":    targetFile,
		"content": "line 4\n",
		"append":  true,
	})
	if err != nil {
		t.Fatalf("expected append to succeed, got: %v", err)
	}
	if appendRes.(map[string]any)["status"] != "appended" {
		t.Errorf("expected status 'appended', got: %v", appendRes.(map[string]any)["status"])
	}

	// Verify appended content
	readResAfterAppend, err := readTool.Handler(ctx, map[string]any{
		"path": targetFile,
	})
	if err != nil {
		t.Fatalf("expected read after append to succeed: %v", err)
	}
	if readResAfterAppend.(map[string]any)["total_lines"] != 4 {
		t.Errorf("expected 4 total lines, got: %v", readResAfterAppend.(map[string]any)["total_lines"])
	}

	// 5. Overwrite=false error check
	_, err = writeTool.Handler(ctx, map[string]any{
		"path":      targetFile,
		"content":   "will fail",
		"overwrite": false,
	})
	if err == nil {
		t.Fatalf("expected error when overwrite=false on existing file, got nil")
	}
}

func TestArtifactStoreTool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "herd-artifact-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	t.Setenv("HERD_ARTIFACTS_DIR", tempDir)

	mockCtx := &mockClusterContext{nodeName: "artifact-node"}
	tool := buildArtifactStoreTool(mockCtx)
	ctx := context.Background()

	// 1. Store an artifact
	storeRes, err := tool.Handler(ctx, map[string]any{
		"action":  "store",
		"name":    "plan.md",
		"content": "# Test Plan\nDetails here...",
		"metadata": map[string]any{
			"version": "1.0",
			"author":  "agent-ops",
		},
	})
	if err != nil {
		t.Fatalf("expected artifact store to succeed: %v", err)
	}
	storeMap := storeRes.(map[string]any)
	if storeMap["status"] != "stored" || storeMap["name"] != "plan.md" {
		t.Errorf("unexpected store result: %v", storeMap)
	}

	// 2. Get artifact
	getRes, err := tool.Handler(ctx, map[string]any{
		"action": "get",
		"name":   "plan.md",
	})
	if err != nil {
		t.Fatalf("expected artifact get to succeed: %v", err)
	}
	getMap := getRes.(map[string]any)
	if getMap["content"] != "# Test Plan\nDetails here..." {
		t.Errorf("unexpected artifact content: %v", getMap["content"])
	}
	meta := getMap["metadata"].(map[string]string)
	if meta["author"] != "agent-ops" || meta["version"] != "1.0" {
		t.Errorf("unexpected metadata: %v", meta)
	}

	// 3. List artifacts
	listRes, err := tool.Handler(ctx, map[string]any{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("expected artifact list to succeed: %v", err)
	}
	listMap := listRes.(map[string]any)
	if listMap["count"] != 1 {
		t.Errorf("expected 1 artifact, got: %v", listMap["count"])
	}

	// 4. Delete artifact
	delRes, err := tool.Handler(ctx, map[string]any{
		"action": "delete",
		"name":   "plan.md",
	})
	if err != nil {
		t.Fatalf("expected artifact delete to succeed: %v", err)
	}
	if delRes.(map[string]any)["status"] != "deleted" {
		t.Errorf("unexpected delete status: %v", delRes.(map[string]any)["status"])
	}

	// Verify not found after delete
	_, err = tool.Handler(ctx, map[string]any{
		"action": "get",
		"name":   "plan.md",
	})
	if err == nil {
		t.Fatalf("expected error getting deleted artifact, got nil")
	}
}
