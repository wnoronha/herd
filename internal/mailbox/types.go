package mailbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// MessageStatus represents the current processing state of a mailbox message.
type MessageStatus string

const (
	StatusPending    MessageStatus = "pending"
	StatusProcessing MessageStatus = "processing"
	StatusCompleted  MessageStatus = "completed"
	StatusFailed     MessageStatus = "failed"
)

// Message represents a structured message envelope in an actor-style mailbox.
type Message struct {
	ID        string        `json:"id"`
	From      string        `json:"from"`                // Sender address (e.g. "node-1", "mailbox:node-1")
	To        string        `json:"to"`                  // Destination address (e.g. "node-2", "*", "role/worker")
	ThreadID  string        `json:"thread_id,omitempty"` // Conversation thread / correlation ID
	Topic     string        `json:"topic"`               // Category or message subject
	Message   string        `json:"message"`             // Payload content
	Status    MessageStatus `json:"status,omitempty"`    // Message lifecycle state
	ReplyTo   string        `json:"reply_to,omitempty"`  // Explicit reply address (defaults to From if empty)
	CreatedAt int64         `json:"created_at"`          // Unix epoch timestamp in seconds
}

// NormalizeAddress strips any leading "mailbox:" scheme prefix for consistent matching.
func NormalizeAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "mailbox:")
	return strings.TrimPrefix(addr, "@")
}

// KeyFor returns the canonical KV key for a message destined to a given target mailbox.
func KeyFor(target, msgID string) string {
	return fmt.Sprintf("mailbox:%s/%s", NormalizeAddress(target), msgID)
}

// PrefixFor returns the canonical KV prefix for watching or listing a mailbox.
func PrefixFor(target string) string {
	return fmt.Sprintf("mailbox:%s/", NormalizeAddress(target))
}

// GenerateID generates a cryptographically random, collision-resistant message ID.
func GenerateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("msg_%d_%s", time.Now().Unix(), hex.EncodeToString(b))
}

// NewMessage creates a newly initialized mailbox message.
func NewMessage(from, to, topic, msgContent string) *Message {
	id := GenerateID()
	fromNorm := NormalizeAddress(from)
	toNorm := NormalizeAddress(to)
	return &Message{
		ID:        id,
		From:      fromNorm,
		To:        toNorm,
		ThreadID:  id,
		Topic:     topic,
		Message:   msgContent,
		Status:    StatusPending,
		ReplyTo:   KeyFor(fromNorm, "reply_"+id),
		CreatedAt: time.Now().Unix(),
	}
}

// CreateReply creates a symmetric reply envelope targeting the sender of this message.
func (m *Message) CreateReply(from, replyContent string) *Message {
	replyTarget := m.From
	threadID := m.ThreadID
	if threadID == "" {
		threadID = m.ID
	}

	return &Message{
		ID:        fmt.Sprintf("reply_%s", m.ID),
		From:      from,
		To:        replyTarget,
		ThreadID:  threadID,
		Topic:     fmt.Sprintf("reply:%s", m.Topic),
		Message:   replyContent,
		Status:    StatusCompleted,
		CreatedAt: time.Now().Unix(),
	}
}
