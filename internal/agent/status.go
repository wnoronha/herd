package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const fallbackModelName = "herd-builtin-coordinator"

var roleHintTools = []string{
	"dns_lookup", "google_search", "fetch_url", "cluster_exec", "cluster_roster", "shared_kv",
}

// BuildStatus collects agent backend, config, persona, and optional probe information.
func (e *Engine) BuildStatus(ctx context.Context, opts StatusCollectOptions) (*AgentStatusReport, error) {
	if e == nil {
		return nil, fmt.Errorf("agent engine is not initialized")
	}

	baseCfg := e.config
	effCfg := e.resolveEffectiveConfig()
	sources, kvPresent, kvSummary := e.describeConfigSources(baseCfg, effCfg)

	backendClass, backendProvider, model, configured := classifyBackend(effCfg)
	report := &AgentStatusReport{
		NodeName:             e.config.NodeName,
		BackendClass:         backendClass,
		BackendProvider:      backendProvider,
		Model:                model,
		ConfiguredLLM:        configured,
		ConfigSources:        sources,
		KVGlobalPresent:      kvPresent,
		KVGlobalSummary:      kvSummary,
		RegisteredToolsCount: len(e.tools),
		Personas:             e.listPersonaStatus(),
	}

	if backendClass == "fallback" {
		report.FallbackModel = fallbackModelName
		report.Model = fallbackModelName
	}
	report.Reason, report.FixHints = buildStatusReasonFixes(effCfg, configured, backendClass, kvPresent)

	if opts.Role != "" {
		roleStatus, err := e.buildRoleStatus(opts.Role, effCfg, backendProvider, model, configured)
		if err != nil {
			return nil, err
		}
		report.Role = roleStatus
	}

	if opts.Probe && configured && backendClass == "llm" {
		report.Probe = probeLLM(ctx, backendProvider, effCfg)
	} else if opts.Probe && backendClass == "ollama" {
		report.Probe = probeOllama(ctx, effCfg)
	} else if opts.Probe {
		report.Probe = &ProbeStatus{
			Attempted: true,
			OK:        false,
			Error:     "probe skipped: no LLM backend configured (fallback mode)",
		}
	}

	return report, nil
}

func (e *Engine) describeConfigSources(base, effective Config) (sources []string, kvGlobal bool, kvSummary string) {
	if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" {
		sources = append(sources, "env:GEMINI_API_KEY")
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		sources = append(sources, "env:OPENAI_API_KEY")
	}
	if os.Getenv("OPENAI_BASE_URL") != "" || os.Getenv("OPENAI_API_BASE") != "" {
		sources = append(sources, "env:OPENAI_BASE_URL")
	}
	if os.Getenv("OPENAI_MODEL") != "" {
		sources = append(sources, "env:OPENAI_MODEL")
	}
	if os.Getenv("GEMINI_MODEL") != "" {
		sources = append(sources, "env:GEMINI_MODEL")
	}

	if e.cctx != nil {
		if kv := e.cctx.GetKVStore(); kv != nil {
			if entry, ok := kv.Get("agent:config:global"); ok && len(entry.Value) > 0 {
				kvGlobal = true
				var gCfg AgentClusterConfig
				if err := json.Unmarshal(entry.Value, &gCfg); err == nil {
					kvSummary = summarizeKVConfig(gCfg)
					sources = append(sources, "kv:agent:config:global")
				}
			}
			nodeKey := fmt.Sprintf("agent:config:nodes/%s", e.config.NodeName)
			if entry, ok := kv.Get(nodeKey); ok && len(entry.Value) > 0 {
				var nCfg AgentClusterConfig
				if err := json.Unmarshal(entry.Value, &nCfg); err == nil {
					sources = append(sources, "kv:"+nodeKey+" ("+summarizeKVConfig(nCfg)+")")
				}
			}
		}
	}

	if effective.Provider != base.Provider && effective.Provider != "" {
		sources = append(sources, "effective:provider="+effective.Provider)
	}
	if len(sources) == 0 {
		sources = []string{"defaults (no env/KV overrides detected)"}
	}
	return sources, kvGlobal, kvSummary
}

func summarizeKVConfig(cfg AgentClusterConfig) string {
	parts := []string{}
	if cfg.Provider != "" {
		parts = append(parts, "provider="+cfg.Provider)
	}
	if cfg.Model != "" {
		parts = append(parts, "model="+cfg.Model)
	}
	if cfg.APIKey != "" {
		parts = append(parts, "key="+redactKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		parts = append(parts, "base_url="+cfg.BaseURL)
	}
	return strings.Join(parts, ", ")
}

func redactKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "***"
	}
	return "***" + key[len(key)-4:]
}

func classifyBackend(cfg Config) (class, provider, model string, configured bool) {
	if cfg.Provider == "ollama" {
		return "ollama", "ollama", firstNonEmpty(cfg.Model, cfg.OpenAIModel, "ollama"), true
	}

	geminiPath := cfg.Provider == "gemini" || (cfg.APIKey != "" && cfg.Provider != "openai")
	if geminiPath {
		if cfg.APIKey != "" {
			return "llm", "gemini", firstNonEmpty(cfg.Model, "gemini-3.8-flash"), true
		}
		return "fallback", "fallback", fallbackModelName, false
	}

	openaiPath := cfg.OpenAIAPIKey != "" || os.Getenv("OPENAI_BASE_URL") != "" || cfg.Provider == "openai"
	if openaiPath {
		modelName := firstNonEmpty(cfg.OpenAIModel, cfg.Model, "gpt-4o-mini")
		if cfg.OpenAIAPIKey != "" || cfg.Provider == "openai" || os.Getenv("OPENAI_BASE_URL") != "" {
			return "llm", "openai", modelName, cfg.OpenAIAPIKey != "" || cfg.Provider == "openai" || os.Getenv("OPENAI_BASE_URL") != ""
		}
	}

	return "fallback", "fallback", fallbackModelName, false
}

func buildStatusReasonFixes(cfg Config, configured bool, backendClass string, kvGlobal bool) (reason string, fixes []string) {
	if configured && backendClass == "llm" {
		return "LLM backend is configured for this node.", nil
	}
	if backendClass == "ollama" {
		return "Ollama provider selected.", []string{"Ensure Ollama is running at " + cfg.OllamaHost}
	}

	reason = "No working LLM credentials or reachable OpenAI-compatible endpoint; prompts use deterministic fallback (keyword heuristics only)."
	fixes = []string{
		`herd kv set agent:config:global '{"provider":"gemini","model":"gemini-3.8-flash","api_key":"<key>"}'`,
		"or export GEMINI_API_KEY / OPENAI_API_KEY and restart the daemon",
	}
	if !kvGlobal {
		fixes = append(fixes, "cluster KV agent:config:global is not set (optional but replicates config to all nodes)")
	}
	return reason, fixes
}

func (e *Engine) listPersonaStatus() []PersonaStatus {
	agents := e.ListAgents()
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Role < agents[j].Role
	})
	seenRole := make(map[string]bool)
	var out []PersonaStatus
	for _, a := range agents {
		if a.Role == "" || seenRole[a.Role] {
			continue
		}
		seenRole[a.Role] = true
		tools := append([]string(nil), a.AllowedTools...)
		sort.Strings(tools)
		out = append(out, PersonaStatus{
			Name:      a.Name,
			Role:      a.Role,
			ToolCount: len(tools),
			Tools:     tools,
		})
	}
	return out
}

func (e *Engine) buildRoleStatus(role string, effCfg Config, provider, model string, configured bool) (*RoleStatus, error) {
	def, ok := e.GetAgent(role)
	if !ok {
		return nil, fmt.Errorf("agent role or name %q not found", role)
	}
	allowed := append([]string(nil), def.AllowedTools...)
	sort.Strings(allowed)

	rs := &RoleStatus{
		Role:             def.Role,
		AllowedTools:     allowed,
		UsesFullToolset:  len(allowed) == 0,
		EffectiveBackend: provider,
		EffectiveModel:   model,
	}
	if !configured {
		rs.EffectiveBackend = "fallback"
		rs.EffectiveModel = fallbackModelName
	}

	if len(allowed) > 0 {
		allowedSet := make(map[string]bool, len(allowed))
		for _, t := range allowed {
			allowedSet[t] = true
		}
		for _, hint := range roleHintTools {
			if _, reg := e.tools[hint]; reg && !allowedSet[hint] {
				rs.MissingFromRole = append(rs.MissingFromRole, hint)
			}
		}
	}

	if def.Provider != "" {
		rs.EffectiveBackend = def.Provider
	}
	if def.Model != "" {
		rs.EffectiveModel = def.Model
	}
	_ = effCfg
	return rs, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
