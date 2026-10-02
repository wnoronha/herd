package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"herd/internal/config"
	"herd/internal/ipc"
)

// RunRoster executes the 'herd roster' command.
func RunRoster(args []string) error {
	fs := flag.NewFlagSet("roster", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Node name to target socket for")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	jsonOutput := fs.Bool("json", false, "Output roster as JSON")

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

	roster, err := client.GetRoster(ctx)
	if err != nil {
		return fmt.Errorf("herd daemon is not running or unreachable at '%s'.\nStart the daemon with 'herd daemon': %w", cfg.SocketPath, err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(roster)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "NODE NAME\tSTATUS\tENDPOINT\tMETADATA")
	for _, m := range roster.Members {
		endpoint := "-"
		if m.Addr != "" && m.Port > 0 {
			endpoint = fmt.Sprintf("%s:%d", m.Addr, m.Port)
		}
		var metaList []string
		for k, v := range m.Metadata {
			metaList = append(metaList, fmt.Sprintf("%s=%s", k, v))
		}
		metaStr := "-"
		if len(metaList) > 0 {
			metaStr = strings.Join(metaList, ",")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Name, m.Status, endpoint, metaStr)
	}
	_ = w.Flush()

	return nil
}
