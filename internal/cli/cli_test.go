package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wnoronha/herd/internal/config"
	"github.com/wnoronha/herd/internal/daemon"
	"github.com/wnoronha/herd/internal/logger"
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

	sock := []string{"--socket", sockPath}

	if err := ExecuteArgs(append([]string{"status"}, sock...)); err != nil {
		t.Errorf("status error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"status", "--json"}, sock...)); err != nil {
		t.Errorf("status --json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"roster"}, sock...)); err != nil {
		t.Errorf("roster error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"roster", "--json"}, sock...)); err != nil {
		t.Errorf("roster --json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"leave"}, sock...)); err != nil {
		t.Errorf("leave error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"leave", "extra-arg"}, sock...)); err == nil {
		t.Errorf("expected error for 'leave extra-arg' (NoArgs)")
	}

	offlineSock := filepath.Join(tmpDir, "nonexistent.sock")
	if err := ExecuteArgs([]string{"status", "--socket", offlineSock}); err == nil {
		t.Errorf("expected error when daemon is offline")
	}

	if err := ExecuteArgs(append([]string{"join"}, sock...)); err == nil {
		t.Errorf("expected error when join address is missing")
	}

	if err := ExecuteArgs(append([]string{"exec", "cli-test-node", "echo", "test-exec"}, sock...)); err != nil {
		t.Errorf("exec error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"exec", "--json", "cli-test-node", "echo", "test-exec-json"}, sock...)); err != nil {
		t.Errorf("exec --json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"exec"}, sock...)); err == nil {
		t.Errorf("expected error when exec arguments are missing")
	}

	if err := ExecuteArgs(append([]string{"forward"}, sock...)); err == nil {
		t.Errorf("expected error when forward arguments are missing")
	}
	if err := ExecuteArgs(append([]string{"forward", "99999", "node:80"}, sock...)); err == nil {
		t.Errorf("expected error for out-of-range forward port")
	}

	src := filepath.Join(tmpDir, "cli_src.txt")
	dst := filepath.Join(tmpDir, "cli_dst.txt")
	_ = os.WriteFile(src, []byte("cp-cli-test"), 0600)
	if err := ExecuteArgs(append([]string{"cp", src, dst}, sock...)); err != nil {
		t.Errorf("cp error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"cp"}, sock...)); err == nil {
		t.Errorf("expected error when cp arguments are missing")
	}

	testKVCLI(t, sockPath)
}

func testKVCLI(t *testing.T, sockPath string) {
	sock := []string{"--socket", sockPath}

	if err := ExecuteArgs(append([]string{"kv", "set", "my_key", "my_val"}, sock...)); err != nil {
		t.Errorf("kv set error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "set", "--ttl", "10m", "--json", "my_ttl_key", "ttl_val"}, sock...)); err != nil {
		t.Errorf("kv set ttl error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "get", "my_key"}, sock...)); err != nil {
		t.Errorf("kv get error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "get", "--json", "my_key"}, sock...)); err != nil {
		t.Errorf("kv get --json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "list", "--prefix", "my_"}, sock...)); err != nil {
		t.Errorf("kv list error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "list", "--json"}, sock...)); err != nil {
		t.Errorf("kv list --json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "delete", "my_key"}, sock...)); err != nil {
		t.Errorf("kv delete error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "delete", "--json", "my_ttl_key"}, sock...)); err != nil {
		t.Errorf("kv delete --json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"kv", "get", "my_key"}, sock...)); err == nil {
		t.Errorf("expected error when getting deleted key")
	}
	if err := ExecuteArgs([]string{"kv", "unknown_subcmd"}); err == nil {
		t.Errorf("expected error for unknown subcommand")
	}
}

func TestDocsCLI(t *testing.T) {
	if err := ExecuteArgs([]string{"docs"}); err != nil {
		t.Errorf("docs error: %v", err)
	}
	if err := ExecuteArgs([]string{"docs", "architecture"}); err != nil {
		t.Errorf("docs architecture error: %v", err)
	}
	if err := ExecuteArgs([]string{"docs", "--all"}); err != nil {
		t.Errorf("docs --all error: %v", err)
	}

	tmpDir := t.TempDir()
	exportPath := filepath.Join(tmpDir, "export-flag")
	if err := ExecuteArgs([]string{"docs", "--export", exportPath}); err != nil {
		t.Errorf("docs --export error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(exportPath, "architecture.md")); err != nil {
		t.Errorf("exported file architecture.md missing: %v", err)
	}

	if err := ExecuteArgs([]string{"docs", "zzzz-not-a-real-topic-999"}); err == nil {
		t.Errorf("expected error for non-existent topic")
	}
	if err := ExecuteArgs([]string{"docs", "--export", tmpDir, "--all"}); err == nil {
		t.Errorf("expected error for mutually exclusive --export/--all")
	}
	if err := ExecuteArgs([]string{"docs", "architecture", "extra"}); err == nil {
		t.Errorf("expected error for extra docs topic arg")
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

	sock := []string{"--socket", sockPath}

	if err := ExecuteArgs(append([]string{"mail", "send", "node-dest", "hello actor mesh"}, sock...)); err != nil {
		t.Errorf("mail send error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"mail", "send", "--json", "--topic", "status", "node-dest", "query payload"}, sock...)); err != nil {
		t.Errorf("mail send json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"mail", "list", "--target", "node-dest"}, sock...)); err != nil {
		t.Errorf("mail list error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"mail", "list", "--target", "node-dest", "--json"}, sock...)); err != nil {
		t.Errorf("mail list json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"mail", "read", "--target", "node-dest"}, sock...)); err != nil {
		t.Errorf("mail read error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"mail", "read", "--target", "node-dest", "--ack"}, sock...)); err != nil {
		t.Errorf("mail read ack error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"mail", "list", "--target", "node-dest"}, sock...)); err != nil {
		t.Errorf("mail list after ack error: %v", err)
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

	sock := []string{"--socket", sockPath}

	if err := ExecuteArgs([]string{"agent", "tools"}); err != nil {
		t.Errorf("agent tools error: %v", err)
	}

	if err := ExecuteArgs(append([]string{"agent", "status"}, sock...)); err != nil {
		t.Errorf("agent status error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"agent", "status", "--json", "--role", "ops"}, sock...)); err != nil {
		t.Errorf("agent status role json error: %v", err)
	}
	if err := ExecuteArgs(append([]string{"agent", "prompt", "show cluster roster"}, sock...)); err != nil {
		t.Errorf("agent prompt error: %v", err)
	}
	err = ExecuteArgs(append([]string{"agent", "prompt", "--role", "ghost-role", "test task"}, sock...))
	if err == nil {
		t.Errorf("expected error for nonexistent role, got nil")
	}
}

func TestAgentInitCLI(t *testing.T) {
	tmpDir := t.TempDir()

	if err := ExecuteArgs([]string{"agent", "init", "--dir", tmpDir}); err != nil {
		t.Fatalf("agent init failed: %v", err)
	}

	targetFile := filepath.Join(tmpDir, "AGENTS.md")
	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		t.Fatalf("expected AGENTS.md at %s, but file was not created", targetFile)
	}

	if err := ExecuteArgs([]string{"agent", "init", "--dir", tmpDir}); err != nil {
		t.Fatalf("agent init second run failed: %v", err)
	}
	if err := ExecuteArgs([]string{"agent", "init", "--dir", tmpDir, "--force"}); err != nil {
		t.Fatalf("agent init with force failed: %v", err)
	}
}
