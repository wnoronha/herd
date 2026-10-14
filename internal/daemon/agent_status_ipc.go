package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/wnoronha/herd/internal/agent"
	"github.com/wnoronha/herd/internal/ipc"
)

// AgentStatus implements ipc.DaemonHandler.
func (d *Daemon) AgentStatus(ctx context.Context, req *ipc.AgentStatusRequest) (*ipc.AgentStatusResponse, error) {
	d.mu.RLock()
	engine := d.AgentEngine
	d.mu.RUnlock()
	if engine == nil {
		return nil, errors.New("agent engine is not initialized")
	}

	opts := agent.StatusCollectOptions{}
	if req != nil {
		opts.Role = req.Role
		opts.Probe = req.Probe
	}
	if opts.Probe {
		// Probes do network I/O (Gemini has no HTTP timeout of its own);
		// bound the whole collection so one stuck probe can't block this
		// connection's serial IPC handler loop indefinitely.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}

	report, err := engine.BuildStatus(ctx, opts)
	if err != nil {
		return nil, err
	}
	return mapAgentStatusReport(report), nil
}

// mapAgentStatusReport converts the engine report field-by-field so renames
// and tag drift fail at compile time instead of silently dropping data the
// way a Marshal/Unmarshal round-trip would.
func mapAgentStatusReport(r *agent.AgentStatusReport) *ipc.AgentStatusResponse {
	resp := &ipc.AgentStatusResponse{
		NodeName:             r.NodeName,
		BackendClass:         r.BackendClass,
		BackendProvider:      r.BackendProvider,
		Model:                r.Model,
		FallbackModel:        r.FallbackModel,
		ConfiguredLLM:        r.ConfiguredLLM,
		ConfigSources:        r.ConfigSources,
		KVGlobalPresent:      r.KVGlobalPresent,
		KVGlobalSummary:      r.KVGlobalSummary,
		RegisteredToolsCount: r.RegisteredToolsCount,
		Reason:               r.Reason,
		FixHints:             r.FixHints,
	}
	for _, p := range r.Personas {
		resp.Personas = append(resp.Personas, ipc.AgentPersonaStatus{
			Name:      p.Name,
			Role:      p.Role,
			ToolCount: p.ToolCount,
			Tools:     p.Tools,
		})
	}
	if r.Role != nil {
		resp.Role = &ipc.AgentRoleStatus{
			Role:             r.Role.Role,
			AllowedTools:     r.Role.AllowedTools,
			MissingFromRole:  r.Role.MissingFromRole,
			UsesFullToolset:  r.Role.UsesFullToolset,
			EffectiveBackend: r.Role.EffectiveBackend,
			EffectiveModel:   r.Role.EffectiveModel,
		}
	}
	if r.Probe != nil {
		resp.Probe = &ipc.AgentProbeStatus{
			Attempted: r.Probe.Attempted,
			OK:        r.Probe.OK,
			Provider:  r.Probe.Provider,
			Error:     r.Probe.Error,
			Models:    r.Probe.Models,
		}
	}
	return resp
}
