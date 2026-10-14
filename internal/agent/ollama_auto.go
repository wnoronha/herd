package agent

import (
	"context"
	"os"
	"strings"
	"time"
)

const defaultOllamaOpenAIBase = "http://localhost:11434/v1"

// openAIPathExplicit is true when the user configured OpenAI-compatible access (not auto-Ollama).
func openAIPathExplicit(cfg Config) bool {
	return cfg.OpenAIAPIKey != "" || os.Getenv("OPENAI_BASE_URL") != "" || os.Getenv("OPENAI_API_BASE") != "" || cfg.Provider == "openai"
}

func ollamaAutoOpenAICfg(cfg Config) Config {
	out := cfg
	if strings.TrimSpace(out.OpenAIBaseURL) == "" {
		out.OpenAIBaseURL = defaultOllamaOpenAIBase
	}
	if strings.TrimSpace(out.OpenAIAPIKey) == "" {
		out.OpenAIAPIKey = "ollama"
	}
	if modelUnsuitedForOllama(out.OpenAIModel) {
		out.OpenAIModel = firstNonEmpty(os.Getenv("OLLAMA_MODEL"), os.Getenv("OPENAI_MODEL"), "llama3.2")
	}
	return out
}

func modelUnsuitedForOllama(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || model == "gpt-4o-mini" {
		return true
	}
	return strings.HasPrefix(model, "gemini-")
}

func (e *Engine) tryAutoOllamaBackends(
	ctx context.Context,
	req *PromptRequest,
	effCfg Config,
	allowedTools []string,
	start time.Time,
) (*PromptResponse, error, string) {
	if effCfg.Provider == "gemini" {
		return nil, nil, ""
	}

	discovered, _, _ := applyDiscoveredOllamaModels(ctx, effCfg)
	effCfg = discovered

	var lastErr error
	if !openAIPathExplicit(effCfg) {
		tryCfg := ollamaAutoOpenAICfg(effCfg)
		resp, err := e.executeWithOpenAI(ctx, req, tryCfg, allowedTools, start)
		if err == nil && resp != nil {
			return resp, nil, ""
		}
		if err != nil {
			lastErr = err
		}
	}

	resp, err := e.executeWithOllama(ctx, req, effCfg, start)
	if err == nil && resp != nil {
		return resp, nil, ""
	}
	if err != nil {
		lastErr = err
	}

	if effCfg.Provider == "ollama" && lastErr != nil {
		return nil, lastErr, ""
	}
	hint := ""
	if lastErr != nil || resp == nil {
		hint = ollamaAutoFailureHint(ctx, effCfg, lastErr)
	}
	return nil, nil, hint
}
