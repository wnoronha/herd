package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAgentMarkdown_ValidFrontmatter(t *testing.T) {
	raw := `---
name: cluster-researcher
role: researcher
description: Deep web and cluster research specialist
model: gemini-3.8-flash
provider: gemini
temperature: 0.2
tools:
  - google_search
  - fetch_url
  - shared_kv
---
# Researcher Instructions
You are an expert research agent embedded in the Herd cluster.
Use google_search and fetch_url to gather intelligence.
`
	def, err := ParseAgentMarkdown([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error parsing agent markdown: %v", err)
	}

	if def.Name != "cluster-researcher" {
		t.Errorf("expected name 'cluster-researcher', got '%s'", def.Name)
	}
	if def.Role != "researcher" {
		t.Errorf("expected role 'researcher', got '%s'", def.Role)
	}
	if def.Description != "Deep web and cluster research specialist" {
		t.Errorf("unexpected description: %s", def.Description)
	}
	if def.Model != "gemini-3.8-flash" {
		t.Errorf("expected model 'gemini-3.8-flash', got '%s'", def.Model)
	}
	if def.Provider != "gemini" {
		t.Errorf("expected provider 'gemini', got '%s'", def.Provider)
	}
	if def.Temperature == nil || *def.Temperature != 0.2 {
		t.Errorf("expected temperature 0.2, got %v", def.Temperature)
	}
	if len(def.AllowedTools) != 3 {
		t.Fatalf("expected 3 allowed tools, got %d", len(def.AllowedTools))
	}
	if def.AllowedTools[0] != "google_search" || def.AllowedTools[1] != "fetch_url" || def.AllowedTools[2] != "shared_kv" {
		t.Errorf("unexpected tools: %v", def.AllowedTools)
	}
	if def.SystemPrompt == "" || def.SystemPrompt[:len("# Researcher Instructions")] != "# Researcher Instructions" {
		t.Errorf("unexpected system prompt: %s", def.SystemPrompt)
	}
}

func TestParseAgentMarkdown_UnclosedFrontmatter(t *testing.T) {
	raw := `---
name: broken
role: broken
Some unclosed content without closing dashes
`
	_, err := ParseAgentMarkdown([]byte(raw))
	if err == nil {
		t.Fatal("expected error on unclosed frontmatter, got nil")
	}
}

func TestParseAgentMarkdown_NoFrontmatter(t *testing.T) {
	raw := `# Pure Markdown
You are a simple assistant without frontmatter metadata.
`
	def, err := ParseAgentMarkdown([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.SystemPrompt != "# Pure Markdown\nYou are a simple assistant without frontmatter metadata." {
		t.Errorf("unexpected system prompt: %s", def.SystemPrompt)
	}
}

func TestParseAgentsMarkdown_MultiAgent(t *testing.T) {
	raw := `---
agents:
  - name: ops-agent
    role: ops
    tools:
      - cluster_exec
      - cluster_roster
  - name: dev-agent
    role: dev
    tools:
      - shared_kv
---
Global cluster agent guidelines.
`
	agents, err := ParseAgentsMarkdown([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}

	if agents[0].Name != "ops-agent" || agents[0].Role != "ops" {
		t.Errorf("unexpected agent 0: %+v", agents[0])
	}
	if agents[0].SystemPrompt != "Global cluster agent guidelines." {
		t.Errorf("expected shared system prompt, got '%s'", agents[0].SystemPrompt)
	}
	if len(agents[0].AllowedTools) != 2 {
		t.Errorf("expected 2 tools for ops, got %d", len(agents[0].AllowedTools))
	}

	if agents[1].Name != "dev-agent" || agents[1].Role != "dev" {
		t.Errorf("unexpected agent 1: %+v", agents[1])
	}
	if agents[1].SystemPrompt != "Global cluster agent guidelines." {
		t.Errorf("expected shared system prompt for dev, got '%s'", agents[1].SystemPrompt)
	}
}

func TestParseAgentMarkdownFile_InfersFilename(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sentinel.agent.md")

	content := `---
tools:
  - cluster_roster
---
Sentinel agent monitoring node heartbeats.
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	def, err := ParseAgentMarkdownFile(filePath)
	if err != nil {
		t.Fatalf("failed to parse file: %v", err)
	}

	if def.Name != "sentinel" {
		t.Errorf("expected inferred name 'sentinel', got '%s'", def.Name)
	}
	if def.Role != "sentinel" {
		t.Errorf("expected inferred role 'sentinel', got '%s'", def.Role)
	}
	if len(def.AllowedTools) != 1 || def.AllowedTools[0] != "cluster_roster" {
		t.Errorf("unexpected tools: %v", def.AllowedTools)
	}
}

func TestLoadAgentsFromDir(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "worker.agent.md")
	content1 := `---
name: worker-node
role: worker
tools:
  - cluster_exec
---
Worker persona.
`
	if err := os.WriteFile(file1, []byte(content1), 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}

	file2 := filepath.Join(tmpDir, "agents.md")
	content2 := `---
name: coordinator
role: leader
tools:
  - cluster_roster
  - agent_broadcast
---
Coordinator persona.
`
	if err := os.WriteFile(file2, []byte(content2), 0644); err != nil {
		t.Fatalf("failed to write file2: %v", err)
	}

	// Ignored file
	file3 := filepath.Join(tmpDir, "notes.txt")
	if err := os.WriteFile(file3, []byte("random text"), 0644); err != nil {
		t.Fatalf("failed to write file3: %v", err)
	}

	agents, err := LoadAgentsFromDir(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error loading agents: %v", err)
	}

	if agents["worker"] == nil || agents["worker-node"] == nil {
		t.Errorf("expected worker agent indexed by role and name")
	}
	if agents["leader"] == nil || agents["coordinator"] == nil {
		t.Errorf("expected coordinator agent indexed by role and name")
	}
	if agents["notes"] != nil {
		t.Errorf("notes.txt should not be loaded as agent")
	}
}

func TestEnsureDefaultAgentsFile(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")

	// 1. Initial creation
	targetFile, created, err := EnsureDefaultAgentsFile(agentsDir)
	if err != nil {
		t.Fatalf("EnsureDefaultAgentsFile failed: %v", err)
	}
	if !created {
		t.Fatalf("expected created to be true on first run")
	}
	if filepath.Base(targetFile) != "AGENTS.md" {
		t.Errorf("expected targetFile to be AGENTS.md, got %s", targetFile)
	}

	// Verify file content can be parsed into 3 default personas
	agents, err := ParseAgentsMarkdownFile(targetFile)
	if err != nil {
		t.Fatalf("failed to parse generated AGENTS.md: %v", err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 default agents (coordinator, ops, researcher), got %d", len(agents))
	}

	// 2. Second call should not overwrite
	_, createdAgain, err := EnsureDefaultAgentsFile(agentsDir)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if createdAgain {
		t.Errorf("expected created to be false when file already exists")
	}

	// 3. Force overwrite
	_, createdForce, err := EnsureDefaultAgentsFile(agentsDir, true)
	if err != nil {
		t.Fatalf("force overwrite failed: %v", err)
	}
	if !createdForce {
		t.Errorf("expected created to be true when force=true")
	}
}
