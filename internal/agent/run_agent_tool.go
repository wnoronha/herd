package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// AgentRunner executes task prompts using configured agent definitions.
type AgentRunner interface {
	ExecutePrompt(ctx context.Context, req *PromptRequest) (*PromptResponse, error)
	ListAgents() []*AgentDefinition
}

// buildRunAgentTool creates the generic run_agent tool following Google ADK's AgentTool pattern.
func buildRunAgentTool(runner AgentRunner) ToolDefinition {
	return ToolDefinition{
		Name:        "run_agent",
		Description: "Execute an isolated prompt or delegated sub-task against any configured agent persona or model, returning its response.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"description": "Name or role of the agent persona to invoke (e.g. 'coordinator', 'ops', 'researcher', or custom persona)",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The task prompt or question to evaluate",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "Optional background context, data, or state to supply alongside the prompt",
				},
			},
			"required": []string{"agent", "prompt"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			if runner == nil {
				return nil, errors.New("agent runner is not available")
			}

			agentName, ok := args["agent"].(string)
			if !ok || strings.TrimSpace(agentName) == "" {
				return nil, errors.New("parameter 'agent' is required")
			}
			agentName = strings.TrimSpace(agentName)

			promptVal, ok := args["prompt"].(string)
			if !ok || strings.TrimSpace(promptVal) == "" {
				return nil, errors.New("parameter 'prompt' is required")
			}
			promptVal = strings.TrimSpace(promptVal)

			fullPrompt := promptVal
			if ctxVal, ok := args["context"].(string); ok && strings.TrimSpace(ctxVal) != "" {
				fullPrompt = fmt.Sprintf("Context:\n%s\n\nTask:\n%s", strings.TrimSpace(ctxVal), promptVal)
			}

			resp, err := runner.ExecutePrompt(ctx, &PromptRequest{
				Role:   agentName,
				Prompt: fullPrompt,
			})
			if err != nil {
				return nil, fmt.Errorf("failed executing agent '%s': %w", agentName, err)
			}

			return map[string]any{
				"agent":        agentName,
				"prompt":       promptVal,
				"response":     resp.Response,
				"model_used":   resp.ModelUsed,
				"execution_ms": resp.ExecutionMs,
				"tools_called": resp.ToolsCalled,
			}, nil
		},
	}
}
