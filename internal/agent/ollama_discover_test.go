package agent

import "testing"

func TestPickOllamaModel(t *testing.T) {
	installed := []string{"llama3.2:latest", "gemma2:2b"}
	if got := pickOllamaModel(installed, "llama3.2"); got != "llama3.2:latest" {
		t.Fatalf("expected llama3.2:latest, got %q", got)
	}
	if got := pickOllamaModel(installed, "missing"); got != "" {
		t.Fatalf("expected empty on no match (no silent wrong-model fallback), got %q", got)
	}
	if got := pickOllamaModel(installed, "llama3.2:latest"); got != "llama3.2:latest" {
		t.Fatalf("expected :latest normalization to match, got %q", got)
	}
}
