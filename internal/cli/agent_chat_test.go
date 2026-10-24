package cli

import (
	"testing"
)

// TestParseChatCommand verifies slash dispatch. Plain prompts and unknown
// commands fall through to the agent.
func TestParseChatCommand(t *testing.T) {
	cases := []struct {
		line  string
		isCmd bool
		name  string
		arg   string
	}{
		{"/tools", true, "tools", ""},
		{"/history", true, "history", ""},
		{"/new", true, "new", ""},
		{"/resume chat-abc123", true, "resume", "chat-abc123"},
		{"/RESUME chat-abc123", true, "resume", "chat-abc123"},
		{"/help", true, "help", ""},
		{"run uptime on all nodes", false, "", ""},
		{"exit", false, "", ""},
		{"quit", false, "", ""},
		{"/unknown", false, "", ""},
		{"/resume", true, "resume", ""},
	}
	for _, tc := range cases {
		cmd, ok := parseChatCommand(tc.line)
		if ok != tc.isCmd {
			t.Errorf("parseChatCommand(%q) = %v, want %v", tc.line, ok, tc.isCmd)
			continue
		}
		if !ok {
			continue
		}
		if cmd.name != tc.name || cmd.arg != tc.arg {
			t.Errorf("parseChatCommand(%q) = %+v, want name %q arg %q", tc.line, cmd, tc.name, tc.arg)
		}
	}
}

// TestParseApprovalAnswer verifies only explicit yes approves. Empty input
// declines so pressing enter never runs a destructive call.
func TestParseApprovalAnswer(t *testing.T) {
	for _, yes := range []string{"y", "Y", "yes", "YES", "  yes  "} {
		if !parseApprovalAnswer(yes) {
			t.Errorf("expected %q to approve", yes)
		}
	}
	for _, no := range []string{"", "n", "no", "cancel", "yess", "1"} {
		if parseApprovalAnswer(no) {
			t.Errorf("expected %q to decline", no)
		}
	}
}

// TestAppendChatHistory verifies the log cap keeps /history bounded.
func TestAppendChatHistory(t *testing.T) {
	var log []string
	for i := 0; i < maxChatHistory+10; i++ {
		log = appendChatHistory(log, "entry")
	}
	if len(log) != maxChatHistory {
		t.Errorf("expected cap %d, got %d", maxChatHistory, len(log))
	}
}

// TestRunChatCommand verifies /new rotates the session and /resume adopts
// the given ID while exit words stay plain prompts.
func TestRunChatCommand(t *testing.T) {
	id, log := runChatCommand(chatCommand{name: "new"}, "chat-old", []string{"user: hi"})
	if id == "chat-old" || id == "" {
		t.Errorf("expected /new to rotate the session, got %q", id)
	}
	if len(log) != 0 {
		t.Errorf("expected /new to reset history, got %v", log)
	}

	id, _ = runChatCommand(chatCommand{name: "resume", arg: "chat-abc"}, "chat-old", nil)
	if id != "chat-abc" {
		t.Errorf("expected /resume to adopt the ID, got %q", id)
	}

	id, _ = runChatCommand(chatCommand{name: "resume"}, "chat-old", nil)
	if id != "chat-old" {
		t.Errorf("expected bare /resume to keep the session, got %q", id)
	}
}
