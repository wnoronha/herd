package mailbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeAddress(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"node-1", "node-1"},
		{"mailbox:node-1", "node-1"},
		{"@node-2", "node-2"},
		{" mailbox:role/sre ", "role/sre"},
		{"mailbox:*", "*"},
	}

	for _, c := range cases {
		got := NormalizeAddress(c.input)
		if got != c.expected {
			t.Errorf("NormalizeAddress(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func TestKeyHelpers(t *testing.T) {
	if KeyFor("mailbox:node-2", "msg-1") != "mailbox:node-2/msg-1" {
		t.Errorf("unexpected KeyFor output: %s", KeyFor("mailbox:node-2", "msg-1"))
	}
	if PrefixFor("mailbox:node-2") != "mailbox:node-2/" {
		t.Errorf("unexpected PrefixFor output: %s", PrefixFor("mailbox:node-2"))
	}
}

func TestMessageSerialization(t *testing.T) {
	msg := &Message{
		ID:        "msg-test-1",
		From:      "node-1",
		To:        "node-2",
		Topic:     "health_check",
		Message:   "Check RAM",
		CreatedAt: 1727700000,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	str := string(data)
	if !strings.Contains(str, `"from":"node-1"`) || !strings.Contains(str, `"to":"node-2"`) {
		t.Errorf("JSON output missing from/to: %s", str)
	}

	// Unmarshal back
	var parsed Message
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if parsed.From != "node-1" || parsed.To != "node-2" {
		t.Errorf("parsed message mismatch: %+v", parsed)
	}
}

func TestCreateReply(t *testing.T) {
	orig := &Message{
		ID:       "msg-alpha",
		From:     "node-1",
		To:       "node-2",
		ThreadID: "thread-42",
		Topic:    "query",
		Message:  "What is your status?",
	}

	reply := orig.CreateReply("node-2", "Status is healthy")

	if reply.To != "node-1" {
		t.Errorf("expected reply To='node-1', got %q", reply.To)
	}
	if reply.From != "node-2" {
		t.Errorf("expected reply From='node-2', got %q", reply.From)
	}
	if reply.ThreadID != "thread-42" {
		t.Errorf("expected reply ThreadID='thread-42', got %q", reply.ThreadID)
	}
	if reply.Topic != "reply:query" {
		t.Errorf("expected reply Topic='reply:query', got %q", reply.Topic)
	}
	if reply.Status != StatusCompleted {
		t.Errorf("expected reply Status='completed', got %q", reply.Status)
	}
}

func TestNewMessage(t *testing.T) {
	msg := NewMessage("mailbox:node-1", "mailbox:node-2", "ping", "hello mesh")
	if msg.From != "node-1" {
		t.Errorf("expected From='node-1', got %q", msg.From)
	}
	if msg.To != "node-2" {
		t.Errorf("expected To='node-2', got %q", msg.To)
	}
	if msg.Topic != "ping" || msg.Message != "hello mesh" {
		t.Errorf("unexpected topic/message: %+v", msg)
	}
	if msg.ThreadID != msg.ID {
		t.Errorf("expected ThreadID=%q, got %q", msg.ID, msg.ThreadID)
	}
	if msg.Status != StatusPending {
		t.Errorf("expected StatusPending, got %q", msg.Status)
	}
	if msg.ReplyTo != "mailbox:node-1/reply_"+msg.ID {
		t.Errorf("unexpected ReplyTo: %q", msg.ReplyTo)
	}
}
