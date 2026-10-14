package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (e *Engine) executeWithOllama(ctx context.Context, req *PromptRequest, cfg Config, start time.Time) (*PromptResponse, error) {
	// Simple Ollama client fallback
	url := fmt.Sprintf("%s/api/chat", strings.TrimRight(cfg.OllamaHost, "/"))
	model := cfg.Model
	if model == "" || strings.HasPrefix(model, "gemini-") {
		model = "gemma2:2b"
	}

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": fmt.Sprintf("You are Herd Agent for node '%s'.", e.config.NodeName)},
			{"role": "user", "content": req.Prompt},
		},
		"stream": false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to reach Ollama at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Ollama response: %w", err)
	}

	return &PromptResponse{
		Response:    result.Message.Content,
		ModelUsed:   model,
		ExecutionMs: time.Since(start).Milliseconds(),
	}, nil
}
