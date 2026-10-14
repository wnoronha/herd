package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
	ApplyConfigDefaults(&cfg)

	agentsMap := make(map[string]*AgentDefinition)
	for k, v := range LoadDefaultAgents(extraDirs...) {
		agentsMap[k] = v
	}

	eng := &Engine{
		config: cfg,
		cctx:   cctx,
		agents: agentsMap,
	}
	eng.tools = registerDefaultToolMap(cctx, eng)
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
// It always returns a copy so callers can neither race nor mutate engine state.
func (e *Engine) getEffectiveTools(allowed []string) map[string]ToolDefinition {
	if len(allowed) == 0 {
		out := make(map[string]ToolDefinition, len(e.tools))
		for name, def := range e.tools {
			out[name] = def
		}
		return out
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
			applyClusterConfig(&effective, gCfg)
		}
	}

	// 2. Check per-node override: agent:config:nodes/<node>
	nodeKey := fmt.Sprintf("agent:config:nodes/%s", effective.NodeName)
	if entry, ok := kvStore.Get(nodeKey); ok && len(entry.Value) > 0 {
		var nCfg AgentClusterConfig
		if err := json.Unmarshal(entry.Value, &nCfg); err == nil {
			applyClusterConfig(&effective, nCfg)
		}
	}

	return effective
}

// applyClusterConfig overlays non-empty KV cluster-agent fields onto cfg.
func applyClusterConfig(effective *Config, c AgentClusterConfig) {
	if c.Provider != "" {
		effective.Provider = c.Provider
	}
	if c.Model != "" {
		effective.OpenAIModel = c.Model
		effective.Model = c.Model
	}
	if c.BaseURL != "" {
		effective.OpenAIBaseURL = c.BaseURL
	}
	if c.APIKey != "" {
		if c.Provider == "gemini" {
			effective.APIKey = c.APIKey
		} else {
			effective.OpenAIAPIKey = c.APIKey
		}
	}
	if c.SystemPrompt != "" {
		effective.SystemPrompt = c.SystemPrompt
	}
}

func (e *Engine) effectiveConfigForRequest(req *PromptRequest) (Config, []string, error) {
	effCfg := e.resolveEffectiveConfig()
	var allowedTools []string
	if req.Role != "" {
		agentDef, ok := e.GetAgent(req.Role)
		if !ok {
			return effCfg, nil, fmt.Errorf("agent role or name '%s' not found", req.Role)
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
	return effCfg, allowedTools, nil
}

// ExecutePrompt runs a task prompt through the agent reasoning and tool loop.
func (e *Engine) ExecutePrompt(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
	if req == nil || req.Prompt == "" {
		return nil, errors.New("prompt cannot be empty")
	}

	start := time.Now()
	effCfg, allowedTools, err := e.effectiveConfigForRequest(req)
	if err != nil {
		return nil, err
	}

	var resp *PromptResponse
	var ollamaHint string

	// 1. Explicit Gemini Provider or Gemini API key
	if effCfg.Provider == "gemini" || (effCfg.APIKey != "" && effCfg.Provider != "openai") {
		resp, err = e.executeWithGemini(ctx, req, effCfg, allowedTools, start)
	} else if openAIPathExplicit(effCfg) {
		resp, err = e.executeWithOpenAI(ctx, req, effCfg, allowedTools, start)
		if err != nil {
			if effCfg.Provider == "openai" || effCfg.OpenAIAPIKey != "" {
				// Explicit selection failed — propagate as hard error.
				return nil, err
			}
			// Opportunistic attempt (env-based) failed — surface the real
			// cause in the fallback hint instead of a confusing Ollama hint.
			ollamaHint = "OpenAI attempt failed (" + err.Error() + ");"
			err = nil
		}
	}

	if resp == nil && err == nil {
		// 3. Auto-probe local Ollama (OpenAI-compatible /v1 with tools, then native /api/chat)
		var autoHint string
		resp, autoHint, err = e.tryAutoOllamaBackends(ctx, req, effCfg, allowedTools, start)
		if err != nil {
			return nil, err
		}
		ollamaHint += autoHint
	}

	if resp == nil && err == nil {
		// 4. Fallback: Heuristic Deterministic Coordinator for zero-config offline operations
		resp, err = e.executeDeterministicFallback(ctx, req, allowedTools, start, ollamaHint)
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
