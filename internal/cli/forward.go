package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/wnoronha/herd/internal/subsys"
)

func forwardAction(localPort int, targetExpr, bindHost string) error {
	// SplitHostPort handles IPv6 literals ([::1]:80) as well as host:port.
	targetNode, portStr, err := net.SplitHostPort(targetExpr)
	if err != nil || targetNode == "" {
		return fmt.Errorf("target must be in format <target-node>:<target-port>, got %s", targetExpr)
	}

	targetPort, err := strconv.Atoi(portStr)
	if err != nil || targetPort < 1 || targetPort > 65535 {
		return fmt.Errorf("invalid target port %q: must be 1-65535", portStr)
	}

	client, _, err := daemonClient()
	if err != nil {
		return err
	}
	roster, err := client.GetRoster(context.Background())
	targetAddr := ""
	if err == nil {
		for _, m := range roster.Members {
			if m.Name == targetNode {
				targetAddr = net.JoinHostPort(m.Addr, strconv.Itoa(targetPort))
				break
			}
		}
	}
	if targetAddr == "" {
		targetAddr = net.JoinHostPort(targetNode, strconv.Itoa(targetPort))
	}

	localListen := net.JoinHostPort(bindHost, strconv.Itoa(localPort))
	pf := subsys.NewPortForwarder(localListen, targetAddr, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pf.Start(ctx); err != nil {
		return fmt.Errorf("failed to start port forwarder: %w", err)
	}
	defer func() { _ = pf.Stop() }()

	fmt.Printf("Forwarding %s -> %s (%s)\n", pf.LocalAddr().String(), targetNode, targetAddr)
	fmt.Println("Press Ctrl+C to stop port forwarding...")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nStopping port forwarder...")
	return nil
}
