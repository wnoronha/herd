package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/wnoronha/herd/internal/ipc"
)

func mailSendAction(to, msgContent, topic, threadID, ttl string, waitReply bool, waitTimeout time.Duration, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &ipc.MailSendRequest{
		To:       to,
		Topic:    topic,
		Message:  msgContent,
		TTL:      ttl,
		ThreadID: threadID,
	}

	resp, err := client.MailSend(ctx, req)
	if err != nil {
		return fmt.Errorf("mail send failed: %w", err)
	}

	if !waitReply {
		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}
		fmt.Printf("Queued message %s -> %s (key: %s)\n", resp.ID, to, resp.Key)
		return nil
	}

	fmt.Printf("Message %s dispatched. Waiting for reply from %s (timeout: %s)...\n", resp.ID, to, waitTimeout)
	expectedThreadID := threadID
	if expectedThreadID == "" {
		expectedThreadID = resp.ID
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), waitTimeout)
	defer waitCancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for reply after %s", waitTimeout)
		case <-ticker.C:
			readResp, err := client.MailRead(waitCtx, &ipc.MailReadRequest{
				Ack: true,
			})
			if err != nil {
				continue
			}
			for _, m := range readResp.Messages {
				if m.ThreadID == expectedThreadID || m.ID == "reply_"+resp.ID {
					if jsonOutput {
						enc := json.NewEncoder(os.Stdout)
						enc.SetIndent("", "  ")
						return enc.Encode(m)
					}
					fmt.Printf("\n--- Reply from %s (Thread: %s) ---\n", m.From, m.ThreadID)
					fmt.Println(m.Message)
					return nil
				}
			}
		}
	}
}

func mailListAction(target, topic string, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.MailList(ctx, &ipc.MailListRequest{
		Target: target,
		Topic:  topic,
	})
	if err != nil {
		return fmt.Errorf("mail list failed: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Messages) == 0 {
		fmt.Println("No messages in mailbox.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tFROM\tTO\tTOPIC\tTHREAD\tAGE\tPREVIEW")
	now := time.Now().Unix()
	for _, m := range resp.Messages {
		age := fmt.Sprintf("%ds", now-m.CreatedAt)
		preview := strings.ReplaceAll(m.Message, "\n", " ")
		if len(preview) > 40 {
			preview = preview[:37] + "..."
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID, m.From, m.To, m.Topic, m.ThreadID, age, preview)
	}
	return w.Flush()
}

func mailReadAction(target, topic string, ack, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.MailRead(ctx, &ipc.MailReadRequest{
		Target: target,
		Topic:  topic,
		Ack:    ack,
	})
	if err != nil {
		return fmt.Errorf("mail read failed: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Messages) == 0 {
		fmt.Println("No messages in mailbox.")
		return nil
	}

	for i, m := range resp.Messages {
		fmt.Printf("=== [%d/%d] Message: %s ===\n", i+1, len(resp.Messages), m.ID)
		fmt.Printf("From:      %s\n", m.From)
		fmt.Printf("To:        %s\n", m.To)
		fmt.Printf("Topic:     %s\n", m.Topic)
		if m.ThreadID != "" {
			fmt.Printf("Thread:    %s\n", m.ThreadID)
		}
		if m.ReplyTo != "" {
			fmt.Printf("ReplyTo:   %s\n", m.ReplyTo)
		}
		fmt.Printf("Date:      %s\n", time.Unix(m.CreatedAt, 0).Format(time.RFC3339))
		fmt.Printf("\n%s\n\n", m.Message)
	}

	if ack {
		fmt.Printf("Acknowledged and removed %d messages.\n", resp.Count)
	}
	return nil
}

func mailWatchAction(target, topic string, ack bool, interval time.Duration, jsonOutput bool) error {
	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	targetName := target
	if targetName == "" {
		targetName = "local node"
	}
	fmt.Printf("Watching mailbox for %s (interval: %s, Ctrl+C to stop)...\n", targetName, interval)

	seen := make(map[string]bool)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopping mailbox watcher.")
			return nil
		case <-ticker.C:
			req := &ipc.MailReadRequest{
				Target: target,
				Topic:  topic,
				Ack:    ack,
			}
			resp, err := client.MailRead(ctx, req)
			if err != nil {
				continue
			}

			for _, m := range resp.Messages {
				if !ack && seen[m.ID] {
					continue
				}
				seen[m.ID] = true

				if jsonOutput {
					data, _ := json.Marshal(m)
					fmt.Println(string(data))
				} else {
					fmt.Printf("[%s] From: %s | Topic: %s | Thread: %s\n%s\n---\n",
						time.Unix(m.CreatedAt, 0).Format("15:04:05"),
						m.From, m.Topic, m.ThreadID, m.Message)
				}
			}
		}
	}
}
