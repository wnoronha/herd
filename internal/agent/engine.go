package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
