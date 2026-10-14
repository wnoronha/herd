package cli

import (
	"context"
	"flag"
	"fmt"
	"time"
)

// RunJoin executes the 'herd join <address>' command.
func RunJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	socketFlags := RegisterSocketFlags(fs)

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: herd join [flags] <address:port>")
	}
	targetAddr := remaining[0]

	client, _, err := ConnectDaemon(socketFlags)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.JoinNode(ctx, targetAddr)
	if err != nil {
		return fmt.Errorf("failed to join node %s: %w", targetAddr, err)
	}

	fmt.Printf("Successfully joined cluster: %s (contacted %d node(s))\n", resp.Message, resp.JoinedNodes)
	return nil
}
