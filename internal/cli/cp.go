package cli

import (
	"context"
	"flag"
	"fmt"
	"time"

	"herd/internal/config"
	"herd/internal/ipc"
	"herd/internal/subsys"
)

// RunCP executes the 'herd cp' command.
func RunCP(args []string) error {
	fs := flag.NewFlagSet("cp", flag.ExitOnError)
	nodeName := fs.String("node-name", "", "Local node daemon to query")
	socketPath := fs.String("socket", "", "Daemon Unix domain socket path")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 2 {
		return fmt.Errorf("usage: herd cp [flags] <source> <destination>\nexamples:\n  herd cp ./local.txt node-1:/tmp/remote.txt\n  herd cp node-1:/tmp/remote.txt ./local.txt")
	}

	srcTarget := subsys.ParseCPTarget(remaining[0])
	dstTarget := subsys.ParseCPTarget(remaining[1])

	cfg, err := config.Load(config.Options{
		NodeName:   *nodeName,
		SocketPath: *socketPath,
	})
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client := ipc.NewClient(cfg.SocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var req ipc.CPRequest
	if srcTarget.IsRemote {
		// Download from remote node
		req = ipc.CPRequest{
			Source:      srcTarget.Path,
			Destination: dstTarget.Path,
			TargetNode:  srcTarget.Node,
			IsDownload:  true,
		}
	} else if dstTarget.IsRemote {
		// Upload to remote node
		req = ipc.CPRequest{
			Source:      srcTarget.Path,
			Destination: dstTarget.Path,
			TargetNode:  dstTarget.Node,
			IsDownload:  false,
		}
	} else {
		// Local-to-local copy
		req = ipc.CPRequest{
			Source:      srcTarget.Path,
			Destination: dstTarget.Path,
		}
	}

	resp, err := client.CopyFile(ctx, &req)
	if err != nil {
		return fmt.Errorf("herd cp failed: %w", err)
	}

	fmt.Printf("%s (100%% complete)\n", resp.Message)
	return nil
}
