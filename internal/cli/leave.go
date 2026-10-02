package cli

import (
	"context"
	"flag"
	"fmt"
	"time"

	"herd/internal/config"
	"herd/internal/ipc"
)

// RunLeave executes the 'herd leave' command.
func RunLeave(args []string) error {
	fs := flag.NewFlagSet("leave", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Node name to target socket for")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.LeaveCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to leave cluster: %w", err)
	}

	fmt.Printf("Cluster departure result: %s\n", resp.Message)
	return nil
}
