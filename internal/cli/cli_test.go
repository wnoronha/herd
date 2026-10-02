package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"herd/internal/config"
	"herd/internal/daemon"
	"herd/internal/logger"
)

func TestStatusAndRosterCLI(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "herd.sock")

	cfg, err := config.Load(config.Options{
		NodeName:   "cli-test-node",
		DataDir:    tmpDir,
		SocketPath: sockPath,
		BindAddr:   "127.0.0.1",
		BindPort:   -1,
	})
	if err != nil {
		t.Fatalf("config.Load error: %v", err)
	}

	d, err := daemon.New(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("daemon.New error: %v", err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("daemon.Start error: %v", err)
	}
	defer func() { _ = d.Stop() }()

	// 1. RunStatus
	if err := RunStatus([]string{"--socket", sockPath}); err != nil {
		t.Errorf("RunStatus error: %v", err)
	}

	// 2. RunStatus with --json
	if err := RunStatus([]string{"--socket", sockPath, "--json"}); err != nil {
		t.Errorf("RunStatus --json error: %v", err)
	}

	// 3. RunRoster
	if err := RunRoster([]string{"--socket", sockPath}); err != nil {
		t.Errorf("RunRoster error: %v", err)
	}

	// 4. RunRoster with --json
	if err := RunRoster([]string{"--socket", sockPath, "--json"}); err != nil {
		t.Errorf("RunRoster --json error: %v", err)
	}

	// 5. RunLeave
	if err := RunLeave([]string{"--socket", sockPath}); err != nil {
		t.Errorf("RunLeave error: %v", err)
	}

	// 6. Test offline error
	offlineSock := filepath.Join(tmpDir, "nonexistent.sock")
	if err := RunStatus([]string{"--socket", offlineSock}); err == nil {
		t.Errorf("expected error when daemon is offline")
	}

	// 7. RunJoin missing argument error
	if err := RunJoin([]string{"--socket", sockPath}); err == nil {
		t.Errorf("expected error when join address is missing")
	}

	// 8. RunExec
	if err := RunExec([]string{"--socket", sockPath, "cli-test-node", "echo", "test-exec"}); err != nil {
		t.Errorf("RunExec error: %v", err)
	}

	// 9. RunExec with --json
	if err := RunExec([]string{"--socket", sockPath, "--json", "cli-test-node", "echo", "test-exec-json"}); err != nil {
		t.Errorf("RunExec --json error: %v", err)
	}

	// 10. RunExec missing arguments
	if err := RunExec([]string{"--socket", sockPath}); err == nil {
		t.Errorf("expected error when exec arguments are missing")
	}

	// 11. RunForward missing arguments
	if err := RunForward([]string{"--socket", sockPath}); err == nil {
		t.Errorf("expected error when forward arguments are missing")
	}

	// 12. RunCP
	src := filepath.Join(tmpDir, "cli_src.txt")
	dst := filepath.Join(tmpDir, "cli_dst.txt")
	_ = os.WriteFile(src, []byte("cp-cli-test"), 0600)
	if err := RunCP([]string{"--socket", sockPath, src, dst}); err != nil {
		t.Errorf("RunCP error: %v", err)
	}

	// 13. RunCP missing arguments
	if err := RunCP([]string{"--socket", sockPath}); err == nil {
		t.Errorf("expected error when cp arguments are missing")
	}

	// 14. RunKV tests
	testKVCLI(t, sockPath)
}

func testKVCLI(t *testing.T, sockPath string) {
	// Set
	if err := RunKV([]string{"set", "--socket", sockPath, "my_key", "my_val"}); err != nil {
		t.Errorf("RunKV set error: %v", err)
	}

	// Set with TTL and JSON
	if err := RunKV([]string{"set", "--socket", sockPath, "--ttl", "10m", "--json", "my_ttl_key", "ttl_val"}); err != nil {
		t.Errorf("RunKV set ttl error: %v", err)
	}

	// Get
	if err := RunKV([]string{"get", "--socket", sockPath, "my_key"}); err != nil {
		t.Errorf("RunKV get error: %v", err)
	}

	// Get with JSON
	if err := RunKV([]string{"get", "--socket", sockPath, "--json", "my_key"}); err != nil {
		t.Errorf("RunKV get --json error: %v", err)
	}

	// List
	if err := RunKV([]string{"list", "--socket", sockPath, "--prefix", "my_"}); err != nil {
		t.Errorf("RunKV list error: %v", err)
	}

	// List with JSON
	if err := RunKV([]string{"list", "--socket", sockPath, "--json"}); err != nil {
		t.Errorf("RunKV list --json error: %v", err)
	}

	// Delete
	if err := RunKV([]string{"delete", "--socket", sockPath, "my_key"}); err != nil {
		t.Errorf("RunKV delete error: %v", err)
	}

	// Delete with JSON
	if err := RunKV([]string{"delete", "--socket", sockPath, "--json", "my_ttl_key"}); err != nil {
		t.Errorf("RunKV delete --json error: %v", err)
	}

	// Get deleted key should fail
	if err := RunKV([]string{"get", "--socket", sockPath, "my_key"}); err == nil {
		t.Errorf("expected error when getting deleted key")
	}

	// Usage / help / missing args
	if err := RunKV([]string{}); err == nil {
		t.Errorf("expected error for empty args")
	}
	if err := RunKV([]string{"help"}); err != nil {
		t.Errorf("unexpected error for help: %v", err)
	}
	if err := RunKV([]string{"unknown_subcmd"}); err == nil {
		t.Errorf("expected error for unknown subcommand")
	}
}

func TestDocsCLI(t *testing.T) {
	// 1. List summary
	if err := RunDocs([]string{}); err != nil {
		t.Errorf("RunDocs() error: %v", err)
	}

	// 2. View specific topic
	if err := RunDocs([]string{"architecture"}); err != nil {
		t.Errorf("RunDocs('architecture') error: %v", err)
	}

	// 3. View all docs
	if err := RunDocs([]string{"--all"}); err != nil {
		t.Errorf("RunDocs('--all') error: %v", err)
	}

	// 4. Export flag
	tmpDir := t.TempDir()
	exportPath1 := filepath.Join(tmpDir, "export-flag")
	if err := RunDocs([]string{"--export", exportPath1}); err != nil {
		t.Errorf("RunDocs('--export') error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(exportPath1, "architecture.md")); err != nil {
		t.Errorf("exported file architecture.md missing: %v", err)
	}

	// 5. Export positional argument
	exportPath2 := filepath.Join(tmpDir, "export-arg")
	if err := RunDocs([]string{"export", exportPath2}); err != nil {
		t.Errorf("RunDocs('export') error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(exportPath2, "protocol.md")); err != nil {
		t.Errorf("exported file protocol.md missing: %v", err)
	}

	// 6. Unknown topic should return error
	if err := RunDocs([]string{"nonexistent-guide"}); err == nil {
		t.Errorf("expected error for non-existent topic")
	}
}

func TestMailCLI(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "herd.sock")

	cfg, err := config.Load(config.Options{
		NodeName:   "cli-mail-node",
		DataDir:    tmpDir,
		SocketPath: sockPath,
		BindAddr:   "127.0.0.1",
		BindPort:   -1,
	})
	if err != nil {
		t.Fatalf("config.Load error: %v", err)
	}

	d, err := daemon.New(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("daemon.New error: %v", err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("daemon.Start error: %v", err)
	}
	defer func() { _ = d.Stop() }()

	// 1. Send mail
	if err := RunMail([]string{"send", "--socket", sockPath, "node-dest", "hello actor mesh"}); err != nil {
		t.Errorf("RunMail send error: %v", err)
	}

	// 2. Send mail with json
	if err := RunMail([]string{"send", "--socket", sockPath, "--json", "--topic", "status", "node-dest", "query payload"}); err != nil {
		t.Errorf("RunMail send json error: %v", err)
	}

	// 3. List mail
	if err := RunMail([]string{"list", "--socket", sockPath, "--target", "node-dest"}); err != nil {
		t.Errorf("RunMail list error: %v", err)
	}

	// 4. List mail with json
	if err := RunMail([]string{"list", "--socket", sockPath, "--target", "node-dest", "--json"}); err != nil {
		t.Errorf("RunMail list json error: %v", err)
	}

	// 5. Read mail without ack
	if err := RunMail([]string{"read", "--socket", sockPath, "--target", "node-dest"}); err != nil {
		t.Errorf("RunMail read error: %v", err)
	}

	// 6. Read mail with ack
	if err := RunMail([]string{"read", "--socket", sockPath, "--target", "node-dest", "--ack"}); err != nil {
		t.Errorf("RunMail read ack error: %v", err)
	}

	// 7. List again to ensure empty
	if err := RunMail([]string{"list", "--socket", sockPath, "--target", "node-dest"}); err != nil {
		t.Errorf("RunMail list after ack error: %v", err)
	}

	// 8. Help output
	if err := RunMail([]string{"help"}); err != nil {
		t.Errorf("RunMail help error: %v", err)
	}
}

func TestAgentCLI(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "herd.sock")

	cfg, err := config.Load(config.Options{
		NodeName:   "cli-agent-node",
		DataDir:    tmpDir,
		SocketPath: sockPath,
		BindAddr:   "127.0.0.1",
		BindPort:   -1,
	})
	if err != nil {
		t.Fatalf("config.Load error: %v", err)
	}

	d, err := daemon.New(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("daemon.New error: %v", err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("daemon.Start error: %v", err)
	}
	defer func() { _ = d.Stop() }()

	// 1. Run tools
	if err := RunAgent([]string{"tools"}); err != nil {
		t.Errorf("RunAgent tools error: %v", err)
	}

	// 2. Default prompt
	if err := RunAgent([]string{"prompt", "--socket", sockPath, "show cluster roster"}); err != nil {
		t.Errorf("RunAgent prompt error: %v", err)
	}

	// 3. Prompt with non-existent role
	err = RunAgent([]string{"prompt", "--socket", sockPath, "--role", "ghost-role", "test task"})
	if err == nil {
		t.Errorf("expected error for nonexistent role, got nil")
	}
}

func TestAgentInitCLI(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Initial run scaffolds AGENTS.md
	if err := RunAgent([]string{"init", "--dir", tmpDir}); err != nil {
		t.Fatalf("RunAgent init failed: %v", err)
	}

	targetFile := filepath.Join(tmpDir, "AGENTS.md")
	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		t.Fatalf("expected AGENTS.md at %s, but file was not created", targetFile)
	}

	// 2. Second run without force should succeed without overwrite
	if err := RunAgent([]string{"init", "--dir", tmpDir}); err != nil {
		t.Fatalf("RunAgent init second run failed: %v", err)
	}

	// 3. Run with force
	if err := RunAgent([]string{"init", "--dir", tmpDir, "--force"}); err != nil {
		t.Fatalf("RunAgent init with force failed: %v", err)
	}
}

