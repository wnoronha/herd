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

// ExitCodeError is returned by RunExec when a remote command exits with a non-zero code.
// main() should call os.Exit with its Code field rather than printing an error message.
type ExitCodeError struct {
	Code int
}

func (e ExitCodeError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// RunExec executes the 'herd exec' command.
func RunExec(args []string) error {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	socketNodeName := fs.String("node-name", "", "Local node daemon to communicate with")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")
	timeoutSec := fs.Int("timeout", 30, "Command execution timeout in seconds")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 2 {
		return fmt.Errorf("usage: herd exec [flags] <node-name|all|tag:key=val> <command> [args...]")
	}

	target := remaining[0]
	command := remaining[1]
	cmdArgs := remaining[2:]

	cfg, err := config.Load(config.Options{
		NodeName:   *socketNodeName,
		SocketPath: *socketPath,
	})
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client := ipc.NewClient(cfg.SocketPath)
	client.SetTimeout(time.Duration(*timeoutSec+5) * time.Second)

	ctx := context.Background()
	req := &ipc.ExecRequest{
		Target:         target,
		Command:        command,
		Args:           cmdArgs,
		TimeoutSeconds: *timeoutSec,
	}

	resp, err := client.ExecCommand(ctx, req)
	if err != nil {
		return fmt.Errorf("herd exec failed: %w", err)
	}

	if *jsonOutput {
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
