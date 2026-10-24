package agent

import (
	"errors"
	"testing"
)

// TestIsToolUnsupportedError verifies the HTTP-400 tool-less retry classifier
// in resolveADKModel/runADKAgent: local models without tool support retry with
// Tools:nil on the same openaimodel instead of failing.
func TestIsToolUnsupportedError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"tool 400", errors.New("request failed with status 400: tool_choice not supported"), true},
		{"function 400", errors.New("400 function calling unsupported by model"), true},
		{"bad request 400", errors.New("Bad Request (400): tools not available"), true},
		{"400 without tool words", errors.New("request failed 400"), false},
		{"tool without 400", errors.New("tool failed: connection refused"), false},
		{"unrelated 500", errors.New("request failed with status 500: internal error"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isToolUnsupportedError(tc.err); got != tc.want {
				t.Errorf("isToolUnsupportedError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
