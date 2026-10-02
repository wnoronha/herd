package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"herd/internal/subsys"
)

// ActiveForward represents a running port forwarder tunnel.
type ActiveForward struct {
	ID          string               `json:"id"`
	LocalAddr   string               `json:"local_addr"`
	TargetNode  string               `json:"target_node"`
	TargetAddr  string               `json:"target_addr"`
	StartedAt   time.Time            `json:"started_at"`
	forwarder   *subsys.PortForwarder `json:"-"`
	cancel      context.CancelFunc   `json:"-"`
}

// forwardRegistry tracks active port forwarders across the node's agent lifecycle.
type forwardRegistry struct {
	mu      sync.Mutex
	tunnels map[string]*ActiveForward
}

var globalForwardRegistry = &forwardRegistry{
	tunnels: make(map[string]*ActiveForward),
}

// buildClusterForwardTool creates the cluster_forward tool.
func buildClusterForwardTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "cluster_forward",
		Description: "Dynamically establish, list, or terminate bidirectional TCP port forwarding tunnels across cluster mesh nodes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Forwarding action: 'start', 'stop', or 'list'",
					"enum":        []string{"start", "stop", "list"},
				},
				"local_port": map[string]any{
					"type":        "integer",
					"description": "Local port to bind the forward listener to (required for action='start', optional for 'stop')",
				},
				"target_node": map[string]any{
					"type":        "string",
					"description": "Target mesh peer node name or address (required for action='start')",
				},
				"target_port": map[string]any{
					"type":        "integer",
					"description": "Remote port on the target node to forward traffic to (required for action='start')",
				},
				"bind_host": map[string]any{
					"type":        "string",
					"description": "Local IP interface to bind to (default: '127.0.0.1')",
				},
				"id": map[string]any{
					"type":        "string",
					"description": "Forward tunnel identifier to stop (optional for action='stop')",
				},
			},
			"required": []string{"action"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			action, ok := args["action"].(string)
			if !ok || strings.TrimSpace(action) == "" {
				return nil, errors.New("parameter 'action' is required")
			}
			action = strings.ToLower(strings.TrimSpace(action))

			switch action {
			case "start":
				var localPort int
				if lp, ok := args["local_port"].(float64); ok && lp > 0 {
					localPort = int(lp)
				} else {
					return nil, errors.New("parameter 'local_port' must be a positive integer")
				}

				targetNode, ok := args["target_node"].(string)
				if !ok || strings.TrimSpace(targetNode) == "" {
					return nil, errors.New("parameter 'target_node' is required")
				}
				targetNode = strings.TrimSpace(targetNode)

				var targetPort int
				if tp, ok := args["target_port"].(float64); ok && tp > 0 {
					targetPort = int(tp)
				} else {
					return nil, errors.New("parameter 'target_port' must be a positive integer")
				}

				bindHost := "127.0.0.1"
				if bh, ok := args["bind_host"].(string); ok && strings.TrimSpace(bh) != "" {
					bindHost = strings.TrimSpace(bh)
				}

				tunnelID := fmt.Sprintf("fwd-%d", localPort)
				globalForwardRegistry.mu.Lock()
				if _, exists := globalForwardRegistry.tunnels[tunnelID]; exists {
					globalForwardRegistry.mu.Unlock()
					return nil, fmt.Errorf("a forward tunnel is already running on local port %d (id: %s)", localPort, tunnelID)
				}
				globalForwardRegistry.mu.Unlock()

				// Resolve target node IP address via roster store if possible
				targetAddr := ""
				if cctx != nil {
					store := cctx.GetRosterStore()
					if store != nil {
						for _, m := range store.GetMembers() {
							if m.Name == targetNode {
								targetAddr = net.JoinHostPort(m.Addr, strconv.Itoa(targetPort))
								break
							}
						}
					}
				}
				if targetAddr == "" {
					targetAddr = net.JoinHostPort(targetNode, strconv.Itoa(targetPort))
				}

				localListen := net.JoinHostPort(bindHost, strconv.Itoa(localPort))
				pf := subsys.NewPortForwarder(localListen, targetAddr, nil)

				fwdCtx, cancel := context.WithCancel(context.Background())
				if err := pf.Start(fwdCtx); err != nil {
					cancel()
					return nil, fmt.Errorf("failed to start port forwarder on %s: %w", localListen, err)
				}

				fwdInfo := &ActiveForward{
					ID:         tunnelID,
					LocalAddr:  pf.LocalAddr().String(),
					TargetNode: targetNode,
					TargetAddr: targetAddr,
					StartedAt:  time.Now().UTC(),
					forwarder:  pf,
					cancel:     cancel,
				}

				globalForwardRegistry.mu.Lock()
				globalForwardRegistry.tunnels[tunnelID] = fwdInfo
				globalForwardRegistry.mu.Unlock()

				return map[string]any{
					"status":      "started",
					"id":          tunnelID,
					"local_addr":  fwdInfo.LocalAddr,
					"target_node": targetNode,
					"target_addr": targetAddr,
				}, nil

			case "stop":
				idVal, _ := args["id"].(string)
				if idVal == "" {
					if lp, ok := args["local_port"].(float64); ok && lp > 0 {
						idVal = fmt.Sprintf("fwd-%d", int(lp))
					}
				}
				if idVal == "" {
					return nil, errors.New("either 'id' or 'local_port' is required for action 'stop'")
				}

				globalForwardRegistry.mu.Lock()
				fwdInfo, exists := globalForwardRegistry.tunnels[idVal]
				if !exists {
					globalForwardRegistry.mu.Unlock()
					return nil, fmt.Errorf("forward tunnel '%s' not found", idVal)
				}
				delete(globalForwardRegistry.tunnels, idVal)
				globalForwardRegistry.mu.Unlock()

				if fwdInfo.cancel != nil {
					fwdInfo.cancel()
				}
				if fwdInfo.forwarder != nil {
					_ = fwdInfo.forwarder.Stop()
				}

				return map[string]any{
					"status": "stopped",
					"id":     idVal,
				}, nil

			case "list":
				globalForwardRegistry.mu.Lock()
				defer globalForwardRegistry.mu.Unlock()

				type TunnelSummary struct {
					ID            string `json:"id"`
					LocalAddr     string `json:"local_addr"`
					TargetNode    string `json:"target_node"`
					TargetAddr    string `json:"target_addr"`
					UptimeSeconds int64  `json:"uptime_seconds"`
				}

				var list []TunnelSummary
				now := time.Now().UTC()
				for _, t := range globalForwardRegistry.tunnels {
					list = append(list, TunnelSummary{
						ID:            t.ID,
						LocalAddr:     t.LocalAddr,
						TargetNode:    t.TargetNode,
						TargetAddr:    t.TargetAddr,
						UptimeSeconds: int64(now.Sub(t.StartedAt).Seconds()),
					})
				}

				return map[string]any{
					"tunnels": list,
					"count":   len(list),
				}, nil

			default:
				return nil, fmt.Errorf("unsupported action '%s', expected 'start', 'stop', or 'list'", action)
			}
		},
	}
}

// buildClusterManageTool creates the cluster_manage tool.
func buildClusterManageTool(cctx ClusterContext) ToolDefinition {
	return ToolDefinition{
		Name:        "cluster_manage",
		Description: "Inspect cluster daemon status, trigger peer joins by network address, or initiate graceful leaves.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Cluster management action: 'status', 'join', or 'leave'",
					"enum":        []string{"status", "join", "leave"},
				},
				"address": map[string]any{
					"type":        "string",
					"description": "Peer address or endpoint to join (required for action='join')",
				},
			},
			"required": []string{"action"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			action, ok := args["action"].(string)
			if !ok || strings.TrimSpace(action) == "" {
				return nil, errors.New("parameter 'action' is required")
			}
			action = strings.ToLower(strings.TrimSpace(action))

			if cctx == nil {
				return nil, errors.New("cluster context is not available")
			}

			switch action {
			case "status":
				statusResp, err := cctx.GetStatus(ctx)
				if err != nil {
					return nil, fmt.Errorf("failed to get daemon status: %w", err)
				}
				return map[string]any{
					"node_name":      statusResp.NodeName,
					"public_key":     statusResp.PublicKey,
					"tailcat_addr":   statusResp.TailcatAddr,
					"bind_port":      statusResp.BindPort,
					"state":          statusResp.State,
					"member_count":   statusResp.MemberCount,
					"uptime_seconds": statusResp.UptimeSeconds,
				}, nil

			case "join":
				addr, ok := args["address"].(string)
				if !ok || strings.TrimSpace(addr) == "" {
					return nil, errors.New("parameter 'address' is required for action 'join'")
				}
				joinResp, err := cctx.JoinNode(ctx, strings.TrimSpace(addr))
				if err != nil {
					return nil, fmt.Errorf("failed to join peer '%s': %w", addr, err)
				}
				return map[string]any{
					"status":       "joined",
					"address":      addr,
					"joined_nodes": joinResp.JoinedNodes,
					"message":      joinResp.Message,
				}, nil

			case "leave":
				leaveResp, err := cctx.LeaveCluster(ctx)
				if err != nil {
					return nil, fmt.Errorf("failed to leave cluster: %w", err)
				}
				msg := "gracefully left the cluster"
				if leaveResp != nil && leaveResp.Message != "" {
					msg = leaveResp.Message
				}
				return map[string]any{
					"status":  "left",
					"message": msg,
				}, nil

			default:
				return nil, fmt.Errorf("unsupported action '%s', expected 'status', 'join', or 'leave'", action)
			}
		},
	}
}
