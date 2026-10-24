package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Version is set at build time; default for dev builds.
var Version = "dev"

// RootCmd is the base command for the Herd CLI.
var RootCmd = &cobra.Command{
	Use:   "herd",
	Short: "Peer-to-peer mesh clustering daemon and multi-tool CLI",
	Long: `Herd forms decentralized P2P clusters with SWIM gossip, distributed KV,
remote exec, and an embedded AI agent runtime.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var (
	rootNodeName string
	rootSocket   string
)

func init() {
	RootCmd.PersistentFlags().StringVar(&rootNodeName, "node-name", "", "Local node daemon to communicate with")
	RootCmd.PersistentFlags().StringVar(&rootSocket, "socket", "", "Daemon Unix domain socket path")

	RootCmd.AddCommand(newCompletionCmd())
	RootCmd.Version = Version
	RootCmd.SetVersionTemplate("herd {{.Version}}\n")
}

// Execute runs the Cobra command tree.
func Execute() error {
	if len(os.Args) <= 1 {
		return RootCmd.Help()
	}
	RootCmd.SetArgs(os.Args[1:])
	return RootCmd.Execute()
}

func rootSocketFlags() SocketFlags {
	return SocketFlags{
		NodeName:   &rootNodeName,
		SocketPath: &rootSocket,
	}
}

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return RootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				return RootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				return RootCmd.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return RootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell %q (use bash, zsh, fish, or powershell)", args[0])
			}
		},
	}
}

// ExecuteArgs runs the CLI with the given args (for tests).
func ExecuteArgs(args []string) error {
	resetCLIFlags(RootCmd)
	RootCmd.SetArgs(args)
	return RootCmd.Execute()
}

func resetCLIFlags(cmd *cobra.Command) {
	rootNodeName = ""
	rootSocket = ""
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range cmd.Commands() {
		resetCLIFlags(sub)
	}
}
