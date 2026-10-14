package daemon

import (
	"context"
	"sync"

	"github.com/wnoronha/herd/internal/subsys"
)

// managedForwarder pairs a forwarder with the cancel func of its owned
// detached context so shutdown can terminate dials as well as listeners.
type managedForwarder struct {
	pf     *subsys.PortForwarder
	cancel context.CancelFunc
}

// ForwarderRegistry tracks local port-forward tunnels owned by the daemon.
type ForwarderRegistry struct {
	mu         sync.Mutex
	forwarders map[string]*managedForwarder
}

func newForwarderRegistry() *ForwarderRegistry {
	return &ForwarderRegistry{
		forwarders: make(map[string]*managedForwarder),
	}
}

// validForwardPort reports whether p is a usable TCP port (1-65535).
func validForwardPort(p int) bool {
	return p >= 1 && p <= 65535
}

// Set replaces any existing forwarder on listenAddr and returns the previous instance if present.
func (r *ForwarderRegistry) Set(listenAddr string, pf *subsys.PortForwarder, cancel context.CancelFunc) *subsys.PortForwarder {
	r.mu.Lock()
	old := r.forwarders[listenAddr]
	r.forwarders[listenAddr] = &managedForwarder{pf: pf, cancel: cancel}
	r.mu.Unlock()
	if old != nil {
		if old.cancel != nil {
			old.cancel()
		}
		return old.pf
	}
	return nil
}

// StopAll stops every registered forwarder and clears the registry.
// Forwarders are snapshot under lock and stopped outside it so a slow/hung
// tunnel never blocks concurrent Set (ForwardPort RPCs).
func (r *ForwarderRegistry) StopAll() {
	r.mu.Lock()
	all := make([]*managedForwarder, 0, len(r.forwarders))
	for _, m := range r.forwarders {
		all = append(all, m)
	}
	r.forwarders = make(map[string]*managedForwarder)
	r.mu.Unlock()
	for _, m := range all {
		if m.cancel != nil {
			m.cancel()
		}
		_ = m.pf.Stop()
	}
}
