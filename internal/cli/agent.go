package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"herd/internal/agent"
	"herd/internal/config"
	"herd/internal/ipc"
)

// RunAgent handles the "agent" CLI command and its subcommands.
func RunAgent(args []string) error {
	if len(args) == 0 {
		printAgentUsage()
		return nil
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "init":
		return runAgentInit(subArgs)
	case "prompt":
		return runAgentPrompt(subArgs)
	case "chat":
		return runAgentChat(subArgs)
	case "tools":
		return runAgentTools(subArgs)
	case "-h", "--help", "help":
		printAgentUsage()
		return nil
	default:
		// If first arg isn't a known subcommand, treat entire args as a prompt
		return runAgentPrompt(args)
	}
}

func printAgentUsage() {
	fmt.Println(`Usage:
  herd agent init [--dir <path>] [--force]
  herd agent prompt "<task>" [--target <node>] [--role <name>]
  herd agent chat [--target <node>] [--role <name>]
  herd agent tools

Commands:
  init      Scaffold starter AGENTS.md with default personas (coordinator, ops, researcher)
  prompt    Send a natural language task to the local or remote node agent
  chat      Start an interactive conversation with the node agent
  tools     List all available cluster tools registered on the agent

Flags:
  -dir <path>          Directory to initialize AGENTS.md in (for 'init')
  -force               Overwrite existing AGENTS.md (for 'init')
  -global              Initialize into user config directory (~/.config/herd/agents)
  -socket <path>       Custom daemon Unix domain socket
  -target <node>       Target a specific cluster node agent
  -model <name>        Override LLM model (e.g. gemini-3.8-flash, gemma2:2b)
  -role <name>         Activate specific declarative agent role (e.g. researcher, ops)`)
}

func runAgentInit(args []string) error {
	fs := flag.NewFlagSet("agent init", flag.ExitOnError)
	dir := fs.String("dir", ".", "Directory to scaffold AGENTS.md in")
	nodeName := fs.String("node-name", "", "Target node name to scaffold in node config directory")
	force := fs.Bool("force", false, "Overwrite existing AGENTS.md if present")
	global := fs.Bool("global", false, "Scaffold into global user config directory (~/.config/herd/agents)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	targetDir := *dir
	if *global {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		targetDir = filepath.Join(home, ".config", "herd", "agents")
	} else if *nodeName != "" {
		cfg, err := config.Load(config.Options{NodeName: *nodeName})
		if err != nil {
			return fmt.Errorf("failed to load node config: %w", err)
		}
		targetDir = filepath.Join(cfg.ConfigDir, "agents")
	}

	targetFile, created, err := agent.EnsureDefaultAgentsFile(targetDir, *force)
	if err != nil {
		return fmt.Errorf("failed to initialize agents markdown: %w", err)
	}

	if !created {
		fmt.Printf("Agent definitions already exist at %s (use --force to overwrite)\n", targetFile)
		return nil
	}

	fmt.Printf("Initialized default agent personas at %s\n", targetFile)
	fmt.Println("Available personas: coordinator, ops, researcher")
	fmt.Println("Run with: herd agent prompt \"<task>\" --role ops")
	return nil
}

func runAgentPrompt(args []string) error {
	fs := flag.NewFlagSet("agent prompt", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Node name to target socket for")
	socketPath := fs.String("socket", "", "Custom Unix domain socket path")
	targetNode := fs.String("target", "", "Target node name")
	model := fs.String("model", "", "Model name")
	role := fs.String("role", "", "Agent role or persona name (e.g. researcher, ops)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("prompt cannot be empty\nUsage: herd agent prompt \"<task>\"")
	}

	promptText := strings.Join(remaining, " ")

	cfg, err := config.Load(config.Options{
		NodeName:   *nodeName,
		SocketPath: *socketPath,
	})
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client := ipc.NewClient(cfg.SocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	fmt.Printf("Dispatching task to Herd Agent: %q\n\n", promptText)

	req := &ipc.AgentPromptRequest{
		Prompt:     promptText,
		TargetNode: *targetNode,
		Model:      *model,
		Role:       *role,
	}

	resp, err := client.AgentPrompt(ctx, req)
	if err != nil {
		return fmt.Errorf("agent prompt failed: %w", err)
	}

	if len(resp.ToolsCalled) > 0 {
		fmt.Println("Tools Executed:")
		for i, tc := range resp.ToolsCalled {
			fmt.Printf("  [%d] %s(%s)\n", i+1, tc.Name, tc.Arguments)
		}
		fmt.Println()
	}

	if resp.Response != "" {
		fmt.Println("Agent Response:")
		fmt.Println(resp.Response)
	}

	nodeStr := resp.NodeName
	if nodeStr == "" {
		nodeStr = cfg.NodeName
	}
	roleStr := resp.AgentRole
	if roleStr == "" {
		if *role != "" {
			roleStr = *role
		} else {
			roleStr = "coordinator"
		}
	}
	fmt.Printf("\n(Node: %s, Agent: %s, Model: %s, Time: %dms)\n", nodeStr, roleStr, resp.ModelUsed, resp.ExecutionMs)

	return nil
}

func runAgentChat(args []string) error {
	fs := flag.NewFlagSet("agent chat", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Node name to target socket for")
	socketPath := fs.String("socket", "", "Custom Unix domain socket path")
	targetNode := fs.String("target", "", "Target node name")
	model := fs.String("model", "", "Model name")
	role := fs.String("role", "", "Agent role or persona name (e.g. researcher, ops)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(config.Options{
		NodeName:   *nodeName,
		SocketPath: *socketPath,
	})
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client := ipc.NewClient(cfg.SocketPath)

	fmt.Println("Starting Herd Agent Interactive Chat Session (type 'exit' or 'quit' to end)")
	fmt.Println("-----------------------------------------------------------------------------")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("herd> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		req := &ipc.AgentPromptRequest{
			Prompt:     line,
			TargetNode: *targetNode,
			Model:      *model,
			Role:       *role,
		}

		resp, err := client.AgentPrompt(ctx, req)
		cancel()

		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}

		if len(resp.ToolsCalled) > 0 {
			for _, tc := range resp.ToolsCalled {
				fmt.Printf("  [tool] %s(%s)\n", tc.Name, tc.Arguments)
			}
		}

		fmt.Println(resp.Response)
		chatNode := resp.NodeName
		if chatNode == "" {
			chatNode = cfg.NodeName
		}
		chatRole := resp.AgentRole
		if chatRole == "" {
			if *role != "" {
				chatRole = *role
			} else {
				chatRole = "coordinator"
			}
		}
		fmt.Printf("(Node: %s, Agent: %s, Model: %s, Time: %dms)\n\n", chatNode, chatRole, resp.ModelUsed, resp.ExecutionMs)
	}

	return nil
}

func runAgentTools(args []string) error {
	tools := agent.BuildDefaultTools(nil)
	fmt.Println("=== Registered Herd Agent Tools ===")
	fmt.Println()
	for i, t := range tools {
		fmt.Printf("%d. %s\n", i+1, t.Name)
		fmt.Printf("   Description: %s\n", t.Description)
		if len(t.Parameters) > 0 {
			var params []string
			if props, ok := t.Parameters["properties"].(map[string]any); ok {
				for pName := range props {
					params = append(params, pName)
				}
			}
			if len(params) > 0 {
				fmt.Printf("   Parameters:  %s\n", strings.Join(params, ", "))
			}
		}
		fmt.Println()
	}
	return nil
}
