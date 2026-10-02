package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Clear relevant env vars
	envVars := []string{
		"HERD_NODE_NAME", "HERD_CONFIG_DIR", "HERD_DATA_DIR",
		"HERD_STATE_DIR", "HERD_SOCKET", "HERD_SOCKET_PATH",
		"HERD_JOIN", "HERD_BIND_PORT", "HERD_BIND_ADDR",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR",
	}
	for _, k := range envVars {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.NodeName == "" {
		t.Errorf("expected non-empty NodeName")
	}
	if cfg.BindPort != DefaultBindPort {
		t.Errorf("expected BindPort %d, got %d", DefaultBindPort, cfg.BindPort)
	}
	if cfg.BindAddr != DefaultBindAddr {
		t.Errorf("expected BindAddr %s, got %s", DefaultBindAddr, cfg.BindAddr)
	}

	homeDir, _ := os.UserHomeDir()
	expectedConfigDir := filepath.Join(homeDir, ".config", "herd", cfg.NodeName)
	if cfg.ConfigDir != expectedConfigDir {
		t.Errorf("expected ConfigDir %s, got %s", expectedConfigDir, cfg.ConfigDir)
	}

	expectedDataDir := filepath.Join(homeDir, ".local", "share", "herd", cfg.NodeName)
	if cfg.DataDir != expectedDataDir {
		t.Errorf("expected DataDir %s, got %s", expectedDataDir, cfg.DataDir)
	}

	expectedStateDir := filepath.Join(homeDir, ".local", "state", "herd", cfg.NodeName)
	if cfg.StateDir != expectedStateDir {
		t.Errorf("expected StateDir %s, got %s", expectedStateDir, cfg.StateDir)
	}

	expectedSocket := filepath.Join("/tmp", "herd-"+cfg.NodeName+".sock")
	if cfg.SocketPath != expectedSocket {
		t.Errorf("expected SocketPath %s, got %s", expectedSocket, cfg.SocketPath)
	}
}

func TestLoadEnvironmentOverrides(t *testing.T) {
	t.Setenv("HERD_NODE_NAME", "node-alpha")
	t.Setenv("HERD_CONFIG_DIR", "/custom/config")
	t.Setenv("HERD_DATA_DIR", "/custom/data")
	t.Setenv("HERD_STATE_DIR", "/custom/state")
	t.Setenv("HERD_SOCKET", "/custom/sock/herd.sock")
	t.Setenv("HERD_JOIN", "10.0.0.1:7946")
	t.Setenv("HERD_BIND_PORT", "8888")
	t.Setenv("HERD_BIND_ADDR", "127.0.0.1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.NodeName != "node-alpha" {
		t.Errorf("expected node-alpha, got %s", cfg.NodeName)
	}
	if cfg.ConfigDir != "/custom/config" {
		t.Errorf("expected /custom/config, got %s", cfg.ConfigDir)
	}
	if cfg.DataDir != "/custom/data" {
		t.Errorf("expected /custom/data, got %s", cfg.DataDir)
	}
	if cfg.StateDir != "/custom/state" {
		t.Errorf("expected /custom/state, got %s", cfg.StateDir)
	}
	if cfg.SocketPath != "/custom/sock/herd.sock" {
		t.Errorf("expected /custom/sock/herd.sock, got %s", cfg.SocketPath)
	}
	if cfg.JoinAddr != "10.0.0.1:7946" {
		t.Errorf("expected 10.0.0.1:7946, got %s", cfg.JoinAddr)
	}
	if cfg.BindPort != 8888 {
		t.Errorf("expected 8888, got %d", cfg.BindPort)
	}
	if cfg.BindAddr != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1, got %s", cfg.BindAddr)
	}
}

func TestXDGEnvironmentVariables(t *testing.T) {
	t.Setenv("HERD_NODE_NAME", "node-beta")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	t.Setenv("XDG_RUNTIME_DIR", "/xdg/run")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.ConfigDir != "/xdg/config/herd/node-beta" {
		t.Errorf("unexpected ConfigDir: %s", cfg.ConfigDir)
	}
	if cfg.DataDir != "/xdg/data/herd/node-beta" {
		t.Errorf("unexpected DataDir: %s", cfg.DataDir)
	}
	if cfg.StateDir != "/xdg/state/herd/node-beta" {
		t.Errorf("unexpected StateDir: %s", cfg.StateDir)
	}
	if cfg.SocketPath != "/xdg/run/herd-node-beta.sock" {
		t.Errorf("unexpected SocketPath: %s", cfg.SocketPath)
	}
}

func TestMultiNodeIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	cfg1, err := Load(Options{NodeName: "node1"})
	if err != nil {
		t.Fatalf("Load(node1) error: %v", err)
	}
	cfg2, err := Load(Options{NodeName: "node2"})
	if err != nil {
		t.Fatalf("Load(node2) error: %v", err)
	}

	if cfg1.DataDir == cfg2.DataDir {
		t.Errorf("expected isolated DataDirs, got same: %s", cfg1.DataDir)
	}
	if cfg1.SocketPath == cfg2.SocketPath {
		t.Errorf("expected isolated SocketPaths, got same: %s", cfg1.SocketPath)
	}

	if err := cfg1.EnsureDirs(); err != nil {
		t.Fatalf("cfg1.EnsureDirs() failed: %v", err)
	}
	if err := cfg2.EnsureDirs(); err != nil {
		t.Fatalf("cfg2.EnsureDirs() failed: %v", err)
	}

	if _, err := os.Stat(cfg1.DataDir); os.IsNotExist(err) {
		t.Errorf("cfg1 DataDir was not created")
	}
	if _, err := os.Stat(cfg2.DataDir); os.IsNotExist(err) {
		t.Errorf("cfg2 DataDir was not created")
	}
}

func TestConfigFileLoading(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	payload := map[string]any{
		"node_name": "config-file-node",
		"bind_port": 9999,
		"bind_addr": "192.168.1.100",
		"join_addr": "192.168.1.1:7946",
	}
	bytes, _ := json.Marshal(payload)
	if err := os.WriteFile(configFile, bytes, 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(Options{ConfigFile: configFile})
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.NodeName != "config-file-node" {
		t.Errorf("expected config-file-node, got %s", cfg.NodeName)
	}
	if cfg.BindPort != 9999 {
		t.Errorf("expected 9999, got %d", cfg.BindPort)
	}
	if cfg.BindAddr != "192.168.1.100" {
		t.Errorf("expected 192.168.1.100, got %s", cfg.BindAddr)
	}
	if cfg.JoinAddr != "192.168.1.1:7946" {
		t.Errorf("expected 192.168.1.1:7946, got %s", cfg.JoinAddr)
	}
}
