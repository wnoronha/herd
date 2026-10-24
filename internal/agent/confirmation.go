package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// destructiveToolNames lists tools that mutate local or remote state. Calls
// to these tools from interactive chat sessions require an explicit yes or
// no answer before Herd executes them. One-shot prompts, mailbox tasks, and
// automated callers cannot answer, so they keep the existing behavior.
var destructiveToolNames = map[string]bool{
	"cluster_exec":    true,
	"cluster_cp":      true,
	"file_write":      true,
	"artifact_store":  true,
	"cluster_forward": true,
	"cluster_manage":  true,
}

// ConfirmationRequiredFor reports whether toolName mutates state and needs
// approval in interactive chat sessions. Matching ignores case and padding.
func ConfirmationRequiredFor(toolName string) bool {
	return destructiveToolNames[strings.ToLower(strings.TrimSpace(toolName))]
}

// ToolDecision carries the chat user's yes or no answer to one pending
// confirmation prompt. Approved runs the blocked call once; a denied call
// never executes.
type ToolDecision struct {
	Approved bool `json:"approved"`
}

// ConfirmationPrompt describes one destructive call blocked for approval.
// The CLI (command-line interface) renders ToolName, Arguments, and Hint,
// then returns the user's answer in the next request.
type ConfirmationPrompt struct {
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
	Hint      string `json:"hint"`
}

// pendingApproval records one blocked call per chat session.
type pendingApproval struct {
	tool string
	args string
}

// confirmState holds pending tool approvals keyed by chat session ID.
type confirmState struct {
	mu      sync.Mutex
	pending map[string]pendingApproval
}

// recordPendingConfirmation stores the blocked call for sessionID, replacing
// any prior entry. Approval never carries across different calls.
func (e *Engine) recordPendingConfirmation(sessionID, toolName, argsJSON string) {
	e.confirm.mu.Lock()
	defer e.confirm.mu.Unlock()
	if e.confirm.pending == nil {
		e.confirm.pending = make(map[string]pendingApproval)
	}
	e.confirm.pending[sessionID] = pendingApproval{tool: toolName, args: argsJSON}
}

// denyPending drops the session entry and reports the blocked tool name.
func (e *Engine) denyPending(sessionID string) (string, bool) {
	e.confirm.mu.Lock()
	defer e.confirm.mu.Unlock()
	entry, ok := e.confirm.pending[sessionID]
	if !ok {
		return "", false
	}
	delete(e.confirm.pending, sessionID)
	return entry.tool, true
}

// takePending removes the session entry and returns it. Approval executes
// the recorded call directly, so taking also consumes it: each yes runs
// the exact blocked call a single time.
func (e *Engine) takePending(sessionID string) (pendingApproval, bool) {
	e.confirm.mu.Lock()
	defer e.confirm.mu.Unlock()
	entry, ok := e.confirm.pending[sessionID]
	if !ok {
		return pendingApproval{}, false
	}
	delete(e.confirm.pending, sessionID)
	return entry, true
}

// pendingPrompt returns the session entry for the response path.
func (e *Engine) pendingPrompt(sessionID string) (ConfirmationPrompt, bool) {
	e.confirm.mu.Lock()
	defer e.confirm.mu.Unlock()
	entry, ok := e.confirm.pending[sessionID]
	if !ok {
		return ConfirmationPrompt{}, false
	}
	return ConfirmationPrompt{
		ToolName:  entry.tool,
		Arguments: entry.args,
		Hint:      confirmationHint(entry.tool),
	}, true
}

// confirmationHint explains why Herd blocked the call.
func confirmationHint(toolName string) string {
	return fmt.Sprintf("Tool '%s' mutates cluster or local state. Answer yes to run it once, or no to skip it.", toolName)
}

// confirmationModeKey carries chat confirmation state through the request
// context into ADK callbacks, following the run_agent depth pattern.
type confirmationModeKey struct{}

// confirmationMode marks interactive chat runs. Only chat sessions carry a
// SessionID, so only they can answer a prompt.
type confirmationMode struct {
	interactive bool
	sessionID   string
}

// contextWithConfirmationMode tags ctx for the ADK run. A non-empty
// sessionID marks an interactive chat session.
func contextWithConfirmationMode(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, confirmationModeKey{}, confirmationMode{
		interactive: sessionID != "",
		sessionID:   sessionID,
	})
}

// confirmationModeFrom reads the tag. Callbacks receive an ADK context that
// embeds the request context, so values set before runner.Run stay visible.
func confirmationModeFrom(ctx context.Context) confirmationMode {
	mode, _ := ctx.Value(confirmationModeKey{}).(confirmationMode)
	return mode
}

// canonicalToolArgs renders tool args in a stable form for the prompt.
func canonicalToolArgs(args map[string]any) string {
	if args == nil {
		return "{}"
	}
	raw, err := json.Marshal(args)
	if err != nil || raw == nil {
		return "{}"
	}
	return string(raw)
}

// adkConfirmer covers the ADK confirmation call used by the gate. The
// gate accepts a plain context so unit tests run without ADK types.
type adkConfirmer interface {
	RequestConfirmation(hint string, payload any) error
}

// requestADKConfirmation raises the standard ADK confirmation event when
// ctx carries it. It returns an error when ctx has no ADK channel; the
// caller still blocks the call through its short-circuit result.
func requestADKConfirmation(ctx context.Context, hint string, payload any) error {
	confirmer, ok := ctx.(adkConfirmer)
	if !ok || confirmer == nil {
		return fmt.Errorf("no ADK confirmation channel in context")
	}
	return confirmer.RequestConfirmation(hint, payload)
}

// confirmationGate blocks destructive calls in interactive sessions until
// the user approves them. It returns proceed=false plus a short-circuit
// result when the call must wait for a yes or no answer. Policy checks run
// before this gate, so rejections still fail closed without prompting.
func (e *Engine) confirmationGate(ctx context.Context, toolName string, args map[string]any) (bool, map[string]any) {
	if !ConfirmationRequiredFor(toolName) {
		return true, nil
	}
	mode := confirmationModeFrom(ctx)
	if !mode.interactive || mode.sessionID == "" {
		return true, nil
	}
	argsJSON := canonicalToolArgs(args)
	hint := confirmationHint(toolName)
	// Raise the standard ADK confirmation event so the request is visible
	// in session history. The returned map blocks execution; Herd delivers
	// the yes or no answer through the next chat request.
	_ = requestADKConfirmation(ctx, hint, map[string]any{"tool": toolName, "arguments": args})
	e.recordPendingConfirmation(mode.sessionID, toolName, argsJSON)
	return false, map[string]any{
		"confirmation_required": true,
		"tool":                  toolName,
		"arguments":             args,
		"hint":                  hint,
	}
}

// executeApprovedCall runs the recorded call directly after a yes answer.
// Direct execution keeps approval backend-independent: the model does not
// need to regenerate the call. Policy checks run again here so a whitelist
// change between prompt and answer still fails closed.
func (e *Engine) executeApprovedCall(ctx context.Context, entry pendingApproval) ToolCall {
	fail := func(format string, a ...any) ToolCall {
		raw, _ := json.Marshal(map[string]any{"error": fmt.Sprintf(format, a...)})
		if raw == nil {
			raw = []byte(`{"error":"tool execution failed"}`)
		}
		return ToolCall{Name: entry.tool, Arguments: entry.args, Result: string(raw)}
	}

	e.mu.RLock()
	def, ok := e.tools[entry.tool]
	e.mu.RUnlock()
	if !ok {
		return fail("tool '%s' is not allowed or registered for this agent", entry.tool)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(entry.args), &args); err != nil {
		args = map[string]any{}
	}
	if def.Name == "cluster_exec" {
		if cmd, ok := args["command"].(string); ok {
			if policyErr := e.validateExecPolicy(cmd); policyErr != nil {
				return fail("%s", policyErr.Error())
			}
		}
	}
	if def.Handler == nil {
		return fail("tool '%s' has no handler", entry.tool)
	}
	result, err := def.Handler(ctx, args)
	if err != nil {
		return fail("%s", err.Error())
	}
	if result == nil {
		return ToolCall{Name: entry.tool, Arguments: entry.args, Result: `{"result":null}`}
	}
	raw, merr := json.Marshal(result)
	if merr != nil || raw == nil {
		return ToolCall{Name: entry.tool, Arguments: entry.args, Result: fmt.Sprintf(`{"result":%q}`, fmt.Sprintf("%v", result))}
	}
	return ToolCall{Name: entry.tool, Arguments: entry.args, Result: string(raw)}
}
