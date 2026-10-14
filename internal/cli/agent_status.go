package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wnoronha/herd/internal/ipc"
)

type clusterAgentStatusOutput struct {
	Local  *ipc.AgentStatusResponse            `json:"local"`
	Nodes  map[string]*ipc.AgentStatusResponse `json:"nodes,omitempty"`
	Errors map[string]string                   `json:"errors,omitempty"`
}

func agentStatusAction(role string, probe, cluster, jsonOutput bool) error {
	if cluster {
		return agentStatusCluster(role, probe, jsonOutput)
	}

	report, err := fetchAgentStatus(role, probe)
	if err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printAgentStatus(report)
	return nil
}

func fetchAgentStatus(role string, probe bool) (*ipc.AgentStatusResponse, error) {
	client, _, err := daemonClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentStatusTimeout(probe))
	defer cancel()
	return client.AgentStatus(ctx, &ipc.AgentStatusRequest{
		Role:  role,
		Probe: probe,
	})
}

func agentStatusTimeout(probe bool) time.Duration {
	if probe {
		return 45 * time.Second
	}
	return 10 * time.Second
}

func agentStatusCluster(role string, probe, jsonOutput bool) error {
	client, cfg, err := daemonClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentStatusTimeout(probe)*3)
	defer cancel()

	out := clusterAgentStatusOutput{
		Nodes:  make(map[string]*ipc.AgentStatusResponse),
		Errors: make(map[string]string),
	}

	local, err := client.AgentStatus(ctx, &ipc.AgentStatusRequest{Role: role, Probe: probe})
	if err != nil {
		return fmt.Errorf("local agent status: %w", err)
	}
	out.Local = local

	roster, err := client.GetRoster(ctx)
	if err != nil {
		return fmt.Errorf("roster: %w", err)
	}

	remoteArgs := []string{"agent", "status", "--json"}
	if role != "" {
		remoteArgs = append(remoteArgs, "--role", role)
	}
	if probe {
		remoteArgs = append(remoteArgs, "--probe")
	}

	for _, member := range roster.Members {
		if member.Name == cfg.NodeName {
			continue
		}
		if member.Status != "alive" {
			out.Errors[member.Name] = "member status: " + member.Status
			continue
		}

		execResp, err := client.ExecCommand(ctx, &ipc.ExecRequest{
			Target:         member.Name,
			Command:        "herd",
			Args:           remoteArgs,
			TimeoutSeconds: int(agentStatusTimeout(probe).Seconds()),
		})
		if err != nil {
			out.Errors[member.Name] = err.Error()
			continue
		}
		if len(execResp.Results) == 0 {
			out.Errors[member.Name] = "no exec results"
			continue
		}
		res := execResp.Results[0]
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(res.Stdout)
			}
			out.Errors[member.Name] = msg
			continue
		}
		var nodeReport ipc.AgentStatusResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &nodeReport); err != nil {
			out.Errors[member.Name] = "invalid agent status JSON: " + err.Error()
			continue
		}
		out.Nodes[member.Name] = &nodeReport
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	printAgentStatus(out.Local)
	fmt.Println()
	fmt.Println("=== Cluster Agent Status ===")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "NODE\tBACKEND\tMODEL\tLLM CONFIGURED")
	for name, rep := range out.Nodes {
		_, _ = fmt.Fprintf(w, "%s\t%s/%s\t%s\t%v\n", name, rep.BackendClass, rep.BackendProvider, rep.Model, rep.ConfiguredLLM)
	}
	for name, errMsg := range out.Errors {
		_, _ = fmt.Fprintf(w, "%s\tERROR\t-\t%s\n", name, errMsg)
	}
	_ = w.Flush()
	return nil
}

func printAgentStatus(report *ipc.AgentStatusResponse) {
	if report == nil {
		return
	}
	fmt.Printf("=== Herd Agent (%s) ===\n", report.NodeName)
	backendLabel := report.BackendClass
	if report.BackendProvider != "" {
		backendLabel = fmt.Sprintf("%s (%s)", report.BackendClass, report.BackendProvider)
	}
	if report.ConfiguredLLM && report.BackendClass == "llm" {
		backendLabel += " — configured"
	}
	fmt.Printf("Backend:     %s\n", backendLabel)
	fmt.Printf("Model:       %s\n", report.Model)
	if len(report.ConfigSources) > 0 {
		fmt.Printf("Config:      %s\n", strings.Join(report.ConfigSources, "; "))
	}
	if report.KVGlobalPresent {
		fmt.Printf("Cluster KV:  agent:config:global (%s)\n", report.KVGlobalSummary)
	}
	fmt.Printf("Tools:       %d registered\n", report.RegisteredToolsCount)

	if len(report.Personas) > 0 {
		var names []string
		for _, p := range report.Personas {
			names = append(names, fmt.Sprintf("%s (%d tools)", p.Role, p.ToolCount))
		}
		fmt.Printf("Personas:    %s\n", strings.Join(names, ", "))
	}

	if report.Role != nil {
		fmt.Println()
		fmt.Printf("Role:        %s\n", report.Role.Role)
		if report.Role.UsesFullToolset {
			fmt.Printf("Tools:       all registered tools (no allowlist)\n")
		} else {
			fmt.Printf("Allowed:     %s\n", strings.Join(report.Role.AllowedTools, ", "))
		}
		if len(report.Role.MissingFromRole) > 0 {
			fmt.Printf("Not in role: %s (add to AGENTS.md or use a role that includes them)\n",
				strings.Join(report.Role.MissingFromRole, ", "))
		}
	}

	if report.Probe != nil && report.Probe.Attempted {
		fmt.Println()
		if report.Probe.OK {
			fmt.Printf("Probe:       OK (%s reachable)\n", report.Probe.Provider)
		} else {
			fmt.Printf("Probe:       FAILED (%s): %s\n", report.Probe.Provider, report.Probe.Error)
		}
	}

	if report.Reason != "" {
		fmt.Println()
		fmt.Printf("Note:        %s\n", report.Reason)
	}
	if len(report.FixHints) > 0 {
		fmt.Println("Fix:")
		for _, hint := range report.FixHints {
			fmt.Printf("  • %s\n", hint)
		}
	}
	if report.ConfiguredLLM && report.BackendClass == "llm" {
		fmt.Println()
		fmt.Println("Next:        herd agent prompt \"hello\"")
	}
}
