package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"herd/internal/cli"
	"herd/internal/config"
	"herd/internal/daemon"
	"herd/internal/logger"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	var err error
	switch command {
	case "daemon":
		err = runDaemon(args)
	case "status":
		err = cli.RunStatus(args)
	case "roster":
		err = cli.RunRoster(args)
	case "join":
		err = cli.RunJoin(args)
	case "leave":
		err = cli.RunLeave(args)
	case "exec":
		err = cli.RunExec(args)
	case "forward":
		err = cli.RunForward(args)
	case "cp":
		err = cli.RunCP(args)
	case "kv":
		err = cli.RunKV(args)
	case "agent":
		err = cli.RunAgent(args)
	case "mail":
		err = cli.RunMail(args)
	case "docs":
		err = cli.RunDocs(args)
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		var exitErr cli.ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Herd - Peer-to-Peer Mesh Clustering Daemon & Multi-Tool CLI

Usage:
  herd <command> [arguments]

Commands:
  daemon       Start the Herd background mesh node daemon
  status       Check daemon status and cluster health
  roster       List all known cluster peers and their state
  join         Join a peer node by address
  leave        Gracefully leave the cluster mesh
  exec         Execute a remote command across mesh nodes
  forward      Set up dynamic port forwarding to a mesh peer
  cp           Copy files across cluster nodes
  kv           Distributed key-value store operations (get, set, delete, list)
  agent        AI Agent operations (prompt, chat, tools)
  mail         Actor-style Mailbox operations (send, list, read, watch)
  docs         View embedded documentation guides and specifications

Use "herd <command> -h" for more information about a command.`)
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Cluster node name (default: hostname)")
	bindAddr := fs.String("bind-addr", "0.0.0.0", "Bind IP address for gossip and tunnel transport")
	bindPort := fs.Int("bind-port", -1, "Bind port for gossip and tunnel transport (default 7946, 0 for dynamic)")
	joinAddr := fs.String("join", "", "Seed peer address to join on startup (e.g. 10.0.0.1:7946)")
	configDir := fs.String("config-dir", "", "Custom configuration directory")
	dataDir := fs.String("data-dir", "", "Custom data storage directory")
	stateDir := fs.String("state-dir", "", "Custom state directory")
	socketPath := fs.String("socket", "", "Custom Unix domain socket path")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(config.Options{
		NodeName:   *nodeName,
		BindAddr:   *bindAddr,
		BindPort:   *bindPort,
		JoinAddr:   *joinAddr,
		ConfigDir:  *configDir,
		DataDir:    *dataDir,
		StateDir:   *stateDir,
		SocketPath: *socketPath,
	})
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	unifiedLog, err := logger.NewLogger(os.Getenv("HERD_LOG_LEVEL"), os.Getenv("HERD_LOG_FORMAT"))
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	unifiedLog = unifiedLog.WithNode(cfg.NodeName)
	defer func() { _ = unifiedLog.Sync() }()

	d, err := daemon.New(cfg, unifiedLog)
	if err != nil {
		return fmt.Errorf("failed to initialize daemon: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Handle graceful shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		unifiedLog.Info("Received signal, initiating graceful shutdown...", zap.String("signal", sig.String()))
	case <-d.ShutdownChan():
		unifiedLog.Info("Daemon received shutdown request via IPC...")
	}

	if err := d.Stop(); err != nil {
		unifiedLog.Error("Error during shutdown", zap.Error(err))
	}

	return nil
}
