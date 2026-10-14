package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

func statusAction(jsonOutput bool) error {
	client, cfg, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), DefaultIPCTimeout)
	defer cancel()

	status, err := client.GetStatus(ctx)
	if err != nil {
		return DaemonUnreachableError(cfg, err)
	}

	if jsonOutput {
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
