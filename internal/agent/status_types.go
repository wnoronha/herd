package agent

// AgentStatusReport is the structured agent backend / persona status for one node.
type AgentStatusReport struct {
	NodeName             string          `json:"node_name"`
	BackendClass         string          `json:"backend_class"`    // llm | auto | ollama | fallback
	BackendProvider      string          `json:"backend_provider"` // gemini | openai | ollama | fallback
	Model                string          `json:"model"`
	FallbackModel        string          `json:"fallback_model,omitempty"`
	ConfiguredLLM        bool            `json:"configured_llm"`
	ConfigSources        []string        `json:"config_sources,omitempty"`
	KVGlobalPresent      bool            `json:"kv_global_present"`
	KVGlobalSummary      string          `json:"kv_global_summary,omitempty"`
	RegisteredToolsCount int             `json:"registered_tools_count"`
	Personas             []PersonaStatus `json:"personas,omitempty"`
	Role                 *RoleStatus     `json:"role,omitempty"`
	Probe                *ProbeStatus    `json:"probe,omitempty"`
	Reason               string          `json:"reason,omitempty"`
	FixHints             []string        `json:"fix_hints,omitempty"`
}

// PersonaStatus summarizes a loaded declarative agent persona.
type PersonaStatus struct {
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	ToolCount int      `json:"tool_count"`
	Tools     []string `json:"tools,omitempty"`
}

// RoleStatus details one persona role and tool gaps vs the full registry.
type RoleStatus struct {
	Role             string   `json:"role"`
	AllowedTools     []string `json:"allowed_tools,omitempty"`
	MissingFromRole  []string `json:"missing_from_role,omitempty"`
	UsesFullToolset  bool     `json:"uses_full_toolset"`
	EffectiveBackend string   `json:"effective_backend"`
	EffectiveModel   string   `json:"effective_model"`
}

// ProbeStatus holds optional live API reachability checks.
type ProbeStatus struct {
	Attempted bool     `json:"attempted"`
	OK        bool     `json:"ok"`
	Provider  string   `json:"provider,omitempty"`
	Error     string   `json:"error,omitempty"`
	Models    []string `json:"models,omitempty"`
}

// StatusCollectOptions controls agent status collection.
type StatusCollectOptions struct {
	Role  string
	Probe bool
}
