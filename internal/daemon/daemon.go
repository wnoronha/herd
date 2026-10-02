package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"
	"herd/internal/agent"
	"herd/internal/config"
	"herd/internal/identity"
	"herd/internal/ipc"
	"herd/internal/kv"
	"herd/internal/logger"
	"herd/internal/roster"
	"herd/internal/subsys"
	"herd/internal/transport/tailcat"
)

// Daemon coordinates the node identity, Tailcat transport, Memberlist gossip, and IPC server.
type Daemon struct {
	Config        *config.Config
	Identity      *identity.Identity
	Transport     *tailcat.Transport
	Store         *roster.Store
	Delegate      *roster.Delegate
	KVStore       *kv.Store
	KVDelegate    *kv.GossipDelegate
	Memberlist    *memberlist.Memberlist
	IPCServer     *ipc.Server
	AgentEngine   *agent.Engine
	StartTime     time.Time
	Logger        logger.Logger
	agentCancel   context.CancelFunc
	agentWg       sync.WaitGroup
	processedMsgs sync.Map
	forwarders    map[string]*subsys.PortForwarder
	forwardersMu  sync.Mutex

	mu         sync.RWMutex
	state      string
	shutdownCh chan struct{}
	closed     atomic.Bool
}

// New creates and initializes a new Daemon instance with a unified Logger.
func New(cfg *config.Config, log logger.Logger) (*Daemon, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if log == nil {
		log = logger.NewNop()
	}
	if cfg.NodeName != "" {
		log = log.WithNode(cfg.NodeName)
	}

	if err := cfg.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("failed to ensure directories: %w", err)
	}

	// Load or generate identity
	id, err := identity.LoadOrGenerate(cfg.DataDir, cfg.NodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize identity: %w", err)
	}

	d := &Daemon{
		Config:     cfg,
		Identity:   id,
		StartTime:  time.Now(),
		Logger:     log,
		state:      "initializing",
		shutdownCh: make(chan struct{}),
		forwarders: make(map[string]*subsys.PortForwarder),
	}

	return d, nil
}

// Start launches the Tailcat transport, Memberlist gossip engine, and IPC server.
func (d *Daemon) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. Initialize Tailcat Transport
	tr, err := tailcat.NewTransport(tailcat.Config{
		BindAddr:         d.Config.BindAddr,
		BindPort:         d.Config.BindPort,
		Identity:         d.Identity,
		PacketBufferSize: 1024,
		Logger:           d.Logger.Named("transport"),
	})
	if err != nil {
		return fmt.Errorf("failed to start tailcat transport: %w", err)
	}
	d.Transport = tr
	d.Config.BindPort = tr.GetPort()

	d.Logger.Infof("Starting Herd daemon for node '%s' (Tailcat Addr: %s)...", d.Config.NodeName, tr.TailcatAddr())

	// 2. Initialize Roster Store & Delegate
	meta := &roster.NodeMeta{
		NodeName:    d.Config.NodeName,
		TailcatAddr: tr.TailcatAddr(),
		Version:     "1.0.0",
		StartTime:   d.StartTime,
	}
	d.Store = roster.NewStore(meta)
	d.Delegate = roster.NewDelegate(d.Store, meta)
	d.Delegate.SetKeyRegistrar(tr)
	tr.RegisterStreamHandler(tailcat.StreamTypeExec, d.handleExecStream)
	tr.RegisterStreamHandler(tailcat.StreamTypeAgent, d.handleAgentStream)
	tr.RegisterStreamHandler(tailcat.StreamTypeFile, d.handleFileStream)
	d.KVStore = kv.NewStore(d.Config.NodeName)
	d.KVDelegate = kv.NewGossipDelegate(d.KVStore, func() int {
		if d.Memberlist != nil {
			return d.Memberlist.NumMembers()
		}
		return 1
	})
	d.Delegate.SetCustomDelegate(d.KVDelegate)

	// 3. Initialize Memberlist
	mcfg := memberlist.DefaultWANConfig()
	mcfg.Name = d.Config.NodeName
	mcfg.BindPort = tr.GetPort()
	mcfg.AdvertisePort = tr.GetPort()
	mcfg.TCPTimeout = 10 * time.Second
	mcfg.ProbeTimeout = 5 * time.Second
	mcfg.ProbeInterval = 5 * time.Second
	mcfg.SuspicionMult = 6
	if tr.TailcatIP() != nil {
		mcfg.AdvertiseAddr = tr.TailcatIP().String()
		mcfg.AdvertisePort = tr.GetPort()
	} else if d.Config.BindAddr != "" && d.Config.BindAddr != "0.0.0.0" {
		mcfg.BindAddr = d.Config.BindAddr
		mcfg.AdvertiseAddr = d.Config.BindAddr
		mcfg.AdvertisePort = tr.GetPort()
	}
	mcfg.Transport = tr
	mcfg.Delegate = d.Delegate
	mcfg.Events = d.Delegate
	mcfg.Ping = d.Delegate
	mcfg.Logger = d.Logger.Named("gossip").ToStdLogger()

	ml, err := memberlist.Create(mcfg)
	if err != nil {
		_ = tr.Shutdown()
		return fmt.Errorf("failed to create memberlist: %w", err)
	}
	d.Memberlist = ml

	// 4. Initialize AI Agent Engine & Background Reactive Watcher
	agentsDir := filepath.Join(d.Config.ConfigDir, "agents")
	if seededFile, created, err := agent.EnsureDefaultAgentsFile(agentsDir); err == nil && created {
		d.Logger.Named("agent").Infof("Seeded default AGENTS.md at %s", seededFile)
	}

	d.AgentEngine = agent.NewEngine(agent.Config{NodeName: d.Config.NodeName}, d, agentsDir)
	actx, acancel := context.WithCancel(ctx)
	d.agentCancel = acancel
	d.agentWg.Add(1)
	go d.runAgentInboxWatcher(actx)

	// 5. Start IPC Server
	d.IPCServer = ipc.NewServer(d.Config.SocketPath, d)
	if err := d.IPCServer.Start(); err != nil {
		_ = ml.Shutdown()
		_ = tr.Shutdown()
		return fmt.Errorf("failed to start IPC server: %w", err)
	}

	d.state = "running"
	d.Logger.Infof("Herd daemon started on %s:%d. IPC socket: %s", d.Config.BindAddr, tr.GetPort(), d.Config.SocketPath)

	// 6. Connect to seed node if configured
	if d.Config.JoinAddr != "" {
		d.Logger.Infof("Joining seed peer %s...", d.Config.JoinAddr)
		go func(addr string) {
			_, joinErr := d.JoinNode(context.Background(), addr)
			if joinErr != nil {
				d.Logger.Warnf("Warning: failed to join seed %s: %v", addr, joinErr)
			} else {
				d.Logger.Infof("Successfully joined cluster via %s", addr)
			}
		}(d.Config.JoinAddr)
	}

	return nil
}

// Stop gracefully stops the daemon, broadcasting a leave to the cluster.
func (d *Daemon) Stop() error {
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.state = "stopping"
	close(d.shutdownCh)

	var errs []error

	// 1. Stop Agent Inbox Watcher
	if d.agentCancel != nil {
		d.agentCancel()
	}
	d.agentWg.Wait()

	// 2. Stop IPC Server
	if d.IPCServer != nil {
		if err := d.IPCServer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop IPC server: %w", err))
		}
	}

	// 3. Gracefully Leave Gossip Cluster
	if d.Memberlist != nil {
		d.Logger.Infof("Shutting down Herd daemon (broadcasting leave)...")
		if err := d.Memberlist.Leave(2 * time.Second); err != nil {
			errs = append(errs, fmt.Errorf("failed to leave memberlist: %w", err))
		}
		if err := d.Memberlist.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown memberlist: %w", err))
		}
	}

	// 4. Stop Port Forwarders
	d.forwardersMu.Lock()
	for _, pf := range d.forwarders {
		_ = pf.Stop()
	}
	d.forwarders = make(map[string]*subsys.PortForwarder)
	d.forwardersMu.Unlock()

	// 5. Shutdown Tailcat Transport
	if d.Transport != nil {
		if err := d.Transport.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown transport: %w", err))
		}
	}

	d.state = "stopped"

	// 5. Clean up IPC socket file
	if d.Config != nil && d.Config.SocketPath != "" {
		_ = os.Remove(d.Config.SocketPath)
	}

	d.Logger.Infof("Herd daemon stopped.")
	return errors.Join(errs...)
}

// ShutdownChan returns a channel that is closed when the daemon shuts down.
func (d *Daemon) ShutdownChan() <-chan struct{} {
	return d.shutdownCh
}

// GetNodeName implements agent.ClusterContext.
func (d *Daemon) GetNodeName() string {
	return d.Config.NodeName
}

// GetRosterStore implements agent.ClusterContext.
func (d *Daemon) GetRosterStore() *roster.Store {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.Store
}

// GetKVStore implements agent.ClusterContext.
func (d *Daemon) GetKVStore() *kv.Store {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.KVStore
}

// GetKVDelegate implements agent.ClusterContext.
func (d *Daemon) GetKVDelegate() *kv.GossipDelegate {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.KVDelegate
}

// GetDataDir implements agent.ClusterContext.
func (d *Daemon) GetDataDir() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.Config != nil && d.Config.DataDir != "" {
		return d.Config.DataDir
	}
	return ""
}
