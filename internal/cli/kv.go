package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

func kvGetAction(key string, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVGet(ctx, key)
	if err != nil {
		return fmt.Errorf("kv get failed: %w", err)
	}

	if jsonOutput {
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

func kvSetAction(key, value, ttl string, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVSet(ctx, key, value, ttl)
	if err != nil {
		return fmt.Errorf("kv set failed: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("OK (version %d)\n", resp.Version)
	return nil
}

func kvDeleteAction(key string, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVDelete(ctx, key)
	if err != nil {
		return fmt.Errorf("kv delete failed: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("Deleted %q\n", key)
	return nil
}

func kvListAction(prefix string, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.KVList(ctx, prefix)
	if err != nil {
		return fmt.Errorf("kv list failed: %w", err)
	}

	if jsonOutput {
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
