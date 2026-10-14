package cli

import (
	"context"
	"flag"
	"fmt"
	"time"
)

// RunLeave executes the 'herd leave' command.
func RunLeave(args []string) error {
	fs := flag.NewFlagSet("leave", flag.ExitOnError)
	socketFlags := RegisterSocketFlags(fs)

	if err := fs.Parse(args); err != nil {
		return err
	}

	client, _, err := ConnectDaemon(socketFlags)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.LeaveCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to leave cluster: %w", err)
	}

	fmt.Printf("Cluster departure result: %s\n", resp.Message)
	return nil
}
