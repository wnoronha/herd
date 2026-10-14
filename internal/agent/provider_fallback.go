package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (e *Engine) executeDeterministicFallback(ctx context.Context, req *PromptRequest, allowedTools []string, start time.Time, ollamaHint string) (*PromptResponse, error) {
	// Deterministic coordinator parser when no API key is provided
	toolsToUse := e.getEffectiveTools(allowedTools)
	lowerPrompt := strings.ToLower(req.Prompt)

	if tool, ok := toolsToUse["cluster_roster"]; ok {
		if tc, resp, handled := tryFallbackClusterRoster(ctx, tool, lowerPrompt); handled {
			return e.buildFallbackResponse(resp, tc, start), nil
		}
	}
	if tool, ok := toolsToUse["cluster_exec"]; ok {
		if tc, resp, handled := e.tryFallbackClusterExec(ctx, tool, lowerPrompt); handled {
			return e.buildFallbackResponse(resp, tc, start), nil
		}
	}
	if tool, ok := toolsToUse["cluster_cp"]; ok {
		if tc, resp, handled := tryFallbackClusterCP(ctx, tool, req.Prompt); handled {
			return e.buildFallbackResponse(resp, tc, start), nil
		}
	}
	if tool, ok := toolsToUse["google_search"]; ok {
		if tc, resp, handled := tryFallbackGoogleSearch(ctx, tool, lowerPrompt); handled {
			return e.buildFallbackResponse(resp, tc, start), nil
		}
	}
	if tool, ok := toolsToUse["fetch_url"]; ok {
		if tc, resp, handled := tryFallbackFetchURL(ctx, tool, req.Prompt); handled {
			return e.buildFallbackResponse(resp, tc, start), nil
		}
	}

	note := "Note: Set OPENAI_API_KEY, OPENAI_BASE_URL, or GEMINI_API_KEY for cloud LLMs, or run a local model with Ollama (ollama pull llama3.2)."
	if strings.TrimSpace(ollamaHint) != "" {
		note = "Ollama auto-probe: " + ollamaHint
	}
	defaultResp := fmt.Sprintf(
		"Herd Node Agent ('%s') received task: %q\n\n%s",
		e.config.NodeName, req.Prompt, note,
	)

	return &PromptResponse{
		Response:    defaultResp,
		ModelUsed:   "herd-builtin-coordinator",
		ExecutionMs: time.Since(start).Milliseconds(),
	}, nil
}

func (e *Engine) buildFallbackResponse(responseText string, tc *ToolCall, start time.Time) *PromptResponse {
	var toolsCalled []ToolCall
	if tc != nil {
		toolsCalled = append(toolsCalled, *tc)
	}
	return &PromptResponse{
		Response:    responseText,
		ToolsCalled: toolsCalled,
		ModelUsed:   "herd-builtin-coordinator",
		ExecutionMs: time.Since(start).Milliseconds(),
	}
}

func tryFallbackClusterRoster(ctx context.Context, tool ToolDefinition, lowerPrompt string) (*ToolCall, string, bool) {
	if !strings.Contains(lowerPrompt, "roster") && !strings.Contains(lowerPrompt, "nodes") && !strings.Contains(lowerPrompt, "cluster") {
		return nil, "", false
	}
	res, err := tool.Handler(ctx, map[string]any{"filter_status": "all"})
	if err != nil {
		return nil, "", false
	}
	resBytes, _ := json.MarshalIndent(res, "", "  ")
	tc := &ToolCall{
		Name:      "cluster_roster",
		Arguments: `{"filter_status": "all"}`,
		Result:    string(resBytes),
	}
	resp := fmt.Sprintf("Cluster Roster:\n```json\n%s\n```", string(resBytes))
	return tc, resp, true
}

func (e *Engine) tryFallbackClusterExec(ctx context.Context, tool ToolDefinition, lowerPrompt string) (*ToolCall, string, bool) {
	if !strings.Contains(lowerPrompt, "uptime") && !strings.Contains(lowerPrompt, "uname") && !strings.Contains(lowerPrompt, "exec") {
		return nil, "", false
	}
	target := "all"
	cmd := "uptime"
	if strings.Contains(lowerPrompt, "uname") {
		cmd = "uname -a"
	}
	// Enforce the same cluster policy as the LLM tool loop.
	if policyErr := e.validateExecPolicy(cmd); policyErr != nil {
		return nil, "", false
	}
	res, err := tool.Handler(ctx, map[string]any{"target": target, "command": cmd})
	if err != nil {
		return nil, "", false
	}
	resBytes, _ := json.MarshalIndent(res, "", "  ")
	tc := &ToolCall{
		Name:      "cluster_exec",
		Arguments: fmt.Sprintf(`{"target": "%s", "command": "%s"}`, target, cmd),
		Result:    string(resBytes),
	}
	resp := fmt.Sprintf("Executed '%s' on %s:\n```json\n%s\n```", cmd, target, string(resBytes))
	return tc, resp, true
}

func tryFallbackClusterCP(ctx context.Context, tool ToolDefinition, prompt string) (*ToolCall, string, bool) {
	lowerPrompt := strings.ToLower(prompt)
	if !strings.Contains(lowerPrompt, "copy") && !strings.Contains(lowerPrompt, " cp ") && !strings.HasPrefix(lowerPrompt, "cp ") {
		return nil, "", false
	}
	parts := strings.Fields(prompt)
	var src, dst string
	for i, p := range parts {
		if (strings.EqualFold(p, "cp") || strings.EqualFold(p, "copy")) && i+2 < len(parts) {
			src = parts[i+1]
			dst = parts[i+2]
			break
		}
	}
	if src == "" || dst == "" {
		return nil, "", false
	}
	res, err := tool.Handler(ctx, map[string]any{"source": src, "destination": dst})
	if err != nil {
		return nil, "", false
	}
	resBytes, _ := json.MarshalIndent(res, "", "  ")
	tc := &ToolCall{
		Name:      "cluster_cp",
		Arguments: fmt.Sprintf(`{"source": "%s", "destination": "%s"}`, src, dst),
		Result:    string(resBytes),
	}
	resp := fmt.Sprintf("Transferred file from %s to %s:\n```json\n%s\n```", src, dst, string(resBytes))
	return tc, resp, true
}

func tryFallbackGoogleSearch(ctx context.Context, tool ToolDefinition, lowerPrompt string) (*ToolCall, string, bool) {
	if !strings.Contains(lowerPrompt, "search") {
		return nil, "", false
	}
	query := strings.TrimPrefix(lowerPrompt, "search for ")
	query = strings.TrimPrefix(query, "search ")
	query = strings.TrimPrefix(query, "google ")
	res, err := tool.Handler(ctx, map[string]any{"query": query})
	if err != nil {
		return nil, "", false
	}
	resBytes, _ := json.MarshalIndent(res, "", "  ")
	tc := &ToolCall{
		Name:      "google_search",
		Arguments: fmt.Sprintf(`{"query": "%s"}`, query),
		Result:    string(resBytes),
	}
	resp := fmt.Sprintf("Search results for %q:\n```json\n%s\n```", query, string(resBytes))
	return tc, resp, true
}

func tryFallbackFetchURL(ctx context.Context, tool ToolDefinition, prompt string) (*ToolCall, string, bool) {
	lowerPrompt := strings.ToLower(prompt)
	if !strings.Contains(lowerPrompt, "fetch") && !strings.Contains(lowerPrompt, "http://") && !strings.Contains(lowerPrompt, "https://") {
		return nil, "", false
	}
	words := strings.Fields(prompt)
	var targetURL string
	for _, w := range words {
		if strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
			targetURL = w
			break
		}
	}
	if targetURL == "" {
		return nil, "", false
	}
	res, err := tool.Handler(ctx, map[string]any{"url": targetURL})
	if err != nil {
		return nil, "", false
	}
	resBytes, _ := json.MarshalIndent(res, "", "  ")
	tc := &ToolCall{
		Name:      "fetch_url",
		Arguments: fmt.Sprintf(`{"url": "%s"}`, targetURL),
		Result:    string(resBytes),
	}
	resp := fmt.Sprintf("Fetched content from %s:\n```json\n%s\n```", targetURL, string(resBytes))
	return tc, resp, true
}
