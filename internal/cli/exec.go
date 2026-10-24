package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/wnoronha/herd/internal/ipc"
)

// ExitCodeError is returned when a remote command exits with a non-zero code.
type ExitCodeError struct {
	Code int
}

func (e ExitCodeError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

func execAction(target, command string, cmdArgs []string, jsonOutput bool, timeoutSec int) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}
	client.SetTimeout(time.Duration(timeoutSec+5) * time.Second)

	ctx := context.Background()
	req := &ipc.ExecRequest{
		Target:         target,
		Command:        command,
		Args:           cmdArgs,
		TimeoutSeconds: timeoutSec,
	}

	resp, err := client.ExecCommand(ctx, req)
	if err != nil {
		return fmt.Errorf("herd exec failed: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	lastExitCode := 0
	for _, res := range resp.Results {
		if len(resp.Results) > 1 {
			fmt.Printf("=== [%s] ===\n", res.NodeName)
		}
		if res.Stdout != "" {
			fmt.Print(res.Stdout)
		}
		if res.Stderr != "" {
			fmt.Fprint(os.Stderr, res.Stderr)
		}
		if res.Error != "" {
			fmt.Fprintf(os.Stderr, "[error: %s]\n", res.Error)
		}
		if res.ExitCode != 0 {
			lastExitCode = res.ExitCode
		}
	}

	if lastExitCode != 0 {
		return ExitCodeError{Code: lastExitCode}
	}

	return nil
}
