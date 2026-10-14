package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func ollamaHost(cfg Config) string {
	host := strings.TrimSpace(cfg.OllamaHost)
	if host == "" {
		host = "http://localhost:11434"
	}
	return strings.TrimRight(host, "/")
}

func listOllamaModels(ctx context.Context, host string) ([]string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags HTTP %d", resp.StatusCode)
	}
	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range tags.Models {
		if n := strings.TrimSpace(m.Name); n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

func ollamaNameMatches(requested, available string) bool {
	requested = strings.TrimSpace(requested)
	available = strings.TrimSpace(available)
	if requested == "" || available == "" {
		return false
	}
	// Normalize the implicit :latest tag so "llama3.2:latest" matches "llama3.2".
	requested = strings.TrimSuffix(requested, ":latest")
	available = strings.TrimSuffix(available, ":latest")
	return available == requested || strings.HasPrefix(available, requested+":")
}

// pickOllamaModel returns the installed model matching the first candidate,
// or "" when nothing matches. It never silently substitutes an unrelated
// model: callers must surface the miss instead of executing the wrong model.
func pickOllamaModel(installed []string, candidates ...string) string {
	for _, want := range candidates {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		for _, have := range installed {
			if ollamaNameMatches(want, have) {
				return have
			}
		}
	}
	return ""
}

func preferredOllamaCandidates(cfg Config) []string {
	return []string{
		os.Getenv("OLLAMA_MODEL"),
		cfg.OpenAIModel,
		cfg.Model,
		os.Getenv("OPENAI_MODEL"),
		"llama3.2",
		"llama3.1",
		"gemma3",
		"gemma2:2b",
	}
}

// applyDiscoveredOllamaModels sets OpenAIModel and Model when defaults target cloud models.
func applyDiscoveredOllamaModels(ctx context.Context, cfg Config) (Config, []string, error) {
	host := ollamaHost(cfg)
	installed, err := listOllamaModels(ctx, host)
	if err != nil {
		return cfg, nil, err
	}
	if len(installed) == 0 {
		return cfg, installed, nil
	}
	pick := pickOllamaModel(installed, preferredOllamaCandidates(cfg)...)
	if pick == "" {
		return cfg, installed, nil
	}
	out := cfg
	if modelUnsuitedForOllama(out.OpenAIModel) {
		out.OpenAIModel = pick
	}
	if out.Model == "" || strings.HasPrefix(out.Model, "gemini-") || out.Model == "gpt-4o-mini" {
		out.Model = pick
	}
	return out, installed, nil
}

func ollamaAutoFailureHint(ctx context.Context, cfg Config, lastErr error) string {
	host := ollamaHost(cfg)
	installed, err := listOllamaModels(ctx, host)
	return ollamaAutoFailureHintWithList(host, installed, err, cfg, lastErr)
}

// ollamaAutoFailureHintWithList renders the hint from an already-fetched model
// list so callers can reuse discovery instead of re-listing per prompt.
func ollamaAutoFailureHintWithList(host string, installed []string, listErr error, cfg Config, lastErr error) string {
	if listErr != nil {
		return fmt.Sprintf("Could not reach Ollama at %s (%v). Start it with: ollama serve", host, listErr)
	}
	if len(installed) == 0 {
		return "Ollama is running but no models are installed. Run: ollama pull llama3.2 (or set OLLAMA_MODEL to a model you pull)"
	}
	want := pickOllamaModel(installed, preferredOllamaCandidates(cfg)...)
	if want == "" {
		return fmt.Sprintf("Ollama is up with models %v but none match (want %q); pull one with: ollama pull %q", installed, firstCandidate(cfg), firstCandidate(cfg))
	}
	if lastErr != nil {
		return fmt.Sprintf("Ollama is up (e.g. %q) but the chat request failed: %v", want, lastErr)
	}
	return fmt.Sprintf("Ollama is up with models %v but chat did not succeed; try: ollama run %q", installed, want)
}

// firstCandidate reports the model the user asked for, for hint messages.
func firstCandidate(cfg Config) string {
	for _, c := range preferredOllamaCandidates(cfg) {
		if strings.TrimSpace(c) != "" {
			return strings.TrimSpace(c)
		}
	}
	return "llama3.2"
}
