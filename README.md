# Herd

[![Go Report Card](https://goreportcard.com/badge/github.com/rtk-ai/herd)](https://goreportcard.com/report/github.com/rtk-ai/herd)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Herd is a lightweight, zero-configuration peer-to-peer mesh clustering daemon and multi-tool CLI built in Go. It enables nodes to form decentralized, secure P2P clusters over WireGuard-powered transports with SWIM gossip membership, local Unix IPC control, distributed key-value storage, and remote execution.

---

## Documentation

Complete documentation is organized into modular guides:

- **[System Architecture](docs/architecture.md)** — Daemon controller, Tailcat WireGuard data plane, and SWIM gossip layer.
- **[CLI & Daemon Commands](docs/commands.md)** — Full command reference (`status`, `roster`, `join`, `leave`, `exec`, `forward`, `cp`, `kv`, `mail`, `agent`, `docs`), arguments, and `HERD_*` environment variables.
- **[Actor-Style Mailbox](docs/mailbox.md)** — Asynchronous actor messaging, reactive agent loop, thread tracking, and symmetric reply routing.
- **[Unified Logging Architecture](docs/logging.md)** — Zap-backed abstract logger, subsystem namespacing, third-party adapters, and anti-pattern rules.
- **[Google ADK-Go Integration](docs/adk-integration.md)** — Embedded AI agent runtime, cluster tools (`ClusterRoster`, `ClusterExec`, `SharedKV`, `AgentMail`), and natural language cluster orchestration.
- **[Wire Protocol & Transport](docs/protocol.md)** — Curve25519 ECDH handshake, ChaCha20-Poly1305 framing, stream multiplexing, and Tailcat DERP NAT traversal.
- **[Distributed Key-Value Store](docs/distributed-kv.md)** — Last-Write-Wins (LWW) conflict resolution, naming conventions & schemas, gossip replication, and anti-entropy push/pull sync.
- **[Testing & Verification](docs/testing-process.md)** — Unit test suites, integration tests, and WAN cross-device verification (Raspberry Pi 5 / macOS).
- **[Cross-Platform & Deployment](docs/cross-platform.md)** — Multi-architecture binaries (Linux arm64/amd64, macOS Apple Silicon/Intel, Windows) and shell portability.

---

## Quickstart

### 1. Build & Install
```bash
# Build native binary
make build

# Install to ~/.local/bin/herd
make install
```

### 2. Start Node 1 (Bootstrap Node)
```bash
HERD_NODE_NAME=node-1 herd daemon
```

Check status to get the self-contained Tailcat bootstrap address:
```bash
HERD_NODE_NAME=node-1 herd status
```

### 3. Start Node 2 (Remote Machine / Raspberry Pi / Mac)
```bash
HERD_NODE_NAME=node-2 herd daemon -join <tcpGFw...-tailcat-address-from-node-1>
```

### 4. Cluster Operations
```bash
# View active cluster roster
herd roster

# Write and read distributed KV data
herd kv set cluster/motd "Decentralized mesh active"
herd kv get cluster/motd

# Send an asynchronous actor task to another node's mailbox
herd mail send --wait node-2 "Verify docker service status"

# Execute remote commands across nodes
herd exec node-2 "hostname && uptime"

# View embedded offline documentation guides
herd docs mailbox
```

---

## Pre-Built Binaries

Pre-compiled static binaries are published to the [GitHub Releases page](https://github.com/wnoronha/herd/releases):

| Platform | Binary |
|---|---|
| Linux (AMD64) | `herd-linux-amd64` |
| Linux (ARM64) | `herd-linux-arm64` |
| macOS (Apple Silicon) | `herd-darwin-arm64` |
| macOS (Intel) | `herd-darwin-amd64` |
| Windows (AMD64) | `herd-windows-amd64.exe` |

Download and run directly without unpacking:
```bash
curl -LO https://github.com/wnoronha/herd/releases/latest/download/herd-linux-amd64
chmod +x herd-linux-amd64
./herd-linux-amd64 version
```

---

## Agent Instructions & Persistent Memory

- Work tracking with `bw`: Run `bw prime` before starting work.
- Persistent memory with `icm`: Recall context with `icm recall "query"` and store updates with `icm store`.
