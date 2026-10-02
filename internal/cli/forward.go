package cli

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"herd/internal/config"
	"herd/internal/ipc"
	"herd/internal/subsys"
)

// RunForward executes the 'herd forward' command.
func RunForward(args []string) error {
	fs := flag.NewFlagSet("forward", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Local node daemon to query for peer discovery")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")
	bindHost := fs.String("bind", "127.0.0.1", "Local IP interface to bind listener to")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 2 {
		return fmt.Errorf("usage: herd forward [flags] <local-port> <target-node>:<target-port>")
	}

	localPort, err := strconv.Atoi(remaining[0])
	if err != nil || localPort <= 0 {
		return fmt.Errorf("invalid local port: %s", remaining[0])
	}

	targetExpr := remaining[1]
	parts := strings.Split(targetExpr, ":")
	if len(parts) != 2 {
		return fmt.Errorf("target must be in format <target-node>:<target-port>, got %s", targetExpr)
	}

	targetNode := parts[0]
	targetPort, err := strconv.Atoi(parts[1])
	if err != nil || targetPort <= 0 {
		return fmt.Errorf("invalid target port: %s", parts[1])
	}

	cfg, err := config.Load(config.Options{
		NodeName:   *nodeName,
		SocketPath: *socketPath,
	})
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client := ipc.NewClient(cfg.SocketPath)
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

	localListen := net.JoinHostPort(*bindHost, strconv.Itoa(localPort))
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
