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

	// Build GenAI Tools declarations
	var funcDecls []*genai.FunctionDeclaration
	for _, t := range toolsToUse {
		schema := &genai.Schema{
			Type: genai.TypeObject,
		}
		if propMap, ok := t.Parameters["properties"].(map[string]any); ok {
			schema.Properties = make(map[string]*genai.Schema)
			for propName, propDef := range propMap {
				pDefMap, _ := propDef.(map[string]any)
				propType, _ := pDefMap["type"].(string)
				propDesc, _ := pDefMap["description"].(string)

				var gType genai.Type
				switch propType {
				case "string":
					gType = genai.TypeString
				case "integer", "number":
					gType = genai.TypeInteger
				case "boolean":
					gType = genai.TypeBoolean
				default:
					gType = genai.TypeString
				}

				schema.Properties[propName] = &genai.Schema{
					Type:        gType,
					Description: propDesc,
				}
			}
		}

		funcDecls = append(funcDecls, &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schema,
		})
	}

	genConfig := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				{Text: sysPrompt},
			},
		},
		Tools: []*genai.Tool{
			{FunctionDeclarations: funcDecls},
		},
	}

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
		resp, err := client.Models.GenerateContent(ctx, model, history, genConfig)
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
		type geminiExecResult struct {
			fc        *genai.FunctionCall
			argsBytes []byte
			resBytes  []byte
			resMap    map[string]any
		}

		results := make([]geminiExecResult, len(functionCalls))
		var wg sync.WaitGroup
		wg.Add(len(functionCalls))

		for i, fc := range functionCalls {
			go func(idx int, call *genai.FunctionCall) {
				defer wg.Done()

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

				results[idx] = geminiExecResult{
					fc:        call,
					argsBytes: argsBytes,
					resBytes:  resBytes,
					resMap:    resMap,
				}
			}(i, fc)
		}
		wg.Wait()

		var responseParts []*genai.Part
		for _, r := range results {
			toolsCalled = append(toolsCalled, ToolCall{
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
