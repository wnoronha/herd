package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

func probeLLM(ctx context.Context, provider string, cfg Config) *ProbeStatus {
	ps := &ProbeStatus{Attempted: true, Provider: provider}
	switch provider {
	case "gemini":
		// Capability check on the real generation path (minimal "ping"),
		// not a metadata fetch. Bounded so a stuck API can't hang status.
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		client, err := genai.NewClient(pctx, &genai.ClientConfig{
			APIKey:  cfg.APIKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			ps.Error = err.Error()
			return ps
		}
		resp, err := client.Models.GenerateContent(pctx, firstNonEmpty(cfg.Model, "gemini-3.8-flash"), genai.Text("ping"), nil)
		if err != nil {
			ps.Error = err.Error()
			return ps
		}
		if len(resp.Candidates) == 0 {
			ps.Error = "gemini returned no candidates"
			return ps
		}
		ps.OK = true
		return ps
	case "openai":
		base := strings.TrimRight(cfg.OpenAIBaseURL, "/")
		if base == "" {
			ps.Error = "openai base URL is empty"
			return ps
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
		if err != nil {
			ps.Error = err.Error()
			return ps
		}
		if cfg.OpenAIAPIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.OpenAIAPIKey)
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			ps.Error = err.Error()
			return ps
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			ps.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			return ps
		}
		ps.OK = true
		return ps
	default:
		ps.Error = "probe not implemented for provider " + provider
		return ps
	}
}

func probeOllama(ctx context.Context, cfg Config) *ProbeStatus {
	ps := &ProbeStatus{Attempted: true, Provider: "ollama"}
	host := strings.TrimRight(cfg.OllamaHost, "/")
	if host == "" {
		host = "http://localhost:11434"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/api/tags", nil)
	if err != nil {
		ps.Error = err.Error()
		return ps
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		ps.Error = err.Error()
		return ps
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ps.Error = fmt.Sprintf("HTTP %d from Ollama", resp.StatusCode)
		return ps
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		ps.Error = err.Error()
		return ps
	}
	for _, m := range payload.Models {
		if n := strings.TrimSpace(m.Name); n != "" {
			ps.Models = append(ps.Models, n)
		}
	}
	if len(ps.Models) == 0 {
		ps.Error = "Ollama is running but no models are installed (run: ollama pull llama3.2)"
		return ps
	}
	ps.OK = true
	return ps
}
