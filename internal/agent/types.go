package agent

import (
	"context"
	"time"

	"gopkg.in/yaml.v3"
)

// Config defines the configuration for the embedded AI agent.
type Config struct {
	NodeName      string
	Model         string
	Provider      string // "openai" (default), "gemini", "ollama", "fallback"
	APIKey        string
	OpenAIBaseURL string
	OpenAIAPIKey  string
	OpenAIModel   string
	OllamaHost    string
	SystemPrompt  string
	Timeout       time.Duration
}

// AgentClusterConfig represents the JSON structure stored in KV under agent:config:*
type AgentClusterConfig struct {
	Provider     string  `json:"provider,omitempty"`
	Model        string  `json:"model,omitempty"`
	BaseURL      string  `json:"base_url,omitempty"`
	APIKey       string  `json:"api_key,omitempty"`
	SystemPrompt string  `json:"system_prompt,omitempty"`
	Temperature  float64 `json:"temperature,omitempty"`
}

// SubAgentRef references a sub-agent by config path or name.
type SubAgentRef struct {
	ConfigPath string `yaml:"config_path" json:"config_path,omitempty"`
	Name       string `yaml:"name" json:"name,omitempty"`
}

// ToolsList represents a list of tool names that can be unmarshaled from either
// simple string sequences or ADK-style object mappings (e.g. [{name: "google_search"}]).
type ToolsList []string

// UnmarshalYAML implements custom YAML unmarshaling for tools sequences.
func (t *ToolsList) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	if value.Kind != yaml.SequenceNode {
		return nil
	}
	var res []string
	for _, n := range value.Content {
		switch n.Kind {
		case yaml.ScalarNode:
			res = append(res, n.Value)
		case yaml.MappingNode:
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == "name" {
					res = append(res, n.Content[i+1].Value)
				}
			}
		}
	}
	*t = res
	return nil
}

// AgentDefinition describes a declarative agent persona defined via Markdown/YAML frontmatter or ADK Agent Config.
type AgentDefinition struct {
	Name         string        `yaml:"name" json:"name"`
	Role         string        `yaml:"role" json:"role"`
	Description  string        `yaml:"description" json:"description"`
	Model        string        `yaml:"model" json:"model"`
	Provider     string        `yaml:"provider" json:"provider"`
	BaseURL      string        `yaml:"base_url" json:"base_url,omitempty"`
	APIKey       string        `yaml:"api_key" json:"api_key,omitempty"`
	Temperature  *float64      `yaml:"temperature" json:"temperature,omitempty"`
	AllowedTools ToolsList     `yaml:"tools" json:"tools,omitempty"`
	SystemPrompt string        `yaml:"system_prompt" json:"system_prompt"`
	Instruction  string        `yaml:"instruction" json:"instruction,omitempty"`
	AgentClass   string        `yaml:"agent_class" json:"agent_class,omitempty"`
	SubAgents    []SubAgentRef `yaml:"sub_agents" json:"sub_agents,omitempty"`
}

// ToolDefinition describes a callable tool exposed to the agent.
type ToolDefinition struct {
	Name        string                                                      `json:"name"`
	Description string                                                      `json:"description"`
	Parameters  map[string]any                                              `json:"parameters"`
	Handler     func(ctx context.Context, args map[string]any) (any, error) `json:"-"`
}

// OpenAITool represents an OpenAI standard tool definition.
type OpenAITool struct {
	Type     string           `json:"type"`
	Function OpenAIFunctionDef `json:"function"`
}

// OpenAIFunctionDef describes an OpenAI tool function schema.
type OpenAIFunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// OpenAIChatMessage represents a message in an OpenAI chat completion request.
type OpenAIChatMessage struct {
	Role       string            `json:"role"`
	Content    any               `json:"content,omitempty"`
	Name       string            `json:"name,omitempty"`
	ToolCalls  []OpenAIToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

// OpenAIToolCall represents a tool call emitted by an OpenAI model.
type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall contains function name and JSON arguments string.
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OpenAIChatRequest represents a POST payload to /v1/chat/completions.
type OpenAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []OpenAIChatMessage `json:"messages"`
	Tools       []OpenAITool        `json:"tools,omitempty"`
	ToolChoice  string              `json:"tool_choice,omitempty"`
	Temperature float64             `json:"temperature,omitempty"`
}

// OpenAIChatResponse represents the response from /v1/chat/completions.
type OpenAIChatResponse struct {
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// PromptRequest represents a single-shot or conversational prompt to the agent.
type PromptRequest struct {
	Prompt     string        `json:"prompt"`
	TargetNode string        `json:"target_node,omitempty"`
	SessionID  string        `json:"session_id,omitempty"`
	History    []ChatMessage `json:"history,omitempty"`
	Role       string        `json:"role,omitempty"`
	Model      string        `json:"model,omitempty"`
	Provider   string        `json:"provider,omitempty"`
}

// ChatMessage represents a single message in an agent conversation.
type ChatMessage struct {
	Role    string `json:"role"` // "user", "assistant", "system", "tool"
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// PromptResponse represents the agent's completed response and execution trace.
type PromptResponse struct {
	Response    string     `json:"response"`
	ToolsCalled []ToolCall `json:"tools_called,omitempty"`
	ModelUsed   string     `json:"model_used"`
	ExecutionMs int64      `json:"execution_ms"`
	NodeName    string     `json:"node_name,omitempty"`
	AgentRole   string     `json:"agent_role,omitempty"`
}

// ToolCall logs a tool invocation executed during an agent run.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
}
