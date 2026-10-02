package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"herd/internal/config"
	"herd/internal/ipc"
)

// RunStatus executes the 'herd status' command.
func RunStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Node name to target socket for")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	jsonOutput := fs.Bool("json", false, "Output status as JSON")

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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	status, err := client.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("herd daemon is not running or unreachable at '%s'.\nStart the daemon with 'herd daemon': %w", cfg.SocketPath, err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}

	fmt.Println("=== Herd Node Status ===")
	fmt.Printf("Node Name:     %s\n", status.NodeName)
	fmt.Printf("State:         %s\n", status.State)
	fmt.Printf("Tailcat Addr:  %s\n", status.TailcatAddr)
	fmt.Printf("Bind Port:     %d\n", status.BindPort)
	fmt.Printf("Cluster Size:  %d member(s)\n", status.MemberCount)
	fmt.Printf("Uptime:        %ds\n", status.UptimeSeconds)
	if len(status.Metadata) > 0 {
		fmt.Printf("Metadata:      %+v\n", status.Metadata)
	}

	return nil
}
