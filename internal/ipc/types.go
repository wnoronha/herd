package ipc

import (
	"context"
	"encoding/json"

	"herd/internal/mailbox"
)

// Request is the generic IPC request wrapper.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is the generic IPC response wrapper.
type Response struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// StatusResponse contains the daemon and cluster node status.
type StatusResponse struct {
	NodeName      string            `json:"node_name"`
	PublicKey     string            `json:"public_key"`
	TailcatAddr   string            `json:"tailcat_addr"`
	BindPort      int               `json:"bind_port"`
	State         string            `json:"state"`
	MemberCount   int               `json:"member_count"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// MemberInfo represents information about a member in the cluster.
type MemberInfo struct {
	Name     string            `json:"name"`
	Addr     string            `json:"addr"`
	Port     uint16            `json:"port"`
	Status   string            `json:"status"` // alive, suspect, dead, left
	Metadata map[string]string `json:"metadata,omitempty"`
}

// RosterResponse contains the list of cluster members.
type RosterResponse struct {
	Members []MemberInfo `json:"members"`
}

// JoinRequest specifies the address of a peer node to join.
type JoinRequest struct {
	Address string `json:"address"`
}

// JoinResponse returns the result of joining a peer.
type JoinResponse struct {
	JoinedNodes int    `json:"joined_nodes"`
	Message     string `json:"message"`
}

// LeaveResponse returns the result of leaving the cluster.
type LeaveResponse struct {
	Message string `json:"message"`
}

// StopResponse returns the result of stopping the daemon.
type StopResponse struct {
	Message string `json:"message"`
}

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

// ForwardRequest defines a port forwarding tunnel request.
type ForwardRequest struct {
	LocalPort  int    `json:"local_port"`
	TargetNode string `json:"target_node"`
	TargetPort int    `json:"target_port"`
}

// ForwardResponse confirms a port forward tunnel status.
type ForwardResponse struct {
	Message    string `json:"message"`
	ListenAddr string `json:"listen_addr"`
}

// CPRequest specifies a file copy operation.
type CPRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	TargetNode  string `json:"target_node,omitempty"`
	IsDownload  bool   `json:"is_download"`
}

// CPResponse returns file copy result.
type CPResponse struct {
	BytesTransferred int64  `json:"bytes_transferred"`
	Message          string `json:"message"`
}

// KVGetRequest specifies the key to retrieve.
type KVGetRequest struct {
	Key string `json:"key"`
}

// KVEntryDTO represents a key-value record transfer format.
type KVEntryDTO struct {
	Key          string `json:"key"`
	Value        string `json:"value"`
	Version      uint64 `json:"version"`
	Timestamp    int64  `json:"timestamp"`
	WriterNodeID string `json:"writer_node_id"`
	Tombstone    bool   `json:"tombstone,omitempty"`
}

// KVGetResponse returns the result of a KV lookup.
type KVGetResponse struct {
	Found bool        `json:"found"`
	Entry *KVEntryDTO `json:"entry,omitempty"`
}

// KVSetRequest specifies key, value, and optional TTL.
type KVSetRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	TTL   string `json:"ttl,omitempty"`
}

// KVSetResponse confirms a KV write.
type KVSetResponse struct {
	Key     string `json:"key"`
	Version uint64 `json:"version"`
	Message string `json:"message"`
}

// KVDeleteRequest specifies a key to delete.
type KVDeleteRequest struct {
	Key string `json:"key"`
}

// KVDeleteResponse confirms a KV deletion.
type KVDeleteResponse struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

// KVListRequest specifies a prefix to list.
type KVListRequest struct {
	Prefix string `json:"prefix"`
}

// KVListResponse returns matching KV records.
type KVListResponse struct {
	Entries []KVEntryDTO `json:"entries"`
}

// AgentPromptRequest contains task instructions for the local node agent.
type AgentPromptRequest struct {
	Prompt     string `json:"prompt"`
	TargetNode string `json:"target_node,omitempty"`
	Model      string `json:"model,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Role       string `json:"role,omitempty"`
}

// ToolCallDTO represents a recorded tool call in an agent prompt execution.
type ToolCallDTO struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
}

// AgentPromptResponse contains the agent's completed response and execution trace.
type AgentPromptResponse struct {
	Response    string        `json:"response"`
	ToolsCalled []ToolCallDTO `json:"tools_called,omitempty"`
	ModelUsed   string        `json:"model_used"`
	ExecutionMs int64         `json:"execution_ms"`
	NodeName    string        `json:"node_name,omitempty"`
	AgentRole   string        `json:"agent_role,omitempty"`
}

// MailSendRequest specifies a message to deliver into a target mailbox.
type MailSendRequest struct {
	To       string `json:"to"`
	Topic    string `json:"topic"`
	Message  string `json:"message"`
	TTL      string `json:"ttl,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
}

// MailSendResponse confirms queuing of a mailbox message.
type MailSendResponse struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Status string `json:"status"`
}

// MailListRequest specifies the mailbox target and optional topic to inspect.
type MailListRequest struct {
	Target string `json:"target,omitempty"`
	Topic  string `json:"topic,omitempty"`
}

// MailListResponse contains matching messages in the mailbox.
type MailListResponse struct {
	Messages []mailbox.Message `json:"messages"`
}

// MailReadRequest specifies the mailbox target to read and optionally acknowledge.
type MailReadRequest struct {
	Target string `json:"target,omitempty"`
	Topic  string `json:"topic,omitempty"`
	Ack    bool   `json:"ack,omitempty"`
}

// MailReadResponse contains the read messages and acknowledgement count.
type MailReadResponse struct {
	Messages []mailbox.Message `json:"messages"`
	Count    int               `json:"count"`
	Acked    bool              `json:"acked"`
}

// DaemonHandler defines the methods exposed over local IPC.
type DaemonHandler interface {
	GetStatus(ctx context.Context) (*StatusResponse, error)
	GetRoster(ctx context.Context) (*RosterResponse, error)
	JoinNode(ctx context.Context, addr string) (*JoinResponse, error)
	LeaveCluster(ctx context.Context) (*LeaveResponse, error)
	StopDaemon(ctx context.Context) (*StopResponse, error)
	ExecCommand(ctx context.Context, req *ExecRequest) (*ExecResponse, error)
	ForwardPort(ctx context.Context, req *ForwardRequest) (*ForwardResponse, error)
	CopyFile(ctx context.Context, req *CPRequest) (*CPResponse, error)
	KVGet(ctx context.Context, req *KVGetRequest) (*KVGetResponse, error)
	KVSet(ctx context.Context, req *KVSetRequest) (*KVSetResponse, error)
	KVDelete(ctx context.Context, req *KVDeleteRequest) (*KVDeleteResponse, error)
	KVList(ctx context.Context, req *KVListRequest) (*KVListResponse, error)
	AgentPrompt(ctx context.Context, req *AgentPromptRequest) (*AgentPromptResponse, error)
	MailSend(ctx context.Context, req *MailSendRequest) (*MailSendResponse, error)
	MailList(ctx context.Context, req *MailListRequest) (*MailListResponse, error)
	MailRead(ctx context.Context, req *MailReadRequest) (*MailReadResponse, error)
}

