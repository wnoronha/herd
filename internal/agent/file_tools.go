package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxFileReadBytes = 10 * 1024 * 1024 // 10MB safety limit
	defaultMaxLines  = 1000
)

// buildFileReadTool creates the file_read primitive tool.
func buildFileReadTool() ToolDefinition {
	return ToolDefinition{
		Name:        "file_read",
		Description: "Read text contents of a local file on the current node with optional line offset and line limit.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative path of the file to read",
				},
				"offset_lines": map[string]any{
					"type":        "integer",
					"description": "1-based starting line number (default: 1)",
				},
				"max_lines": map[string]any{
					"type":        "integer",
					"description": "Maximum number of lines to return (default: 1000, max: 10000)",
				},
			},
			"required": []string{"path"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			pathVal, ok := args["path"].(string)
			if !ok || strings.TrimSpace(pathVal) == "" {
				return nil, errors.New("parameter 'path' is required")
			}
			filePath := filepath.Clean(strings.TrimSpace(pathVal))

			offset := 1
			if o, ok := args["offset_lines"].(float64); ok && o >= 1 {
				offset = int(o)
			}
			maxLines := defaultMaxLines
			if m, ok := args["max_lines"].(float64); ok && m > 0 {
				maxLines = int(m)
				if maxLines > 10000 {
					maxLines = 10000
				}
			}

			fileInfo, err := os.Stat(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to stat file '%s': %w", filePath, err)
			}
			if fileInfo.IsDir() {
				return nil, fmt.Errorf("path '%s' is a directory, not a file", filePath)
			}
			if fileInfo.Size() > maxFileReadBytes {
				return nil, fmt.Errorf("file size (%d bytes) exceeds 10MB safety limit", fileInfo.Size())
			}

			f, err := os.Open(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to open file '%s': %w", filePath, err)
			}
			defer func() { _ = f.Close() }()

			var lines []string
			scanner := bufio.NewScanner(f)
			currentLine := 0
			totalLines := 0

			for scanner.Scan() {
				currentLine++
				totalLines++
				if currentLine >= offset && len(lines) < maxLines {
					lines = append(lines, scanner.Text())
				}
			}
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("error reading file '%s': %w", filePath, err)
			}

			return map[string]any{
				"path":           filePath,
				"total_lines":    totalLines,
				"offset_lines":   offset,
				"returned_lines": len(lines),
				"content":        strings.Join(lines, "\n"),
			}, nil
		},
	}
}

// buildFileWriteTool creates the file_write primitive tool.
func buildFileWriteTool() ToolDefinition {
	return ToolDefinition{
		Name:        "file_write",
		Description: "Write content to a local file on the current node with support for overwrite or append and automatic parent directory creation.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Target filesystem path",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content string to write into the file",
				},
				"append": map[string]any{
					"type":        "boolean",
					"description": "If true, append to existing file instead of overwriting (default: false)",
				},
				"overwrite": map[string]any{
					"type":        "boolean",
					"description": "If false and file exists and append is false, returns error (default: true)",
				},
			},
			"required": []string{"path", "content"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			pathVal, ok := args["path"].(string)
			if !ok || strings.TrimSpace(pathVal) == "" {
				return nil, errors.New("parameter 'path' is required")
			}
			filePath := filepath.Clean(strings.TrimSpace(pathVal))

			contentVal, ok := args["content"].(string)
			if !ok {
				return nil, errors.New("parameter 'content' is required and must be a string")
			}

			appendMode := false
			if a, ok := args["append"].(bool); ok {
				appendMode = a
			}

			overwriteMode := true
			if o, ok := args["overwrite"].(bool); ok {
				overwriteMode = o
			}

			// Ensure parent directory exists
			dir := filepath.Dir(filePath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create directory '%s': %w", dir, err)
			}

			// Check file existence
			_, statErr := os.Stat(filePath)
			exists := statErr == nil

			if exists && !overwriteMode && !appendMode {
				return nil, fmt.Errorf("file '%s' already exists and overwrite=false", filePath)
			}

			flags := os.O_CREATE | os.O_WRONLY
			status := "created"
			if appendMode {
				flags |= os.O_APPEND
				if exists {
					status = "appended"
				}
			} else {
				flags |= os.O_TRUNC
				if exists {
					status = "overwritten"
				}
			}

			f, err := os.OpenFile(filePath, flags, 0644)
			if err != nil {
				return nil, fmt.Errorf("failed to open file '%s': %w", filePath, err)
			}
			defer func() { _ = f.Close() }()

			n, err := f.WriteString(contentVal)
			if err != nil {
				return nil, fmt.Errorf("failed to write content to '%s': %w", filePath, err)
			}

			return map[string]any{
				"path":          filePath,
				"bytes_written": n,
				"status":        status,
			}, nil
		},
	}
}

// ArtifactMeta holds metadata for a stored artifact.
type ArtifactMeta struct {
	Name        string            `json:"name"`
	Size        int64             `json:"size"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// resolveArtifactsDir finds the local directory for artifacts storage.
func resolveArtifactsDir(cctx ClusterContext) string {
	if cctx != nil {
		if dataDir := cctx.GetDataDir(); dataDir != "" {
			return filepath.Join(dataDir, "artifacts")
		}
	}
	if env := os.Getenv("HERD_ARTIFACTS_DIR"); env != "" {
		return env
	}
	if envData := os.Getenv("HERD_DATA_DIR"); envData != "" {
		return filepath.Join(envData, "artifacts")
	}
	home, err := os.UserHomeDir()
	if err == nil {
		node := "default"
		if cctx != nil && cctx.GetNodeName() != "" {
			node = cctx.GetNodeName()
		}
		return filepath.Join(home, ".local", "share", "herd", node, "artifacts")
	}
	return filepath.Join(os.TempDir(), "herd-artifacts")
}

// buildArtifactStoreTool creates the artifact_store tool following the ADK Artifacts pattern.
func buildArtifactStoreTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "artifact_store",
		Description: "Store, retrieve, list, or delete multi-turn execution artifacts (documents, diffs, code, logs) on the local node's artifact repository.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Artifact action: 'store', 'get', 'list', or 'delete'",
					"enum":        []string{"store", "get", "list", "delete"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Artifact identifier/filename (required for store, get, delete)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Artifact content string (required for action='store')",
				},
				"metadata": map[string]any{
					"type":        "object",
					"description": "Optional key-value metadata tags to store with the artifact",
				},
			},
			"required": []string{"action"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			action, ok := args["action"].(string)
			if !ok || strings.TrimSpace(action) == "" {
				return nil, errors.New("parameter 'action' is required")
			}
			action = strings.ToLower(strings.TrimSpace(action))

			artifactsDir := resolveArtifactsDir(cctx)
			if err := os.MkdirAll(artifactsDir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create artifacts directory '%s': %w", artifactsDir, err)
			}

			switch action {
			case "store":
				return executeArtifactStore(artifactsDir, args)
			case "get":
				return executeArtifactGet(artifactsDir, args)
			case "list":
				return executeArtifactList(artifactsDir)
			case "delete":
				return executeArtifactDelete(artifactsDir, args)
			default:
				return nil, fmt.Errorf("unsupported action '%s', expected 'store', 'get', 'list', or 'delete'", action)
			}
		},
	}
}

func executeArtifactStore(artifactsDir string, args map[string]any) (any, error) {
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, errors.New("parameter 'name' is required for action 'store'")
	}
	cleanName := filepath.Base(strings.TrimSpace(name))
	content, ok := args["content"].(string)
	if !ok {
		return nil, errors.New("parameter 'content' is required for action 'store'")
	}

	dataPath := filepath.Join(artifactsDir, cleanName)
	metaPath := filepath.Join(artifactsDir, cleanName+".meta.json")

	now := time.Now().UTC()
	meta := ArtifactMeta{
		Name:      cleanName,
		Size:      int64(len(content)),
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  make(map[string]string),
	}

	// If previously existing, retain CreatedAt
	if existingBytes, err := os.ReadFile(metaPath); err == nil {
		var oldMeta ArtifactMeta
		if err := json.Unmarshal(existingBytes, &oldMeta); err == nil && !oldMeta.CreatedAt.IsZero() {
			meta.CreatedAt = oldMeta.CreatedAt
		}
	}

	if metaMap, ok := args["metadata"].(map[string]any); ok {
		for k, v := range metaMap {
			meta.Metadata[k] = fmt.Sprintf("%v", v)
		}
	}

	if err := os.WriteFile(dataPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to store artifact data: %w", err)
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize artifact metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, metaBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to store artifact metadata: %w", err)
	}

	return map[string]any{
		"status":     "stored",
		"name":       cleanName,
		"size":       meta.Size,
		"created_at": meta.CreatedAt.Format(time.RFC3339),
		"updated_at": meta.UpdatedAt.Format(time.RFC3339),
		"metadata":   meta.Metadata,
	}, nil
}

func executeArtifactGet(artifactsDir string, args map[string]any) (any, error) {
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, errors.New("parameter 'name' is required for action 'get'")
	}
	cleanName := filepath.Base(strings.TrimSpace(name))
	dataPath := filepath.Join(artifactsDir, cleanName)
	metaPath := filepath.Join(artifactsDir, cleanName+".meta.json")

	contentBytes, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, fmt.Errorf("artifact '%s' not found: %w", cleanName, err)
	}

	var meta ArtifactMeta
	if metaBytes, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(metaBytes, &meta)
	} else {
		fi, _ := os.Stat(dataPath)
		meta = ArtifactMeta{
			Name:      cleanName,
			Size:      int64(len(contentBytes)),
			UpdatedAt: fi.ModTime().UTC(),
		}
	}

	return map[string]any{
		"name":       cleanName,
		"content":    string(contentBytes),
		"size":       len(contentBytes),
		"created_at": meta.CreatedAt.Format(time.RFC3339),
		"updated_at": meta.UpdatedAt.Format(time.RFC3339),
		"metadata":   meta.Metadata,
	}, nil
}

func executeArtifactList(artifactsDir string) (any, error) {
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read artifacts directory: %w", err)
	}

	var artifacts []ArtifactMeta
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		cleanName := entry.Name()
		dataPath := filepath.Join(artifactsDir, cleanName)
		metaPath := filepath.Join(artifactsDir, cleanName+".meta.json")

		var meta ArtifactMeta
		if metaBytes, err := os.ReadFile(metaPath); err == nil {
			_ = json.Unmarshal(metaBytes, &meta)
		}
		if meta.Name == "" {
			fi, _ := os.Stat(dataPath)
			meta.Name = cleanName
			if fi != nil {
				meta.Size = fi.Size()
				meta.UpdatedAt = fi.ModTime().UTC()
			}
		}
		artifacts = append(artifacts, meta)
	}
	return map[string]any{
		"artifacts": artifacts,
		"count":     len(artifacts),
	}, nil
}

func executeArtifactDelete(artifactsDir string, args map[string]any) (any, error) {
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, errors.New("parameter 'name' is required for action 'delete'")
	}
	cleanName := filepath.Base(strings.TrimSpace(name))
	dataPath := filepath.Join(artifactsDir, cleanName)
	metaPath := filepath.Join(artifactsDir, cleanName+".meta.json")

	_ = os.Remove(dataPath)
	_ = os.Remove(metaPath)

	return map[string]any{
		"status": "deleted",
		"name":   cleanName,
	}, nil
}
