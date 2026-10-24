package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"
)

// maxRunAgentDepth caps run_agent self-delegation recursion. The depth travels
// in the request context, incremented once per Engine.ExecutePrompt entry.
const maxRunAgentDepth = 5

type runAgentDepthKey struct{}

// contextWithRunAgentDepth returns ctx carrying an incremented delegation depth.
func contextWithRunAgentDepth(ctx context.Context) context.Context {
	depth, _ := ctx.Value(runAgentDepthKey{}).(int)
	return context.WithValue(ctx, runAgentDepthKey{}, depth+1)
}

// runAgentDepthOf reads the delegation depth from ctx.
func runAgentDepthOf(ctx context.Context) int {
	depth, _ := ctx.Value(runAgentDepthKey{}).(int)
	return depth
}

// herdADKTool bridges one Herd ToolDefinition into the ADK tool runtime. It
// implements tool.Tool plus the structural internal interfaces ADK's base flow
// requires (Declaration + Run + ProcessRequest via toolutils.PackTool) so Herd
// mesh tools execute inside ADK's parallel tool engine.
type herdADKTool struct {
	def    ToolDefinition
	engine *Engine
}

// Name implements tool.Tool.
func (t *herdADKTool) Name() string { return t.def.Name }

// Description implements tool.Tool.
func (t *herdADKTool) Description() string { return t.def.Description }

// IsLongRunning implements tool.Tool.
func (t *herdADKTool) IsLongRunning() bool { return false }

// Declaration exposes the full JSON schema to the model. ParametersJsonSchema
// carries the complete schema including required fields for both the Gemini
// and OpenAI ADK model backends.
func (t *herdADKTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:                 t.def.Name,
		Description:          t.def.Description,
		ParametersJsonSchema: t.def.Parameters,
	}
}

// ProcessRequest packs this tool into the LLM request for ADK parallel execution.
func (t *herdADKTool) ProcessRequest(_ agent.Context, req *model.LLMRequest) error {
	return toolutils.PackTool(req, t)
}

// Run executes the wrapped Herd handler. cluster_exec policy is enforced here
// as defense-in-depth so no provider path (including deterministicLLM) can
// bypass agent:policy:allowed_exec. Non-map results and handler errors are
// normalized into map[string]any so ADK FunctionResponse serialization never
// fails.
func (t *herdADKTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	argMap, ok := args.(map[string]any)
	if !ok {
		if args == nil {
			argMap = map[string]any{}
		} else {
			return map[string]any{"error": fmt.Sprintf("tool '%s' received invalid arguments type %T", t.def.Name, args)}, nil
		}
	}

	if t.def.Name == "cluster_exec" && t.engine != nil {
		if cmd, ok := argMap["command"].(string); ok {
			if policyErr := t.engine.validateExecPolicy(cmd); policyErr != nil {
				return map[string]any{"error": policyErr.Error()}, nil
			}
		}
	}

	if t.def.Name == "run_agent" {
		if runAgentDepthOf(ctx) > maxRunAgentDepth {
			return map[string]any{"error": "run_agent delegation depth exceeded (max 5); refusing recursive self-delegation"}, nil
		}
	}

	if t.def.Handler == nil {
		return map[string]any{"error": fmt.Sprintf("tool '%s' has no handler", t.def.Name)}, nil
	}
	result, err := t.def.Handler(ctx, argMap)
	if err != nil {
		return map[string]any{"error": err.Error()}, nil
	}
	if result == nil {
		return map[string]any{"result": nil}, nil
	}
	if m, ok := result.(map[string]any); ok {
		return m, nil
	}
	// Normalize non-map results (slices, strings) via JSON round-trip so the
	// FunctionResponse stays a JSON object; fall back to {"result": ...}.
	resBytes, merr := json.Marshal(result)
	if merr != nil {
		return map[string]any{"result": fmt.Sprintf("%v", result)}, nil
	}
	var resMap map[string]any
	if uerr := json.Unmarshal(resBytes, &resMap); uerr != nil {
		return map[string]any{"result": string(resBytes)}, nil
	}
	return resMap, nil
}

// buildADKTools converts the role-scoped Herd tool registry into ADK tools.
func (e *Engine) buildADKTools(allowed []string) []tool.Tool {
	scoped := e.getEffectiveTools(allowed)
	out := make([]tool.Tool, 0, len(scoped))
	for _, def := range scoped {
		def := def
		out = append(out, &herdADKTool{def: def, engine: e})
	}
	return out
}

// herdBeforeToolCallback enforces agent:policy:allowed_exec fail-closed on
// cluster_exec before the tool runs. Returning a non-nil map with a nil error
// short-circuits the tool Run and delivers the structured policy rejection to
// the model.
func herdBeforeToolCallback(e *Engine) llmagent.BeforeToolCallback {
	return func(_ agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		if t == nil || t.Name() != "cluster_exec" {
			return nil, nil
		}
		cmd, _ := args["command"].(string)
		if err := e.validateExecPolicy(cmd); err != nil {
			return map[string]any{"error": err.Error()}, nil
		}
		return nil, nil
	}
}

// herdOnToolErrorCallback preserves the Herd rejection contract for unknown or
// disallowed tool calls: ADK emits newToolNotFoundError when the model calls a
// tool outside its scoped toolset, and this callback formats it as the exact
// rejection string role-scoping tests assert on.
func herdOnToolErrorCallback() llmagent.OnToolErrorCallback {
	return func(_ agent.Context, t tool.Tool, _ map[string]any, _ error) (map[string]any, error) {
		name := "<unknown>"
		if t != nil {
			name = t.Name()
		}
		return map[string]any{"error": fmt.Sprintf("tool '%s' is not allowed or registered for this agent", name)}, nil
	}
}

// validateExecPolicy checks a command against the
// agent:policy:allowed_exec whitelist in the KV store. A missing policy allows
// all; a corrupt policy fails closed.
func (e *Engine) validateExecPolicy(command string) error {
	if e.cctx == nil {
		return nil
	}
	kvStore := e.cctx.GetKVStore()
	if kvStore == nil {
		return nil
	}

	entry, ok := kvStore.Get("agent:policy:allowed_exec")
	if !ok || len(entry.Value) == 0 {
		return nil
	}

	var allowedList []string
	if err := json.Unmarshal(entry.Value, &allowedList); err != nil {
		return fmt.Errorf("invalid cluster policy (agent:policy:allowed_exec): %w", err)
	}
	if len(allowedList) == 0 {
		return nil
	}

	trimmedCmd := strings.TrimSpace(command)
	for _, pattern := range allowedList {
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(trimmedCmd, strings.TrimSuffix(pattern, "*")) {
				return nil
			}
		} else if strings.EqualFold(trimmedCmd, pattern) {
			return nil
		}
	}

	return fmt.Errorf("command %q rejected by cluster policy (agent:policy:allowed_exec)", command)
}
