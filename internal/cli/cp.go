package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/wnoronha/herd/internal/ipc"
	"github.com/wnoronha/herd/internal/subsys"
)

func cpAction(source, destination string) error {
	srcTarget := subsys.ParseCPTarget(source)
	dstTarget := subsys.ParseCPTarget(destination)

	client, _, err := daemonClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var req ipc.CPRequest
	if srcTarget.IsRemote {
		req = ipc.CPRequest{
			Source:      srcTarget.Path,
			Destination: dstTarget.Path,
			TargetNode:  srcTarget.Node,
			IsDownload:  true,
		}
	} else if dstTarget.IsRemote {
		req = ipc.CPRequest{
			Source:      srcTarget.Path,
			Destination: dstTarget.Path,
			TargetNode:  dstTarget.Node,
			IsDownload:  false,
		}
	} else {
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
