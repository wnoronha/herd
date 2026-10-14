package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/wnoronha/herd/internal/subsys"
)

func forwardAction(localPort int, targetExpr, bindHost string) error {
	parts := strings.Split(targetExpr, ":")
	if len(parts) != 2 {
		return fmt.Errorf("target must be in format <target-node>:<target-port>, got %s", targetExpr)
	}

	targetNode := parts[0]
	targetPort, err := strconv.Atoi(parts[1])
	if err != nil || targetPort <= 0 {
		return fmt.Errorf("invalid target port: %s", parts[1])
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
