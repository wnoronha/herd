package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"herd/internal/config"
	"herd/internal/ipc"
)

// RunKV executes the 'herd kv' subcommands: get, set, delete, list.
func RunKV(args []string) error {
	if len(args) == 0 {
		printKVUsage()
		return fmt.Errorf("subcommand required: get, set, delete, or list")
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "get":
		return runKVGet(subArgs)
	case "set":
		return runKVSet(subArgs)
	case "delete", "del", "rm":
		return runKVDelete(subArgs)
	case "list", "ls":
		return runKVList(subArgs)
	case "help", "-h", "--help":
		printKVUsage()
		return nil
	default:
		printKVUsage()
		return fmt.Errorf("unknown kv subcommand: %s", subcmd)
	}
}

func printKVUsage() {
	fmt.Println(`Usage:
  herd kv get <key> [flags]
  herd kv set <key> <value> [flags]
  herd kv delete <key> [flags]
  herd kv list [flags]

Flags:
  --socket <path>       Daemon Unix domain socket path
  --node-name <name>    Local node name
  --ttl <duration>      Time-to-live expiration for set (e.g. 10m, 1h)
  --prefix <prefix>     Key prefix filter for list
  --json                Output results in JSON format`)
}

func getIPCClient(socketNodeName, socketPath string) (*ipc.Client, error) {
	cfg, err := config.Load(config.Options{
		NodeName:   socketNodeName,
		SocketPath: socketPath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return ipc.NewClient(cfg.SocketPath), nil
}

func runKVGet(args []string) error {
	fs := flag.NewFlagSet("kv get", flag.ExitOnError)
	socketNodeName := fs.String("node-name", "", "Local node daemon to communicate with")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: herd kv get <key>")
	}
	key := remaining[0]

	client, err := getIPCClient(*socketNodeName, *socketPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVGet(ctx, key)
	if err != nil {
		return fmt.Errorf("kv get failed: %w", err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if !resp.Found || resp.Entry == nil {
		return fmt.Errorf("key %q not found", key)
	}

	fmt.Println(resp.Entry.Value)
	return nil
}

func runKVSet(args []string) error {
	fs := flag.NewFlagSet("kv set", flag.ExitOnError)
	socketNodeName := fs.String("node-name", "", "Local node daemon to communicate with")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	ttl := fs.String("ttl", "", "Optional TTL duration (e.g. 5m, 1h)")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 2 {
		return fmt.Errorf("usage: herd kv set <key> <value> [--ttl duration]")
	}
	key := remaining[0]
	value := remaining[1]

	client, err := getIPCClient(*socketNodeName, *socketPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVSet(ctx, key, value, *ttl)
	if err != nil {
		return fmt.Errorf("kv set failed: %w", err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("OK (version %d)\n", resp.Version)
	return nil
}

func runKVDelete(args []string) error {
	fs := flag.NewFlagSet("kv delete", flag.ExitOnError)
	socketNodeName := fs.String("node-name", "", "Local node daemon to communicate with")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: herd kv delete <key>")
	}
	key := remaining[0]

	client, err := getIPCClient(*socketNodeName, *socketPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVDelete(ctx, key)
	if err != nil {
		return fmt.Errorf("kv delete failed: %w", err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("Deleted %q\n", key)
	return nil
}

func runKVList(args []string) error {
	fs := flag.NewFlagSet("kv list", flag.ExitOnError)
	socketNodeName := fs.String("node-name", "", "Local node daemon to communicate with")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	prefix := fs.String("prefix", "", "Filter keys by prefix")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := getIPCClient(*socketNodeName, *socketPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVList(ctx, *prefix)
	if err != nil {
		return fmt.Errorf("kv list failed: %w", err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Entries) == 0 {
		fmt.Println("No keys found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "KEY\tVALUE\tVERSION\tWRITER")
	for _, e := range resp.Entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", e.Key, e.Value, e.Version, e.WriterNodeID)
	}
	_ = w.Flush()
	return nil
}
