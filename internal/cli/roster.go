package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// RunRoster executes the 'herd roster' command.
func RunRoster(args []string) error {
	fs := flag.NewFlagSet("roster", flag.ExitOnError)
	socketFlags := RegisterSocketFlags(fs)
	jsonOutput := fs.Bool("json", false, "Output roster as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	client, cfg, err := ConnectDaemon(socketFlags)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), DefaultIPCTimeout)
	defer cancel()

	roster, err := client.GetRoster(ctx)
	if err != nil {
		return DaemonUnreachableError(cfg, err)
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
