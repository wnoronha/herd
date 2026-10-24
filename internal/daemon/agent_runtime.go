package daemon

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/wnoronha/herd/internal/agent"
)

// AgentRuntime owns the embedded agent engine and mailbox reactive loop.
type AgentRuntime struct {
	d  *Daemon
	wg sync.WaitGroup
}

func newAgentRuntime(d *Daemon) *AgentRuntime {
	return &AgentRuntime{d: d}
}

// Start initializes the agent engine, seeds default agent definitions, and starts the inbox watcher.
func (a *AgentRuntime) Start(ctx context.Context) {
	agentsDir := filepath.Join(a.d.Config.ConfigDir, "agents")
	if seededFile, created, err := agent.EnsureDefaultAgentsFile(agentsDir); err == nil && created {
		a.d.Logger.Named("agent").Infof("Seeded default AGENTS.md at %s", seededFile)
	}

	a.d.AgentEngine = agent.NewEngine(agent.Config{NodeName: a.d.Config.NodeName}, a.d, agentsDir)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.d.runAgentInboxWatcher(ctx)
	}()
}

// Stop waits for the inbox watcher goroutine to exit (ctx cancellation is owned by the daemon).
func (a *AgentRuntime) Stop() {
	a.wg.Wait()
}
