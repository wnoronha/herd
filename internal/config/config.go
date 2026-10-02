package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultBindPort = 7946
	DefaultBindAddr = "0.0.0.0"
)

// Config represents the runtime configuration for a Herd node.
type Config struct {
	NodeName   string `json:"node_name"`
	ConfigDir  string `json:"config_dir"`
	DataDir    string `json:"data_dir"`
	StateDir   string `json:"state_dir"`
	SocketPath string `json:"socket_path"`
	ConfigFile string `json:"config_file"`
	JoinAddr   string `json:"join_addr,omitempty"`
	BindAddr   string `json:"bind_addr,omitempty"`
	BindPort   int    `json:"bind_port,omitempty"`
}

// Options allows overriding configuration resolution.
type Options struct {
	NodeName   string
	ConfigDir  string
	DataDir    string
	StateDir   string
	SocketPath string
	ConfigFile string
	JoinAddr   string
	BindAddr   string
	BindPort   int
}

// Load resolves the Herd configuration based on defaults, XDG specifications,
// configuration files, and environment variable overrides.
func Load(opts ...Options) (*Config, error) {
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}

	cfg := &Config{
		BindAddr: DefaultBindAddr,
		BindPort: DefaultBindPort,
	}

	cfg.NodeName = resolveNodeName(opt.NodeName)
	resolvePaths(cfg, opt)

	// If config file exists, load values from it
	if _, err := os.Stat(cfg.ConfigFile); err == nil {
		if err := cfg.loadFromFile(cfg.ConfigFile); err != nil {
			return nil, fmt.Errorf("failed to load config file: %w", err)
		}
	}

	resolveNetwork(cfg, opt)
	return cfg, nil
}

func resolveNodeName(optNodeName string) string {
	if envNode := os.Getenv("HERD_NODE_NAME"); envNode != "" {
		return envNode
	}
	if optNodeName != "" {
		return optNodeName
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "default"
	}
	return sanitizeNodeName(hostname)
}

func resolvePaths(cfg *Config, opt Options) {
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		homeDir = "/tmp"
	}

	// Config Directory
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(homeDir, ".config")
	}
	cfg.ConfigDir = filepath.Join(configHome, "herd", cfg.NodeName)
	if envConfigDir := os.Getenv("HERD_CONFIG_DIR"); envConfigDir != "" {
		cfg.ConfigDir = envConfigDir
	}
	if opt.ConfigDir != "" {
		cfg.ConfigDir = opt.ConfigDir
	}

	// Data Directory
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(homeDir, ".local", "share")
	}
	cfg.DataDir = filepath.Join(dataHome, "herd", cfg.NodeName)
	if envDataDir := os.Getenv("HERD_DATA_DIR"); envDataDir != "" {
		cfg.DataDir = envDataDir
	}
	if opt.DataDir != "" {
		cfg.DataDir = opt.DataDir
	}

	// State Directory
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(homeDir, ".local", "state")
	}
	cfg.StateDir = filepath.Join(stateHome, "herd", cfg.NodeName)
	if envStateDir := os.Getenv("HERD_STATE_DIR"); envStateDir != "" {
		cfg.StateDir = envStateDir
	}
	if opt.StateDir != "" {
		cfg.StateDir = opt.StateDir
	}

	// Socket Path
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir != "" {
		cfg.SocketPath = filepath.Join(runtimeDir, fmt.Sprintf("herd-%s.sock", cfg.NodeName))
	} else {
		cfg.SocketPath = filepath.Join("/tmp", fmt.Sprintf("herd-%s.sock", cfg.NodeName))
	}
	if envSocket := os.Getenv("HERD_SOCKET"); envSocket != "" {
		cfg.SocketPath = envSocket
	} else if envSocketPath := os.Getenv("HERD_SOCKET_PATH"); envSocketPath != "" {
		cfg.SocketPath = envSocketPath
	}
	if opt.SocketPath != "" {
		cfg.SocketPath = opt.SocketPath
	}

	// Config File
	cfg.ConfigFile = filepath.Join(cfg.ConfigDir, "config.json")
	if envConfigFile := os.Getenv("HERD_CONFIG_FILE"); envConfigFile != "" {
		cfg.ConfigFile = envConfigFile
	}
	if opt.ConfigFile != "" {
		cfg.ConfigFile = opt.ConfigFile
	}
}

func resolveNetwork(cfg *Config, opt Options) {
	if envJoin := os.Getenv("HERD_JOIN"); envJoin != "" {
		cfg.JoinAddr = envJoin
	}
	if opt.JoinAddr != "" {
		cfg.JoinAddr = opt.JoinAddr
	}

	if envBindAddr := os.Getenv("HERD_BIND_ADDR"); envBindAddr != "" {
		cfg.BindAddr = envBindAddr
	}
	if opt.BindAddr != "" {
		cfg.BindAddr = opt.BindAddr
	}

	if envBindPort := os.Getenv("HERD_BIND_PORT"); envBindPort != "" {
		if p, err := strconv.Atoi(envBindPort); err == nil {
			cfg.BindPort = p
		}
	}
	if opt.BindPort > 0 {
		cfg.BindPort = opt.BindPort
	} else if opt.BindPort == 0 && opt.NodeName != "" {
		// Explicit dynamic port requested in multi-node tests/options
		cfg.BindPort = 0
	}
}

func (c *Config) loadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	type configAlias Config
	var aux configAlias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.NodeName != "" {
		c.NodeName = aux.NodeName
	}
	if aux.JoinAddr != "" {
		c.JoinAddr = aux.JoinAddr
	}
	if aux.BindAddr != "" {
		c.BindAddr = aux.BindAddr
	}
	if aux.BindPort != 0 {
		c.BindPort = aux.BindPort
	}
	return nil
}

// EnsureDirs ensures that config, data, and state directories exist with secure permissions.
func (c *Config) EnsureDirs() error {
	dirs := []string{c.ConfigDir, c.DataDir, c.StateDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	socketDir := filepath.Dir(c.SocketPath)
	if socketDir != "" && socketDir != "." {
		if err := os.MkdirAll(socketDir, 0700); err != nil {
			return fmt.Errorf("failed to create socket directory %s: %w", socketDir, err)
		}
	}

	return nil
}

func sanitizeNodeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var result []rune
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			result = append(result, r)
		}
	}
	if len(result) == 0 {
		return "default"
	}
	return string(result)
}
