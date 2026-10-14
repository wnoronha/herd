package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/wnoronha/herd/internal/config"
	"github.com/wnoronha/herd/internal/ipc"
)

// SocketFlags are shared daemon socket / node-name flags for CLI commands.
type SocketFlags struct {
	NodeName   *string
	SocketPath *string
}

// RegisterSocketFlags adds --node-name and --socket to a command flag set.
func RegisterSocketFlags(fs *flag.FlagSet) SocketFlags {
	return SocketFlags{
		NodeName:   fs.String("node-name", "", "Local node daemon to communicate with"),
		SocketPath: fs.String("socket", "", "Daemon Unix domain socket path"),
	}
}

// ConnectDaemon resolves config and returns an IPC client for the local daemon.
func ConnectDaemon(flags SocketFlags) (*ipc.Client, *config.Config, error) {
	cfg, err := config.Load(config.Options{
		NodeName:   *flags.NodeName,
		SocketPath: *flags.SocketPath,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	return ipc.NewClient(cfg.SocketPath), cfg, nil
}

// DaemonUnreachableError formats a consistent message when the daemon socket is down.
func DaemonUnreachableError(cfg *config.Config, err error) error {
	return fmt.Errorf("herd daemon is not running or unreachable at '%s'.\nStart the daemon with 'herd daemon': %w", cfg.SocketPath, err)
}

// DefaultIPCTimeout is the standard CLI timeout for daemon status/roster calls.
const DefaultIPCTimeout = 3 * time.Second
