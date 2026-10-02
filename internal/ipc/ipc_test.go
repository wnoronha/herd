package ipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"herd/internal/mailbox"
)

type mockDaemonHandler struct {
	status *StatusResponse
	roster *RosterResponse
	join   *JoinResponse
	leave  *LeaveResponse
	stop   *StopResponse
	err    error
}

func (m *mockDaemonHandler) GetStatus(ctx context.Context) (*StatusResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.status, nil
}

func (m *mockDaemonHandler) GetRoster(ctx context.Context) (*RosterResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.roster, nil
}

func (m *mockDaemonHandler) JoinNode(ctx context.Context, addr string) (*JoinResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.join, nil
}

func (m *mockDaemonHandler) LeaveCluster(ctx context.Context) (*LeaveResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.leave, nil
}

func (m *mockDaemonHandler) StopDaemon(ctx context.Context) (*StopResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.stop, nil
}

func (m *mockDaemonHandler) ExecCommand(ctx context.Context, req *ExecRequest) (*ExecResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ExecResponse{
		Results: []NodeExecResult{
			{NodeName: "test-node-1", Stdout: "echo output", ExitCode: 0},
		},
	}, nil
}

func (m *mockDaemonHandler) ForwardPort(ctx context.Context, req *ForwardRequest) (*ForwardResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ForwardResponse{
		Message:    "forwarding active",
		ListenAddr: "127.0.0.1:8080",
	}, nil
}

func (m *mockDaemonHandler) AgentPrompt(ctx context.Context, req *AgentPromptRequest) (*AgentPromptResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &AgentPromptResponse{
		Response:  "mock agent response",
		ModelUsed: "mock-model",
	}, nil
}

func (m *mockDaemonHandler) CopyFile(ctx context.Context, req *CPRequest) (*CPResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &CPResponse{
		BytesTransferred: 1024,
		Message:          "copied successfully",
	}, nil
}

func (m *mockDaemonHandler) KVGet(ctx context.Context, req *KVGetRequest) (*KVGetResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &KVGetResponse{
		Found: true,
		Entry: &KVEntryDTO{Key: req.Key, Value: "ipc-val", Version: 1},
	}, nil
}

func (m *mockDaemonHandler) KVSet(ctx context.Context, req *KVSetRequest) (*KVSetResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &KVSetResponse{Key: req.Key, Version: 1, Message: "ok"}, nil
}

func (m *mockDaemonHandler) KVDelete(ctx context.Context, req *KVDeleteRequest) (*KVDeleteResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &KVDeleteResponse{Key: req.Key, Message: "deleted"}, nil
}

func (m *mockDaemonHandler) KVList(ctx context.Context, req *KVListRequest) (*KVListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &KVListResponse{
		Entries: []KVEntryDTO{
			{Key: "k1", Value: "v1", Version: 1},
		},
	}, nil
}

func (m *mockDaemonHandler) MailSend(ctx context.Context, req *MailSendRequest) (*MailSendResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &MailSendResponse{
		ID:     "msg_test_123",
		Key:    "mailbox:" + req.To + "/msg_test_123",
		Status: "queued",
	}, nil
}

func (m *mockDaemonHandler) MailList(ctx context.Context, req *MailListRequest) (*MailListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &MailListResponse{
		Messages: []mailbox.Message{
			{
				ID:      "msg_test_123",
				From:    "sender-node",
				To:      req.Target,
				Topic:   "test-topic",
				Message: "test message",
			},
		},
	}, nil
}

func (m *mockDaemonHandler) MailRead(ctx context.Context, req *MailReadRequest) (*MailReadResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &MailReadResponse{
		Messages: []mailbox.Message{
			{
				ID:      "msg_test_123",
				From:    "sender-node",
				To:      req.Target,
				Topic:   "test-topic",
				Message: "test message",
			},
		},
		Count: 1,
		Acked: req.Ack,
	}, nil
}

func setupTestIPC(t *testing.T) (*Client, string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	handler := &mockDaemonHandler{
		status: &StatusResponse{
			NodeName:      "test-node-1",
			PublicKey:     "pubkey123",
			TailcatAddr:   "addr123",
			BindPort:      7946,
			State:         "running",
			MemberCount:   2,
			UptimeSeconds: 100,
		},
		roster: &RosterResponse{
			Members: []MemberInfo{
				{Name: "test-node-1", Addr: "127.0.0.1", Port: 7946, Status: "alive"},
				{Name: "test-node-2", Addr: "127.0.0.2", Port: 7946, Status: "alive"},
			},
		},
		join: &JoinResponse{
			JoinedNodes: 1,
			Message:     "successfully joined peer",
		},
		leave: &LeaveResponse{
			Message: "gracefully left cluster",
		},
		stop: &StopResponse{
			Message: "shutting down daemon",
		},
	}

	server := NewServer(socketPath, handler)
	if err := server.Start(); err != nil {
		t.Fatalf("server.Start() error: %v", err)
	}

	client := NewClient(socketPath)
	cleanup := func() {
		_ = server.Stop()
	}

	return client, socketPath, cleanup
}

func TestIPCStatusAndRoster(t *testing.T) {
	client, socketPath, cleanup := setupTestIPC(t)
	defer cleanup()

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("os.Stat(socketPath) error: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected socket permissions 0600, got %o", perm)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := client.GetStatus(ctx)
	if err != nil || status.NodeName != "test-node-1" {
		t.Fatalf("client.GetStatus() failed: %v", err)
	}

	roster, err := client.GetRoster(ctx)
	if err != nil || len(roster.Members) != 2 {
		t.Fatalf("client.GetRoster() failed: %v", err)
	}
}

func TestIPCClusterOps(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	join, err := client.JoinNode(ctx, "10.0.0.2:7946")
	if err != nil || join.JoinedNodes != 1 {
		t.Fatalf("client.JoinNode() failed: %v", err)
	}

	leave, err := client.LeaveCluster(ctx)
	if err != nil || leave.Message != "gracefully left cluster" {
		t.Fatalf("client.LeaveCluster() failed: %v", err)
	}

	stop, err := client.StopDaemon(ctx)
	if err != nil || stop.Message != "shutting down daemon" {
		t.Fatalf("client.StopDaemon() failed: %v", err)
	}
}

func TestIPCSubsystems(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	execResp, err := client.ExecCommand(ctx, &ExecRequest{Target: "test-node-1", Command: "echo"})
	if err != nil || len(execResp.Results) != 1 {
		t.Fatalf("client.ExecCommand() failed: %v", err)
	}

	fwdResp, err := client.ForwardPort(ctx, &ForwardRequest{LocalPort: 8080, TargetNode: "node-2", TargetPort: 80})
	if err != nil || fwdResp.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("client.ForwardPort() failed: %v", err)
	}

	cpResp, err := client.CopyFile(ctx, &CPRequest{Source: "a.txt", Destination: "b.txt"})
	if err != nil || cpResp.BytesTransferred != 1024 {
		t.Fatalf("client.CopyFile() failed: %v", err)
	}
}

func TestIPCKV(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	setResp, err := client.KVSet(ctx, "k1", "v1", "1h")
	if err != nil || setResp.Key != "k1" {
		t.Fatalf("client.KVSet() failed: %v", err)
	}

	getResp, err := client.KVGet(ctx, "k1")
	if err != nil || !getResp.Found || getResp.Entry.Value != "ipc-val" {
		t.Fatalf("client.KVGet() failed: %v", err)
	}

	listResp, err := client.KVList(ctx, "k")
	if err != nil || len(listResp.Entries) != 1 {
		t.Fatalf("client.KVList() failed: %v", err)
	}

	delResp, err := client.KVDelete(ctx, "k1")
	if err != nil || delResp.Key != "k1" {
		t.Fatalf("client.KVDelete() failed: %v", err)
	}
}

func TestIPCErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "error_test.sock")

	handler := &mockDaemonHandler{
		err: errors.New("daemon operation failed"),
	}

	server := NewServer(socketPath, handler)
	if err := server.Start(); err != nil {
		t.Fatalf("server.Start() error: %v", err)
	}
	defer func() { _ = server.Stop() }()

	client := NewClient(socketPath)
	ctx := context.Background()

	_, err := client.GetStatus(ctx)
	if err == nil || err.Error() != "daemon operation failed" {
		t.Errorf("expected daemon operation failed error, got: %v", err)
	}
}

func TestStaleSocketCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "stale.sock")

	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("failed to create stale socket: %v", err)
	}

	handler := &mockDaemonHandler{
		status: &StatusResponse{NodeName: "restarted-node"},
	}

	server := NewServer(socketPath, handler)
	if err := server.Start(); err != nil {
		t.Fatalf("server.Start() failed to recover stale socket: %v", err)
	}
	defer func() { _ = server.Stop() }()

	client := NewClient(socketPath)
	status, err := client.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("client.GetStatus() error: %v", err)
	}
	if status.NodeName != "restarted-node" {
		t.Errorf("expected restarted-node, got %s", status.NodeName)
	}
}

func TestClientServer_Mailbox(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	ctx := context.Background()

	// 1. MailSend
	sendResp, err := client.MailSend(ctx, &MailSendRequest{
		To:      "node-beta",
		Topic:   "greeting",
		Message: "hello world",
	})
	if err != nil {
		t.Fatalf("client.MailSend failed: %v", err)
	}
	if sendResp.Status != "queued" || sendResp.ID != "msg_test_123" {
		t.Errorf("unexpected MailSend response: %+v", sendResp)
	}

	// 2. MailList
	listResp, err := client.MailList(ctx, &MailListRequest{
		Target: "node-beta",
	})
	if err != nil {
		t.Fatalf("client.MailList failed: %v", err)
	}
	if len(listResp.Messages) != 1 || listResp.Messages[0].Topic != "test-topic" {
		t.Errorf("unexpected MailList response: %+v", listResp)
	}

	// 3. MailRead
	readResp, err := client.MailRead(ctx, &MailReadRequest{
		Target: "node-beta",
		Ack:    true,
	})
	if err != nil {
		t.Fatalf("client.MailRead failed: %v", err)
	}
	if readResp.Count != 1 || !readResp.Acked {
		t.Errorf("unexpected MailRead response: %+v", readResp)
	}
}

