package daemon

import (
	"github.com/wnoronha/herd/internal/transport/tailcat"
)

// StreamRouter registers daemon stream handlers on the mesh transport.
type StreamRouter struct {
	d *Daemon
}

func newStreamRouter(d *Daemon) *StreamRouter {
	return &StreamRouter{d: d}
}

// Register wires exec, agent, and file stream handlers onto the transport.
func (r *StreamRouter) Register(tr *tailcat.Transport) {
	tr.RegisterStreamHandler(tailcat.StreamTypeExec, r.d.handleExecStream)
	tr.RegisterStreamHandler(tailcat.StreamTypeAgent, r.d.handleAgentStream)
	tr.RegisterStreamHandler(tailcat.StreamTypeFile, r.d.handleFileStream)
}
