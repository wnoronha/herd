package daemon

import (
	"context"
	"encoding/json"
	"errors"

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

	report, err := engine.BuildStatus(ctx, opts)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	var resp ipc.AgentStatusResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
