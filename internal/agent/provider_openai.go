package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (e *Engine) executeWithOpenAI(ctx context.Context, req *PromptRequest, cfg Config, allowedTools []string, start time.Time) (*PromptResponse, error) {
	baseURL := strings.TrimRight(cfg.OpenAIBaseURL, "/")
	endpoint := fmt.Sprintf("%s/chat/completions", baseURL)

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf(
			"You are Herd Agent for node '%s'. You are part of a decentralized P2P mesh cluster. "+
				"Use your cluster and research tools (cluster_roster, cluster_exec, cluster_cp, shared_kv, agent_send_mail, agent_broadcast, agent_read_mailbox, fetch_url, google_search, dns_lookup) "+
				"to discover nodes, execute actions, transfer files, search the web, fetch web content, and coordinate tasks across machines.",
			cfg.NodeName,
		)
	}
	if req.TargetNode != "" {
		systemPrompt += fmt.Sprintf("\nThe user targeted node '%s' for this task. Ensure your operations (such as cluster_exec or cluster_cp) target '%s'.", req.TargetNode, req.TargetNode)
	}

	toolsToUse := e.getEffectiveTools(allowedTools)

	// Build OpenAI tools array
	var openAITools []OpenAITool
	for _, t := range toolsToUse {
		openAITools = append(openAITools, OpenAITool{
			Type: "function",
			Function: OpenAIFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	messages := []OpenAIChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	for _, h := range req.History {
		messages = append(messages, OpenAIChatMessage{
			Role:    h.Role,
			Content: h.Content,
			Name:    h.Name,
		})
	}
	messages = append(messages, OpenAIChatMessage{
		Role:    "user",
		Content: req.Prompt,
	})

	var toolsCalled []ToolCall
	var finalResponse string
	model := cfg.OpenAIModel
	if model == "" {
		model = "gpt-4o-mini"
	}

	client := &http.Client{Timeout: 90 * time.Second}

	for turn := 0; turn < 8; turn++ {
		chatReq := OpenAIChatRequest{
			Model:       model,
			Messages:    messages,
			Tools:       openAITools,
			Temperature: 0.2,
		}

		reqBody, err := json.Marshal(chatReq)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal OpenAI request: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("failed to build OpenAI HTTP request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if cfg.OpenAIAPIKey != "" {
			httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.OpenAIAPIKey))
		}

		httpResp, err := client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("failed to reach OpenAI endpoint at %s: %w", endpoint, err)
		}

		if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
			errBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4<<10))
			_ = httpResp.Body.Close()
			return nil, fmt.Errorf("OpenAI endpoint %s returned HTTP %d: %s", endpoint, httpResp.StatusCode, strings.TrimSpace(string(errBody)))
		}

		var chatResp OpenAIChatResponse
		decodeErr := json.NewDecoder(httpResp.Body).Decode(&chatResp)
		_ = httpResp.Body.Close()

		if decodeErr != nil {
			return nil, fmt.Errorf("failed to decode OpenAI response: %w", decodeErr)
		}

		if chatResp.Error != nil && chatResp.Error.Message != "" {
			return nil, fmt.Errorf("OpenAI API error: %s", chatResp.Error.Message)
		}

		if len(chatResp.Choices) == 0 {
			break
		}

		choice := chatResp.Choices[0]
		assistantMsg := choice.Message

		if assistantMsg.Content != "" {
			finalResponse += assistantMsg.Content
		}

		// If no tool calls, reasoning loop finished
		if len(assistantMsg.ToolCalls) == 0 {
			break
		}

		// Append assistant tool request to message history
		messages = append(messages, OpenAIChatMessage{
			Role:      "assistant",
			Content:   assistantMsg.Content,
			ToolCalls: assistantMsg.ToolCalls,
		})

		// Dispatch and execute tool calls in parallel while preserving order
		for _, r := range e.runOpenAIToolCalls(ctx, toolsToUse, assistantMsg.ToolCalls) {
			toolsCalled = append(toolsCalled, ToolCall{
				Name:      r.tc.Function.Name,
				Arguments: r.tc.Function.Arguments,
				Result:    string(r.resBytes),
			})

			// Add tool response to conversation
			messages = append(messages, OpenAIChatMessage{
				Role:       "tool",
				Name:       r.tc.Function.Name,
				ToolCallID: r.tc.ID,
				Content:    string(r.resBytes),
			})
		}
	}

	return &PromptResponse{
		Response:    finalResponse,
		ToolsCalled: toolsCalled,
		ModelUsed:   model,
		ExecutionMs: time.Since(start).Milliseconds(),
	}, nil
}

// openAIExecResult carries one ordered tool-call outcome.
type openAIExecResult struct {
	tc       OpenAIToolCall
	resBytes []byte
}

// runOpenAIToolCalls executes tool calls in parallel while preserving order.
func (e *Engine) runOpenAIToolCalls(ctx context.Context, toolsToUse map[string]ToolDefinition, calls []OpenAIToolCall) []openAIExecResult {
	results := make([]openAIExecResult, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i, tc := range calls {
		go func(idx int, call OpenAIToolCall) {
			defer wg.Done()
			results[idx] = openAIExecResult{
				tc:       call,
				resBytes: e.execOpenAIToolCall(ctx, toolsToUse, call),
			}
		}(i, tc)
	}
	wg.Wait()
	return results
}

// execOpenAIToolCall runs one OpenAI tool call with policy enforcement.
func (e *Engine) execOpenAIToolCall(ctx context.Context, toolsToUse map[string]ToolDefinition, call OpenAIToolCall) []byte {
	var args map[string]any
	if call.Function.Arguments != "" {
		_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
	}
	if args == nil {
		args = make(map[string]any)
	}

	var toolResult any
	var execErr error

	tool, exists := toolsToUse[call.Function.Name]
	if !exists {
		toolResult = map[string]any{"error": fmt.Sprintf("tool '%s' is not allowed or registered for this agent", call.Function.Name)}
	} else {
		// Check agent:policy:allowed_exec security policy for cluster_exec
		if call.Function.Name == "cluster_exec" {
			if cmd, ok := args["command"].(string); ok {
				if policyErr := e.validateExecPolicy(cmd); policyErr != nil {
					execErr = policyErr
				}
			}
		}

		if execErr == nil {
			toolResult, execErr = tool.Handler(ctx, args)
		}
		if execErr != nil {
			toolResult = map[string]any{"error": execErr.Error()}
		}
	}

	resBytes, _ := json.Marshal(toolResult)
	return resBytes
}

// validateExecPolicy checks command against agent:policy:allowed_exec whitelist in KV store if configured.
func (e *Engine) validateExecPolicy(command string) error {
	if e.cctx == nil {
		return nil
	}
	kvStore := e.cctx.GetKVStore()
	if kvStore == nil {
		return nil
	}

	entry, ok := kvStore.Get("agent:policy:allowed_exec")
	if !ok || len(entry.Value) == 0 {
		return nil // No whitelist policy set: allow all
	}

	var allowedList []string
	if err := json.Unmarshal(entry.Value, &allowedList); err != nil {
		// Fail closed: a corrupt policy must not silently allow everything.
		return fmt.Errorf("invalid cluster policy (agent:policy:allowed_exec): %w", err)
	}
	if len(allowedList) == 0 {
		return nil
	}

	trimmedCmd := strings.TrimSpace(command)
	for _, pattern := range allowedList {
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(trimmedCmd, prefix) {
				return nil
			}
		} else if strings.EqualFold(trimmedCmd, pattern) {
			return nil
		}
	}

	return fmt.Errorf("command %q rejected by cluster policy (agent:policy:allowed_exec)", command)
}
