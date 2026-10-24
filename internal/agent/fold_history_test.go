package agent

import (
	"strings"
	"testing"
)

// TestFoldHistorySkippedWithSessionID verifies History is only folded for
// one-shot calls: with SessionID present the ADK session service already
// accumulates event history, so folding would double-count prior context.
func TestFoldHistorySkippedWithSessionID(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "prior-context-XYZ"},
		{Role: "assistant", Content: "acknowledged"},
	}

	oneShot := foldHistoryIntoPrompt(&PromptRequest{Prompt: "continue", History: history})
	if !strings.Contains(oneShot, "prior-context-XYZ") {
		t.Errorf("expected one-shot fold to include history, got: %q", oneShot)
	}

	withSession := foldHistoryIntoPrompt(&PromptRequest{
		Prompt:    "continue",
		History:   history,
		SessionID: "session-123",
	})
	if strings.Contains(withSession, "prior-context-XYZ") {
		t.Errorf("expected SessionID call to skip folding, got: %q", withSession)
	}
	if withSession != "continue" {
		t.Errorf("expected bare prompt with SessionID, got: %q", withSession)
	}
}
