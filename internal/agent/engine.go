package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

// Engine orchestrates AI agent prompt processing, tool dispatch, and reasoning loops.
type Engine struct {
	config Config
	tools  map[string]ToolDefinition
	cctx   ClusterContext
	agents map[string]*AgentDefinition
	mu     sync.RWMutex
}

// NewEngine initializes an agent engine instance with the default cluster tools.
func NewEngine(cfg Config, cctx ClusterContext, extraDirs ...string) *Engine {
	if cfg.OpenAIBaseURL == "" {
		if envBase := os.Getenv("OPENAI_BASE_URL"); envBase != "" {
			cfg.OpenAIBaseURL = envBase
		} else if envBase := os.Getenv("OPENAI_API_BASE"); envBase != "" {
			cfg.OpenAIBaseURL = envBase
		} else {
			cfg.OpenAIBaseURL = "http://localhost:11434/v1"
		}
	}
	if cfg.OpenAIAPIKey == "" {
		if envKey := os.Getenv("OPENAI_API_KEY"); envKey != "" {
			cfg.OpenAIAPIKey = envKey
		}
	}
	if cfg.OpenAIModel == "" {
		if envModel := os.Getenv("OPENAI_MODEL"); envModel != "" {
			cfg.OpenAIModel = envModel
		} else {
			cfg.OpenAIModel = "gpt-4o-mini"
		}
	}

	if cfg.APIKey == "" {
		if envKey := os.Getenv("GEMINI_API_KEY"); envKey != "" {
			cfg.APIKey = envKey
		} else if envKey := os.Getenv("GOOGLE_API_KEY"); envKey != "" {
			cfg.APIKey = envKey
		}
	}
	if cfg.Model == "" {
		if envModel := os.Getenv("GEMINI_MODEL"); envModel != "" {
			cfg.Model = envModel
		} else {
			cfg.Model = "gemini-3.8-flash"
		}
	}
	if cfg.OllamaHost == "" {
		cfg.OllamaHost = os.Getenv("OLLAMA_HOST")
		if cfg.OllamaHost == "" {
			cfg.OllamaHost = "http://localhost:11434"
		}
	}

	toolList := BuildDefaultTools(cctx)
	toolsMap := make(map[string]ToolDefinition, len(toolList))
	for _, t := range toolList {
		toolsMap[t.Name] = t
	}

	agentsMap := make(map[string]*AgentDefinition)
	for k, v := range LoadDefaultAgents(extraDirs...) {
		agentsMap[k] = v
	}

	eng := &Engine{
		config: cfg,
		tools:  toolsMap,
		cctx:   cctx,
		agents: agentsMap,
	}
	eng.tools["run_agent"] = buildRunAgentTool(eng)
	return eng
}

// RegisterAgent registers a declarative agent definition in the engine.
func (e *Engine) RegisterAgent(def *AgentDefinition) {
	if def == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.agents == nil {
		e.agents = make(map[string]*AgentDefinition)
	}
	if def.Name != "" {
		e.agents[strings.ToLower(def.Name)] = def
	}
	if def.Role != "" {
		e.agents[strings.ToLower(def.Role)] = def
	}
}

// GetAgent retrieves a registered agent definition by role or name.
func (e *Engine) GetAgent(roleOrName string) (*AgentDefinition, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.agents == nil {
		return nil, false
	}
	def, ok := e.agents[strings.ToLower(roleOrName)]
	return def, ok
}

// ListAgents returns all unique registered agent definitions.
func (e *Engine) ListAgents() []*AgentDefinition {
	e.mu.RLock()
	defer e.mu.RUnlock()
	seen := make(map[*AgentDefinition]bool)
	var list []*AgentDefinition
	for _, a := range e.agents {
		if !seen[a] {
			seen[a] = true
			list = append(list, a)
		}
	}
	return list
}

// getEffectiveTools filters available tools by AllowedTools if specified.
func (e *Engine) getEffectiveTools(allowed []string) map[string]ToolDefinition {
	if len(allowed) == 0 {
		return e.tools
	}
	allowedMap := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedMap[strings.ToLower(strings.TrimSpace(a))] = true
	}
	filtered := make(map[string]ToolDefinition)
	for name, def := range e.tools {
		if allowedMap[strings.ToLower(name)] {
			filtered[name] = def
		}
	}
	return filtered
}

// resolveEffectiveConfig checks KV store for agent:config:global and agent:config:nodes/<node> overrides.
func (e *Engine) resolveEffectiveConfig() Config {
	effective := e.config
	if e.cctx == nil {
		return effective
	}
	kvStore := e.cctx.GetKVStore()
	if kvStore == nil {
		return effective
	}

	// 1. Check global cluster agent config: agent:config:global
	if entry, ok := kvStore.Get("agent:config:global"); ok && len(entry.Value) > 0 {
		var gCfg AgentClusterConfig
		if err := json.Unmarshal(entry.Value, &gCfg); err == nil {
			if gCfg.Provider != "" {
				effective.Provider = gCfg.Provider
			}
			if gCfg.Model != "" {
				effective.OpenAIModel = gCfg.Model
				effective.Model = gCfg.Model
			}
			if gCfg.BaseURL != "" {
				effective.OpenAIBaseURL = gCfg.BaseURL
			}
			if gCfg.APIKey != "" {
				if gCfg.Provider == "gemini" {
					effective.APIKey = gCfg.APIKey
				} else {
					effective.OpenAIAPIKey = gCfg.APIKey
				}
			}
			if gCfg.SystemPrompt != "" {
				effective.SystemPrompt = gCfg.SystemPrompt
			}
		}
	}

	// 2. Check per-node override: agent:config:nodes/<node>
	nodeKey := fmt.Sprintf("agent:config:nodes/%s", effective.NodeName)
	if entry, ok := kvStore.Get(nodeKey); ok && len(entry.Value) > 0 {
		var nCfg AgentClusterConfig
		if err := json.Unmarshal(entry.Value, &nCfg); err == nil {
			if nCfg.Provider != "" {
				effective.Provider = nCfg.Provider
			}
			if nCfg.Model != "" {
				effective.OpenAIModel = nCfg.Model
				effective.Model = nCfg.Model
			}
			if nCfg.BaseURL != "" {
				effective.OpenAIBaseURL = nCfg.BaseURL
			}
			if nCfg.APIKey != "" {
				if nCfg.Provider == "gemini" {
					effective.APIKey = nCfg.APIKey
				} else {
					effective.OpenAIAPIKey = nCfg.APIKey
				}
			}
			if nCfg.SystemPrompt != "" {
				effective.SystemPrompt = nCfg.SystemPrompt
			}
		}
	}

	return effective
}

// ExecutePrompt runs a task prompt through the agent reasoning and tool loop.
func (e *Engine) ExecutePrompt(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
	if req == nil || req.Prompt == "" {
		return nil, errors.New("prompt cannot be empty")
	}

	start := time.Now()
	effCfg := e.resolveEffectiveConfig()

	var allowedTools []string
	if req.Role != "" {
		agentDef, ok := e.GetAgent(req.Role)
		if !ok {
			return nil, fmt.Errorf("agent role or name '%s' not found", req.Role)
		}
		if agentDef.Model != "" {
			effCfg.Model = agentDef.Model
			effCfg.OpenAIModel = agentDef.Model
		}
		if agentDef.Provider != "" {
			effCfg.Provider = agentDef.Provider
		}
		if agentDef.BaseURL != "" {
			effCfg.OpenAIBaseURL = agentDef.BaseURL
		}
		if agentDef.APIKey != "" {
			if effCfg.Provider == "gemini" {
				effCfg.APIKey = agentDef.APIKey
			} else {
				effCfg.OpenAIAPIKey = agentDef.APIKey
			}
		}
		if agentDef.SystemPrompt != "" {
			effCfg.SystemPrompt = agentDef.SystemPrompt
		}
		if len(agentDef.AllowedTools) > 0 {
			allowedTools = agentDef.AllowedTools
		}
	}

	if req.Model != "" {
		effCfg.Model = req.Model
		effCfg.OpenAIModel = req.Model
	}
	if req.Provider != "" {
		effCfg.Provider = req.Provider
	}

	var resp *PromptResponse
	var err error

	// 1. Explicit Gemini Provider or Gemini API key
	if effCfg.Provider == "gemini" || (effCfg.APIKey != "" && effCfg.Provider != "openai") {
		resp, err = e.executeWithGemini(ctx, req, effCfg, allowedTools, start)
	} else if effCfg.OpenAIAPIKey != "" || os.Getenv("OPENAI_BASE_URL") != "" || effCfg.Provider == "openai" {
		resp, err = e.executeWithOpenAI(ctx, req, effCfg, allowedTools, start)
		if err != nil {
			if effCfg.Provider == "openai" || effCfg.OpenAIAPIKey != "" {
				// Explicit selection failed — propagate as hard error.
				return nil, err
			}
			// Opportunistic attempt (env-based) failed — clear and fall through to next backend.
			err = nil
		}
	}

	if resp == nil && err == nil {
		// 3. Ollama direct chat if specified
		if effCfg.Provider == "ollama" {
			resp, err = e.executeWithOllama(ctx, req, effCfg, start)
		} else {
			// 4. Fallback: Heuristic Deterministic Coordinator for zero-config offline operations
			resp, err = e.executeDeterministicFallback(ctx, req, allowedTools, start)
		}
	}

	if err != nil {
		return nil, err
	}

	if resp != nil {
		resp.NodeName = e.config.NodeName
		if req.Role != "" {
			resp.AgentRole = req.Role
		} else {
			resp.AgentRole = "coordinator"
		}
	}

	return resp, nil
}

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
				"Use your cluster and research tools (cluster_roster, cluster_exec, cluster_cp, shared_kv, agent_send_mail, agent_broadcast, agent_read_mailbox, fetch_url, google_search) "+
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
			fc       *genai.FunctionCall
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

func (e *Engine) executeDeterministicFallback(ctx context.Context, req *PromptRequest, allowedTools []string, start time.Time) (*PromptResponse, error) {
	// Deterministic coordinator parser when no API key is provided
	toolsToUse := e.getEffectiveTools(allowedTools)
	lowerPrompt := strings.ToLower(req.Prompt)

	if tool, ok := toolsToUse["cluster_roster"]; ok {
		if tc, resp, handled := tryFallbackClusterRoster(ctx, tool, lowerPrompt); handled {
			return e.buildFallbackResponse(resp, tc, start), nil
		}
	}
	if tool, ok := toolsToUse["cluster_exec"]; ok {
		if tc, resp, handled := tryFallbackClusterExec(ctx, tool, lowerPrompt); handled {
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

	defaultResp := fmt.Sprintf(
		"Herd Node Agent ('%s') received task: %q\n\n"+
			"Note: Set OPENAI_API_KEY, OPENAI_BASE_URL, or GEMINI_API_KEY to enable full autonomous multi-step reasoning.",
		e.config.NodeName, req.Prompt,
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

func tryFallbackClusterExec(ctx context.Context, tool ToolDefinition, lowerPrompt string) (*ToolCall, string, bool) {
	if !strings.Contains(lowerPrompt, "uptime") && !strings.Contains(lowerPrompt, "uname") && !strings.Contains(lowerPrompt, "exec") {
		return nil, "", false
	}
	target := "all"
	cmd := "uptime"
	if strings.Contains(lowerPrompt, "uname") {
		cmd = "uname -a"
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

func (e *Engine) executeWithOpenAI(ctx context.Context, req *PromptRequest, cfg Config, allowedTools []string, start time.Time) (*PromptResponse, error) {
	baseURL := strings.TrimRight(cfg.OpenAIBaseURL, "/")
	endpoint := fmt.Sprintf("%s/chat/completions", baseURL)

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf(
			"You are Herd Agent for node '%s'. You are part of a decentralized P2P mesh cluster. "+
				"Use your cluster and research tools (cluster_roster, cluster_exec, cluster_cp, shared_kv, agent_send_mail, agent_broadcast, agent_read_mailbox, fetch_url, google_search) "+
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
			finalResponse = assistantMsg.Content
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
		type openAIExecResult struct {
			tc       OpenAIToolCall
			resBytes []byte
		}

		results := make([]openAIExecResult, len(assistantMsg.ToolCalls))
		var wg sync.WaitGroup
		wg.Add(len(assistantMsg.ToolCalls))

		for i, tc := range assistantMsg.ToolCalls {
			go func(idx int, call OpenAIToolCall) {
				defer wg.Done()

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
				results[idx] = openAIExecResult{
					tc:       call,
					resBytes: resBytes,
				}
			}(i, tc)
		}
		wg.Wait()

		for _, r := range results {
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
		return nil
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

