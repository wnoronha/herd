package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// multiAgentWrapper assists in decoding either a single agent frontmatter or a list of agents.
type multiAgentWrapper struct {
	AgentDefinition `yaml:",inline"`
	Agents          []*AgentDefinition `yaml:"agents"`
}

// splitFrontmatter splits raw markdown bytes into frontmatter YAML (if present) and body.
// Frontmatter must begin with '---' on the first line and end with '---' or '...' on a single line.
func splitFrontmatter(data []byte) ([]byte, string, error) {
	// Strip UTF-8 BOM if present
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	content := string(data)
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		// No frontmatter present; entire content is markdown body
		return nil, strings.TrimSpace(content), nil
	}

	lines := strings.Split(content, "\n")
	firstIdx := -1
	secondIdx := -1

	for i, line := range lines {
		l := strings.TrimRight(line, "\r ")
		if l == "---" {
			if firstIdx == -1 {
				firstIdx = i
			} else {
				secondIdx = i
				break
			}
		} else if firstIdx != -1 && l == "..." {
			secondIdx = i
			break
		}
	}

	if firstIdx != -1 && secondIdx == -1 {
		return nil, "", fmt.Errorf("unclosed YAML frontmatter delimiter '---'")
	}

	yamlContent := strings.Join(lines[firstIdx+1:secondIdx], "\n")
	bodyContent := strings.Join(lines[secondIdx+1:], "\n")

	return []byte(yamlContent), strings.TrimSpace(bodyContent), nil
}

// ParseAgentMarkdown parses a single declarative agent definition from raw markdown bytes.
func ParseAgentMarkdown(data []byte) (*AgentDefinition, error) {
	yamlBytes, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	def := &AgentDefinition{
		SystemPrompt: body,
	}

	if len(yamlBytes) > 0 {
		if err := yaml.Unmarshal(yamlBytes, def); err != nil {
			return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
		}
		// If body is present in markdown, it overrides or sets SystemPrompt
		if body != "" {
			def.SystemPrompt = body
		}
	}

	normalizeAgent(def)
	return def, nil
}

// ParseAgentsMarkdown parses one or more agent definitions from raw markdown bytes.
// If the frontmatter contains an `agents:` list, multiple definitions are returned.
func ParseAgentsMarkdown(data []byte) ([]*AgentDefinition, error) {
	yamlBytes, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	if len(yamlBytes) == 0 {
		def := &AgentDefinition{
			SystemPrompt: body,
		}
		normalizeAgent(def)
		return []*AgentDefinition{def}, nil
	}

	var wrapper multiAgentWrapper
	if err := yaml.Unmarshal(yamlBytes, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	if len(wrapper.Agents) > 0 {
		for _, a := range wrapper.Agents {
			if a.SystemPrompt == "" && body != "" {
				a.SystemPrompt = body
			}
			normalizeAgent(a)
		}
		return wrapper.Agents, nil
	}

	single := &wrapper.AgentDefinition
	if body != "" {
		single.SystemPrompt = body
	}
	normalizeAgent(single)
	return []*AgentDefinition{single}, nil
}

// ParseAgentMarkdownFile loads and parses a single agent from a file, inferring name/role if omitted.
func ParseAgentMarkdownFile(filePath string) (*AgentDefinition, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent file %s: %w", filePath, err)
	}

	def, err := ParseAgentMarkdown(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse agent file %s: %w", filePath, err)
	}

	inferNameAndRole(def, filePath)
	return def, nil
}

// ParseAgentsMarkdownFile loads one or more agents from a file.
func ParseAgentsMarkdownFile(filePath string) ([]*AgentDefinition, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent file %s: %w", filePath, err)
	}

	agents, err := ParseAgentsMarkdown(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse agent file %s: %w", filePath, err)
	}

	for _, a := range agents {
		inferNameAndRole(a, filePath)
	}
	return agents, nil
}

// ParseAgentsYAML parses one or more agent definitions from raw YAML bytes (ADK Agent Config format).
func ParseAgentsYAML(data []byte) ([]*AgentDefinition, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	var wrapper multiAgentWrapper
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse YAML agent config: %w", err)
	}

	if len(wrapper.Agents) > 0 {
		for _, a := range wrapper.Agents {
			normalizeAgent(a)
		}
		return wrapper.Agents, nil
	}

	single := &wrapper.AgentDefinition
	normalizeAgent(single)
	return []*AgentDefinition{single}, nil
}

// ParseAgentsYAMLFile loads one or more agents from a YAML file.
func ParseAgentsYAMLFile(filePath string) ([]*AgentDefinition, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent YAML file %s: %w", filePath, err)
	}

	agents, err := ParseAgentsYAML(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse agent YAML file %s: %w", filePath, err)
	}

	for _, a := range agents {
		inferNameAndRole(a, filePath)
	}
	return agents, nil
}

// LoadAgentsFromDir scans a directory for agent markdown or YAML files.
func LoadAgentsFromDir(dir string) (map[string]*AgentDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	res := make(map[string]*AgentDefinition)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".agent.md") ||
			strings.HasSuffix(lower, ".agent.markdown") ||
			lower == "agents.md" {
			filePath := filepath.Join(dir, name)
			agents, err := ParseAgentsMarkdownFile(filePath)
			if err != nil {
				continue
			}
			for _, a := range agents {
				registerAgentToMap(res, a)
			}
		} else if strings.HasSuffix(lower, ".agent.yaml") ||
			strings.HasSuffix(lower, ".agent.yml") ||
			lower == "root_agent.yaml" ||
			lower == "root_agent.yml" ||
			strings.HasSuffix(lower, ".yaml") ||
			strings.HasSuffix(lower, ".yml") {
			filePath := filepath.Join(dir, name)
			agents, err := ParseAgentsYAMLFile(filePath)
			if err != nil {
				continue
			}
			for _, a := range agents {
				registerAgentToMap(res, a)
			}
		}
	}

	return res, nil
}

// LoadDefaultAgents discovers and loads agent definitions across standard search locations.
func LoadDefaultAgents(extraDirs ...string) map[string]*AgentDefinition {
	agents := make(map[string]*AgentDefinition)

	// Candidate paths
	var searchDirs []string
	cwd, err := os.Getwd()
	if err == nil {
		searchDirs = append(searchDirs,
			cwd,
			filepath.Join(cwd, "agents"),
			filepath.Join(cwd, ".herd", "agents"),
		)
	}

	if home, err := os.UserHomeDir(); err == nil {
		searchDirs = append(searchDirs,
			filepath.Join(home, ".config", "herd", "agents"),
			filepath.Join(home, ".herd", "agents"),
		)
	}

	searchDirs = append(searchDirs, extraDirs...)

	for _, dir := range searchDirs {
		loaded, err := LoadAgentsFromDir(dir)
		if err != nil {
			continue
		}
		for k, v := range loaded {
			agents[k] = v
		}
	}

	return agents
}

// DefaultAgentsTemplate contains the starter declarative agent definitions.
const DefaultAgentsTemplate = `---
agents:
  - name: coordinator
    role: coordinator
    description: Primary cluster coordinator capable of full-mesh operations
    tools:
      - cluster_roster
      - cluster_exec
      - cluster_cp
      - shared_kv
      - agent_send_mail
      - agent_broadcast
      - agent_read_mailbox
      - fetch_url
      - google_search
  - name: ops
    role: ops
    description: Cluster infrastructure and remote system operations specialist
    tools:
      - cluster_roster
      - cluster_exec
      - cluster_cp
  - name: researcher
    role: researcher
    description: Web research and cluster intelligence specialist
    tools:
      - google_search
      - fetch_url
      - shared_kv
---
# Herd Node Agent Personas

You are an autonomous AI agent operating within the Herd decentralized P2P cluster mesh.
Each persona is tuned for specific tasks:
- **coordinator**: Orchestrates cluster discovery, actor mailboxes, and inter-agent coordination.
- **ops**: Executes systems administration and file synchronization across cluster nodes.
- **researcher**: Gathers web documentation and external intelligence, saving findings to KV.
`

// EnsureDefaultAgentsFile scaffolds starter agent definitions into targetDir if no agent files exist.
// Returns the path to the file, a boolean indicating if it was newly created, and an error if any.
func EnsureDefaultAgentsFile(targetDir string, force ...bool) (string, bool, error) {
	if targetDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
		targetDir = filepath.Join(home, ".config", "herd", "agents")
	}

	targetFile := filepath.Join(targetDir, "AGENTS.md")

	overwrite := len(force) > 0 && force[0]
	if !overwrite {
		// If an agent file already exists in targetDir, do not overwrite
		if existing, err := LoadAgentsFromDir(targetDir); err == nil && len(existing) > 0 {
			return targetFile, false, nil
		}
		if _, err := os.Stat(targetFile); err == nil {
			return targetFile, false, nil
		}
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", false, fmt.Errorf("failed to create agents directory %s: %w", targetDir, err)
	}

	if err := os.WriteFile(targetFile, []byte(DefaultAgentsTemplate), 0644); err != nil {
		return "", false, fmt.Errorf("failed to write starter AGENTS.md to %s: %w", targetFile, err)
	}

	return targetFile, true, nil
}

func normalizeAgent(def *AgentDefinition) {
	if def == nil {
		return
	}
	if def.SystemPrompt == "" && def.Instruction != "" {
		def.SystemPrompt = def.Instruction
	}
	if def.Instruction == "" && def.SystemPrompt != "" {
		def.Instruction = def.SystemPrompt
	}
	if def.Name == "" && def.Role != "" {
		def.Name = def.Role
	}
	if def.Role == "" && def.Name != "" {
		def.Role = def.Name
	}
	def.Name = strings.TrimSpace(def.Name)
	def.Role = strings.TrimSpace(def.Role)
	def.SystemPrompt = strings.TrimSpace(def.SystemPrompt)
	def.Instruction = strings.TrimSpace(def.Instruction)
}

func inferNameAndRole(def *AgentDefinition, filePath string) {
	if def == nil {
		return
	}
	if def.Name != "" && def.Role != "" {
		return
	}

	base := filepath.Base(filePath)
	lower := strings.ToLower(base)
	for _, suffix := range []string{
		".agent.markdown", ".agent.md", ".markdown", ".md",
		".agent.yaml", ".agent.yml", ".yaml", ".yml",
	} {
		if strings.HasSuffix(lower, suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}

	clean := strings.ToLower(strings.TrimSpace(base))
	if clean == "agents" || clean == "root_agent" {
		clean = "default"
	}

	if def.Name == "" {
		def.Name = clean
	}
	if def.Role == "" {
		def.Role = def.Name
	}
}

func registerAgentToMap(m map[string]*AgentDefinition, a *AgentDefinition) {
	if a == nil {
		return
	}
	if a.Name != "" {
		m[strings.ToLower(a.Name)] = a
	}
	if a.Role != "" {
		m[strings.ToLower(a.Role)] = a
	}
}
