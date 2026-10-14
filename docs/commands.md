# Herd CLI & Daemon Commands Reference

Complete reference for Herd commands, options, arguments, and environment variables.

See also:
- [Agent Quickstart](agent-quickstart.md) — local Ollama, status/probe, troubleshooting
- [Architecture Overview](architecture.md)
- [Security Deep Dive](security-deep-dive.md)
- [Actor-Style Mailbox](mailbox.md)
- [Unified Logging Architecture](logging.md)
- [Protocol Specification](protocol.md)
- [Distributed KV Guide](distributed-kv.md)
- [Cross-Platform Guide](cross-platform.md)

---

## 1. Global Flags & Environment Overrides

The CLI is built with [Cobra](https://github.com/spf13/cobra). Run `herd --help` or `herd <command> --help` for usage. Shell completion:

```bash
herd completion bash > /etc/bash_completion.d/herd   # or ~/.bash_completion
herd completion zsh  >> ~/.zshrc                    # then enable compinit
```

Most commands share daemon connection flags (`--node-name`, `--socket`) as **persistent** root flags on `herd`. Each command also respects the environment variables below. Put global flags before the subcommand or immediately after it (standard Cobra parsing).

| Setting | Flag | Environment Variable | Default |
|---|---|---|---|
| Node Name | `--node-name <string>` | `HERD_NODE_NAME` | System hostname |
| Socket Path | `--socket <path>` | `HERD_SOCKET_PATH` | `$XDG_RUNTIME_DIR/herd-<node>.sock` |
| Log Level | *(daemon only)* | `HERD_LOG_LEVEL` | `info` (`debug`, `warn`, `error`) |
| Log Format | *(daemon only)* | `HERD_LOG_FORMAT` | `console` (`json`) |

The flags --bind-addr, --bind-port, --join, --advertise-addr, --mdns, --data-dir, --config-dir, and --state-dir are specific to `herd daemon` and are documented in the Daemon section.

---

## 2. Daemon Management

### `herd daemon`
Starts the background mesh node, handling gossip, peer discovery, and IPC.

```bash
# Start with default settings (port 7946, hostname as node name)
herd daemon

# Start with custom name and join an existing Tailcat address
HERD_NODE_NAME=node-2 herd daemon --join tcpGFw...<node-1-tailcat-addr>...

# Start with auto-selected port and custom data directory
herd daemon --node-name worker-1 --bind-port 0 --data-dir /var/lib/herd
```

Local discovery is on by default: nodes on the same LAN find each other
with no flags (see `home-lan`). Disable with `--mdns=false`
(`HERD_MDNS=0`); pin the announced LAN address with
`--advertise-addr 192.168.1.50` (`HERD_ADVERTISE_ADDR`) on multihomed hosts.

---

## 3. Cluster Inspection & Operations

### `herd status`
Displays node health, active Tailcat overlay address, bind port, cluster size, and uptime.

```bash
herd status
```
**Example Output:**
```text
=== Herd Node Status ===
Node Name:     node-1
State:         running
Tailcat Addr:  tcpGFw...<node-1-tailcat-addr>...
Bind Port:     7946
Cluster Size:  2 member(s)
Uptime:        3m20s
```

### `herd roster`
Lists all known cluster nodes, statuses (`alive`, `suspect`, `left`), overlay endpoints, and round-trip ping latencies.

```bash
herd roster
```
**Example Output:**
```text
NODE NAME   STATUS   ENDPOINT                                       METADATA
node-1      alive    fd7a:115c:a1e0:1::1:7946                       -
node-2      alive    fd7a:115c:a1e0:2::1:7946                       -
```

### `herd join <address>`
Connects a running daemon to an existing peer node via IP:port or Tailcat address.

```bash
herd join tcpGFw...<node-1-tailcat-addr>...
herd join 10.0.0.2:7946
```

### `herd leave`
Gracefully broadcasts a departure event to the cluster mesh and closes open streams.

```bash
herd leave
```

---

## 4. Remote Execution & Subsystems

### `herd exec <target> <command> [args...]`
Executes a shell command on a specific cluster node or all nodes (`all`).

```bash
# Run command on a single remote node
herd exec node-2 "uname -a && uptime"

# Run command across all cluster nodes
herd exec all "hostname"

# Custom execution timeout (default: 30s)
herd exec -timeout 10 node-2 "sleep 5"
```

### `herd forward [flags] <local-port> <target-node>:<target-port>`
Sets up a local TCP proxy tunnel forwarded securely to a target node's port.

```bash
# Forward local port 8080 to port 80 on node-2
herd forward 8080 node-2:80
```

Target uses `<node>:<port>`; bracket IPv6 literals (`herd forward 8080 '[::1]:80'`). Ports must be 1-65535.

### `herd cp <source> <destination>`
Copies files directly across nodes over encrypted multiplexed streams.

```bash
# Copy local file to remote node
herd cp ./app.tar.gz node-2:/tmp/app.tar.gz

# Copy remote file to local
herd cp node-2:/var/log/syslog ./syslog.log
```

---

## 5. Distributed Key-Value Store

### `herd kv set <key> <value> [--ttl <duration>]`
Writes a key-value pair and replicates it across the cluster.

```bash
herd kv set config/environment "production"
herd kv set --ttl 1h sessions/user-123 "active"
```

### `herd kv get <key>`
Retrieves a key value and metadata.

```bash
herd kv get config/environment
```

### `herd kv delete <key>`
Deletes a key by broadcasting a tombstone record.

```bash
herd kv delete sessions/user-123
```

### `herd kv list [--prefix <string>]`
Lists all active keys in the distributed store.

```bash
herd kv list
herd kv list --prefix "config/"
```

---

## 6. Actor-Style Mailbox Operations

### `herd mail send <to> <message> [flags]`
Sends an asynchronous message or task envelope to a node, group, or broadcast mailbox.

```bash
# Send a point-to-point task
herd mail send node-2 "Run health inspection"

# Send with custom topic and TTL
herd mail send --topic sys_check --ttl 2h node-2 "Check disk space"

# Send and wait synchronously for recipient agent reply
herd mail send --wait --timeout 45s node-2 "Compute SHA256 of /var/log/syslog"

# Broadcast an announcement to all mesh nodes
herd mail send --topic alert "*" "Scheduled maintenance in 10 minutes"
```

### `herd mail list [flags]`
Lists pending messages waiting in the target mailbox (defaults to local node).

```bash
# List local node mailbox
herd mail list

# List specific node or group mailbox
herd mail list --target node-2
herd mail list --target "*" --json
```

### `herd mail read [flags]`
Reads and optionally acknowledges (removes) messages in the mailbox.

```bash
# Read messages without acknowledging
herd mail read

# Read and acknowledge (consume) messages
herd mail read --ack

# Filter by topic
herd mail read --topic sys_check --ack
```

### `herd mail watch [flags]`
Continuously streams incoming mailbox messages in real-time.

```bash
# Stream messages arriving for local node
herd mail watch

# Stream as JSON objects
herd mail watch --json --ack
```

---

## 7. AI Agent Operations

For a short **local Ollama** walkthrough (pull model → restart daemon → status → prompt), see **[Agent Quickstart](agent-quickstart.md)**.

### `herd agent init [flags]`
Scaffolds a starter `AGENTS.md` file containing standard declarative personas (`coordinator`, `ops`, and `researcher`). Also runs automatically when `herd daemon` starts if no agent definitions exist in the node's configuration directory.

```bash
# Scaffold AGENTS.md into the current directory
herd agent init

# Scaffold into a specific path
herd agent init --dir ./agents

# Scaffold into global user configuration (~/.config/herd/agents)
herd agent init --global

# Force overwrite existing AGENTS.md
herd agent init --force

# Refresh personas on a running node (e.g. after upgrade adds dns_lookup to defaults)
herd agent init --force --for-node my-node
```

Flags:
- `--dir <path>`: Directory to scaffold `AGENTS.md` into (default: current directory)
- `--force`: Overwrite existing `AGENTS.md` file
- `--global`: Scaffold into global user configuration (`~/.config/herd/agents`)
- `--for-node <name>`: Scaffold into that node's config directory (`~/.config/herd/<node>/agents`)

### `herd agent status [flags]`
Shows whether this node's agent will use an **LLM** (Gemini / OpenAI-compatible / explicit Ollama), **auto-probe local Ollama** when no cloud keys are set, or the **deterministic fallback** (`herd-builtin-coordinator`), where config came from (env vs KV), loaded personas, and optional role tool gaps. Use `--probe` to hit the configured endpoint at status time.

```bash
herd agent status
herd agent status --json
herd agent status --role researcher
herd agent status --probe
herd agent status --cluster
```

| Flag | Description |
|------|-------------|
| `--role` | Tool allowlist and missing common tools (e.g. `dns_lookup`) for one persona |
| `--probe` | Live API check against the configured provider (uses quota/network) |
| `--cluster` | Query all **alive** roster nodes via `herd exec <node> herd agent status --json` |
| `--json` | Machine-readable output (cluster mode returns `{local, nodes, errors}`) |

**`backend_class` values** (field `backend_class` in `--json`):

| Value | Description |
|-------|-------------|
| `llm` | Cloud or explicit OpenAI-compatible backend configured |
| `auto` | No cloud keys; each prompt auto-probes local Ollama before fallback |
| `ollama` | Explicit `provider=ollama` (hard error if Ollama unavailable) |
| `fallback` | Deterministic `herd-builtin-coordinator` only |

When the backend is **`auto`**, **`fallback`**, or misconfigured **`llm`**, the command prints **Reason** and **Fix** hints. API keys are never printed in full.

**Example (`herd agent status --json`**, zero cloud keys, Ollama running with models):

```json
{
  "node_name": "node-1",
  "backend_class": "auto",
  "backend_provider": "ollama",
  "model": "llama3.2",
  "configured_llm": false,
  "registered_tools_count": 18,
  "reason": "No cloud LLM keys configured; each prompt auto-probes local Ollama ...",
  "fix_hints": ["Start Ollama locally or set OPENAI_BASE_URL ..."]
}
```

With `--probe` on an **`auto`** backend, Herd calls Ollama `GET /api/tags`. Probe fails if the server is reachable but **no models** are installed.

### `herd agent prompt "<task>" [flags]`
Sends a task prompt to the local embedded AI agent (or a remote node agent). Supports activating declarative roles from Markdown definitions (`AGENTS.md` and `*.agent.md`).

```bash
# Execute prompt with default coordinator persona
herd agent prompt "List all suspect nodes and ping their Tailcat addresses"

# Activate a specific declarative role with scoped tools
herd agent prompt --role ops "Audit remote cluster resources"

# Run deep research with research persona and model override
herd agent prompt --role researcher --model gemini-3.8-flash "Search recent security advisories for Linux kernels"

# Target a remote node agent over Tailcat mesh
herd agent prompt --target node-2 "Analyze system load"
```

Flags:
- `--role <name>`: Activate a specific agent persona defined in `AGENTS.md` or `*.agent.md`
- `--model <name>`: Override model identifier (e.g. `gpt-4o-mini`, `gemini-3.8-flash`)
- `--target <node>`: Target a specific node's agent daemon across the mesh
- `--socket <path>`: Custom Unix domain socket path

### `herd agent chat [flags]`
Starts an interactive terminal chat session with the agent runtime.

```bash
herd agent chat
herd agent chat --role researcher
herd agent chat --target node-2
```

### `herd agent tools`
Lists all registered cluster and research tools available to the AI agent runtime.

```bash
herd agent tools
```

---

## 8. Embedded Documentation Operations

Herd embeds its entire documentation suite directly inside the binary via Go embed, allowing fully self-contained offline reference without internet or repository access.

### `herd docs [topic]`
Lists all embedded documentation topics, displays a specific topic, views all documents concatenated, or exports them to disk.

```bash
# List all available documentation topics and descriptions
herd docs

# Display a specific topic
herd docs mailbox
herd docs logging
herd docs distributed-kv
herd docs architecture

# View all documents concatenated
herd docs --all

# Export all embedded markdown documents to a directory
herd docs --export ./herd-docs
```


