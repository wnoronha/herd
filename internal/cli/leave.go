package cli

import (
	"context"
	"fmt"
	"time"
)

func leaveAction() error {
	client, _, err := daemonClient()
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
