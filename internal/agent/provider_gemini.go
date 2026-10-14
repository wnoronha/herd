package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"google.golang.org/genai"
)

func (e *Engine) executeWithGemini(ctx context.Context, req *PromptRequest, cfg Config, allowedTools []string, start time.Time) (*PromptResponse, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create GenAI client: %w", err)
	}

	model := cfg.Model
	if model == "" {
		model = "gemini-3.8-flash"
	}

	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = fmt.Sprintf(
			"You are Herd Agent for node '%s'. You are part of a decentralized P2P mesh cluster. "+
				"Use your cluster and research tools (cluster_roster, cluster_exec, cluster_cp, shared_kv, agent_send_mail, agent_broadcast, agent_read_mailbox, fetch_url, google_search, dns_lookup) "+
				"to discover nodes, execute actions, transfer files, search the web, fetch web content, and coordinate tasks across machines.",
			e.config.NodeName,
		)
	}
	if req.TargetNode != "" {
		sysPrompt += fmt.Sprintf("\nThe user targeted node '%s' for this task. Ensure your operations (such as cluster_exec or cluster_cp) target '%s'.", req.TargetNode, req.TargetNode)
	}

	toolsToUse := e.getEffectiveTools(allowedTools)
	funcDecls := buildGeminiFuncDecls(toolsToUse)

	var toolsCalled []ToolCall
	history := []*genai.Content{
		{
			Role: "user",
			Parts: []*genai.Part{
				{Text: req.Prompt},
			},
		},
	}

	// Tool execution loop (max 5 turns)
	var finalResponse string
	for turn := 0; turn < 5; turn++ {
		resp, err := client.Models.GenerateContent(ctx, model, history, geminiGenConfig(sysPrompt, funcDecls))
		if err != nil {
			return nil, fmt.Errorf("gemini generation failed: %w", err)
		}

		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			break
		}

		candidateContent := resp.Candidates[0].Content
		history = append(history, candidateContent)

		var functionCalls []*genai.FunctionCall
		for _, part := range candidateContent.Parts {
			if part.FunctionCall != nil {
				functionCalls = append(functionCalls, part.FunctionCall)
			}
			if part.Text != "" {
				finalResponse += part.Text
			}
		}

		if len(functionCalls) == 0 {
			break
		}

		// Execute function calls in parallel while preserving order
		responseParts, calls := e.runGeminiToolCalls(ctx, toolsToUse, functionCalls)
		toolsCalled = append(toolsCalled, calls...)

		history = append(history, &genai.Content{
			Role:  "user",
			Parts: responseParts,
		})
	}

	return &PromptResponse{
		Response:    finalResponse,
		ToolsCalled: toolsCalled,
		ModelUsed:   model,
		ExecutionMs: time.Since(start).Milliseconds(),
	}, nil
}

// geminiExecResult carries one ordered tool-call outcome.
type geminiExecResult struct {
	fc        *genai.FunctionCall
	argsBytes []byte
	resBytes  []byte
	resMap    map[string]any
}

// runGeminiToolCalls executes function calls in parallel while preserving order.
func (e *Engine) runGeminiToolCalls(ctx context.Context, toolsToUse map[string]ToolDefinition, functionCalls []*genai.FunctionCall) ([]*genai.Part, []ToolCall) {
	results := make([]geminiExecResult, len(functionCalls))
	var wg sync.WaitGroup
	wg.Add(len(functionCalls))

	for i, fc := range functionCalls {
		go func(idx int, call *genai.FunctionCall) {
			defer wg.Done()
			results[idx] = e.execGeminiToolCall(ctx, toolsToUse, call)
		}(i, fc)
	}
	wg.Wait()

	var responseParts []*genai.Part
	var calls []ToolCall
	for _, r := range results {
		calls = append(calls, ToolCall{
			Name:      r.fc.Name,
			Arguments: string(r.argsBytes),
			Result:    string(r.resBytes),
		})

		responseParts = append(responseParts, &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				Name:     r.fc.Name,
				Response: r.resMap,
			},
		})
	}
	return responseParts, calls
}

// execGeminiToolCall runs one Gemini function call with policy enforcement.
func (e *Engine) execGeminiToolCall(ctx context.Context, toolsToUse map[string]ToolDefinition, call *genai.FunctionCall) geminiExecResult {
	tool, exists := toolsToUse[call.Name]
	var toolResult any
	var execErr error

	if !exists {
		toolResult = map[string]any{"error": fmt.Sprintf("tool '%s' is not allowed or registered for this agent", call.Name)}
	} else {
		args := make(map[string]any)
		for k, v := range call.Args {
			args[k] = v
		}
		// Check agent:policy:allowed_exec security policy for cluster_exec
		if call.Name == "cluster_exec" {
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

	argsBytes, _ := json.Marshal(call.Args)
	resBytes, _ := json.Marshal(toolResult)

	var resMap map[string]any
	if err := json.Unmarshal(resBytes, &resMap); err != nil {
		resMap = map[string]any{"result": string(resBytes)}
	}

	return geminiExecResult{
		fc:        call,
		argsBytes: argsBytes,
		resBytes:  resBytes,
		resMap:    resMap,
	}
}

// buildGeminiFuncDecls converts tool definitions to GenAI function declarations.
func buildGeminiFuncDecls(toolsToUse map[string]ToolDefinition) []*genai.FunctionDeclaration {
	var funcDecls []*genai.FunctionDeclaration
	for _, t := range toolsToUse {
		funcDecls = append(funcDecls, &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  geminiToolSchema(t),
		})
	}
	return funcDecls
}

// geminiToolSchema converts a tool's JSON-schema properties to a GenAI schema.
func geminiToolSchema(t ToolDefinition) *genai.Schema {
	schema := &genai.Schema{Type: genai.TypeObject}
	propMap, ok := t.Parameters["properties"].(map[string]any)
	if !ok {
		return schema
	}
	schema.Properties = make(map[string]*genai.Schema)
	for propName, propDef := range propMap {
		pDefMap, _ := propDef.(map[string]any)
		propType, _ := pDefMap["type"].(string)
		propDesc, _ := pDefMap["description"].(string)
		schema.Properties[propName] = &genai.Schema{
			Type:        geminiSchemaType(propType),
			Description: propDesc,
		}
	}
	return schema
}

// geminiSchemaType maps JSON-schema type names to GenAI types.
func geminiSchemaType(propType string) genai.Type {
	switch propType {
	case "integer", "number":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	default:
		return genai.TypeString
	}
}

// geminiGenConfig builds the generation config for one reasoning turn.
func geminiGenConfig(sysPrompt string, funcDecls []*genai.FunctionDeclaration) *genai.GenerateContentConfig {
	return &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				{Text: sysPrompt},
			},
		},
		Tools: []*genai.Tool{
			{FunctionDeclarations: funcDecls},
		},
	}
}
