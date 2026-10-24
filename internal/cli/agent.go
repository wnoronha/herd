package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wnoronha/herd/internal/agent"
	"github.com/wnoronha/herd/internal/config"
	"github.com/wnoronha/herd/internal/ipc"
)

// newChatSessionID generates a unique ID for one interactive chat session.
func newChatSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("chat-%d", time.Now().UnixNano())
	}
	return "chat-" + hex.EncodeToString(b[:])
}

func agentInitAction(dir, forNode string, force, global bool) error {
	targetDir := dir
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		targetDir = filepath.Join(home, ".config", "herd", "agents")
	} else if forNode != "" {
		cfg, err := config.Load(config.Options{NodeName: forNode})
		if err != nil {
			return fmt.Errorf("failed to load node config: %w", err)
		}
		targetDir = filepath.Join(cfg.ConfigDir, "agents")
	}

	targetFile, created, err := agent.EnsureDefaultAgentsFile(targetDir, force)
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

func agentPromptAction(promptText, targetNode, model, role string) error {
	client, cfg, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	fmt.Printf("Dispatching task to Herd Agent: %q\n\n", promptText)

	req := &ipc.AgentPromptRequest{
		Prompt:     promptText,
		TargetNode: targetNode,
		Model:      model,
		Role:       role,
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
		if role != "" {
			roleStr = role
		} else {
			roleStr = "coordinator"
		}
	}
	fmt.Printf("\n(Node: %s, Agent: %s, Model: %s, Time: %dms)\n", nodeStr, roleStr, resp.ModelUsed, resp.ExecutionMs)

	return nil
}

func agentChatAction(targetNode, model, role string) error {
	client, cfg, err := daemonClient()
	if err != nil {
		return err
	}

	fmt.Println("Starting Herd Agent Interactive Chat Session (type 'exit' or 'quit' to end)")
	fmt.Println("-----------------------------------------------------------------------------")

	// One session UUID for the whole interactive loop so every turn shares
	// the same ADK InMemoryService conversation.
	chatSessionID := newChatSessionID()

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
			TargetNode: targetNode,
			Model:      model,
			Role:       role,
			SessionID:  chatSessionID,
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
			if role != "" {
				chatRole = role
			} else {
				chatRole = "coordinator"
			}
		}
		fmt.Printf("(Node: %s, Agent: %s, Model: %s, Time: %dms)\n\n", chatNode, chatRole, resp.ModelUsed, resp.ExecutionMs)
	}

	return nil
}

func agentToolsAction() error {
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
