package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Client communicates with the Herd daemon via Unix domain socket.
type Client struct {
	socketPath string
	timeout    time.Duration
}

// NewClient creates an IPC client for communicating with a running daemon.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		timeout:    5 * time.Second,
	}
}

// SetTimeout configures the request timeout for client calls.
func (c *Client) SetTimeout(d time.Duration) {
	c.timeout = d
}

// Call sends an IPC request and decodes the response.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	}

	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to herd daemon at %s: %w", c.socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("failed to set connection deadline: %w", err)
	}

	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to encode request params: %w", err)
		}
		rawParams = p
	}

	req := Request{
		Method: method,
		Params: rawParams,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	respBytes, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !resp.Success {
		return errors.New(resp.Error)
	}

	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("failed to unmarshal response result: %w", err)
		}
	}

	return nil
}

// GetStatus retrieves the daemon and node status.
func (c *Client) GetStatus(ctx context.Context) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.Call(ctx, "GetStatus", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetRoster retrieves the cluster member roster.
func (c *Client) GetRoster(ctx context.Context) (*RosterResponse, error) {
	var resp RosterResponse
	if err := c.Call(ctx, "GetRoster", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// JoinNode instructs the daemon to join a peer.
func (c *Client) JoinNode(ctx context.Context, addr string) (*JoinResponse, error) {
	req := JoinRequest{Address: addr}
	var resp JoinResponse
	if err := c.Call(ctx, "JoinNode", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LeaveCluster instructs the daemon to gracefully leave the cluster.
func (c *Client) LeaveCluster(ctx context.Context) (*LeaveResponse, error) {
	var resp LeaveResponse
	if err := c.Call(ctx, "LeaveCluster", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StopDaemon instructs the daemon to gracefully shut down.
func (c *Client) StopDaemon(ctx context.Context) (*StopResponse, error) {
	var resp StopResponse
	if err := c.Call(ctx, "StopDaemon", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ExecCommand executes a remote command across targeted cluster nodes.
func (c *Client) ExecCommand(ctx context.Context, req *ExecRequest) (*ExecResponse, error) {
	var resp ExecResponse
	if err := c.Call(ctx, "ExecCommand", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ForwardPort requests a dynamic port forward through the mesh.
func (c *Client) ForwardPort(ctx context.Context, req *ForwardRequest) (*ForwardResponse, error) {
	var resp ForwardResponse
	if err := c.Call(ctx, "ForwardPort", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CopyFile transfers files between mesh nodes.
func (c *Client) CopyFile(ctx context.Context, req *CPRequest) (*CPResponse, error) {
	var resp CPResponse
	if err := c.Call(ctx, "CopyFile", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// KVGet retrieves a key from the local KV store.
func (c *Client) KVGet(ctx context.Context, key string) (*KVGetResponse, error) {
	req := &KVGetRequest{Key: key}
	var resp KVGetResponse
	if err := c.Call(ctx, "KVGet", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// KVSet sets a key-value pair in the KV store with optional TTL.
func (c *Client) KVSet(ctx context.Context, key, value, ttl string) (*KVSetResponse, error) {
	req := &KVSetRequest{Key: key, Value: value, TTL: ttl}
	var resp KVSetResponse
	if err := c.Call(ctx, "KVSet", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// KVDelete deletes a key from the KV store.
func (c *Client) KVDelete(ctx context.Context, key string) (*KVDeleteResponse, error) {
	req := &KVDeleteRequest{Key: key}
	var resp KVDeleteResponse
	if err := c.Call(ctx, "KVDelete", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// KVList lists keys matching the given prefix.
func (c *Client) KVList(ctx context.Context, prefix string) (*KVListResponse, error) {
	req := &KVListRequest{Prefix: prefix}
	var resp KVListResponse
	if err := c.Call(ctx, "KVList", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AgentPrompt sends a task prompt to the local node agent.
func (c *Client) AgentPrompt(ctx context.Context, req *AgentPromptRequest) (*AgentPromptResponse, error) {
	var resp AgentPromptResponse
	if err := c.Call(ctx, "AgentPrompt", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// MailSend sends a message to a target mailbox.
func (c *Client) MailSend(ctx context.Context, req *MailSendRequest) (*MailSendResponse, error) {
	var resp MailSendResponse
	if err := c.Call(ctx, "MailSend", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// MailList lists messages in a mailbox.
func (c *Client) MailList(ctx context.Context, req *MailListRequest) (*MailListResponse, error) {
	var resp MailListResponse
	if err := c.Call(ctx, "MailList", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// MailRead reads (and optionally acknowledges) messages in a mailbox.
func (c *Client) MailRead(ctx context.Context, req *MailReadRequest) (*MailReadResponse, error) {
	var resp MailReadResponse
	if err := c.Call(ctx, "MailRead", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

