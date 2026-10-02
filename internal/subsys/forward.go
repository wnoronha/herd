package subsys

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// DialFunc is a function signature for dialing target endpoints.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// PortForwarder binds a local TCP port and proxies bidirectional traffic to a remote target.
type PortForwarder struct {
	localAddr  string
	targetAddr string
	dial       DialFunc
	listener   net.Listener
	closed     atomic.Bool
	wg         sync.WaitGroup
	mu         sync.Mutex
}

// NewPortForwarder creates a new PortForwarder instance.
func NewPortForwarder(localAddr string, targetAddr string, dial DialFunc) *PortForwarder {
	if dial == nil {
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		}
	}
	return &PortForwarder{
		localAddr:  localAddr,
		targetAddr: targetAddr,
		dial:       dial,
	}
}

// Start listens on the local port and begins proxying connections to the target.
func (pf *PortForwarder) Start(ctx context.Context) error {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	l, err := net.Listen("tcp", pf.localAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", pf.localAddr, err)
	}
	pf.listener = l
	pf.closed.Store(false)

	pf.wg.Add(1)
	go pf.acceptLoop(ctx)

	return nil
}

func (pf *PortForwarder) acceptLoop(ctx context.Context) {
	defer pf.wg.Done()

	for {
		clientConn, err := pf.listener.Accept()
		if err != nil {
			if pf.closed.Load() {
				return
			}
			continue
		}

		pf.wg.Add(1)
		go func(src net.Conn) {
			defer pf.wg.Done()
			defer func() { _ = src.Close() }()

			dst, err := pf.dial(ctx, "tcp", pf.targetAddr)
			if err != nil {
				return
			}
			defer func() { _ = dst.Close() }()

			pf.proxy(src, dst)
		}(clientConn)
	}
}

func (pf *PortForwarder) proxy(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(c1, c2)
		_ = c1.Close()
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(c2, c1)
		_ = c2.Close()
	}()

	wg.Wait()
}

// Stop closes the local listener and terminates the forwarder.
func (pf *PortForwarder) Stop() error {
	if !pf.closed.CompareAndSwap(false, true) {
		return nil
	}

	pf.mu.Lock()
	var err error
	if pf.listener != nil {
		err = pf.listener.Close()
	}
	pf.mu.Unlock()

	pf.wg.Wait()
	return err
}

// LocalAddr returns the active listening address.
func (pf *PortForwarder) LocalAddr() net.Addr {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	if pf.listener != nil {
		return pf.listener.Addr()
	}
	return nil
}
