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
	fmt.Println("Commands: /tools list tools, /history show turns, /new start session, /resume <id> resume session, /help help")
	fmt.Println("-----------------------------------------------------------------------------")

	// One session UUID for the whole interactive loop so every turn shares
	// the same ADK InMemoryService conversation.
	chatSessionID := newChatSessionID()
	fmt.Printf("Session: %s\n", chatSessionID)
	var historyLog []string

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
		if cmd, ok := parseChatCommand(line); ok {
			chatSessionID, historyLog = runChatCommand(cmd, chatSessionID, historyLog)
			continue
		}

		historyLog = appendChatHistory(historyLog, "user: "+line)
		resp, err := sendChatPrompt(client, targetNode, model, role, chatSessionID, line, nil)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}
		historyLog = logChatTurn(historyLog, resp)
		printChatFooter(cfg, role, chatSessionID, resp)

		// A blocked destructive call waits for yes or no. The answer
		// travels in the next request for the same session.
		if resp.Confirmation != nil {
			fmt.Printf("\nConfirmation required: %s(%s)\n", resp.Confirmation.ToolName, resp.Confirmation.Arguments)
			fmt.Printf("  %s\n", resp.Confirmation.Hint)
			fmt.Print("Approve and run once? [y/N]: ")
			if !scanner.Scan() {
				break
			}
			approved := parseApprovalAnswer(scanner.Text())
			decision := "declined"
			if approved {
				decision = "approved"
			}
			historyLog = appendChatHistory(historyLog, "user: "+decision+" "+resp.Confirmation.ToolName)
			resp, err = sendChatPrompt(client, targetNode, model, role, chatSessionID, line, &approved)
			if err != nil {
				fmt.Printf("Error: %v\n\n", err)
				continue
			}
			historyLog = logChatTurn(historyLog, resp)
			printChatFooter(cfg, role, chatSessionID, resp)
		}
	}

	return nil
}

// maxChatHistory bounds the local /history log.
const maxChatHistory = 200

// appendChatHistory records one line, dropping the oldest beyond the cap.
func appendChatHistory(log []string, entry string) []string {
	log = append(log, entry)
	if len(log) > maxChatHistory {
		log = log[len(log)-maxChatHistory:]
	}
	return log
}

// chatCommand names one slash command plus its optional argument.
type chatCommand struct {
	name string
	arg  string
}

// parseChatCommand splits "/resume <id>" into name and argument. It reports
// false for plain prompts so they reach the agent.
func parseChatCommand(line string) (chatCommand, bool) {
	if !strings.HasPrefix(line, "/") {
		return chatCommand{}, false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return chatCommand{}, false
	}
	cmd := chatCommand{name: strings.ToLower(strings.TrimPrefix(fields[0], "/"))}
	if len(fields) > 1 {
		cmd.arg = strings.TrimSpace(strings.TrimPrefix(line[len(fields[0]):], " "))
	}
	switch cmd.name {
	case "tools", "history", "new", "resume", "help":
		return cmd, true
	default:
		return chatCommand{}, false
	}
}

// parseApprovalAnswer maps "y" or "yes" to true. Every other answer,
// including an empty line, declines.
func parseApprovalAnswer(input string) bool {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// runChatCommand executes one slash command and returns the updated session
// ID and history log.
func runChatCommand(cmd chatCommand, chatSessionID string, historyLog []string) (string, []string) {
	switch cmd.name {
	case "tools":
		_ = agentToolsAction()
	case "history":
		if len(historyLog) == 0 {
			fmt.Println("(no turns yet)")
			break
		}
		for i, entry := range historyLog {
			fmt.Printf("  [%d] %s\n", i+1, entry)
		}
	case "new":
		chatSessionID = newChatSessionID()
		historyLog = nil
		fmt.Printf("New session: %s\n", chatSessionID)
	case "resume":
		if cmd.arg == "" {
			fmt.Println("Usage: /resume <session-id>")
			break
		}
		chatSessionID = cmd.arg
		fmt.Printf("Resumed session: %s (the daemon must still hold it)\n", chatSessionID)
	case "help":
		fmt.Println("Commands: /tools /history /new /resume <id> /help; exit quit")
	}
	return chatSessionID, historyLog
}

// sendChatPrompt dispatches one chat turn with an optional yes or no answer
// to a pending confirmation prompt. Partial text and tool calls render as
// they arrive over the streaming IPC channel.
func sendChatPrompt(client *ipc.Client, targetNode, model, role, chatSessionID, line string, approved *bool) (*ipc.AgentPromptResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := &ipc.AgentPromptRequest{
		Prompt:       line,
		TargetNode:   targetNode,
		Model:        model,
		Role:         role,
		SessionID:    chatSessionID,
		ToolApproved: approved,
	}
	return client.AgentPromptStream(ctx, req, func(chunk ipc.AgentPromptChunk) error {
		if chunk.ToolCall != nil {
			fmt.Printf("\n  [tool] %s(%s)\n", chunk.ToolCall.Name, chunk.ToolCall.Arguments)
			return nil
		}
		if chunk.TextDelta != "" {
			fmt.Print(chunk.TextDelta)
		}
		return nil
	})
}

// logChatTurn records tools and text in the history log without reprinting
// them: the streaming channel already rendered them live.
func logChatTurn(historyLog []string, resp *ipc.AgentPromptResponse) []string {
	for _, tc := range resp.ToolsCalled {
		historyLog = appendChatHistory(historyLog, fmt.Sprintf("tool: %s(%s)", tc.Name, tc.Arguments))
	}
	return appendChatHistory(historyLog, "agent: "+resp.Response)
}

// printChatFooter closes one streamed turn with node, agent, model, time,
// and session details.
func printChatFooter(cfg *config.Config, role, chatSessionID string, resp *ipc.AgentPromptResponse) {
	fmt.Println()
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
	fmt.Printf("(Node: %s, Agent: %s, Model: %s, Time: %dms, Session: %s)\n\n", chatNode, chatRole, resp.ModelUsed, resp.ExecutionMs, chatSessionID)
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
