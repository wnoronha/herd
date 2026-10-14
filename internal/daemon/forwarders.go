package daemon

import (
	"sync"

	"github.com/wnoronha/herd/internal/subsys"
)

// ForwarderRegistry tracks local port-forward tunnels owned by the daemon.
type ForwarderRegistry struct {
	mu         sync.Mutex
	forwarders map[string]*subsys.PortForwarder
}

func newForwarderRegistry() *ForwarderRegistry {
	return &ForwarderRegistry{
		forwarders: make(map[string]*subsys.PortForwarder),
	}
}

// Set replaces any existing forwarder on listenAddr and returns the previous instance if present.
func (r *ForwarderRegistry) Set(listenAddr string, pf *subsys.PortForwarder) *subsys.PortForwarder {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.forwarders[listenAddr]
	r.forwarders[listenAddr] = pf
	return old
}

// StopAll stops every registered forwarder and clears the registry.
func (r *ForwarderRegistry) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, pf := range r.forwarders {
		_ = pf.Stop()
	}
	r.forwarders = make(map[string]*subsys.PortForwarder)
}
