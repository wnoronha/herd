package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/adk/v2/session/database"
	"google.golang.org/genai"
)

// Engine orchestrates AI agent prompt processing through ADK llmagent + runner.
type Engine struct {
	config     Config
	tools      map[string]ToolDefinition
	cctx       ClusterContext
	agents     map[string]*AgentDefinition
	mu         sync.RWMutex
	sessionSvc session.Service
	sessionMu  sync.Mutex
	confirm    confirmState
}

// herdADKAppName and herdADKUserID match the runner invocation identity
// in runADKAgent and collectADKTrace.
const (
	herdADKAppName = "herd"
	herdADKUserID  = "herd-user"
	// sessionsDBFile is the SQLite file under StateDir holding chat sessions.
	sessionsDBFile = "sessions.db"
	// compactionInterval triggers ADK sliding-window compaction every N
	// user invocations, keeping long chats bounded without extra daemons.
	compactionInterval = 10
)

// sessionService returns the shared ADK session service, creating it lazily.
// With StateDir set it opens the ADK database service backed by SQLite at
// StateDir/sessions.db so chats survive daemon restarts; otherwise (tests,
// empty StateDir, or open failure) it falls back to ADK InMemoryService.
// Stateful multi-turn chat sessions persist here; one-shot prompts with an
// empty SessionID use an ephemeral in-memory service instead so the
// long-lived daemon never leaks one session per prompt.
func (e *Engine) sessionService() session.Service {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	if e.sessionSvc != nil {
		return e.sessionSvc
	}
	if dir := strings.TrimSpace(e.config.StateDir); dir != "" {
		if svc, err := openDatabaseSessionService(dir); err == nil {
			e.sessionSvc = svc
			return e.sessionSvc
		}
	}
	if e.cctx != nil {
		if dir := strings.TrimSpace(e.cctx.GetStateDir()); dir != "" {
			if svc, err := openDatabaseSessionService(dir); err == nil {
				e.sessionSvc = svc
				return e.sessionSvc
			}
		}
	}
	e.sessionSvc = session.InMemoryService()
	return e.sessionSvc
}

// openDatabaseSessionService opens the ADK database session service at
// dir/sessions.db, running AutoMigrate on every startup as ADK requires.
func openDatabaseSessionService(dir string) (session.Service, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, sessionsDBFile)
	svc, err := database.NewSessionService(sqlite.Open(dbPath))
	if err != nil {
		return nil, err
	}
	if err := database.AutoMigrate(svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// compactionConfig enables ADK sliding-window compaction for chat sessions.
func compactionConfig() *compaction.Config {
	return &compaction.Config{CompactionInterval: compactionInterval}
}

// SessionInfo summarizes one persisted chat session for list/resume.
type SessionInfo struct {
	ID             string    `json:"id"`
	LastUpdateTime time.Time `json:"last_update_time"`
}

// ListSessions returns persisted chat sessions for the Herd app/user.
func (e *Engine) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	svc := e.sessionService()
	list, err := svc.List(ctx, &session.ListRequest{AppName: herdADKAppName, UserID: herdADKUserID})
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	if list == nil {
		return out, nil
	}
	for _, s := range list.Sessions {
		if s == nil {
			continue
		}
		out = append(out, SessionInfo{ID: s.ID(), LastUpdateTime: s.LastUpdateTime()})
	}
	return out, nil
}

// newSessionID generates a unique session identifier.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("herd-%d", time.Now().UnixNano())
	}
	return "herd-" + hex.EncodeToString(b[:])
}

// NewEngine initializes an agent engine instance with the default cluster tools.
func NewEngine(cfg Config, cctx ClusterContext, extraDirs ...string) *Engine {
	ApplyConfigDefaults(&cfg)

	agentsMap := make(map[string]*AgentDefinition)
	for k, v := range LoadDefaultAgents(extraDirs...) {
		agentsMap[k] = v
	}

	eng := &Engine{
		config: cfg,
		cctx:   cctx,
		agents: agentsMap,
	}
	eng.tools = registerDefaultToolMap(cctx, eng)
	return eng
}

// RegisterAgent registers a declarative agent definition in the engine.
func (e *Engine) RegisterAgent(def *AgentDefinition) {
	if def == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.agents == nil {
		e.agents = make(map[string]*AgentDefinition)
	}
	if def.Name != "" {
		e.agents[strings.ToLower(def.Name)] = def
	}
	if def.Role != "" {
		e.agents[strings.ToLower(def.Role)] = def
	}
}

// GetAgent retrieves a registered agent definition by role or name.
func (e *Engine) GetAgent(roleOrName string) (*AgentDefinition, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.agents == nil {
		return nil, false
	}
	def, ok := e.agents[strings.ToLower(roleOrName)]
	return def, ok
}

// ListAgents returns all unique registered agent definitions.
func (e *Engine) ListAgents() []*AgentDefinition {
	e.mu.RLock()
	defer e.mu.RUnlock()
	seen := make(map[*AgentDefinition]bool)
	var list []*AgentDefinition
	for _, a := range e.agents {
		if !seen[a] {
			seen[a] = true
			list = append(list, a)
		}
	}
	return list
}

// getEffectiveTools filters available tools by AllowedTools if specified.
// It always returns a copy so callers can neither race nor mutate engine state.
func (e *Engine) getEffectiveTools(allowed []string) map[string]ToolDefinition {
	if len(allowed) == 0 {
		out := make(map[string]ToolDefinition, len(e.tools))
		for name, def := range e.tools {
			out[name] = def
		}
		return out
	}
	allowedMap := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedMap[strings.ToLower(strings.TrimSpace(a))] = true
	}
	filtered := make(map[string]ToolDefinition)
	for name, def := range e.tools {
		if allowedMap[strings.ToLower(name)] {
			filtered[name] = def
		}
	}
	return filtered
}

// resolveEffectiveConfig checks KV store for agent:config:global and agent:config:nodes/<node> overrides.
func (e *Engine) resolveEffectiveConfig() Config {
	effective := e.config
	if e.cctx == nil {
		return effective
	}
	kvStore := e.cctx.GetKVStore()
	if kvStore == nil {
		return effective
	}

	// 1. Check global cluster agent config: agent:config:global
	if entry, ok := kvStore.Get("agent:config:global"); ok && len(entry.Value) > 0 {
		var gCfg AgentClusterConfig
		if err := json.Unmarshal(entry.Value, &gCfg); err == nil {
			applyClusterConfig(&effective, gCfg)
		}
	}

	// 2. Check per-node override: agent:config:nodes/<node>
	nodeKey := fmt.Sprintf("agent:config:nodes/%s", effective.NodeName)
	if entry, ok := kvStore.Get(nodeKey); ok && len(entry.Value) > 0 {
		var nCfg AgentClusterConfig
		if err := json.Unmarshal(entry.Value, &nCfg); err == nil {
			applyClusterConfig(&effective, nCfg)
		}
	}

	return effective
}

// applyClusterConfig overlays non-empty KV cluster-agent fields onto cfg.
func applyClusterConfig(effective *Config, c AgentClusterConfig) {
	if c.Provider != "" {
		effective.Provider = c.Provider
	}
	if c.Model != "" {
		effective.OpenAIModel = c.Model
		effective.Model = c.Model
	}
	if c.BaseURL != "" {
		effective.OpenAIBaseURL = c.BaseURL
	}
	if c.APIKey != "" {
		if c.Provider == "gemini" {
			effective.APIKey = c.APIKey
		} else {
			effective.OpenAIAPIKey = c.APIKey
		}
	}
	if c.SystemPrompt != "" {
		effective.SystemPrompt = c.SystemPrompt
	}
}

func (e *Engine) effectiveConfigForRequest(req *PromptRequest) (Config, []string, error) {
	effCfg := e.resolveEffectiveConfig()
	var allowedTools []string
	if req.Role != "" {
		agentDef, ok := e.GetAgent(req.Role)
		if !ok {
			return effCfg, nil, fmt.Errorf("agent role or name '%s' not found", req.Role)
		}
		if agentDef.Model != "" {
			effCfg.Model = agentDef.Model
			effCfg.OpenAIModel = agentDef.Model
		}
		if agentDef.Provider != "" {
			effCfg.Provider = agentDef.Provider
		}
		if agentDef.BaseURL != "" {
			effCfg.OpenAIBaseURL = agentDef.BaseURL
		}
		if agentDef.APIKey != "" {
			if effCfg.Provider == "gemini" {
				effCfg.APIKey = agentDef.APIKey
			} else {
				effCfg.OpenAIAPIKey = agentDef.APIKey
			}
		}
		if agentDef.SystemPrompt != "" {
			effCfg.SystemPrompt = agentDef.SystemPrompt
		}
		if len(agentDef.AllowedTools) > 0 {
			allowedTools = agentDef.AllowedTools
		}
	}
	if req.Model != "" {
		effCfg.Model = req.Model
		effCfg.OpenAIModel = req.Model
	}
	if req.Provider != "" {
		effCfg.Provider = req.Provider
	}
	return effCfg, allowedTools, nil
}

// decidePendingConfirmation answers a yes or no request without a model
// turn. It reports done=false when req carries no decision for a live
// session, letting the caller run the model normally.
func (e *Engine) decidePendingConfirmation(ctx context.Context, req *PromptRequest, start time.Time, modelUsed string) (*PromptResponse, bool) {
	if req.ToolDecision == nil || req.SessionID == "" {
		return nil, false
	}
	if !req.ToolDecision.Approved {
		if toolName, ok := e.denyPending(req.SessionID); ok {
			return &PromptResponse{
				Response:    fmt.Sprintf("Tool call '%s' was declined. No action was executed.", toolName),
				ModelUsed:   modelUsed,
				ExecutionMs: time.Since(start).Milliseconds(),
			}, true
		}
		return &PromptResponse{
			Response:    "No tool call awaits approval in this session.",
			ModelUsed:   modelUsed,
			ExecutionMs: time.Since(start).Milliseconds(),
		}, true
	}
	if entry, ok := e.takePending(req.SessionID); ok {
		called := e.executeApprovedCall(ctx, entry)
		return &PromptResponse{
			Response:    fmt.Sprintf("Approved and executed %s.\nResult: %s", entry.tool, called.Result),
			ToolsCalled: []ToolCall{called},
			ModelUsed:   modelUsed,
			ExecutionMs: time.Since(start).Milliseconds(),
		}, true
	}
	return nil, false
}

// ExecutePrompt runs a task prompt through the ADK llmagent + runner loop.
// System prompts travel via llmagent InstructionProvider so markdown prompts
// containing {curly_braces} never trigger ADK session-state template errors.
// The run_agent delegation depth travels in ctx to cap recursive self-delegation.
func (e *Engine) ExecutePrompt(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
	if req == nil || req.Prompt == "" {
		return nil, errors.New("prompt cannot be empty")
	}
	ctx = contextWithRunAgentDepth(ctx)

	start := time.Now()
	effCfg, allowedTools, err := e.effectiveConfigForRequest(req)
	if err != nil {
		return nil, err
	}

	llm, modelUsed, hint, llmErr := e.resolveADKModel(ctx, effCfg)
	if llmErr != nil {
		return nil, llmErr
	}
	// Deterministic fallback still runs inside ADK so 100% of tool execution
	// and tracing stays unified; the auto-probe hint surfaces in the no-match
	// note, preserving the legacy coordinator contract.
	if llm == nil {
		if hint != "" {
			hint = "Ollama auto-probe: " + hint
		}
		llm = newDeterministicLLM(e.validateExecPolicy, hint)
		modelUsed = deterministicModelName
	}

	// A yes or no answer to a pending confirmation never reaches the
	// model. A no leaves the call unexecuted. A yes runs the exact
	// recorded call once instead of a new model turn, so approval works
	// with every backend.
	if decided, done := e.decidePendingConfirmation(ctx, req, start, modelUsed); done {
		return decided, nil
	}

	resp, err := e.runADKAgent(ctx, req, effCfg, allowedTools, start, llm, modelUsed, false)
	if err != nil {
		// Tool-less retry for local models without tool support: same ADK
		// openaimodel + llmagent with Tools: nil on HTTP 400.
		if llm != nil && !isDeterministicLLM(llm) && isToolUnsupportedError(err) {
			retryResp, retryErr := e.runADKAgent(ctx, req, effCfg, nil, start, llm, modelUsed, true)
			if retryErr != nil {
				return nil, retryErr
			}
			resp = retryResp
		} else {
			return nil, err
		}
	}

	if resp != nil {
		resp.NodeName = e.config.NodeName
		if req.Role != "" {
			resp.AgentRole = req.Role
		} else {
			resp.AgentRole = "coordinator"
		}
	}

	return resp, nil
}

// isDeterministicLLM reports whether llm is the air-gapped fallback.
func isDeterministicLLM(llm model.LLM) bool {
	_, ok := llm.(*deterministicLLM)
	return ok
}

// StreamEvent carries one partial agent event to streaming callers.
type StreamEvent struct {
	TextDelta string
	ToolCall  *ToolCall
}

// ExecutePromptStreaming runs a task prompt and invokes yield for each
// partial text delta and dispatched tool call as ADK events arrive. It
// returns the same final PromptResponse as ExecutePrompt.
func (e *Engine) ExecutePromptStreaming(ctx context.Context, req *PromptRequest, yield func(ev StreamEvent) bool) (*PromptResponse, error) {
	if req == nil || req.Prompt == "" {
		return nil, errors.New("prompt cannot be empty")
	}
	ctx = contextWithRunAgentDepth(ctx)

	start := time.Now()
	effCfg, allowedTools, err := e.effectiveConfigForRequest(req)
	if err != nil {
		return nil, err
	}

	llm, modelUsed, hint, llmErr := e.resolveADKModel(ctx, effCfg)
	if llmErr != nil {
		return nil, llmErr
	}
	if llm == nil {
		if hint != "" {
			hint = "Ollama auto-probe: " + hint
		}
		llm = newDeterministicLLM(e.validateExecPolicy, hint)
		modelUsed = deterministicModelName
	}

	if decided, done := e.decidePendingConfirmation(ctx, req, start, modelUsed); done {
		return decided, nil
	}

	resp, err := e.runADKAgentStreaming(ctx, req, effCfg, allowedTools, start, llm, modelUsed, false, yield)
	if err != nil {
		if llm != nil && !isDeterministicLLM(llm) && isToolUnsupportedError(err) {
			retryResp, retryErr := e.runADKAgentStreaming(ctx, req, effCfg, nil, start, llm, modelUsed, true, yield)
			if retryErr != nil {
				return nil, retryErr
			}
			resp = retryResp
		} else {
			return nil, err
		}
	}

	if resp != nil {
		resp.NodeName = e.config.NodeName
		if req.Role != "" {
			resp.AgentRole = req.Role
		} else {
			resp.AgentRole = "coordinator"
		}
	}

	return resp, nil
}

// runADKAgentStreaming mirrors runADKAgent but forwards partial text and
// tool dispatches to yield as the runner event stream arrives.
func (e *Engine) runADKAgentStreaming(ctx context.Context, req *PromptRequest, effCfg Config, allowedTools []string, start time.Time, llm model.LLM, modelUsed string, toolsNil bool, yield func(ev StreamEvent) bool) (*PromptResponse, error) {
	systemPrompt := e.resolveSystemPrompt(effCfg, req)

	// Tag chat sessions like the unary path so destructive tools pause
	// for approval. Delegated sub-runs inherit the outer chat session.
	gateSessionID := req.SessionID
	if gateSessionID == "" {
		if outer := confirmationModeFrom(ctx); outer.interactive {
			gateSessionID = outer.sessionID
		}
	}
	ctx = contextWithConfirmationMode(ctx, gateSessionID)

	tools := e.buildADKTools(allowedTools)
	if toolsNil {
		tools = nil
	}

	cfg := llmagent.Config{
		Name:        "herd-agent",
		Description: "Herd mesh cluster agent",
		Model:       llm,
		InstructionProvider: func(_ agent.ReadonlyContext) (string, error) {
			return systemPrompt, nil
		},
		Tools:                tools,
		BeforeToolCallbacks:  []llmagent.BeforeToolCallback{herdBeforeToolCallback(e)},
		OnToolErrorCallbacks: []llmagent.OnToolErrorCallback{herdOnToolErrorCallback()},
	}
	adkAgent, err := llmagent.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ADK agent: %w", err)
	}

	svc := e.sessionService()
	sessionID := req.SessionID
	if sessionID == "" {
		svc = session.InMemoryService()
		sessionID = newSessionID()
	}

	r, err := runner.New(runner.Config{
		AppName:           herdADKAppName,
		Agent:             adkAgent,
		SessionService:    svc,
		AutoCreateSession: true,
		Compaction:        compactionConfig(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create ADK runner: %w", err)
	}

	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: foldHistoryIntoPrompt(req)}},
	}

	trace, err := collectADKTraceStreaming(ctx, r, sessionID, msg, yield)
	if err != nil {
		return nil, err
	}

	resp := &PromptResponse{
		Response:    trace.text,
		ToolsCalled: trace.toolsCalled,
		ModelUsed:   modelUsed,
		ExecutionMs: time.Since(start).Milliseconds(),
	}
	// Surface a blocked destructive call so the chat client can ask yes
	// or no and return the answer in the next request.
	if prompt, ok := e.pendingPrompt(sessionID); ok {
		prompt := prompt
		resp.Confirmation = &prompt
	}
	return resp, nil
}

// collectADKTraceStreaming drains the runner event stream like
// collectADKTrace while forwarding partial text and tool dispatches.
func collectADKTraceStreaming(ctx context.Context, r *runner.Runner, sessionID string, msg *genai.Content, yield func(ev StreamEvent) bool) (*adkTrace, error) {
	pending := map[string]*pendingCall{}
	var order []string
	results := map[string]string{}
	var finalResponse strings.Builder
	streaming := yield != nil

	emit := func(ev StreamEvent) {
		if !streaming {
			return
		}
		// Best-effort: a closed chat window stops rendering but the run
		// still completes so the final response stays accurate.
		defer func() { _ = recover() }()
		if !yield(ev) {
			streaming = false
		}
	}

	for ev, rerr := range r.Run(ctx, "herd-user", sessionID, msg, agent.RunConfig{}) {
		if rerr != nil {
			return nil, rerr
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		isUserEvent := ev.Content.Role == "user"
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			switch {
			case part.FunctionCall != nil:
				recordFunctionCall(pending, &order, part.FunctionCall)
				argBytes, _ := json.Marshal(part.FunctionCall.Args)
				if argBytes == nil {
					argBytes = []byte("{}")
				}
				emit(StreamEvent{ToolCall: &ToolCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argBytes),
				}})
			case part.FunctionResponse != nil:
				results[part.FunctionResponse.ID] = marshalResponseMap(part.FunctionResponse.Response)
			case part.Text != "":
				if !isUserEvent {
					finalResponse.WriteString(part.Text)
					emit(StreamEvent{TextDelta: part.Text})
				}
			}
		}
	}

	var toolsCalled []ToolCall
	for _, id := range order {
		p := pending[id]
		toolsCalled = append(toolsCalled, ToolCall{
			Name:      p.name,
			Arguments: p.args,
			Result:    results[id],
		})
	}
	return &adkTrace{toolsCalled: toolsCalled, text: finalResponse.String()}, nil
}

// resolveADKModel picks the ADK model for the effective config: explicit
// Gemini, explicit OpenAI-compatible, auto-probed local Ollama, or nil (the
// caller substitutes deterministicLLM) with a human-readable hint.
func (e *Engine) resolveADKModel(ctx context.Context, effCfg Config) (model.LLM, string, string, error) {
	if effCfg.Provider == "gemini" || (effCfg.APIKey != "" && effCfg.Provider != "openai") {
		llm, name, err := buildADKModel(ctx, effCfg)
		if err != nil {
			// Explicit selection failed: propagate as a hard error.
			return nil, "", "", err
		}
		return llm, name, "", nil
	}
	if openAIPathExplicit(effCfg) {
		llm, name, err := buildADKModel(ctx, effCfg)
		if err != nil {
			if effCfg.Provider == "openai" || effCfg.OpenAIAPIKey != "" {
				return nil, "", "", err
			}
			return nil, "", "OpenAI attempt failed (" + err.Error() + ");", nil
		}
		return llm, name, "", nil
	}
	// Auto-probe local Ollama before falling back.
	autoCfg, autoHint, fatalErr := e.resolveAutoOllamaConfig(ctx, effCfg)
	if fatalErr != nil {
		return nil, "", "", fatalErr
	}
	if autoCfg != nil {
		llm, name, err := buildADKModel(ctx, *autoCfg)
		if err != nil {
			return nil, "", autoHint + " OpenAI attempt failed (" + err.Error() + ");", nil
		}
		return llm, name, autoHint, nil
	}
	return nil, "", autoHint, nil
}

// runADKAgent executes one prompt through llmagent.New + runner.Runner and
// reconstructs the PromptResponse trace from the session event stream.
// FunctionCall parts and their FunctionResponse payloads are matched by call
// ID, preserving the exact order of FunctionCalls within each turn.
func (e *Engine) runADKAgent(ctx context.Context, req *PromptRequest, effCfg Config, allowedTools []string, start time.Time, llm model.LLM, modelUsed string, toolsNil bool) (*PromptResponse, error) {
	systemPrompt := e.resolveSystemPrompt(effCfg, req)

	// Tag chat sessions so destructive tools pause for approval. One-shot
	// calls carry no SessionID and keep direct execution. Delegated sub-runs
	// (run_agent) inherit the outer chat session for the gate so personas
	// cannot bypass approval, while their runner session stays ephemeral.
	gateSessionID := req.SessionID
	if gateSessionID == "" {
		if outer := confirmationModeFrom(ctx); outer.interactive {
			gateSessionID = outer.sessionID
		}
	}
	ctx = contextWithConfirmationMode(ctx, gateSessionID)

	tools := e.buildADKTools(allowedTools)
	if toolsNil {
		tools = nil
	}

	cfg := llmagent.Config{
		Name:        "herd-agent",
		Description: "Herd mesh cluster agent",
		Model:       llm,
		InstructionProvider: func(_ agent.ReadonlyContext) (string, error) {
			return systemPrompt, nil
		},
		Tools:                tools,
		BeforeToolCallbacks:  []llmagent.BeforeToolCallback{herdBeforeToolCallback(e)},
		OnToolErrorCallbacks: []llmagent.OnToolErrorCallback{herdOnToolErrorCallback()},
	}
	adkAgent, err := llmagent.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ADK agent: %w", err)
	}

	// Session handling: persist only when the caller supplies a SessionID.
	// One-shot prompts run on an ephemeral service that is discarded with the
	// call, so the daemon never accumulates unbounded sessions.
	// Persistence is the ADK database service at StateDir/sessions.db, so
	// chats survive daemon restarts. ADK compaction bounds prompt growth.
	svc := e.sessionService()
	sessionID := req.SessionID
	if sessionID == "" {
		svc = session.InMemoryService()
		sessionID = newSessionID()
	}

	r, err := runner.New(runner.Config{
		AppName:           herdADKAppName,
		Agent:             adkAgent,
		SessionService:    svc,
		AutoCreateSession: true,
		Compaction:        compactionConfig(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create ADK runner: %w", err)
	}

	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: foldHistoryIntoPrompt(req)}},
	}

	trace, err := collectADKTrace(ctx, r, sessionID, msg)
	if err != nil {
		return nil, err
	}

	resp := &PromptResponse{
		Response:    trace.text,
		ToolsCalled: trace.toolsCalled,
		ModelUsed:   modelUsed,
		ExecutionMs: time.Since(start).Milliseconds(),
	}
	// Surface a blocked destructive call so the chat client can ask yes
	// or no and return the answer in the next request.
	if prompt, ok := e.pendingPrompt(sessionID); ok {
		prompt := prompt
		resp.Confirmation = &prompt
	}
	return resp, nil
}

// resolveSystemPrompt returns the effective system prompt with the target-node
// suffix, defaulting to the Herd coordinator prompt.
func (e *Engine) resolveSystemPrompt(effCfg Config, req *PromptRequest) string {
	systemPrompt := effCfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf(
			"You are Herd Agent for node '%s'. You are part of a decentralized P2P mesh cluster. "+
				"Use your cluster and research tools (cluster_roster, cluster_exec, cluster_cp, shared_kv, agent_send_mail, agent_broadcast, agent_read_mailbox, fetch_url, google_search, dns_lookup) "+
				"to discover nodes, execute actions, transfer files, search the web, fetch web content, and coordinate tasks across machines.",
			e.config.NodeName,
		)
	}
	if req.TargetNode != "" {
		systemPrompt += fmt.Sprintf("\nThe user targeted node '%s' for this task. Ensure your operations (such as cluster_exec or cluster_cp) target '%s'.", req.TargetNode, req.TargetNode)
	}
	return systemPrompt
}

// adkTrace holds the reconstructed tool-call trace and final response text.
type adkTrace struct {
	toolsCalled []ToolCall
	text        string
}

// collectADKTrace drains the runner event stream, matching FunctionResponse
// payloads to their FunctionCall by ID while preserving call order.
func collectADKTrace(ctx context.Context, r *runner.Runner, sessionID string, msg *genai.Content) (*adkTrace, error) {
	pending := map[string]*pendingCall{}
	var order []string
	results := map[string]string{}
	var finalResponse strings.Builder

	for ev, rerr := range r.Run(ctx, "herd-user", sessionID, msg, agent.RunConfig{}) {
		if rerr != nil {
			return nil, rerr
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		// FunctionResponse events carry Role "user"; only the echoed
		// user prompt itself must not leak into the response text.
		isUserEvent := ev.Content.Role == "user"
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			switch {
			case part.FunctionCall != nil:
				recordFunctionCall(pending, &order, part.FunctionCall)
			case part.FunctionResponse != nil:
				results[part.FunctionResponse.ID] = marshalResponseMap(part.FunctionResponse.Response)
			case part.Text != "":
				if !isUserEvent {
					finalResponse.WriteString(part.Text)
				}
			}
		}
	}

	var toolsCalled []ToolCall
	for _, id := range order {
		p := pending[id]
		toolsCalled = append(toolsCalled, ToolCall{
			Name:      p.name,
			Arguments: p.args,
			Result:    results[id],
		})
	}
	return &adkTrace{toolsCalled: toolsCalled, text: finalResponse.String()}, nil
}

// pendingCall records one emitted FunctionCall until its response arrives.
type pendingCall struct {
	name string
	args string
}

// recordFunctionCall stores one emitted FunctionCall in call order.
func recordFunctionCall(pending map[string]*pendingCall, order *[]string, fc *genai.FunctionCall) {
	argBytes, _ := json.Marshal(fc.Args)
	if argBytes == nil {
		argBytes = []byte("{}")
	}
	pending[fc.ID] = &pendingCall{name: fc.Name, args: string(argBytes)}
	*order = append(*order, fc.ID)
}

// marshalResponseMap renders a FunctionResponse payload as a JSON object
// string for the ToolsCalled audit trace.
func marshalResponseMap(response map[string]any) string {
	resBytes, _ := json.Marshal(response)
	if resBytes == nil {
		return "{}"
	}
	return string(resBytes)
}

// foldHistoryIntoPrompt prepends req.History messages to the user prompt so
// prior conversation context survives for one-shot calls without a persisted
// session. History is only for one-shot calls: when SessionID is present the
// ADK session service already accumulates full event history, so folding here
// would double-count prior context.
func foldHistoryIntoPrompt(req *PromptRequest) string {
	if len(req.History) == 0 || req.SessionID != "" {
		return req.Prompt
	}
	var sb strings.Builder
	sb.WriteString("Previous conversation:\n")
	for _, h := range req.History {
		role := h.Role
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(h.Content)
		sb.WriteString("\n")
	}
	sb.WriteString("\nCurrent task:\n")
	sb.WriteString(req.Prompt)
	return sb.String()
}
