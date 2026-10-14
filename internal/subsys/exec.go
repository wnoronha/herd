package subsys

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/wnoronha/herd/internal/roster"
)

// ExecRequest specifies command execution parameters across nodes.
type ExecRequest struct {
	Target         string   `json:"target"` // "node-name", "all", or "tag:key=value"
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

// NodeExecResult captures the execution output and exit status for a single node.
type NodeExecResult struct {
	NodeName string `json:"node_name"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// ExecResponse aggregates execution results from all targeted nodes.
type ExecResponse struct {
	Results []NodeExecResult `json:"results"`
}

// ExecuteLocal runs a command locally on the current host.
func ExecuteLocal(ctx context.Context, nodeName string, command string, args []string, timeout time.Duration) NodeExecResult {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := createCommand(execCtx, command, args)
	cmd.Env = append(os.Environ(), "HERD_NODE_NAME="+nodeName)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0
	errMsg := ""

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if stderrBuf.Len() > 0 {
				errMsg = strings.TrimSpace(stderrBuf.String())
			} else {
				errMsg = exitErr.Error()
			}
		} else {
			exitCode = 1
			errMsg = err.Error()
		}
	}

	return NodeExecResult{
		NodeName: nodeName,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
		Error:    errMsg,
	}
}

// TargetNodes resolves which nodes from the roster match the target expression.
func TargetNodes(target string, localName string, store *roster.Store) []string {
	if target == "" || target == localName {
		return []string{localName}
	}

	if target == "all" {
		if store == nil {
			return []string{localName}
		}
		members := store.GetMembers()
		var res []string
		for _, m := range members {
			if m.Status == "alive" {
				res = append(res, m.Name)
			}
		}
		if len(res) == 0 {
			return []string{localName}
		}
		return res
	}

	if strings.HasPrefix(target, "tag:") {
		tagExpr := strings.TrimPrefix(target, "tag:")
		parts := strings.SplitN(tagExpr, "=", 2)
		tagKey := parts[0]
		tagVal := ""
		if len(parts) == 2 {
			tagVal = parts[1]
		}

		if store == nil {
			return nil
		}
		var res []string
		for _, m := range store.GetMembers() {
			if m.Status != "alive" || m.Meta == nil {
				continue
			}
			if v, ok := m.Meta.Tags[tagKey]; ok {
				if tagVal == "" || v == tagVal {
					res = append(res, m.Name)
				}
			}
		}
		return res
	}

	return []string{target}
}

// createCommand creates an OS-appropriate command execution context.
func createCommand(ctx context.Context, command string, args []string) *exec.Cmd {
	if len(args) > 0 {
		return exec.CommandContext(ctx, command, args...) //nolint:gosec // remote command execution is herd's core product surface; commands arrive via authenticated mesh peers and exec policy
	}

	if runtime.GOOS == "windows" {
		// On Windows, prefer PowerShell if available, fallback to cmd.exe
		if _, err := exec.LookPath("powershell.exe"); err == nil {
			return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command) //nolint:gosec // fixed interpreter + args; only the script text is remote-supplied (see above)
		}
		return exec.CommandContext(ctx, "cmd.exe", "/C", command) //nolint:gosec // fixed interpreter + args; only the script text is remote-supplied (see above)
	}

	// On Unix (Linux, macOS, BSDs), /bin/sh is standard POSIX
	if _, err := exec.LookPath("sh"); err == nil {
		return exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // fixed interpreter + args; only the script text is remote-supplied (see above)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command) //nolint:gosec // fixed interpreter + args; only the script text is remote-supplied (see above)
}
