package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"herd/internal/agent"
	"herd/internal/kv"
	"herd/internal/mailbox"
)

// runMailboxWatcher runs the background reactive event loop watching for incoming messages
// in the node's Mailbox and broadcast address.
func (d *Daemon) runMailboxWatcher(ctx context.Context) {
	defer d.agentWg.Done()
	if d.KVStore == nil || d.AgentEngine == nil {
		return
	}

	nodeName := d.Config.NodeName
	prefixes := []string{
		mailbox.PrefixFor(nodeName), // e.g. "mailbox:node-1/"
		mailbox.PrefixFor("*"),      // e.g. "mailbox:*/" (broadcast)
	}

	// 1. Process any pre-existing unhandled mailbox tasks on startup
	for _, prefix := range prefixes {
		existing := d.KVStore.List(prefix)
		for _, entry := range existing {
			d.processMailboxEntry(ctx, entry)
		}
	}

	// 2. Launch watchers for each prefix and multiplex entries into a unified channel
	mergedCh := make(chan *kv.Entry, 64)
	var watcherWg sync.WaitGroup

	for _, p := range prefixes {
		watchCh, cancelWatch := d.KVStore.Watch(p)
		defer cancelWatch()

		watcherWg.Add(1)
		go func(ch <-chan *kv.Entry) {
			defer watcherWg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case entry, ok := <-ch:
					if !ok {
						return
					}
					select {
					case mergedCh <- entry:
					case <-ctx.Done():
						return
					}
				}
			}
		}(watchCh)
	}

	// 3. Real-time reactive loop: process messages as they arrive across any watched mailbox prefix
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-mergedCh:
			if !ok {
				return
			}
			d.processMailboxEntry(ctx, entry)
		}
	}
}

// runAgentInboxWatcher is a backward-compatible alias for runMailboxWatcher.
func (d *Daemon) runAgentInboxWatcher(ctx context.Context) {
	d.runMailboxWatcher(ctx)
}

// processMailboxEntry parses, deduplicates, executes, and acknowledges an incoming message entry.
func (d *Daemon) processMailboxEntry(ctx context.Context, entry *kv.Entry) {
	if entry == nil || entry.Tombstone || entry.IsExpired(time.Now()) {
		return
	}
	if len(entry.Value) == 0 {
		return
	}

	var msg mailbox.Message
	if err := json.Unmarshal(entry.Value, &msg); err != nil {
		return
	}

	normTo := mailbox.NormalizeAddress(msg.To)
	if normTo != d.Config.NodeName && normTo != "*" && normTo != "all" {
		return
	}

	// Deduplicate in case of duplicate gossip deliveries
	if _, loaded := d.processedMsgs.LoadOrStore(msg.ID, true); loaded {
		return
	}

	// Do not dispatch replies to reply messages (prevents infinite ping-pong loops)
	if strings.HasPrefix(msg.Topic, "reply:") || strings.HasPrefix(msg.ID, "reply_") {
		d.Logger.Named("mailbox").Infof("Received reply for task %s from '%s'", msg.ID, msg.From)
		return
	}

	d.Logger.Named("mailbox").Infof("Waking up agent '%s' to process task %s from '%s' (topic: %s, thread: %s)",
		d.Config.NodeName, msg.ID, msg.From, msg.Topic, msg.ThreadID)

	// Acknowledge by deleting message from mailbox
	if d.KVDelegate != nil {
		d.KVDelegate.DeleteAndBroadcast(entry.Key)
	} else if d.KVStore != nil {
		d.KVStore.Delete(entry.Key)
	}

	taskPrompt := fmt.Sprintf("Incoming task from '%s' on topic '%s' (thread: %s):\n%s",
		msg.From, msg.Topic, msg.ThreadID, msg.Message)
	execCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	var role string
	if strings.HasPrefix(msg.Topic, "task:") {
		role = strings.TrimPrefix(msg.Topic, "task:")
	} else if strings.HasPrefix(msg.Topic, "role:") {
		role = strings.TrimPrefix(msg.Topic, "role:")
	}

	var resp *agent.PromptResponse
	var err error
	if d.AgentEngine != nil {
		resp, err = d.AgentEngine.ExecutePrompt(execCtx, &agent.PromptRequest{
			Prompt: taskPrompt,
			Role:   role,
		})
	}

	var replyContent string
	if err != nil {
		replyContent = fmt.Sprintf("Error executing task: %v", err)
		d.Logger.Named("mailbox").Errorf("Task execution error for %s: %v", msg.ID, err)
	} else if resp != nil {
		replyContent = resp.Response
		d.Logger.Named("mailbox").Infof("Task %s completed successfully", msg.ID)
	}

	// Symmetric reply routing: deliver reply directly to sender's mailbox
	reply := msg.CreateReply(d.Config.NodeName, replyContent)

	replyKey := mailbox.KeyFor(reply.To, reply.ID)
	if msg.ReplyTo != "" {
		replyKey = msg.ReplyTo
	}

	if replyBytes, err := json.Marshal(reply); err == nil {
		if d.KVDelegate != nil {
			d.KVDelegate.SetAndBroadcast(replyKey, replyBytes, 1*time.Hour)
		} else if d.KVStore != nil {
			d.KVStore.Set(replyKey, replyBytes, 1*time.Hour)
		}
		d.Logger.Named("mailbox").Infof("Dispatched reply for message %s to %s", msg.ID, replyKey)
	}
}

