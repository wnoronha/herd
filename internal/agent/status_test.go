package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wnoronha/herd/internal/kv"
	"github.com/wnoronha/herd/internal/roster"
)

func TestBuildStatusFallbackWithoutKeys(t *testing.T) {
	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   roster.NewStore(&roster.NodeMeta{NodeName: "test-node"}),
		kvStore:  kv.NewStore("test-node"),
	}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	report, err := engine.BuildStatus(context.Background(), StatusCollectOptions{})
	if err != nil {
		t.Fatalf("BuildStatus: %v", err)
	}
	if report.BackendClass != "auto" {
		t.Fatalf("expected auto ollama probe backend, got %s", report.BackendClass)
	}
	if strings.Contains(report.Model, "(") || strings.Contains(report.Model, " ") {
		t.Fatalf("expected machine-readable model, got %q", report.Model)
	}
	if report.ConfiguredLLM {
		t.Fatal("expected configured_llm false without keys")
	}
	if report.Reason == "" {
		t.Fatal("expected reason describing auto ollama probe")
	}
}

func TestBuildStatusKVGlobalGemini(t *testing.T) {
	kvStore := kv.NewStore("test-node")
	globalJSON, _ := json.Marshal(AgentClusterConfig{
		Provider: "gemini",
		Model:    "gemini-3.8-flash",
		APIKey:   "AIzaSyTestKey1234567890",
	})
	kvStore.Set("agent:config:global", globalJSON, 0)

	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   roster.NewStore(&roster.NodeMeta{NodeName: "test-node"}),
		kvStore:  kvStore,
	}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)

	report, err := engine.BuildStatus(context.Background(), StatusCollectOptions{})
	if err != nil {
		t.Fatalf("BuildStatus: %v", err)
	}
	if !report.ConfiguredLLM || report.BackendProvider != "gemini" {
		t.Fatalf("expected configured gemini, got %+v", report)
	}
	if !report.KVGlobalPresent {
		t.Fatal("expected kv global present")
	}
	if report.KVGlobalSummary == "" || report.KVGlobalSummary == "AIzaSyTestKey1234567890" {
		t.Fatalf("expected redacted kv summary, got %q", report.KVGlobalSummary)
	}
}

func TestBuildStatusRoleMissingTools(t *testing.T) {
	mockCtx := &mockClusterContext{
		nodeName: "test-node",
		roster:   roster.NewStore(&roster.NodeMeta{NodeName: "test-node"}),
		kvStore:  kv.NewStore("test-node"),
	}
	engine := NewEngine(Config{NodeName: "test-node"}, mockCtx)
	engine.RegisterAgent(&AgentDefinition{
		Name:         "ops",
		Role:         "ops",
		AllowedTools: []string{"cluster_roster", "cluster_exec", "cluster_cp"},
	})

	report, err := engine.BuildStatus(context.Background(), StatusCollectOptions{Role: "ops"})
	if err != nil {
		t.Fatalf("BuildStatus role: %v", err)
	}
	if report.Role == nil {
		t.Fatal("expected role status")
	}
	// Unconfigured auto backend: role must reflect the Ollama probe path,
	// not a forced fallback.
	if report.Role.EffectiveBackend != "ollama" {
		t.Fatalf("expected role backend ollama (auto), got %q", report.Role.EffectiveBackend)
	}
	foundDNS := false
	for _, m := range report.Role.MissingFromRole {
		if m == "dns_lookup" {
			foundDNS = true
		}
	}
	if !foundDNS {
		t.Fatalf("expected dns_lookup missing from ops role, got %v", report.Role.MissingFromRole)
	}
}
