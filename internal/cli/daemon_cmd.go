package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wnoronha/herd/internal/config"
	"github.com/wnoronha/herd/internal/daemon"
	"github.com/wnoronha/herd/internal/logger"
	"go.uber.org/zap"
)

func init() {
	RootCmd.AddCommand(newDaemonCmd())
}

func newDaemonCmd() *cobra.Command {
	var (
		bindAddr      string
		bindPort      int
		joinAddr      string
		advertiseAddr string
		mdns          bool
		configDir     string
		dataDir       string
		stateDir      string
	)

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the Herd background mesh node daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeName := rootNodeName
			socketPath := rootSocket

			opts := config.Options{
				NodeName:      nodeName,
				BindAddr:      bindAddr,
				BindPort:      bindPort,
				JoinAddr:      joinAddr,
				AdvertiseAddr: advertiseAddr,
				ConfigDir:     configDir,
				DataDir:       dataDir,
				StateDir:      stateDir,
				SocketPath:    socketPath,
			}
			if cmd.Flags().Changed("mdns") {
				opts.Mdns = mdns
				opts.MdnsSet = true
			}
			cfg, err := config.Load(opts)
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
		},
	}

	// Node identity and socket come from the root persistent flags so
	// `herd --node-name X daemon` and `herd daemon --node-name X` agree.
	cmd.Flags().StringVar(&bindAddr, "bind-addr", "0.0.0.0", "Bind IP address for gossip and tunnel transport")
	cmd.Flags().IntVar(&bindPort, "bind-port", -1, "Bind port for gossip and tunnel transport (default 7946, 0 for dynamic)")
	cmd.Flags().StringVar(&joinAddr, "join", "", "Seed peer address to join on startup (e.g. 10.0.0.1:7946)")
	cmd.Flags().StringVar(&advertiseAddr, "advertise-addr", "", "LAN address announced for local discovery (default: auto-detected)")
	cmd.Flags().BoolVar(&mdns, "mdns", true, "Enable mDNS LAN discovery (use --mdns=false to disable)")
	cmd.Flags().StringVar(&configDir, "config-dir", "", "Custom configuration directory")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Custom data storage directory")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "Custom state directory")

	return cmd
}
