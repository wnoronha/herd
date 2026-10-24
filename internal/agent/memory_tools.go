package agent

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/adk/v2/memory"
)

// buildMemoryRecallTool creates the memory_recall thin Herd wrapper over the
// ADK memory service. It reads prior chat sessions via SearchMemory and never
// writes to KV gossip; KV keeps only shared cluster facts.
func buildMemoryRecallTool(eng *Engine) ToolDefinition {
	return ToolDefinition{
		Name:        "memory_recall",
		Description: "Recall relevant prior chat sessions from ADK long-term memory (read-only; KV gossip keeps only shared cluster facts).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural-language query to search prior sessions",
				},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			query, _ := args["query"].(string)
			if strings.TrimSpace(query) == "" {
				return nil, errors.New("parameter 'query' is required")
			}
			if eng == nil {
				return map[string]any{"memories": []any{}}, nil
			}
			svc := eng.memoryService()
			if svc == nil {
				return map[string]any{"memories": []any{}}, nil
			}
			resp, err := svc.SearchMemory(ctx, &memory.SearchRequest{
				Query:   query,
				UserID:  herdADKUserID,
				AppName: herdADKAppName,
			})
			if err != nil {
				return nil, err
			}
			if resp == nil || len(resp.Memories) == 0 {
				return map[string]any{"memories": []any{}, "count": 0}, nil
			}
			var out []map[string]any
			for _, m := range resp.Memories {
				text := ""
				if m.Content != nil {
					var parts []string
					for _, p := range m.Content.Parts {
						if p != nil && p.Text != "" {
							parts = append(parts, p.Text)
						}
					}
					text = strings.Join(parts, "\n")
				}
				out = append(out, map[string]any{
					"id":        m.ID,
					"author":    m.Author,
					"content":   text,
					"timestamp": m.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
				})
			}
			return map[string]any{"memories": out, "count": len(out)}, nil
		},
	}
}
