package daemon

import (
	"context"
	"net"
	"time"

	"herd/internal/agent"
	"herd/internal/kv"
	"herd/internal/roster"
)

// KVStorage abstracts distributed key-value storage operations.
type KVStorage interface {
	Get(key string) (*kv.Entry, bool)
	Set(key string, value []byte, ttl time.Duration) *kv.Entry
	Delete(key string) bool
	List(prefix string) []*kv.Entry
	Watch(prefix string) (<-chan *kv.Entry, func())
}

// KVBroadcaster abstracts distributed KV gossip broadcast operations.
type KVBroadcaster interface {
	SetAndBroadcast(key string, value []byte, ttl time.Duration) *kv.Entry
	DeleteAndBroadcast(key string)
}

// RosterMembership abstracts cluster membership queries.
type RosterMembership interface {
	GetMembers() []*roster.Member
	GetMember(name string) (*roster.Member, bool)
}

// MeshTransport abstracts P2P stream multiplexing, addressing, and connection handling.
type MeshTransport interface {
	GetPort() int
	TailcatAddr() string
	RegisterStreamHandler(streamType byte, fn func(net.Conn))
	RegisterPeerTailcat(tailcatAddr, directAddr string)
	DialStream(addr string, streamType byte, timeout time.Duration) (net.Conn, error)
	Shutdown() error
}

// AgentExecutor abstracts the embedded AI reasoning engine execution.
type AgentExecutor interface {
	ExecutePrompt(ctx context.Context, req *agent.PromptRequest) (*agent.PromptResponse, error)
}
