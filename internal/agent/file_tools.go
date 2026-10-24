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
			if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec // standard workspace-dir permissions for file output
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

			f, err := os.OpenFile(filePath, flags, 0644) //nolint:gosec // agent workspace files are group-readable by design (user-owned, not secrets)
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
	Name      string            `json:"name"`
	Size      int64             `json:"size"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
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

// artifactSharedSession scopes ADK artifact entries so they are visible
// across chat sessions (ADK artifacts are session-scoped by default).
const artifactSharedSession = "shared"

// buildArtifactStoreTool creates the artifact_store tool as a thin Herd wrapper
// over the ADK artifact service with versioned entries. Disk files remain as a
// wrapped export fallback; the ADK service is the versioned source of truth.
func buildArtifactStoreTool(cctx ClusterContext, eng ...*Engine) ToolDefinition {
	var engine *Engine
	if len(eng) > 0 {
		engine = eng[0]
	}
	return ToolDefinition{
		Name:        "artifact_store",
		Description: "Store, retrieve, list, or delete versioned multi-turn execution artifacts via the ADK artifact service (disk export wrapped as fallback).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Artifact action: 'store', 'get', 'list', 'delete', or 'versions'",
					"enum":        []string{"store", "get", "list", "delete", "versions"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Artifact identifier/filename (required for store, get, delete, versions)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Artifact content string (required for action='store')",
				},
				"version": map[string]any{
					"type":        "number",
					"description": "Optional artifact version for get (default: latest)",
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
			if err := os.MkdirAll(artifactsDir, 0755); err != nil { //nolint:gosec // standard artifact-store permissions
				return nil, fmt.Errorf("failed to create artifacts directory '%s': %w", artifactsDir, err)
			}

			switch action {
			case "store":
				return executeArtifactStore(ctx, engine, artifactsDir, args)
			case "get":
				return executeArtifactGet(ctx, engine, artifactsDir, args)
			case "list":
				return executeArtifactList(ctx, engine, artifactsDir)
			case "delete":
				return executeArtifactDelete(ctx, engine, artifactsDir, args)
			case "versions":
				return executeArtifactVersions(ctx, engine, args)
			default:
				return nil, fmt.Errorf("unsupported action '%s', expected 'store', 'get', 'list', 'delete', or 'versions'", action)
			}
		},
	}
}

func executeArtifactStore(ctx context.Context, eng *Engine, artifactsDir string, args map[string]any) (any, error) {
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
	if existingBytes, err := os.ReadFile(metaPath); err == nil { //nolint:gosec // metaPath is Join(artifactsDir, filepath.Base(name)); traversal neutralized
		var oldMeta ArtifactMeta
		if err := json.Unmarshal(existingBytes, &oldMeta); err == nil && !oldMeta.CreatedAt.IsZero() {
			meta.CreatedAt = oldMeta.CreatedAt
		}
	}

	customMeta := map[string]any{}
	if metaMap, ok := args["metadata"].(map[string]any); ok {
		for k, v := range metaMap {
			meta.Metadata[k] = fmt.Sprintf("%v", v)
			customMeta[k] = fmt.Sprintf("%v", v)
		}
	}

	if err := os.WriteFile(dataPath, []byte(content), 0644); err != nil { //nolint:gosec // artifact store is group-readable by design
		return nil, fmt.Errorf("failed to store artifact data: %w", err)
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize artifact metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, metaBytes, 0644); err != nil { //nolint:gosec // artifact metadata is group-readable by design
		return nil, fmt.Errorf("failed to store artifact metadata: %w", err)
	}

	// Thin-layer versioning: also save to the ADK artifact service so list
	// and versions report versioned entries. Disk stays as export fallback.
	var version int64
	if eng != nil {
		version = saveArtifactToADK(ctx, eng, cleanName, content, customMeta)
	}

	return map[string]any{
		"status":     "stored",
		"name":       cleanName,
		"size":       meta.Size,
		"version":    version,
		"created_at": meta.CreatedAt.Format(time.RFC3339),
		"updated_at": meta.UpdatedAt.Format(time.RFC3339),
		"metadata":   meta.Metadata,
	}, nil
}

func executeArtifactGet(ctx context.Context, eng *Engine, artifactsDir string, args map[string]any) (any, error) {
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, errors.New("parameter 'name' is required for action 'get'")
	}
	cleanName := filepath.Base(strings.TrimSpace(name))
	var wantVersion int64
	if v, ok := args["version"].(float64); ok && v > 0 {
		wantVersion = int64(v)
	}
	// Prefer the ADK versioned service; fall back to disk export.
	if eng != nil {
		if content, version, found := loadArtifactFromADK(ctx, eng, cleanName, wantVersion); found {
			return map[string]any{
				"name":    cleanName,
				"content": content,
				"size":    len(content),
				"version": version,
			}, nil
		}
	}
	dataPath := filepath.Join(artifactsDir, cleanName)
	metaPath := filepath.Join(artifactsDir, cleanName+".meta.json")

	contentBytes, err := os.ReadFile(dataPath) //nolint:gosec // dataPath is Join(artifactsDir, filepath.Base(name)); traversal neutralized
	if err != nil {
		return nil, fmt.Errorf("artifact '%s' not found: %w", cleanName, err)
	}

	var meta ArtifactMeta
	if metaBytes, err := os.ReadFile(metaPath); err == nil { //nolint:gosec // metaPath is Join(artifactsDir, filepath.Base(name)); traversal neutralized
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

func executeArtifactList(ctx context.Context, eng *Engine, artifactsDir string) (any, error) {
	entries, err := os.ReadDir(artifactsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read artifacts directory: %w", err)
	}

	var artifacts []ArtifactMeta
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		cleanName := entry.Name()
		dataPath := filepath.Join(artifactsDir, cleanName)
		metaPath := filepath.Join(artifactsDir, cleanName+".meta.json")

		var meta ArtifactMeta
		if metaBytes, err := os.ReadFile(metaPath); err == nil { //nolint:gosec // metaPath is Join(artifactsDir, filepath.Base(name)); traversal neutralized
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
		seen[cleanName] = true
	}
	// Merge ADK versioned entries so list reports versioned artifacts even
	// when the disk export is missing.
	var versioned []adkVersionedEntry
	for _, a := range artifacts {
		versioned = append(versioned, adkVersionedEntry{ArtifactMeta: a})
	}
	if eng != nil {
		for _, ve := range listArtifactsFromADK(ctx, eng, seen) {
			versioned = append(versioned, ve)
		}
	}
	return map[string]any{
		"artifacts": versioned,
		"count":     len(versioned),
	}, nil
}

func executeArtifactDelete(ctx context.Context, eng *Engine, artifactsDir string, args map[string]any) (any, error) {
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, errors.New("parameter 'name' is required for action 'delete'")
	}
	cleanName := filepath.Base(strings.TrimSpace(name))
	dataPath := filepath.Join(artifactsDir, cleanName)
	metaPath := filepath.Join(artifactsDir, cleanName+".meta.json")

	_ = os.Remove(dataPath)
	_ = os.Remove(metaPath)
	deleteArtifactFromADK(ctx, eng, cleanName)

	return map[string]any{
		"status": "deleted",
		"name":   cleanName,
	}, nil
}

func executeArtifactVersions(ctx context.Context, eng *Engine, args map[string]any) (any, error) {
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, errors.New("parameter 'name' is required for action 'versions'")
	}
	cleanName := filepath.Base(strings.TrimSpace(name))
	if eng == nil {
		return map[string]any{"name": cleanName, "versions": []any{}, "count": 0}, nil
	}
	return versionsArtifactFromADK(ctx, eng, cleanName)
}
