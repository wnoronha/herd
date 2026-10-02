package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAgentsYAML_ADKFormat(t *testing.T) {
	// Standard ADK Agent Config format from https://adk.dev/agents/config/
	adkYaml := `# yaml-language-server: $schema=https://raw.githubusercontent.com/google/adk-python/refs/heads/main/src/google/adk/agents/config_schemas/AgentConfig.json
agent_class: LlmAgent
name: tutor_agent
model: gemini-flash-latest
description: Learning assistant that provides tutoring in code and math.
instruction: |
  You are a learning assistant that helps students with coding and math questions.
tools:
  - name: google_search
  - fetch_url
sub_agents:
  - config_path: code_tutor_agent.yaml
`

	agents, err := ParseAgentsYAML([]byte(adkYaml))
	if err != nil {
		t.Fatalf("unexpected error parsing ADK YAML: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	agent := agents[0]
	if agent.Name != "tutor_agent" {
		t.Errorf("expected name 'tutor_agent', got '%s'", agent.Name)
	}
	if agent.Role != "tutor_agent" {
		t.Errorf("expected role 'tutor_agent', got '%s'", agent.Role)
	}
	if agent.AgentClass != "LlmAgent" {
		t.Errorf("expected agent_class 'LlmAgent', got '%s'", agent.AgentClass)
	}
	if agent.SystemPrompt != "You are a learning assistant that helps students with coding and math questions." {
		t.Errorf("expected SystemPrompt to match instruction, got: %q", agent.SystemPrompt)
	}
	if len(agent.AllowedTools) != 2 {
		t.Fatalf("expected 2 tools, got %d (%v)", len(agent.AllowedTools), agent.AllowedTools)
	}
	if agent.AllowedTools[0] != "google_search" || agent.AllowedTools[1] != "fetch_url" {
		t.Errorf("unexpected tools: %v", agent.AllowedTools)
	}
	if len(agent.SubAgents) != 1 || agent.SubAgents[0].ConfigPath != "code_tutor_agent.yaml" {
		t.Errorf("unexpected sub_agents: %v", agent.SubAgents)
	}
}

func TestLoadAgentsFromDir_YAMLAndMarkdown(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "herd-adk-load-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Write a YAML agent config
	yamlContent := `name: yaml-worker
instruction: Process batch data in the cluster.
tools:
  - cluster_exec
`
	if err := os.WriteFile(filepath.Join(tempDir, "worker.agent.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write worker.agent.yaml: %v", err)
	}

	// Write a root_agent.yaml (ADK default)
	rootYaml := `name: root
instruction: Root supervisor agent.
`
	if err := os.WriteFile(filepath.Join(tempDir, "root_agent.yaml"), []byte(rootYaml), 0644); err != nil {
		t.Fatalf("failed to write root_agent.yaml: %v", err)
	}

	// Write a Markdown agent
	mdContent := `---
name: md-ops
tools:
  - cluster_cp
---
Execute ops tasks.
`
	if err := os.WriteFile(filepath.Join(tempDir, "ops.agent.md"), []byte(mdContent), 0644); err != nil {
		t.Fatalf("failed to write ops.agent.md: %v", err)
	}

	loaded, err := LoadAgentsFromDir(tempDir)
	if err != nil {
		t.Fatalf("unexpected error loading agents: %v", err)
	}

	if loaded["yaml-worker"] == nil {
		t.Errorf("expected 'yaml-worker' to be loaded")
	}
	if loaded["root"] == nil && loaded["default"] == nil {
		t.Errorf("expected 'root' or 'default' to be loaded")
	}
	if loaded["md-ops"] == nil {
		t.Errorf("expected 'md-ops' to be loaded")
	}
}
