package subsys

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"herd/internal/roster"
)

func TestExecuteLocal(t *testing.T) {
	ctx := context.Background()

	// 1. Successful execution
	res := ExecuteLocal(ctx, "local-node", "echo", []string{"hello", "world"}, 5*time.Second)
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if res.Stdout != "hello world\n" {
		t.Errorf("expected 'hello world\\n', got %q", res.Stdout)
	}

	// 2. Nonexistent command error
	resErr := ExecuteLocal(ctx, "local-node", "nonexistent-cmd-xyz123", nil, 2*time.Second)
	if resErr.ExitCode == 0 || resErr.Error == "" {
		t.Errorf("expected error for nonexistent command, got exitCode=%d, error=%s", resErr.ExitCode, resErr.Error)
	}
}

func TestTargetNodesResolution(t *testing.T) {
	meta1 := &roster.NodeMeta{
		NodeName: "node-1",
		Tags:     map[string]string{"env": "prod", "role": "worker"},
	}
	meta2 := &roster.NodeMeta{
		NodeName: "node-2",
		Tags:     map[string]string{"env": "prod", "role": "database"},
	}
	meta3 := &roster.NodeMeta{
		NodeName: "node-3",
		Tags:     map[string]string{"env": "dev", "role": "worker"},
	}

	store := roster.NewStore(meta1)
	store.UpsertMember("node-2", "127.0.0.1", 7947, "alive", meta2)
	store.UpsertMember("node-3", "127.0.0.1", 7948, "alive", meta3)

	// 1. Direct name
	nodes := TargetNodes("node-2", "node-1", store)
	if len(nodes) != 1 || nodes[0] != "node-2" {
		t.Errorf("expected [node-2], got %v", nodes)
	}

	// 2. All target
	all := TargetNodes("all", "node-1", store)
	if len(all) != 3 {
		t.Errorf("expected 3 nodes for 'all', got %d (%v)", len(all), all)
	}

	// 3. Tag matching: tag:env=prod
	prodNodes := TargetNodes("tag:env=prod", "node-1", store)
	if len(prodNodes) != 2 {
		t.Errorf("expected 2 nodes for tag:env=prod, got %d (%v)", len(prodNodes), prodNodes)
	}

	// 4. Tag matching: tag:role=worker
	workers := TargetNodes("tag:role=worker", "node-1", store)
	if len(workers) != 2 {
		t.Errorf("expected 2 worker nodes, got %d (%v)", len(workers), workers)
	}
}

func TestPortForwarder(t *testing.T) {
	// 1. Start echo server
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start echo listener: %v", err)
	}
	defer func() { _ = echoListener.Close() }()

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	// 2. Start PortForwarder
	targetAddr := echoListener.Addr().String()
	pf := NewPortForwarder("127.0.0.1:0", targetAddr, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pf.Start(ctx); err != nil {
		t.Fatalf("PortForwarder.Start error: %v", err)
	}
	defer func() { _ = pf.Stop() }()

	forwardAddr := pf.LocalAddr().String()

	// 3. Test multiple concurrent clients through the forwarder
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", forwardAddr)
			if err != nil {
				t.Errorf("client %d failed to connect to forwarder: %v", id, err)
				return
			}
			defer func() { _ = conn.Close() }()

			msg := []byte("ping-through-mesh-forwarder")
			if _, err := conn.Write(msg); err != nil {
				t.Errorf("client %d write error: %v", id, err)
				return
			}

			buf := make([]byte, len(msg))
			if _, err := io.ReadFull(conn, buf); err != nil {
				t.Errorf("client %d read error: %v", id, err)
				return
			}

			if !bytes.Equal(buf, msg) {
				t.Errorf("client %d data mismatch: got %s, expected %s", id, buf, msg)
			}
		}(i)
	}

	wg.Wait()
}

func TestCPTargetParsingAndCopy(t *testing.T) {
	// 1. Target Parsing
	t1 := ParseCPTarget("node-1:/var/log/app.log")
	if !t1.IsRemote || t1.Node != "node-1" || t1.Path != "/var/log/app.log" {
		t.Errorf("unexpected parse for remote target: %+v", t1)
	}

	t2 := ParseCPTarget("./local_file.txt")
	if t2.IsRemote || t2.Path != "./local_file.txt" {
		t.Errorf("unexpected parse for local target: %+v", t2)
	}

	// 2. Local File Copy
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "source.txt")
	dstFile := filepath.Join(tmpDir, "destination.txt")

	testData := []byte("herd-cp-distributed-file-data-payload")
	if err := os.WriteFile(srcFile, testData, 0600); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	n, err := CopyLocalFile(srcFile, dstFile)
	if err != nil {
		t.Fatalf("CopyLocalFile error: %v", err)
	}
	if n != int64(len(testData)) {
		t.Errorf("bytes copied %d, expected %d", n, len(testData))
	}

	readBack, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read dst file: %v", err)
	}
	if !bytes.Equal(readBack, testData) {
		t.Errorf("content mismatch in copied file")
	}
}

func TestFileStreamUploadAndDownload(t *testing.T) {
	tmpDir := t.TempDir()
	serverFile := filepath.Join(tmpDir, "server_file.txt")
	clientFile := filepath.Join(tmpDir, "client_file.txt")
	uploadedFile := filepath.Join(tmpDir, "uploaded_file.txt")

	payload := []byte("herd-file-stream-distributed-test-payload-12345")
	if err := os.WriteFile(serverFile, payload, 0644); err != nil {
		t.Fatalf("failed to write server file: %v", err)
	}

	// 1. Test Download: Client downloads serverFile -> clientFile
	c1, c2 := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- HandleFileStream(c2)
	}()

	n, err := DownloadFromStream(c1, serverFile, clientFile)
	_ = c1.Close()
	if err != nil {
		t.Fatalf("DownloadFromStream failed: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("expected %d bytes, got %d", len(payload), n)
	}
	if serverErr := <-errCh; serverErr != nil {
		t.Fatalf("HandleFileStream download error: %v", serverErr)
	}

	readClient, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("failed to read client file: %v", err)
	}
	if !bytes.Equal(readClient, payload) {
		t.Errorf("downloaded content mismatch")
	}

	// 2. Test Upload: Client uploads clientFile -> uploadedFile
	c3, c4 := net.Pipe()
	go func() {
		errCh <- HandleFileStream(c4)
	}()

	n2, err := UploadToStream(c3, clientFile, uploadedFile)
	_ = c3.Close()
	if err != nil {
		t.Fatalf("UploadToStream failed: %v", err)
	}
	if n2 != int64(len(payload)) {
		t.Errorf("expected %d bytes, got %d", len(payload), n2)
	}
	if serverErr := <-errCh; serverErr != nil {
		t.Fatalf("HandleFileStream upload error: %v", serverErr)
	}

	readUploaded, err := os.ReadFile(uploadedFile)
	if err != nil {
		t.Fatalf("failed to read uploaded file: %v", err)
	}
	if !bytes.Equal(readUploaded, payload) {
		t.Errorf("uploaded content mismatch")
	}
}

