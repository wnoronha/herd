package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const defaultOllamaOpenAIBase = "http://localhost:11434/v1"

// openAIPathExplicit is true when the user configured OpenAI-compatible access (not auto-Ollama).
func openAIPathExplicit(cfg Config) bool {
	if cfg.OpenAIAPIKey != "" || os.Getenv("OPENAI_BASE_URL") != "" || os.Getenv("OPENAI_API_BASE") != "" || cfg.Provider == "openai" {
		return true
	}
	// A custom BaseURL (anything but the Ollama default) is an explicit
	// OpenAI-compatible endpoint even without a key.
	if base := strings.TrimRight(strings.TrimSpace(cfg.OpenAIBaseURL), "/"); base != "" && base != defaultOllamaOpenAIBase {
		return true
	}
	return false
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

func (e *Engine) resolveAutoOllamaConfig(
	ctx context.Context,
	effCfg Config,
) (*Config, string, error) {
	if os.Getenv("HERD_DISABLE_AUTO_OLLAMA") != "" {
		return nil, "", nil
	}
	if effCfg.Provider == "gemini" {
		return nil, "", nil
	}

	discovered, installed, listErr := applyDiscoveredOllamaModels(ctx, effCfg)
	if listErr != nil {
		if effCfg.Provider == "ollama" {
			return nil, "", fmt.Errorf("ollama provider selected but probe failed: %w", listErr)
		}
		if openAIPathExplicit(discovered) {
			return &discovered, "", nil
		}
		hint := ollamaAutoFailureHintWithList(ollamaHost(discovered), installed, listErr, discovered, nil)
		return nil, hint, nil
	}
	if len(installed) == 0 {
		if effCfg.Provider == "ollama" {
			return nil, "", fmt.Errorf("ollama provider selected but no models are installed (run: ollama pull llama3.2)")
		}
		if openAIPathExplicit(discovered) {
			return &discovered, "", nil
		}
		hint := ollamaAutoFailureHintWithList(ollamaHost(discovered), installed, nil, discovered, nil)
		return nil, hint, nil
	}
	tryCfg := ollamaAutoOpenAICfg(discovered)
	return &tryCfg, "", nil
}
