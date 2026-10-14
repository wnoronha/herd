package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(
		newStatusCmd(),
		newRosterCmd(),
		newJoinCmd(),
		newLeaveCmd(),
		newExecCmd(),
		newForwardCmd(),
		newCPCmd(),
		newKVCmd(),
		newAgentCmd(),
		newMailCmd(),
		newDocsCmd(),
	)
}

func newStatusCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check daemon status and cluster health",
		RunE: func(cmd *cobra.Command, args []string) error {
			return statusAction(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output status as JSON")
	return cmd
}

func newRosterCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "roster",
		Short: "List all known cluster peers and their state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return rosterAction(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output roster as JSON")
	return cmd
}

func newJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join <address>",
		Short: "Join a peer node by address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return joinAction(args[0])
		},
	}
}

func newLeaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "leave",
		Short: "Gracefully leave the cluster mesh",
		RunE: func(cmd *cobra.Command, args []string) error {
			return leaveAction()
		},
	}
}

func newExecCmd() *cobra.Command {
	var jsonOutput bool
	var timeoutSec int
	cmd := &cobra.Command{
		Use:   "exec <target> <command> [args...]",
		Short: "Execute a remote command across mesh nodes",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return execAction(args[0], args[1], args[2:], jsonOutput, timeoutSec)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	cmd.Flags().IntVar(&timeoutSec, "timeout", 30, "Command execution timeout in seconds")
	return cmd
}

func newForwardCmd() *cobra.Command {
	var bindHost string
	cmd := &cobra.Command{
		Use:   "forward <local-port> <target-node>:<target-port>",
		Short: "Set up dynamic port forwarding to a mesh peer",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			localPort, err := strconv.Atoi(args[0])
			if err != nil || localPort <= 0 {
				return fmt.Errorf("invalid local port: %s", args[0])
			}
			return forwardAction(localPort, args[1], bindHost)
		},
	}
	cmd.Flags().StringVar(&bindHost, "bind", "127.0.0.1", "Local IP interface to bind listener to")
	return cmd
}

func newCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cp <source> <destination>",
		Short: "Copy files across cluster nodes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cpAction(args[0], args[1])
		},
	}
}

func newKVCmd() *cobra.Command {
	kv := &cobra.Command{
		Use:   "kv",
		Short: "Distributed key-value store operations",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown kv subcommand %q (use get, set, delete, or list)", args[0])
			}
			return cmd.Help()
		},
	}
	kv.AddCommand(newKVGetCmd(), newKVSetCmd(), newKVDeleteCmd(), newKVListCmd())
	return kv
}

func newKVGetCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a key value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kvGetAction(args[0], jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func newKVSetCmd() *cobra.Command {
	var ttl string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a key value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kvSetAction(args[0], args[1], ttl, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&ttl, "ttl", "", "Optional TTL duration (e.g. 5m, 1h)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func newKVDeleteCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "delete <key>",
		Aliases: []string{"del", "rm"},
		Short:   "Delete a key",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kvDeleteAction(args[0], jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func newKVListCmd() *cobra.Command {
	var prefix string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List keys by prefix",
		RunE: func(cmd *cobra.Command, args []string) error {
			return kvListAction(prefix, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "Filter keys by prefix")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func newAgentCmd() *cobra.Command {
	agent := &cobra.Command{
		Use:   "agent",
		Short: "AI agent operations (prompt, chat, tools)",
	}
	agent.AddCommand(newAgentInitCmd(), newAgentPromptCmd(), newAgentChatCmd(), newAgentToolsCmd())
	return agent
}

func newAgentInitCmd() *cobra.Command {
	var dir string
	var forNode string
	var force bool
	var global bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold starter AGENTS.md with default personas",
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentInitAction(dir, forNode, force, global)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "Directory to scaffold AGENTS.md in")
	cmd.Flags().StringVar(&forNode, "for-node", "", "Scaffold into config directory for this node name")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing AGENTS.md")
	cmd.Flags().BoolVar(&global, "global", false, "Scaffold into ~/.config/herd/agents")
	return cmd
}

func newAgentPromptCmd() *cobra.Command {
	var targetNode, model, role string
	cmd := &cobra.Command{
		Use:   "prompt [task...]",
		Short: "Send a natural language task to the agent",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentPromptAction(strings.Join(args, " "), targetNode, model, role)
		},
	}
	cmd.Flags().StringVar(&targetNode, "target", "", "Target cluster node")
	cmd.Flags().StringVar(&model, "model", "", "Override LLM model")
	cmd.Flags().StringVar(&role, "role", "", "Agent role or persona name")
	return cmd
}

func newAgentChatCmd() *cobra.Command {
	var targetNode, model, role string
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Start an interactive agent conversation",
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentChatAction(targetNode, model, role)
		},
	}
	cmd.Flags().StringVar(&targetNode, "target", "", "Target cluster node")
	cmd.Flags().StringVar(&model, "model", "", "Override LLM model")
	cmd.Flags().StringVar(&role, "role", "", "Agent role or persona name")
	return cmd
}

func newAgentToolsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List registered cluster tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentToolsAction()
		},
	}
}

func newMailCmd() *cobra.Command {
	mail := &cobra.Command{
		Use:   "mail",
		Short: "Actor-style mailbox operations",
	}
	mail.AddCommand(newMailSendCmd(), newMailListCmd(), newMailReadCmd(), newMailWatchCmd())
	return mail
}

func newMailSendCmd() *cobra.Command {
	var topic, thread, ttl string
	var waitReply bool
	var waitTimeout time.Duration
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "send <to> <message...>",
		Short: "Send a message to a node mailbox",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			to := args[0]
			msg := strings.Join(args[1:], " ")
			return mailSendAction(to, msg, topic, thread, ttl, waitReply, waitTimeout, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&topic, "topic", "general", "Message topic")
	cmd.Flags().StringVar(&thread, "thread", "", "Conversation thread ID")
	cmd.Flags().StringVar(&ttl, "ttl", "1h", "Message TTL")
	cmd.Flags().BoolVar(&waitReply, "wait", false, "Wait for recipient reply")
	cmd.Flags().DurationVar(&waitTimeout, "timeout", 30*time.Second, "Wait timeout")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	return cmd
}

func newMailListCmd() *cobra.Command {
	var target, topic string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List mailbox messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mailListAction(target, topic, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "Mailbox target")
	cmd.Flags().StringVar(&topic, "topic", "", "Filter by topic")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	return cmd
}

func newMailReadCmd() *cobra.Command {
	var target, topic string
	var ack bool
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Read mailbox messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mailReadAction(target, topic, ack, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "Mailbox target")
	cmd.Flags().StringVar(&topic, "topic", "", "Filter by topic")
	cmd.Flags().BoolVar(&ack, "ack", false, "Acknowledge messages")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	return cmd
}

func newMailWatchCmd() *cobra.Command {
	var target, topic string
	var ack bool
	var interval time.Duration
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream new mailbox messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mailWatchAction(target, topic, ack, interval, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "Mailbox target")
	cmd.Flags().StringVar(&topic, "topic", "", "Filter by topic")
	cmd.Flags().BoolVar(&ack, "ack", false, "Acknowledge messages")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Poll interval")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	return cmd
}

func newDocsCmd() *cobra.Command {
	var exportDir string
	var all bool
	cmd := &cobra.Command{
		Use:   "docs [topic]",
		Short: "View embedded documentation guides",
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := ""
			if len(args) > 0 {
				topic = args[0]
			}
			return docsAction(topic, exportDir, all)
		},
	}
	cmd.Flags().StringVar(&exportDir, "export", "", "Export all docs to directory")
	cmd.Flags().BoolVar(&all, "all", false, "Print all guides")
	return cmd
}
