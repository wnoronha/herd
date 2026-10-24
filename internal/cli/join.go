package cli

import (
	"context"
	"fmt"
	"time"
)

func joinAction(targetAddr string) error {
	client, _, err := daemonClient()
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
