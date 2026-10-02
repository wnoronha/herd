---
name: herd
description: >-
  Install, run, and orchestrate Herd peer-to-peer mesh clustering daemon nodes over Tailcat
  encrypted WireGuard overlays, execute cluster-wide commands, proxy ports, transfer files,
  interact with distributed KV, and delegate tasks to autonomous Herd AI agents.
---

# Herd Mesh & Agent Orchestration Skill

Herd is a peer-to-peer mesh clustering daemon with zero-config WireGuard/Tailcat WAN networking,
a distributed Last-Write-Wins (LWW) KV store, actor mailboxes, and an embedded autonomous AI Agent Engine.

This skill equips coding agents (Cursor, Claude, Antigravity, etc.) to install Herd, manage cluster
daemons, execute remote tasks over Tailcat, configure distributed storage, and interact with Herd AI agents.

---

## Core Rules for Agents

1. **Flag Ordering**: Herd uses Go standard library `flag.FlagSet`. **All flags must precede positional arguments**.
   - Correct: `herd exec -timeout 10 node-2 "uname -a"`
   - Incorrect: `herd exec node-2 "uname -a" -timeout 10`
2. **Socket Resolution**: When multiple nodes run locally or use custom socket paths, pass `-socket <path>` or `-node-name <name>`.
3. **Tailcat Mesh Overlay**: Nodes automatically receive a deterministic WireGuard overlay address (`fd7a:115c:...`) derived from their cryptographic identity. Cross-host nodes communicate over encrypted Tailcat streams with automatic NAT traversal and DERP relay fallback.

---

## 1. Installation

### Download Pre-Compiled Binary (GitHub Releases)
```bash
# Linux (AMD64)
curl -LO https://github.com/wnoronha/herd/releases/latest/download/herd-linux-amd64
chmod +x herd-linux-amd64
sudo mv herd-linux-amd64 /usr/local/bin/herd

# Linux (ARM64)
curl -LO https://github.com/wnoronha/herd/releases/latest/download/herd-linux-arm64
chmod +x herd-linux-arm64
sudo mv herd-linux-arm64 /usr/local/bin/herd

# macOS (Apple Silicon)
curl -LO https://github.com/wnoronha/herd/releases/latest/download/herd-darwin-arm64
chmod +x herd-darwin-arm64
sudo mv herd-darwin-arm64 /usr/local/bin/herd

# macOS (Intel)
curl -LO https://github.com/wnoronha/herd/releases/latest/download/herd-darwin-amd64
chmod +x herd-darwin-amd64
sudo mv herd-darwin-amd64 /usr/local/bin/herd
```

### Build from Source
```bash
git clone https://github.com/wnoronha/herd.git
cd herd
make build
# Binary is at bin/herd
```

---

## 2. Daemon Management & Tailcat Overlay

Every Herd daemon creates an encrypted Tailcat overlay interface backed by WireGuard and TSnet.
Nodes discover each other and form an encrypted WAN mesh without requiring manual VPN configuration or port forwarding on routers.

### Starting a Seed Node
```bash
# Start primary node daemon
herd daemon -node-name node-1 -bind-port 7946 -socket /tmp/herd-node1.sock

# Start in background with persistent logs
nohup herd daemon -node-name node-1 -bind-port 7946 -socket /tmp/herd-node1.sock > /tmp/herd-node1.log 2>&1 &
```

> **Important for Cross-Host tailnets**: Do not use `-bind-addr 127.0.0.1` when clustering across different machines or VMs. The default bind address (`0.0.0.0`) allows Tailcat to advertise the overlay address and accept incoming peer connections.

### Joining a Cluster Across Machines / WAN via Tailcat
To join a node on another machine to an existing cluster:

```bash
# Option A: Join using the seed node's LAN or Public IP
herd daemon -node-name worker-1 -bind-port 7946 -join 192.168.1.100:7946

# Option B: Join dynamically using Tailcat overlay IP
herd daemon -node-name worker-1 -bind-port 7946 -join [fd7a:115c:a1e0:beef::1]:7946

# Option C: Join dynamically after daemon is running
herd join -socket /tmp/herd-worker1.sock 192.168.1.100:7946
```

Once joined, memberlist gossip and all subsequent stream requests (exec, file transfer, agent dialog)
are routed over the encrypted Tailcat WireGuard overlay.

### Graceful Leave
```bash
herd leave -socket /tmp/herd-node1.sock
```

---

## 3. Cluster & Tailcat Inspection

```bash
# Check local daemon status and view assigned Tailcat IPv6 address
herd status -socket /tmp/herd-node1.sock

# Output example:
# Node Name:    node-1
# Status:       running
# Tailcat Addr: fd7a:115c:a1e0:beef::1:7946
# Members:      2 alive, 0 dead

# List all discovered cluster members and their Tailcat addresses
herd roster -socket /tmp/herd-node1.sock

# Output as structured JSON (useful for scripts and agent parsing)
herd roster -socket /tmp/herd-node1.sock -json
```

---

## 4. Remote Execution over Tailcat Mesh

Commands run over encrypted Tailcat streams directly on target nodes.

```bash
# Run command on a remote Tailcat node
herd exec -socket /tmp/herd-node1.sock worker-1 "hostname && uname -a"

# Run command across all nodes in the mesh simultaneously
herd exec -socket /tmp/herd-node1.sock all "df -h /"

# Run command with a custom timeout (flags must precede positional args)
herd exec -timeout 60 -socket /tmp/herd-node1.sock worker-1 "docker ps --format 'table {{.Names}}\t{{.Status}}'"

# Target nodes by tag
herd exec -socket /tmp/herd-node1.sock "tag:role=gpu" "nvidia-smi"
```

---

## 5. File Transfers over Tailcat (`cp`)

Files stream bidirectionally through end-to-end encrypted Tailcat tunnels.

```bash
# Copy local file to remote Tailcat worker
herd cp -socket /tmp/herd-node1.sock ./deploy.tar.gz worker-1:/tmp/deploy.tar.gz

# Download remote log file to local host
herd cp -socket /tmp/herd-node1.sock worker-1:/var/log/syslog ./syslog-worker1.log

# Node-to-node transfer across the mesh
herd cp -socket /tmp/herd-node1.sock node-1:/data/snapshot.db worker-1:/data/snapshot.db
```

---

## 6. Port Forwarding over Tailcat Mesh (`forward`)

Expose remote node services onto your local machine via encrypted tunnels.

```bash
# Forward remote node port 8080 to localhost:8080
# Syntax: herd forward [flags] <local-port> <target-node>:<target-port>
herd forward -socket /tmp/herd-node1.sock 8080 worker-1:8080

# Forward remote PostgreSQL port to local port 5433
herd forward -socket /tmp/herd-node1.sock 5433 db-node:5432
```

---

## 7. Distributed KV Store (`kv`)

Replicated across all mesh nodes via anti-entropy gossip and Last-Write-Wins (LWW) conflict resolution.

```bash
# Set a key (replicates across all Tailcat peers)
herd kv set -socket /tmp/herd-node1.sock app/version "v1.2.0"

# Set a key with Time-To-Live (TTL)
herd kv set --ttl 30m -socket /tmp/herd-node1.sock locks/migration "node-1"

# Get a key
herd kv get -socket /tmp/herd-node1.sock app/version

# List keys by prefix
herd kv list -socket /tmp/herd-node1.sock app/

# Delete a key (creates a replicated tombstone)
herd kv delete -socket /tmp/herd-node1.sock locks/migration

# Watch key updates in real time
herd kv watch -socket /tmp/herd-node1.sock app/
```

---

## 8. Actor Mailbox (`mail`)

Point-to-point and broadcast messaging between nodes.

```bash
# Send task to remote node's inbox
herd mail send -socket /tmp/herd-node1.sock worker-1 "Verify service health"

# Send with custom topic and TTL
herd mail send --topic sys_alert --ttl 1h -socket /tmp/herd-node1.sock worker-1 "High memory usage detected"

# Send and wait synchronously for a reply
herd mail send --wait --timeout 30s -socket /tmp/herd-node1.sock worker-1 "Run test suite"

# Broadcast to all nodes
herd mail send --topic broadcast -socket /tmp/herd-node1.sock "*" "Rolling restart starting"

# View received messages in inbox
herd mail inbox -socket /tmp/herd-node1.sock
```

---

## 9. Herd AI Agent Orchestration

Herd includes an autonomous Agent Engine that reasons and executes cluster operations
(`cluster_roster`, `cluster_exec`, `cluster_cp`, `cluster_forward`, `shared_kv`, `agent_send_mail`).

### Step 1: Configure LLM Provider (Mesh-Wide via KV)

Writing `agent:config:global` to the KV store automatically replicates the configuration to every node
in the Tailcat mesh so remote nodes do not need local API keys configured in environment variables:

```bash
# Google Gemini (Default)
herd kv set -socket /tmp/herd-node1.sock agent:config:global \
  '{"provider":"gemini","model":"gemini-3.8-flash","api_key":"<GEMINI_API_KEY>"}'

# OpenAI or OpenAI-Compatible (LocalAI, vLLM, Ollama)
herd kv set -socket /tmp/herd-node1.sock agent:config:global \
  '{"provider":"openai","model":"gpt-4o","openai_api_key":"<OPENAI_KEY>","openai_base_url":"https://api.openai.com/v1"}'
```

### Step 2: Initialize Personas

```bash
# Seeds AGENTS.md with coordinator, ops, and researcher roles
herd agent init -socket /tmp/herd-node1.sock
```

### Step 3: Dispatch Tasks to Cluster Agents

```bash
# General prompt (coordinator persona executes cluster tools autonomously)
herd agent prompt -socket /tmp/herd-node1.sock "Show cluster roster and verify disk usage on all nodes"

# Target a specific remote Tailcat node
herd agent prompt --target worker-1 -socket /tmp/herd-node1.sock \
  "Inspect /var/log/syslog on the target node, check for any out-of-memory errors, and report findings."

# Use specialized personas
herd agent prompt --role ops -socket /tmp/herd-node1.sock "Audit active listening ports across the mesh"
herd agent prompt --role researcher -socket /tmp/herd-node1.sock "Compare OS versions and memory limits across nodes"

# Interactive terminal chat session
herd agent chat -socket /tmp/herd-node1.sock
```

---

## 10. Autonomous Agent Recipes for Cursor & Claude

### Recipe 1: Spin Up a Tailcat Cluster
When asked to spin up or expand a cluster:
```bash
# 1. Start local node
nohup herd daemon -node-name node-1 -bind-port 7946 -socket /tmp/herd-node1.sock > /tmp/node1.log 2>&1 &

# 2. Verify status and extract Tailcat address
herd status -socket /tmp/herd-node1.sock

# 3. Connect second node (on same machine with different port/socket or on remote machine)
nohup herd daemon -node-name node-2 -bind-port 7947 -socket /tmp/herd-node2.sock -join 127.0.0.1:7946 > /tmp/node2.log 2>&1 &

# 4. Confirm mesh convergence
herd roster -socket /tmp/herd-node1.sock
```

### Recipe 2: Remote Node Diagnostics over Tailcat
When asked to investigate a remote node:
```bash
# 1. Check node connectivity in roster
herd roster -socket /tmp/herd-node1.sock

# 2. Run diagnostic commands remotely
herd exec -socket /tmp/herd-node1.sock worker-1 "top -b -n 1 | head -n 20"
herd exec -socket /tmp/herd-node1.sock worker-1 "df -h"
```

### Recipe 3: Delegate Work to Remote Herd Agent
When asked to have a remote agent perform a multi-step task autonomously:
```bash
# The agent discovers the roster, routes tools to the target node over Tailcat, and reports results
herd agent prompt --target worker-1 -socket /tmp/herd-node1.sock \
  "Verify that nginx is running, restart it if it is down, and test http://localhost with curl."
```

### Recipe 4: Access Remote Service via Tailcat Tunnel
When asked to test or access a web dashboard on a remote cluster node:
```bash
# Proxy remote port 9090 (Prometheus, app dashboard, etc.) to local port 9090
herd forward -socket /tmp/herd-node1.sock 9090 worker-1:9090
# Access via curl http://localhost:9090 or local browser
```
