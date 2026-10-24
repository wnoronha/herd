package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/wnoronha/herd/internal/agent"
	"github.com/wnoronha/herd/internal/config"
	"github.com/wnoronha/herd/internal/discover"
	"github.com/wnoronha/herd/internal/identity"
	"github.com/wnoronha/herd/internal/ipc"
	"github.com/wnoronha/herd/internal/kv"
	"github.com/wnoronha/herd/internal/logger"
	"github.com/wnoronha/herd/internal/roster"
	"github.com/wnoronha/herd/internal/transport/tailcat"
)

// Daemon coordinates the node identity, Tailcat transport, Memberlist gossip, and IPC server.
type Daemon struct {
	Config            *config.Config
	Identity          *identity.Identity
	Transport         *tailcat.Transport
	Store             *roster.Store
	Delegate          *roster.Delegate
	KVStore           *kv.Store
	KVDelegate        *kv.GossipDelegate
	Memberlist        *memberlist.Memberlist
	IPCServer         *ipc.Server
	AgentEngine       *agent.Engine
	StartTime         time.Time
	Logger            logger.Logger
	runCtx            context.Context
	runCancel         context.CancelFunc
	processedMsgs     sync.Map
	forwarderRegistry *ForwarderRegistry
	agentRuntime      *AgentRuntime

	// Local discovery (mDNS) state, guarded by mu.
	discoverProvider discover.Provider
	discoverPipeline *discover.Pipeline
	discoverCancel   context.CancelFunc
	warnedClashes    map[string]bool

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
		Config:            cfg,
		Identity:          id,
		StartTime:         time.Now(),
		Logger:            log,
		state:             "initializing",
		shutdownCh:        make(chan struct{}),
		forwarderRegistry: newForwarderRegistry(),
	}
	d.agentRuntime = newAgentRuntime(d)

	return d, nil
}

// getRunCtx returns the daemon run context under a read lock.
// Callers get context.Background() when the daemon was never started,
// making use-before-Start explicit at the call site instead of a silent nil.
func (d *Daemon) getRunCtx() context.Context {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.runCtx == nil {
		return context.Background()
	}
	return d.runCtx
}

// Start launches the Tailcat transport, Memberlist gossip engine, and IPC server.
func (d *Daemon) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.state == "running" || d.state == "starting" {
		d.mu.Unlock()
		return errors.New("daemon already started")
	}
	// Detach lifetime from the caller: a short-lived/canceled caller ctx
	// must not kill the daemon (IPC handlers, streams, watcher, seed join).
	// Values are preserved; only cancellation is detached.
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	d.runCtx, d.runCancel = context.WithCancel(base)
	runCtx := d.runCtx
	d.state = "starting"
	d.mu.Unlock()

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
	newStreamRouter(d).Register(tr)
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
		d.mu.Lock()
		d.state = "stopped"
		d.mu.Unlock()
		return fmt.Errorf("failed to create memberlist: %w", err)
	}
	d.Memberlist = ml

	// 4. Initialize AI Agent Engine & Background Reactive Watcher
	d.agentRuntime.Start(runCtx)

	// 5. Start IPC Server
	d.IPCServer = ipc.NewServer(d.Config.SocketPath, d)
	d.IPCServer.SetRequestContext(runCtx)
	if err := d.IPCServer.Start(); err != nil {
		_ = ml.Shutdown()
		_ = tr.Shutdown()
		d.mu.Lock()
		d.state = "stopped"
		d.mu.Unlock()
		return fmt.Errorf("failed to start IPC server: %w", err)
	}

	d.mu.Lock()
	d.state = "running"
	d.mu.Unlock()
	d.Logger.Infof("Herd daemon started on %s:%d. IPC socket: %s", d.Config.BindAddr, tr.GetPort(), d.Config.SocketPath)

	// 6. Connect to seed node if configured
	if d.Config.JoinAddr != "" {
		d.Logger.Infof("Joining seed peer %s...", d.Config.JoinAddr)
		go func(addr string, jc context.Context) {
			_, joinErr := d.JoinNode(jc, addr)
			if joinErr != nil {
				d.Logger.Warnf("Warning: failed to join seed %s: %v", addr, joinErr)
			} else {
				d.Logger.Infof("Successfully joined cluster via %s", addr)
			}
		}(d.Config.JoinAddr, runCtx)
	}

	// 7. LAN discovery (mDNS): announce and browse unless disabled.
	d.startDiscovery()

	return nil
}

// Stop gracefully stops the daemon, broadcasting a leave to the cluster.
func (d *Daemon) Stop() error {
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Snapshot shared state under lock, then run all blocking shutdown
	// outside the lock so RLock readers (roster/KV/data-dir lookups) and a
	// concurrent Start/Stop never stall or deadlock.
	d.mu.Lock()
	d.state = "stopping"
	close(d.shutdownCh)
	runCancel := d.runCancel
	runtime := d.agentRuntime
	ipcServer := d.IPCServer
	ml := d.Memberlist
	registry := d.forwarderRegistry
	tr := d.Transport
	socketPath := ""
	if d.Config != nil {
		socketPath = d.Config.SocketPath
	}
	d.mu.Unlock()

	var errs []error

	// 0. Stop LAN discovery first: revoke advertisement, end browsing.
	d.stopDiscovery()

	// 1. Stop agent runtime and cancel in-flight IPC/stream work
	if runCancel != nil {
		runCancel()
	}
	if runtime != nil {
		runtime.Stop()
	}

	// 2. Stop IPC Server
	if ipcServer != nil {
		if err := ipcServer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop IPC server: %w", err))
		}
	}

	// 3. Gracefully Leave Gossip Cluster
	if ml != nil {
		d.Logger.Infof("Shutting down Herd daemon (broadcasting leave)...")
		if err := ml.Leave(2 * time.Second); err != nil {
			errs = append(errs, fmt.Errorf("failed to leave memberlist: %w", err))
		}
		if err := ml.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown memberlist: %w", err))
		}
	}

	// 4. Stop Port Forwarders
	if registry != nil {
		registry.StopAll()
	}
	// Agent-owned forward tunnels live in a process-global registry;
	// stop them too so listeners don't leak past daemon shutdown.
	agent.StopAllClusterForwards()

	// 5. Shutdown Tailcat Transport
	if tr != nil {
		if err := tr.Shutdown(); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown transport: %w", err))
		}
	}

	d.mu.Lock()
	d.state = "stopped"
	d.mu.Unlock()

	// 5. Clean up IPC socket file
	if socketPath != "" {
		_ = os.Remove(socketPath)
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

// GetStateDir implements agent.ClusterContext.
func (d *Daemon) GetStateDir() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.Config != nil && d.Config.StateDir != "" {
		return d.Config.StateDir
	}
	return ""
}
