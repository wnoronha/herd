package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"herd/internal/agent"
	"herd/internal/ipc"
	"herd/internal/subsys"
	"herd/internal/transport/tailcat"
)

// handleExecStream handles an incoming StreamTypeExec stream for remote command execution.
func (d *Daemon) handleExecStream(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	var req subsys.ExecRequest
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&req); err != nil {
		return
	}
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	res := subsys.ExecuteLocal(context.Background(), d.Config.NodeName, req.Command, req.Args, timeout)
	enc := json.NewEncoder(conn)
	_ = enc.Encode(res)
}

// handleAgentStream handles an incoming StreamTypeAgent stream for direct agent prompt invocation.
func (d *Daemon) handleAgentStream(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	var req agent.PromptRequest
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&req); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var resp *agent.PromptResponse
	var err error
	if d.AgentEngine != nil {
		resp, err = d.AgentEngine.ExecutePrompt(ctx, &req)
	} else {
		err = errors.New("agent engine not initialized")
	}

	enc := json.NewEncoder(conn)
	if err != nil {
		_ = enc.Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = enc.Encode(resp)
}

// handleFileStream delegates an incoming StreamTypeFile stream to the file transfer subsystem.
func (d *Daemon) handleFileStream(conn net.Conn) {
	if err := subsys.HandleFileStream(conn); err != nil {
		d.Logger.Named("file_stream").Warnf("Stream transfer error: %v", err)
	}
}

// ExecCommand implements ipc.DaemonHandler.
func (d *Daemon) ExecCommand(ctx context.Context, req *ipc.ExecRequest) (*ipc.ExecResponse, error) {
	if req == nil || req.Command == "" {
		return nil, errors.New("command cannot be empty")
	}

	targets := subsys.TargetNodes(req.Target, d.Config.NodeName, d.Store)
	if len(targets) == 0 {
		return nil, fmt.Errorf("no matching nodes found for target: %s", req.Target)
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	var results []ipc.NodeExecResult
	for _, targetName := range targets {
		if targetName == d.Config.NodeName {
			res := subsys.ExecuteLocal(ctx, d.Config.NodeName, req.Command, req.Args, timeout)
			results = append(results, ipc.NodeExecResult{
				NodeName: res.NodeName,
				Stdout:   res.Stdout,
				Stderr:   res.Stderr,
				ExitCode: res.ExitCode,
				Error:    res.Error,
			})
		} else {
			// Remote execution via Tailcat encrypted stream
			member, found := d.Store.GetMember(targetName)
			if !found || member.Status != "alive" {
				results = append(results, ipc.NodeExecResult{
					NodeName: targetName,
					ExitCode: 1,
					Error:    fmt.Sprintf("node %s is not reachable in cluster roster", targetName),
				})
				continue
			}

			targetAddr := net.JoinHostPort(member.Addr, strconv.Itoa(int(member.Port)))
			if member.Addr == "" && member.Meta != nil && member.Meta.TailcatAddr != "" {
				targetAddr = member.Meta.TailcatAddr
			}
			conn, err := d.Transport.DialStream(targetAddr, tailcat.StreamTypeExec, timeout)
			if err != nil {
				results = append(results, ipc.NodeExecResult{
					NodeName: targetName,
					ExitCode: 1,
					Error:    fmt.Sprintf("failed to dial exec stream to %s: %v", targetName, err),
				})
				continue
			}

			remoteReq := subsys.ExecRequest{
				Command:        req.Command,
				Args:           req.Args,
				TimeoutSeconds: int(timeout.Seconds()),
			}
			enc := json.NewEncoder(conn)
			if err := enc.Encode(remoteReq); err != nil {
				_ = conn.Close()
				results = append(results, ipc.NodeExecResult{
					NodeName: targetName,
					ExitCode: 1,
					Error:    fmt.Sprintf("failed to send exec request to %s: %v", targetName, err),
				})
				continue
			}

			var remoteRes subsys.NodeExecResult
			dec := json.NewDecoder(conn)
			if err := dec.Decode(&remoteRes); err != nil {
				_ = conn.Close()
				results = append(results, ipc.NodeExecResult{
					NodeName: targetName,
					ExitCode: 1,
					Error:    fmt.Sprintf("failed to decode exec response from %s: %v", targetName, err),
				})
				continue
			}
			_ = conn.Close()

			results = append(results, ipc.NodeExecResult{
				NodeName: remoteRes.NodeName,
				Stdout:   remoteRes.Stdout,
				Stderr:   remoteRes.Stderr,
				ExitCode: remoteRes.ExitCode,
				Error:    remoteRes.Error,
			})
		}
	}

	return &ipc.ExecResponse{
		Results: results,
	}, nil
}

// ExecuteCommand implements agent.ClusterContext.
func (d *Daemon) ExecuteCommand(ctx context.Context, req *ipc.ExecRequest) (*ipc.ExecResponse, error) {
	return d.ExecCommand(ctx, req)
}

// ForwardPort implements ipc.DaemonHandler.
func (d *Daemon) ForwardPort(ctx context.Context, req *ipc.ForwardRequest) (*ipc.ForwardResponse, error) {
	if req == nil || req.LocalPort <= 0 || req.TargetPort <= 0 || req.TargetNode == "" {
		return nil, errors.New("invalid port forwarding parameters")
	}

	listenAddr := fmt.Sprintf("127.0.0.1:%d", req.LocalPort)

	// Resolve target node in cluster roster if available
	targetAddr := ""
	if d.Store != nil {
		if member, found := d.Store.GetMember(req.TargetNode); found {
			if member.Meta != nil && member.Meta.TailcatAddr != "" {
				host, _, _ := net.SplitHostPort(member.Meta.TailcatAddr)
				if host == "" {
					host = strings.Trim(member.Meta.TailcatAddr, "[] ")
				}
				targetAddr = net.JoinHostPort(host, strconv.Itoa(req.TargetPort))
			} else if member.Addr != "" {
				targetAddr = net.JoinHostPort(member.Addr, strconv.Itoa(req.TargetPort))
			}
		}
	}
	if targetAddr == "" {
		targetAddr = net.JoinHostPort(req.TargetNode, strconv.Itoa(req.TargetPort))
	}

	pf := subsys.NewPortForwarder(listenAddr, targetAddr, nil)
	if err := pf.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to start port forwarder on %s: %w", listenAddr, err)
	}

	d.forwardersMu.Lock()
	if old, exists := d.forwarders[listenAddr]; exists {
		_ = old.Stop()
	}
	d.forwarders[listenAddr] = pf
	d.forwardersMu.Unlock()

	return &ipc.ForwardResponse{
		Message:    fmt.Sprintf("Tunnel established: %s -> %s (%s)", listenAddr, req.TargetNode, targetAddr),
		ListenAddr: listenAddr,
	}, nil
}

// CopyFile implements ipc.DaemonHandler and agent.ClusterContext.
func (d *Daemon) CopyFile(ctx context.Context, req *ipc.CPRequest) (*ipc.CPResponse, error) {
	if req == nil || req.Source == "" || req.Destination == "" {
		return nil, errors.New("source and destination cannot be empty")
	}

	srcTarget := subsys.ParseCPTarget(req.Source)
	dstTarget := subsys.ParseCPTarget(req.Destination)

	if req.TargetNode != "" && !srcTarget.IsRemote && !dstTarget.IsRemote {
		if req.IsDownload {
			srcTarget.Node = req.TargetNode
			srcTarget.IsRemote = true
		} else {
			dstTarget.Node = req.TargetNode
			dstTarget.IsRemote = true
		}
	}

	if srcTarget.Node == d.Config.NodeName {
		srcTarget.IsRemote = false
	}
	if dstTarget.Node == d.Config.NodeName {
		dstTarget.IsRemote = false
	}

	var transferred int64
	var err error

	switch {
	case !srcTarget.IsRemote && !dstTarget.IsRemote:
		transferred, err = subsys.CopyLocalFile(srcTarget.Path, dstTarget.Path)
	case !srcTarget.IsRemote && dstTarget.IsRemote:
		transferred, err = d.uploadToNode(ctx, srcTarget.Path, dstTarget.Node, dstTarget.Path)
	case srcTarget.IsRemote && !dstTarget.IsRemote:
		transferred, err = d.downloadFromNode(ctx, srcTarget.Node, srcTarget.Path, dstTarget.Path)
	case srcTarget.IsRemote && dstTarget.IsRemote:
		// Transfer between two remote nodes using a local temporary buffer
		tmpFile, tErr := os.CreateTemp("", "herd-cp-*")
		if tErr != nil {
			return nil, fmt.Errorf("failed to create temporary file for transfer: %w", tErr)
		}
		tmpPath := tmpFile.Name()
		_ = tmpFile.Close()
		defer func() { _ = os.Remove(tmpPath) }()

		_, err = d.downloadFromNode(ctx, srcTarget.Node, srcTarget.Path, tmpPath)
		if err != nil {
			return nil, fmt.Errorf("failed to download from %s: %w", srcTarget.Node, err)
		}
		transferred, err = d.uploadToNode(ctx, tmpPath, dstTarget.Node, dstTarget.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to upload to %s: %w", dstTarget.Node, err)
		}
	}

	if err != nil {
		return nil, err
	}

	return &ipc.CPResponse{
		BytesTransferred: transferred,
		Message:          fmt.Sprintf("Transferred %s to %s (%d bytes)", req.Source, req.Destination, transferred),
	}, nil
}

func (d *Daemon) dialNodeFileStream(ctx context.Context, targetNode string) (net.Conn, error) {
	if d.Transport == nil {
		return nil, errors.New("transport is not initialized")
	}

	d.mu.RLock()
	store := d.Store
	d.mu.RUnlock()

	if store == nil {
		return nil, errors.New("roster store not available")
	}

	member, found := store.GetMember(targetNode)
	if !found || member.Status != "alive" {
		return nil, fmt.Errorf("node %s is not reachable in cluster roster", targetNode)
	}

	targetAddr := net.JoinHostPort(member.Addr, strconv.Itoa(int(member.Port)))
	if member.Addr == "" && member.Meta != nil && member.Meta.TailcatAddr != "" {
		targetAddr = member.Meta.TailcatAddr
	}

	return d.Transport.DialStream(targetAddr, tailcat.StreamTypeFile, 30*time.Second)
}

func (d *Daemon) uploadToNode(ctx context.Context, localPath, targetNode, remotePath string) (int64, error) {
	conn, err := d.dialNodeFileStream(ctx, targetNode)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	return subsys.UploadToStream(conn, localPath, remotePath)
}

func (d *Daemon) downloadFromNode(ctx context.Context, targetNode, remotePath, localPath string) (int64, error) {
	conn, err := d.dialNodeFileStream(ctx, targetNode)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	return subsys.DownloadFromStream(conn, remotePath, localPath)
}
