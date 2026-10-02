package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"herd/internal/ipc"
	"herd/internal/kv"
	"herd/internal/mailbox"
	"herd/internal/roster"
)

// ClusterContext provides access to daemon cluster primitives for tool execution.
type ClusterContext interface {
	GetNodeName() string
	GetRosterStore() *roster.Store
	GetKVStore() *kv.Store
	GetKVDelegate() *kv.GossipDelegate
	ExecuteCommand(ctx context.Context, req *ipc.ExecRequest) (*ipc.ExecResponse, error)
	CopyFile(ctx context.Context, req *ipc.CPRequest) (*ipc.CPResponse, error)
	GetDataDir() string
	JoinNode(ctx context.Context, addr string) (*ipc.JoinResponse, error)
	LeaveCluster(ctx context.Context) (*ipc.LeaveResponse, error)
	GetStatus(ctx context.Context) (*ipc.StatusResponse, error)
}

// BuildDefaultTools constructs the full suite of cluster & inter-agent tools.
func BuildDefaultTools(cctx ClusterContext) []ToolDefinition {
	return []ToolDefinition{
		buildClusterRosterTool(cctx),
		buildClusterExecTool(cctx),
		buildClusterCPTool(cctx),
		buildSharedKVTool(cctx),
		buildAgentSendMailTool(cctx),
		buildAgentBroadcastTool(cctx),
		buildAgentReadMailboxTool(cctx),
		buildFetchURLTool(),
		buildGoogleSearchTool(),
		buildFileReadTool(),
		buildFileWriteTool(),
		buildArtifactStoreTool(cctx),
		buildClusterForwardTool(cctx),
		buildClusterManageTool(cctx),
		buildRunAgentTool(nil),
	}
}

func buildClusterRosterTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "cluster_roster",
		Description: "Discover all known cluster mesh nodes, their status (alive, suspect, left), Tailcat overlay endpoints, and round-trip ping latencies.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter_status": map[string]any{
					"type":        "string",
					"description": "Optional filter by node status: 'alive', 'suspect', or 'all' (default: 'alive')",
				},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			store := cctx.GetRosterStore()
			if store == nil {
				return nil, fmt.Errorf("roster store not available")
			}

			filter := "alive"
			if f, ok := args["filter_status"].(string); ok && f != "" {
				filter = f
			}

			members := store.GetMembers()
			type NodeInfo struct {
				Name      string `json:"name"`
				Status    string `json:"status"`
				Address   string `json:"address"`
				Port      uint16 `json:"port"`
				RTTMs     int64  `json:"rtt_ms,omitempty"`
				IsSelf    bool   `json:"is_self"`
			}

			var res []NodeInfo
			selfName := cctx.GetNodeName()
			for _, m := range members {
				if filter != "all" && m.Status != filter {
					continue
				}
				res = append(res, NodeInfo{
					Name:    m.Name,
					Status:  m.Status,
					Address: m.Addr,
					Port:    m.Port,
					RTTMs:   m.LastRTT.Milliseconds(),
					IsSelf:  m.Name == selfName,
				})
			}
			return res, nil
		},
	}
}

func buildClusterExecTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "cluster_exec",
		Description: "Execute a shell command on a specific cluster node or across all nodes in the mesh over encrypted Tailcat streams.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type":        "string",
					"description": "Target node name (e.g. 'node-1', 'node-2', or 'all')",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run (e.g. 'uname -a', 'uptime', 'df -h')",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional timeout in seconds (default: 30)",
				},
			},
			"required": []string{"target", "command"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			target, _ := args["target"].(string)
			command, _ := args["command"].(string)
			if target == "" || command == "" {
				return nil, fmt.Errorf("both 'target' and 'command' parameters are required")
			}

			timeoutSec := 30
			if t, ok := args["timeout_seconds"].(float64); ok && t > 0 {
				timeoutSec = int(t)
			}

			req := &ipc.ExecRequest{
				Target:         target,
				Command:        command,
				TimeoutSeconds: timeoutSec,
			}

			resp, err := cctx.ExecuteCommand(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("exec failed: %w", err)
			}
			return resp.Results, nil
		},
	}
}

func buildClusterCPTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "cluster_cp",
		Description: "Copy files across cluster nodes or locally over encrypted Tailcat channels. Paths can be local paths (e.g. '/tmp/file.txt') or node-prefixed (e.g. 'node-1:/tmp/file.txt').",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "Source file path, optionally prefixed with node name (e.g. '/tmp/data.csv' or 'node-1:/tmp/data.csv')",
				},
				"destination": map[string]any{
					"type":        "string",
					"description": "Destination file path, optionally prefixed with node name (e.g. '/tmp/data.csv' or 'node-2:/tmp/data.csv')",
				},
			},
			"required": []string{"source", "destination"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			source, _ := args["source"].(string)
			destination, _ := args["destination"].(string)
			if source == "" || destination == "" {
				return nil, fmt.Errorf("both 'source' and 'destination' parameters are required")
			}

			req := &ipc.CPRequest{
				Source:      source,
				Destination: destination,
			}

			resp, err := cctx.CopyFile(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("file copy failed: %w", err)
			}
			return map[string]any{
				"status":            "success",
				"bytes_transferred": resp.BytesTransferred,
				"message":           resp.Message,
			}, nil
		},
	}
}

func buildSharedKVTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "shared_kv",
		Description: "Read, write, delete, or list distributed key-value data replicated across the cluster mesh.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"get", "set", "delete", "list"},
					"description": "Action to perform on the distributed KV store: 'get', 'set', 'delete', 'list'",
				},
				"key": map[string]any{
					"type":        "string",
					"description": "The key name to get/set/delete",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "The value string to set (required for 'set')",
				},
				"ttl": map[string]any{
					"type":        "string",
					"description": "Optional TTL duration for 'set' (e.g. '10m', '1h')",
				},
				"prefix": map[string]any{
					"type":        "string",
					"description": "Optional key prefix filter for 'list'",
				},
			},
			"required": []string{"action"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			action, _ := args["action"].(string)
			store := cctx.GetKVStore()
			delegate := cctx.GetKVDelegate()
			if store == nil {
				return nil, fmt.Errorf("KV store not available")
			}

			switch action {
			case "get":
				key, _ := args["key"].(string)
				if key == "" {
					return nil, fmt.Errorf("'key' is required for action 'get'")
				}
				entry, found := store.Get(key)
				if !found || entry == nil {
					return map[string]any{"found": false, "key": key}, nil
				}
				return map[string]any{
					"found":     true,
					"key":       entry.Key,
					"value":     string(entry.Value),
					"version":   entry.Version,
					"writer":    entry.WriterNodeID,
					"timestamp": entry.Timestamp,
				}, nil

			case "set":
				key, _ := args["key"].(string)
				value, _ := args["value"].(string)
				if key == "" || value == "" {
					return nil, fmt.Errorf("'key' and 'value' are required for action 'set'")
				}
				var ttl time.Duration
				if ttlStr, ok := args["ttl"].(string); ok && ttlStr != "" {
					var err error
					ttl, err = time.ParseDuration(ttlStr)
					if err != nil {
						return nil, fmt.Errorf("invalid ttl duration: %w", err)
					}
				}
				if delegate != nil {
					entry := delegate.SetAndBroadcast(key, []byte(value), ttl)
					return map[string]any{"status": "ok", "key": entry.Key, "version": entry.Version}, nil
				}
				entry := store.Set(key, []byte(value), ttl)
				return map[string]any{"status": "ok", "key": entry.Key, "version": entry.Version}, nil

			case "delete":
				key, _ := args["key"].(string)
				if key == "" {
					return nil, fmt.Errorf("'key' is required for action 'delete'")
				}
				if delegate != nil {
					delegate.DeleteAndBroadcast(key)
				} else {
					store.Delete(key)
				}
				return map[string]any{"status": "deleted", "key": key}, nil

			case "list":
				prefix, _ := args["prefix"].(string)
				entries := store.List(prefix)
				type KeySummary struct {
					Key     string `json:"key"`
					Value   string `json:"value"`
					Version uint64 `json:"version"`
				}
				var res []KeySummary
				for _, e := range entries {
					res = append(res, KeySummary{
						Key:     e.Key,
						Value:   string(e.Value),
						Version: e.Version,
					})
				}
				return res, nil

			default:
				return nil, fmt.Errorf("unsupported action: %s", action)
			}
		},
	}
}

func buildAgentSendMailTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "agent_send_mail",
		Description: "Send an asynchronous actor-style message to a node, group, or broadcast mailbox over the cluster mesh.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to": map[string]any{
					"type":        "string",
					"description": "Destination address (e.g. 'node-2', '*', 'mailbox:node-1')",
				},
				"topic": map[string]any{
					"type":        "string",
					"description": "Message category / topic (e.g. 'task_request', 'status_check', 'telemetry')",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Message body or task instruction for the recipient",
				},
				"ttl": map[string]any{
					"type":        "string",
					"description": "Optional TTL duration for message expiration (default: '1h')",
				},
				"thread_id": map[string]any{
					"type":        "string",
					"description": "Optional conversation thread ID for tracking multi-turn dialogs",
				},
			},
			"required": []string{"to", "topic", "message"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			to, _ := args["to"].(string)
			if to == "" {
				to, _ = args["target_node"].(string)
			}
			topic, _ := args["topic"].(string)
			msgText, _ := args["message"].(string)
			threadID, _ := args["thread_id"].(string)
			if to == "" || topic == "" || msgText == "" {
				return nil, fmt.Errorf("'to', 'topic', and 'message' are required")
			}

			ttl := 1 * time.Hour
			if ttlStr, ok := args["ttl"].(string); ok && ttlStr != "" {
				if parsedTTL, err := time.ParseDuration(ttlStr); err == nil {
					ttl = parsedTTL
				}
			}

			normTo := mailbox.NormalizeAddress(to)
			msg := mailbox.NewMessage(cctx.GetNodeName(), normTo, topic, msgText)
			if threadID != "" {
				msg.ThreadID = threadID
			}

			mailboxKey := mailbox.KeyFor(normTo, msg.ID)
			valBytes, err := json.Marshal(msg)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal mailbox message: %w", err)
			}

			delegate := cctx.GetKVDelegate()
			store := cctx.GetKVStore()
			if delegate != nil {
				delegate.SetAndBroadcast(mailboxKey, valBytes, ttl)
			} else if store != nil {
				store.Set(mailboxKey, valBytes, ttl)
			} else {
				return nil, fmt.Errorf("KV store not available to deliver message")
			}

			return map[string]any{
				"status":      "queued",
				"message_id":  msg.ID,
				"mailbox_key": mailboxKey,
				"to":          normTo,
			}, nil
		},
	}
}

func buildAgentBroadcastTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "agent_broadcast",
		Description: "Broadcast an announcement or event to all node agents in the cluster mesh.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{
					"type":        "string",
					"description": "Broadcast topic (e.g. 'cluster_alert', 'maintenance', 'agent_event')",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Broadcast announcement payload",
				},
				"ttl": map[string]any{
					"type":        "string",
					"description": "Optional TTL duration (default: '30m')",
				},
			},
			"required": []string{"topic", "message"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			topic, _ := args["topic"].(string)
			msgText, _ := args["message"].(string)
			if topic == "" || msgText == "" {
				return nil, fmt.Errorf("'topic' and 'message' are required for broadcast")
			}

			ttl := 30 * time.Minute
			if ttlStr, ok := args["ttl"].(string); ok && ttlStr != "" {
				if parsedTTL, err := time.ParseDuration(ttlStr); err == nil {
					ttl = parsedTTL
				}
			}

			msg := mailbox.NewMessage(cctx.GetNodeName(), "*", topic, msgText)
			broadcastKey := mailbox.KeyFor("*", msg.ID)

			valBytes, err := json.Marshal(msg)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal broadcast envelope: %w", err)
			}

			delegate := cctx.GetKVDelegate()
			store := cctx.GetKVStore()
			if delegate != nil {
				delegate.SetAndBroadcast(broadcastKey, valBytes, ttl)
			} else if store != nil {
				store.Set(broadcastKey, valBytes, ttl)
			} else {
				return nil, fmt.Errorf("KV store not available to broadcast")
			}

			return map[string]any{
				"status":        "broadcasted",
				"message_id":    msg.ID,
				"broadcast_key": broadcastKey,
				"topic":         topic,
			}, nil
		},
	}
}

func buildAgentReadMailboxTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "agent_read_mailbox",
		Description: "Read, process, and acknowledge pending messages from the local node mailbox or a specific target mailbox.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type":        "string",
					"description": "Optional mailbox target (defaults to local node)",
				},
				"ack": map[string]any{
					"type":        "boolean",
					"description": "If true, acknowledge and remove processed messages from mailbox (default: false)",
				},
				"topic": map[string]any{
					"type":        "string",
					"description": "Optional topic filter",
				},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			store := cctx.GetKVStore()
			if store == nil {
				return nil, fmt.Errorf("KV store not available")
			}

			ack, _ := args["ack"].(bool)
			topicFilter, _ := args["topic"].(string)
			target, _ := args["target"].(string)
			if target == "" {
				target = cctx.GetNodeName()
			}
			normTarget := mailbox.NormalizeAddress(target)

			entries := store.List(mailbox.PrefixFor(normTarget))

			var messages []mailbox.Message
			var keysToAck []string

			for _, e := range entries {
				var env mailbox.Message
				if err := json.Unmarshal(e.Value, &env); err == nil {
					if topicFilter != "" && !strings.EqualFold(env.Topic, topicFilter) {
						continue
					}
					messages = append(messages, env)
					keysToAck = append(keysToAck, e.Key)
				}
			}

			if ack && len(keysToAck) > 0 {
				delegate := cctx.GetKVDelegate()
				for _, k := range keysToAck {
					if delegate != nil {
						delegate.DeleteAndBroadcast(k)
					} else {
						store.Delete(k)
					}
				}
			}

			return map[string]any{
				"count":    len(messages),
				"messages": messages,
				"acked":    ack,
			}, nil
		},
	}
}


