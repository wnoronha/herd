package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/genai"
)

// deterministicModelName is reported as ModelUsed when no cloud LLM or local
// Ollama endpoint is reachable and the air-gapped heuristic coordinator runs.
const deterministicModelName = "herd-builtin-coordinator"

// openaiPlaceholderKey is supplied whenever a custom BaseURL is used without
// an explicit key so openai-go/v3 does not fail on a missing OPENAI_API_KEY.
const openaiPlaceholderKey = "ollama"

// buildADKModel constructs a per-request ADK model.LLM for the effective
// config. It never uses model.Register: per-node KV overrides mean two
// concurrent prompts can need different BaseURLs for the same model name, and
// the global registry panics on duplicate patterns. Construction is
// request-scoped so each Engine call is isolated.
func buildADKModel(ctx context.Context, cfg Config) (model.LLM, string, error) {
	if cfg.Provider == "gemini" || (cfg.APIKey != "" && cfg.Provider != "openai") {
		modelName := cfg.Model
		if modelName == "" {
			modelName = "gemini-3.8-flash"
		}
		llm, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{
			APIKey:  cfg.APIKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return nil, "", fmt.Errorf("failed to create Gemini ADK model: %w", err)
		}
		return llm, modelName, nil
	}

	if isOpenAIEndpointConfigured(cfg) {
		modelName := cfg.OpenAIModel
		if modelName == "" {
			modelName = "gpt-4o-mini"
		}
		baseURL := strings.TrimRight(cfg.OpenAIBaseURL, "/")
		apiKey := cfg.OpenAIAPIKey
		if apiKey == "" {
			apiKey = openaiPlaceholderKey
		}
		llm, err := openaimodel.NewModel(ctx, modelName, &openaimodel.ClientConfig{
			APIKey:     apiKey,
			BaseURL:    baseURL,
			HTTPClient: &http.Client{Timeout: 90 * time.Second},
			API:        openaimodel.APIChatCompletions,
		})
		if err != nil {
			return nil, "", fmt.Errorf("failed to create OpenAI ADK model: %w", err)
		}
		return llm, modelName, nil
	}

	return nil, "", fmt.Errorf("no LLM endpoint configured")
}

// isOpenAIEndpointConfigured reports whether cfg selects an explicit
// OpenAI-compatible endpoint: an API key, an explicit provider selection, or
// a non-default BaseURL. A real key is still required for the default
// api.openai.com endpoint; keyless local Ollama uses a placeholder key.
func isOpenAIEndpointConfigured(cfg Config) bool {
	if cfg.OpenAIAPIKey != "" || cfg.Provider == "openai" {
		return true
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.OpenAIBaseURL), "/")
	if base == "" {
		return false
	}
	if strings.Contains(base, "api.openai.com") {
		return cfg.OpenAIAPIKey != ""
	}
	return true
}

// isToolUnsupportedError reports whether err looks like an HTTP 400 from a
// local model that does not support tool calling, so the caller can retry the
// same openaimodel with Tools: nil instead of a separate native client.
func isToolUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "400") &&
		(strings.Contains(msg, "tool") || strings.Contains(msg, "function") ||
			strings.Contains(msg, "Bad Request"))
}

// deterministicLLM is the air-gapped fallback coordinator implemented as an
// ADK model.LLM. Turn 1 inspects the user prompt and emits a genai.FunctionCall
// when a heuristic rule matches an allowed tool in req.Tools. Turn 2 receives
// the FunctionResponse and formats it into the user-facing markdown response.
// All tool execution and tracing stay inside ADK.
type deterministicLLM struct {
	policyCheck func(command string) error
	probeHint   string
}

// deterministicLLMName identifies the fallback model in traces.
const deterministicLLMName = "herd-builtin-coordinator"

// newDeterministicLLM builds the offline fallback LLM. policyCheck enforces
// agent:policy:allowed_exec before Turn 1 emits a cluster_exec FunctionCall so
// rejected commands leave ToolsCalled empty. probeHint surfaces the Ollama
// auto-probe outcome in the no-match note.
func newDeterministicLLM(policyCheck func(command string) error, probeHint string) *deterministicLLM {
	return &deterministicLLM{policyCheck: policyCheck, probeHint: probeHint}
}

// Name implements model.LLM.
func (d *deterministicLLM) Name() string { return deterministicLLMName }

// GenerateContent implements model.LLM as a 2-turn heuristic coordinator.
func (d *deterministicLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if fr := findFunctionResponse(req.Contents); fr != nil {
			text := formatDeterministicTurnTwo(req.Contents, fr)
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role:  "model",
					Parts: []*genai.Part{{Text: text}},
				},
			}, nil)
			return
		}
		if call := d.matchTurnOne(req); call != nil {
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role:  "model",
					Parts: []*genai.Part{{FunctionCall: call}},
				},
			}, nil)
			return
		}
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: d.noMatchNote(req)}},
			},
		}, nil)
	}
}

// matchTurnOne applies the heuristic coordinator rules. It only emits a
// FunctionCall whose name is present in req.Tools (already role-scoped), and
// validates exec policy before emitting cluster_exec so rejections stay silent.
func (d *deterministicLLM) matchTurnOne(req *model.LLMRequest) *genai.FunctionCall {
	prompt := lastUserText(req.Contents)
	lowerPrompt := strings.ToLower(prompt)
	if prompt == "" {
		return nil
	}

	if hasTool(req, "cluster_roster") &&
		(strings.Contains(lowerPrompt, "roster") || strings.Contains(lowerPrompt, "nodes") || strings.Contains(lowerPrompt, "cluster")) {
		return &genai.FunctionCall{
			ID:   "det-roster-1",
			Name: "cluster_roster",
			Args: map[string]any{"filter_status": "all"},
		}
	}
	if hasTool(req, "cluster_exec") &&
		(strings.Contains(lowerPrompt, "uptime") || strings.Contains(lowerPrompt, "uname") || strings.Contains(lowerPrompt, "exec")) {
		cmd := "uptime"
		if strings.Contains(lowerPrompt, "uname") {
			cmd = "uname -a"
		}
		if d.policyCheck != nil {
			if err := d.policyCheck(cmd); err != nil {
				return nil
			}
		}
		return &genai.FunctionCall{
			ID:   "det-exec-1",
			Name: "cluster_exec",
			Args: map[string]any{"target": "all", "command": cmd},
		}
	}
	if hasTool(req, "cluster_cp") {
		if src, dst, ok := parseCopyArgs(prompt); ok {
			return &genai.FunctionCall{
				ID:   "det-cp-1",
				Name: "cluster_cp",
				Args: map[string]any{"source": src, "destination": dst},
			}
		}
	}
	if hasTool(req, "google_search") && strings.Contains(lowerPrompt, "search") {
		query := strings.TrimPrefix(lowerPrompt, "search for ")
		query = strings.TrimPrefix(query, "search ")
		query = strings.TrimPrefix(query, "google ")
		return &genai.FunctionCall{
			ID:   "det-search-1",
			Name: "google_search",
			Args: map[string]any{"query": query},
		}
	}
	if hasTool(req, "fetch_url") {
		if targetURL := parseFetchURL(prompt); targetURL != "" {
			return &genai.FunctionCall{
				ID:   "det-fetch-1",
				Name: "fetch_url",
				Args: map[string]any{"url": targetURL},
			}
		}
	}
	return nil
}

// hasTool reports whether name is in the role-scoped toolset of the request.
func hasTool(req *model.LLMRequest, name string) bool {
	if req == nil {
		return false
	}
	_, ok := req.Tools[name]
	return ok
}

// lastUserText returns the text of the most recent user content part.
func lastUserText(contents []*genai.Content) string {
	for i := len(contents) - 1; i >= 0; i-- {
		c := contents[i]
		if c == nil || c.Role == "model" {
			continue
		}
		var sb strings.Builder
		for _, p := range c.Parts {
			if p != nil && p.Text != "" && p.FunctionResponse == nil {
				sb.WriteString(p.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	return ""
}

// findFunctionResponse locates the first FunctionResponse part in contents.
func findFunctionResponse(contents []*genai.Content) *genai.FunctionResponse {
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil {
				return p.FunctionResponse
			}
		}
	}
	return nil
}

// findFunctionCall locates the FunctionCall with the given ID, falling back to
// the first FunctionCall when IDs do not match.
func findFunctionCall(contents []*genai.Content, id string) *genai.FunctionCall {
	var first *genai.FunctionCall
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && p.FunctionCall != nil {
				if first == nil {
					first = p.FunctionCall
				}
				if id != "" && p.FunctionCall.ID == id {
					return p.FunctionCall
				}
			}
		}
	}
	return first
}

// formatDeterministicTurnTwo renders the FunctionResponse payload into the
// user-facing markdown, reusing the preceding FunctionCall args so target,
// command, source, destination, query, and URL strings match the legacy
// coordinator output.
func formatDeterministicTurnTwo(contents []*genai.Content, fr *genai.FunctionResponse) string {
	call := findFunctionCall(contents, fr.ID)
	args := map[string]any{}
	if call != nil && call.Args != nil {
		args = call.Args
	}
	payload := ""
	if fr.Response != nil {
		if pretty, err := json.MarshalIndent(fr.Response, "", "  "); err == nil {
			payload = string(pretty)
		} else {
			payload = "{}"
		}
	}
	switch fr.Name {
	case "cluster_roster":
		return fmt.Sprintf("Cluster Roster:\n```json\n%s\n```", payload)
	case "cluster_exec":
		target, _ := args["target"].(string)
		if target == "" {
			target = "all"
		}
		cmd, _ := args["command"].(string)
		if cmd == "" {
			cmd = "uptime"
		}
		return fmt.Sprintf("Executed '%s' on %s:\n```json\n%s\n```", cmd, target, payload)
	case "cluster_cp":
		src, _ := args["source"].(string)
		dst, _ := args["destination"].(string)
		return fmt.Sprintf("Transferred file from %s to %s:\n```json\n%s\n```", src, dst, payload)
	case "google_search":
		query, _ := args["query"].(string)
		return fmt.Sprintf("Search results for %q:\n```json\n%s\n```", query, payload)
	case "fetch_url":
		u, _ := args["url"].(string)
		return fmt.Sprintf("Fetched content from %s:\n```json\n%s\n```", u, payload)
	default:
		name := fr.Name
		if call != nil && call.Name != "" {
			name = call.Name
		}
		return fmt.Sprintf("Tool '%s' result:\n```json\n%s\n```", name, payload)
	}
}

// noMatchNote renders the coordinator note when no heuristic rule matches,
// preserving the legacy herd-builtin-coordinator contract.
func (d *deterministicLLM) noMatchNote(req *model.LLMRequest) string {
	prompt := lastUserText(req.Contents)
	if strings.TrimSpace(d.probeHint) != "" {
		return fmt.Sprintf(
			"Herd Node Agent received task: %q\n\n%s",
			prompt, d.probeHint,
		)
	}
	return fmt.Sprintf(
		"Herd Node Agent received task: %q\n\nNote: Set OPENAI_API_KEY, OPENAI_BASE_URL, or GEMINI_API_KEY for cloud LLMs, or run a local model with Ollama (ollama pull llama3.2).",
		prompt,
	)
}

// parseCopyArgs extracts cp source/destination from a free-form prompt.
func parseCopyArgs(prompt string) (string, string, bool) {
	lowerPrompt := strings.ToLower(prompt)
	if !strings.Contains(lowerPrompt, "copy") && !strings.Contains(lowerPrompt, " cp ") && !strings.HasPrefix(lowerPrompt, "cp ") {
		return "", "", false
	}
	parts := strings.Fields(prompt)
	for i, p := range parts {
		if (strings.EqualFold(p, "cp") || strings.EqualFold(p, "copy")) && i+2 < len(parts) {
			return parts[i+1], parts[i+2], true
		}
	}
	return "", "", false
}

// parseFetchURL extracts the first http(s) URL from a prompt.
func parseFetchURL(prompt string) string {
	for _, w := range strings.Fields(prompt) {
		if strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
			return w
		}
	}
	return ""
}
