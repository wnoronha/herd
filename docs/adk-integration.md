# Google ADK Integration

Specification and design for embedding Google's Agent Development Kit (`google.golang.org/adk/v2`) into Herd to form a decentralized autonomous agent mesh. The implementation runs every provider through the native ADK runtime (`model.LLM`, `agent/llmagent`, `runner.Runner`).

See also:
- [Agent Quickstart](agent-quickstart.md) — local Ollama, `herd agent status`, troubleshooting
- [Architecture Overview](architecture.md)
- [Actor-Style Mailbox Subsystem](mailbox.md)
- [Unified Logging Architecture](logging.md)
- [CLI Commands Reference](commands.md)
- [Protocol Specification](protocol.md)
- [Distributed KV Guide](distributed-kv.md)

---

## 1. Vision: Decentralized Agentic Mesh

Herd transforms from a passive infrastructure mesh into an autonomous agent mesh where **every cluster node runs an embedded AI agent**.

Each Node Agent:
1. Represents its local machine (Cloud VM, Raspberry Pi 5, Apple Silicon Mac).
2. Has access to cluster-wide tool primitives (Roster discovery, Tailcat remote execution, Distributed KV, Actor Mailbox, and Web grounding).
3. Can collaborate with peer node agents over encrypted Tailcat WireGuard connections.

```mermaid
graph TD
    CLI["User CLI (herd agent prompt / chat)"] -->|IPC JSON-RPC| DAEMON["Herd Daemon Controller"]
    DAEMON --> AGENT["ADK-Go Agent Runtime"]
    
    subgraph ADKTools["ADK Tools Registry"]
        ROSTER_TOOL["ClusterRosterTool (cluster_roster)"]
        EXEC_TOOL["ClusterExecTool (cluster_exec)"]
        CP_TOOL["ClusterCPTool (cluster_cp)"]
        FORWARD_TOOL["ClusterForwardTool (cluster_forward)"]
        MANAGE_TOOL["ClusterManageTool (cluster_manage)"]
        KV_TOOL["SharedKVTool (shared_kv)"]
        MAIL_TOOLS["Mailbox Tools (agent_send_mail / read / broadcast)"]
        FILE_TOOLS["Local File Tools (file_read / file_write)"]
        ARTIFACT_TOOL["ArtifactStoreTool (artifact_store)"]
        RUN_AGENT_TOOL["RunAgentTool (run_agent)"]
        WEB_TOOLS["Web & DNS Tools (fetch_url / google_search / dns_lookup)"]
    end

    subgraph LLMProviders["Model Providers"]
        OPENAI["OpenAI / Ollama / vLLM (/v1)"]
        GEMINI["Google Gemini API (2.5 Flash / Pro)"]
        FALLBACK["Deterministic Fallback Router"]
    end

    AGENT --> ADKTools
    AGENT --> LLMProviders
    ROSTER_TOOL -->|Query Alive Nodes| ROSTER_STORE["Roster Store"]
    EXEC_TOOL -->|Remote Command Streams| TAILCAT["Tailcat WireGuard Mesh"]
    CP_TOOL -->|Encrypted File Transfers| TAILCAT
    FORWARD_TOOL -->|TCP Tunnels| TAILCAT
    KV_TOOL -->|Read/Write Shared State| KV_STORE["Distributed KV Store"]
    MAIL_TOOLS -->|Actor Envelopes & Replies| KV_STORE
    FILE_TOOLS -->|Local Filesystem I/O| DISK["Node Disk / Workspace"]
    ARTIFACT_TOOL -->|Store Run Artifacts| DISK
    RUN_AGENT_TOOL -->|Delegated Sub-Agent Execution| AGENT
    WEB_TOOLS -->|Outbound HTTP/HTTPS| INTERNET["Web / External APIs"]
    TAILCAT <-->|Encrypted WAN Tunnel| REMOTE_AGENT["Remote Node (Raspberry Pi 5 / Mac)"]
```

---

## 2. ADK Tools Registry

The agent runtime is equipped with Go functions implementing the ADK Tool interface:

### 2.1 `ClusterRosterTool` (`cluster_roster`)
- **Purpose**: Allows the agent to inspect cluster membership, alive nodes, Tailcat endpoints, and latencies.
- **Input Parameters**:
  - `filter_status` (`string`): Optional filter by status (`alive`, `suspect`, `all`). Default: `alive`.
- **Return Data**: JSON array of active nodes with metadata.

### 2.2 `ClusterExecTool` (`cluster_exec`)
- **Purpose**: Enables the agent to run arbitrary shell commands on any node in the cluster (or all nodes) over Tailcat encrypted streams. Subject to `agent:policy:allowed_exec` whitelist.
- **Input Parameters**:
  - `target` (`string`): Target node name (e.g. `node-2`, `node-1`, or `all`).
  - `command` (`string`): Shell command to execute.
  - `timeout_seconds` (`int`): Optional execution timeout (default: 30s).
- **Return Data**: JSON object with `stdout`, `stderr`, `exit_code`, and error messages.

### 2.3 `ClusterCPTool` (`cluster_cp`)
- **Purpose**: Enables agents to transfer files directly across nodes over encrypted multiplexed file streams.
- **Input Parameters**:
  - `src` (`string`): Source file path (e.g. `/tmp/report.txt` or `node-1:/var/log/syslog`).
  - `dst` (`string`): Destination file path (e.g. `/tmp/dest.txt` or `node-2:/tmp/report.txt`).
- **Return Data**: JSON object with transfer success status, bytes transferred, and message.

### 2.4 `SharedKVTool` (`shared_kv`)
- **Purpose**: Allows agents to persist and retrieve shared cluster facts (config, policy, roster facts only) across the cluster. Conversational memory lives in ADK memory, not KV gossip.
- **Input Parameters**:
  - `action` (`string`): `"get"`, `"set"`, `"delete"`, or `"list"`.
  - `key` (`string`): Key identifier (e.g. `agent:memory:incidents/high-cpu`).
  - `value` (`string`): Value to store (for `"set"`).
  - `ttl` (`string`): Optional TTL expiration (e.g. `"1h"`, `"24h"`).
- **Return Data**: Found status, entry value, version, and timestamps.

### 2.5 `AgentSendMailTool` (`agent_send_mail`)
- **Purpose**: Sends an actor-style message or task request into a target mailbox (`mailbox:<target>/<msg_id>`) with thread tracking and TTL.
- **Input Parameters**:
  - `to` (`string`): Destination address (node name, role, or `*` broadcast).
  - `topic` (`string`): Message topic / category (e.g. `task`, `audit`, `db_query`).
  - `message` (`string`): Message payload or task description.
  - `thread_id` (`string`): Optional conversation thread ID.
  - `ttl` (`string`): Message expiration duration (default: `"1h"`).
- **Return Data**: Generated message ID, destination key, and queued timestamp.

### 2.6 `AgentBroadcastTool` (`agent_broadcast`)
- **Purpose**: Publishes an event message to all node agents across the mesh (`mailbox:*/<msg_id>`).
- **Input Parameters**:
  - `topic` (`string`): Broadcast topic name.
  - `message` (`string`): Announcement content.
  - `ttl` (`string`): Message expiration (default: `"30m"`).
- **Return Data**: Broadcast message ID and delivery status.

### 2.7 `AgentReadMailboxTool` (`agent_read_mailbox`)
- **Purpose**: Reads, processes, and acknowledges pending messages waiting in the local or target mailbox (`mailbox:<target>/*`).
- **Input Parameters**:
  - `target` (`string`): Optional mailbox target (defaults to local node).
  - `ack` (`bool`): If true, clears consumed messages from the mailbox.
  - `topic` (`string`): Optional topic filter.
- **Return Data**: List of message envelopes with sender, topic, payload, thread ID, and timestamps.

### 2.8 `FetchURLTool` (`fetch_url`)
- **Purpose**: Retrieves content from an HTTP or HTTPS URL, strips HTML tags/scripts, and returns clean text or JSON content for grounding.
- **Input Parameters**:
  - `url` (`string`): The complete HTTP/HTTPS URL to fetch.
  - `max_bytes` (`int`): Optional maximum bytes to read (default: 65536, max: 524288).
- **Return Data**: Status code, content type, parsed text content, and byte length.

### 2.9 `GoogleSearchTool` (`google_search`)
- **Purpose**: Searches the web for real-time external grounding, technical documentation, and error resolution. Supports Google Custom Search API or local mock fallback.
- **Input Parameters**:
  - `query` (`string`): The search query keywords.
  - `num_results` (`int`): Optional number of search results to return (default: 5, max: 10).
- **Return Data**: List of search results with title, link snippet, and URL.

### 2.9.1 `DNSLookupTool` (`dns_lookup`)
- **Purpose**: Resolve DNS records for troubleshooting connectivity, mail routing, and service discovery.
- **Input Parameters**:
  - `name` (`string`): Hostname, domain, or IP (for PTR).
  - `record_type` (`string`): `A`, `AAAA`, `MX`, `TXT`, `NS`, `CNAME`, `PTR`, `SRV`, or `AUTO` (default).
- **Return Data**: Normalized record list with type-specific fields (`value`, `host`, `priority`, etc.).

### 2.10 `FileReadTool` (`file_read`)
- **Purpose**: Direct local filesystem reading on the current node with line offset and count limit (safety capped at 10MB to prevent memory exhaustion).
- **Input Parameters**:
  - `path` (`string`): Target file path.
  - `offset_lines` (`int`): 1-based start line (default: 1).
  - `max_lines` (`int`): Max lines to return (default: 1000, max: 10000).
- **Return Data**: JSON object with cleaned `path`, `total_lines`, `offset_lines`, `returned_lines`, and `content`.

### 2.11 `FileWriteTool` (`file_write`)
- **Purpose**: Direct local filesystem writing with automatic parent directory creation, overwrite control, and append mode.
- **Input Parameters**:
  - `path` (`string`): Target file path.
  - `content` (`string`): Text content to write.
  - `append` (`bool`): If true, append instead of overwrite (default: `false`).
  - `overwrite` (`bool`): If false and file exists without append, returns error (default: `true`).
- **Return Data**: JSON object with `path`, `bytes_written`, and `status` (`created`, `overwritten`, or `appended`).

### 2.12 `ArtifactStoreTool` (`artifact_store`)
- **Purpose**: Thin Herd wrapper over the ADK artifact service with versioned entries (disk export wrapped as fallback) without polluting structured KV gossip.
- **Input Parameters**:
  - `action` (`string`): One of `"store"`, `"get"`, `"list"`, `"delete"`, or `"versions"`.
  - `name` (`string`): Artifact identifier (required for `store`, `get`, `delete`, `versions`).
  - `content` (`string`): Content string (required for `store`).
  - `version` (`number`): Optional pinned version for `get` (default: latest).
  - `metadata` (`object`): Optional key-value metadata tags.
- **Return Data**: Artifact contents, size, version, creation/update timestamps, and metadata tags. `versions` lists all versions; `list` reports versioned entries.

### 2.13 `MemoryRecallTool` (`memory_recall`)
- **Purpose**: Thin Herd wrapper over the ADK memory service. Recalls relevant prior chat sessions read-only without KV gossip writes. The runner also wires ADK `load_memory`, `preload_memory`, and `load_artifacts` tools.
- **Input Parameters**:
  - `query` (`string`): Natural-language query to search prior sessions.
- **Return Data**: Matching memory entries with id, author, content, and timestamp.

### 2.14 `ClusterForwardTool` (`cluster_forward`)
- **Purpose**: Dynamically establishes, lists, or stops bidirectional TCP port forwarding tunnels across mesh nodes over encrypted Tailcat streams.
- **Input Parameters**:
  - `action` (`string`): `"start"`, `"stop"`, or `"list"`.
  - `local_port` (`int`): Local port to bind listener to (required for `start`).
  - `target_node` (`string`): Target node name or address (required for `start`).
  - `target_port` (`int`): Remote port on the target node (required for `start`).
  - `bind_host` (`string`): Local IP interface to bind to (default: `127.0.0.1`).
  - `id` (`string`): Tunnel identifier to stop (required for `stop` if `local_port` omitted).
- **Return Data**: Tunnel status, tunnel ID, listening address, and uptime.

### 2.15 `ClusterManageTool` (`cluster_manage`)
- **Purpose**: Allows agents to inspect node/daemon health, join peer addresses dynamically, or initiate graceful leaves.
- **Input Parameters**:
  - `action` (`string`): `"status"`, `"join"`, or `"leave"`.
  - `address` (`string`): Peer address or endpoint to join (required for `join`).
- **Return Data**: Daemon status (uptime, member count, Tailcat addr), joined peer count, or leave confirmation.

### 2.16 `RunAgentTool` (`run_agent`)
- **Purpose**: Generic agent invocation primitive implementing Google ADK's `AgentTool` pattern. Allows any agent or persona to execute an isolated task prompt against any other agent without hardcoded role hierarchies.
- **Input Parameters**:
  - `agent` (`string`): Target agent name or persona to invoke (e.g. `ops`, `researcher`, or custom persona).
  - `prompt` (`string`): Task prompt or evaluation instruction.
  - `context` (`string`): Optional contextual background data.
- **Return Data**: Sub-agent `response`, `model_used`, `execution_ms`, and `tools_called`.

---

## 3. Parallel Tool Execution Architecture

During multi-step reasoning, models frequently produce multiple independent tool requests in a single turn (e.g. querying status from 4 nodes or running multiple KV queries concurrently).

Herd's agent engine executes all tool calls within a reasoning turn concurrently using bounded goroutines and deterministic pre-allocated indexed slices:

```mermaid
sequenceDiagram
    autonumber
    actor User as User / Mailbox
    participant Engine as Agent Engine
    participant LLM as Model (OpenAI / Gemini)
    participant T1 as Goroutine 1 (cluster_roster)
    participant T2 as Goroutine 2 (shared_kv)
    participant T3 as Goroutine 3 (fetch_url)

    User->>Engine: herd agent prompt "Check roster, read KV, fetch status"
    Engine->>LLM: Request completion with tools schema
    LLM-->>Engine: 3 ToolCalls: [cluster_roster, shared_kv, fetch_url]
    
    par Concurrently Dispatched
        Engine->>T1: Exec cluster_roster (slot 0)
        Engine->>T2: Exec shared_kv (slot 1)
        Engine->>T3: Exec fetch_url (slot 2)
    end

    T1-->>Engine: Result 0 (alive nodes)
    T2-->>Engine: Result 1 (config record)
    T3-->>Engine: Result 2 (HTTP 200 payload)
    
    Note over Engine: sync.WaitGroup.Wait() barriers all goroutines.<br/>Results ordered strictly [0, 1, 2].
    Engine->>LLM: Return tool response messages in exact call order
    LLM-->>Engine: Final synthesized answer
    Engine-->>User: Display agent response and tool audit trace
```

### Key Concurrency Invariants:
1. **ADK Parallel Execution**: Tool calls run inside ADK's parallel tool engine (`platform.RunTasks`). Herd's bridge (`herdADKTool`) is goroutine-safe: handlers that touch shared registries (`cluster_tools.go` forward registry, KV/roster stores) hold their own mutexes.
2. **Deterministic Trace Reconstruction**: `FunctionCall` parts and their `FunctionResponse` payloads are matched by call ID, preserving the exact order of `FunctionCall`s within each turn. This prevents non-deterministic prompt drifting or token mismatch.
3. **Execution Wall-Clock Savings**: Total turn execution time drops from `O(N * Latency)` to `O(max(Latency))`.

---

## 4. Declarative Markdown Agents & Role-Based Scoped Toolsets

Herd supports defining autonomous agent personas and scoped toolsets declaratively via **Markdown files with YAML frontmatter** (`AGENTS.md` and `*.agent.md`).

### 4.1 Specification & YAML Frontmatter Schema

Each markdown agent file defines an agent persona, model requirements, temperature, scoped tools, and a markdown system prompt:

```markdown
---
name: cluster-researcher
role: researcher
description: Deep web and cluster intelligence gathering specialist
model: gemini-3.8-flash
provider: gemini
temperature: 0.2
tools:
  - google_search
  - fetch_url
  - dns_lookup
  - shared_kv
---
# Researcher Persona Instructions

You are an expert research agent embedded in the Herd cluster mesh.
Your primary objective is to search documentation, fetch remote endpoint status,
resolve DNS when debugging connectivity, and persist structured intelligence into the cluster's distributed KV store.

Use `dns_lookup` for A/AAAA/MX/TXT checks; verify external facts with `google_search` before storing conclusions in `shared_kv`.
```

#### Frontmatter Attributes:

| Field | Type | Description |
| :--- | :--- | :--- |
| `name` | `string` | Unique agent identifier (e.g. `cluster-researcher`, `ops-agent`). |
| `role` | `string` | Role name used for `--role <name>` activation (e.g. `researcher`, `ops`). |
| `description`| `string` | Human-readable description of persona capabilities. |
| `model` | `string` | Specific LLM model to activate (e.g. `gpt-4o-mini`, `gemini-3.8-flash`). |
| `provider` | `string` | Model provider override (`"openai"`, `"gemini"`, `"ollama"`). |
| `temperature`| `float` | Sampling temperature (0.0 - 1.0). |
| `tools` | `[]string` | Whitelist of allowed tools. If omitted or empty, all registered tools are available. |
| Body | `markdown` | The markdown text following the closing `---` becomes the agent's `SystemPrompt`. |

### 4.2 Multi-Agent Definitions (`AGENTS.md`)

An `AGENTS.md` file can define multiple agents simultaneously using an `agents:` list:

```markdown
---
agents:
  - name: coordinator
    role: coordinator
    description: Primary cluster coordinator
    tools:
      - cluster_roster
      - cluster_exec
      - cluster_cp
      - shared_kv
      - agent_send_mail
      - agent_broadcast
      - agent_read_mailbox
      - fetch_url
      - google_search
      - dns_lookup
  - name: ops
    role: ops
    description: Cluster infrastructure operator
    tools:
      - cluster_roster
      - cluster_exec
      - cluster_cp
      - dns_lookup
  - name: researcher
    role: researcher
    description: Web research specialist
    tools:
      - google_search
      - fetch_url
      - dns_lookup
      - shared_kv
---
# Base Cluster Operating Guidelines
You are a node agent within the Herd decentralized cluster. Respect node security policies and `agent:policy:allowed_exec`.
```

### 4.3 Standard Discovery Locations & Auto-Seeding

The Herd engine automatically discovers and registers markdown agent definitions from the following directory hierarchy on startup:
1. Current Working Directory: `./AGENTS.md`, `./agents/*.agent.md`, `./.herd/agents/*.agent.md`
2. Node Configuration Directory: `~/.config/herd/<node>/agents/` (or `~/.herd/<node>/agents/`)
3. User Configuration Directory: `~/.config/herd/agents/*.agent.md`, `~/.herd/agents/*.agent.md`

#### Automatic Daemon Auto-Seeding:
When `herd daemon` starts, if no agent definitions exist in the node's agents directory (`~/.config/herd/<node>/agents/`), Herd automatically scaffolds a starter `AGENTS.md` containing `coordinator`, `ops`, and `researcher` personas so the node is immediately operational without manual setup.

#### Manual Initialization (`herd agent init`):
Users can also scaffold an `AGENTS.md` into the current repository or global config using:
```bash
# Scaffold in current repository
herd agent init

# Scaffold into global user configuration
herd agent init --global

# Refresh node personas after upgrading Herd (adds new default tools such as dns_lookup)
herd agent init --force --for-node "$(herd status --json 2>/dev/null | jq -r .node_name // env.HERD_NODE_NAME)"
```

### 4.4 Role-Based Tool Scoping & Security Enforcement

When a role is specified (`--role <name>` in CLI, or `Role` in IPC / Mailbox topic):
1. **Schema Filtering**: Only tools explicitly listed in `AllowedTools` are serialized into the tool schema sent to OpenAI or Gemini. Disallowed tools are completely hidden from the LLM prompt.
2. **Runtime Dispatch Guard**: If a model generates a tool call for a tool not in the role's `AllowedTools`, the tool execution loop intercepts the call and returns an error:
   ```json
   {"error": "tool 'cluster_exec' is not allowed or registered for this agent"}
   ```
3. **Actor Mailbox Role Routing**: Mailbox messages sent with topic `task:<role>` (e.g. `task:researcher`) automatically activate the target role and its scoped toolset when processed by the daemon's mailbox watcher.

### 4.5 Google ADK Agent Config YAML Compatibility

Herd natively supports the **Google ADK Agent Config YAML specification** ([adk.dev/agents/config](https://adk.dev/agents/config/)). Agents can be defined using pure YAML files (`root_agent.yaml`, `*.agent.yaml`, `*.agent.yml`) alongside markdown files:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/google/adk-python/refs/heads/main/src/google/adk/agents/config_schemas/AgentConfig.json
agent_class: LlmAgent
name: tutor_agent
model: gemini-flash-latest
description: Learning assistant that provides tutoring in code and math.
instruction: |
  You are a learning assistant that helps students with coding and math questions.
tools:
  - name: google_search
  - fetch_url
sub_agents:
  - config_path: code_tutor_agent.yaml
```

#### Key ADK Schema Features Supported:
- **`instruction`**: Aliased to `system_prompt` so standard ADK configurations work out-of-the-box.
- **`agent_class`**: Captures ADK class declarations (e.g. `LlmAgent`).
- **Flexible `tools`**: Supports both simple string sequences (`- file_read`) and ADK-style object mappings (`- name: google_search`).
- **`sub_agents`**: Declares dependent agent configs invoked dynamically via `run_agent`.

---

## 5. Supported Model Providers

The agent engine builds one ADK `model.LLM` per request (no global registry) and runs it through `llmagent.New` plus `runner.Runner`. Backend order: **Gemini** (when configured), **OpenAI-compatible** (when `OPENAI_API_KEY`, `OPENAI_BASE_URL`, or `provider=openai`), then **automatic local Ollama** when no cloud keys are set (probes `http://localhost:11434` tags, then drives `http://localhost:11434/v1` through the same ADK `openaimodel` with a tool-less retry on HTTP 400), and finally the **deterministic fallback** (`deterministicLLM`, an in-runtime `model.LLM` that emits a `FunctionCall` on turn one and formats the `FunctionResponse` on turn two) if Ollama is unreachable. Set `provider=ollama` to require Ollama (errors if down). Override defaults with env or `agent:config:*` KV.

| Provider | Environment Variables | Default Endpoint / Model | Best Used For |
|---|---|---|---|
| **OpenAI-Compatible** | `OPENAI_BASE_URL`<br>`OPENAI_API_KEY`<br>`OPENAI_MODEL` | User-defined or `http://localhost:11434/v1`<br>`gpt-4o-mini` / `llama3.2` | OpenAI, Ollama, vLLM, Groq, DeepSeek when explicitly configured |
| **Auto Ollama (zero-key)** | *(optional)* `OLLAMA_MODEL` | `http://localhost:11434/v1` → `llama3.2` | Local Ollama with no KV/env beyond `ollama serve` |
| **Google Gemini API** | `GEMINI_API_KEY` | `gemini-3.8-flash` | Cloud reasoning with Gemini Go SDK |
| **Deterministic Fallback** | *(None required)* | Offline rule-based router | Air-gapped mesh when no LLM is reachable |

Vertex AI is not currently supported. Use the Gemini API directly via GEMINI_API_KEY.

### 5.1 Troubleshooting (local Ollama & status)

Use **`herd agent status --probe`** before debugging prompts. Common cases:

| Observation | What it means | Action |
|-------------|---------------|--------|
| `backend_class: "auto"` | Zero cloud keys; runtime probes Ollama per prompt | Ensure `ollama serve` and `ollama pull <model>` |
| Probe error: no models installed | `GET /api/tags` returned an empty list | `ollama pull llama3.2` or set `OLLAMA_MODEL` |
| Prompt shows `herd-builtin-coordinator` | Auto-probe failed silently before LLM | Read `Ollama auto-probe:` line in response; restart daemon after upgrading Herd |
| `unknown method: AgentStatus` | CLI newer than running daemon | Rebuild and restart `herd daemon` |
| Explicit `provider=ollama` | Ollama is mandatory | Fix Ollama or change KV; no silent fallback |

**Daemon vs CLI:** Agent commands use IPC to the long-running daemon. Rebuild alone is not enough — restart the daemon. See [Agent Quickstart](agent-quickstart.md).

**Model selection:** Auto-Ollama prefers `OLLAMA_MODEL`, then replaces cloud defaults (`gpt-4o-mini`, `gemini-*`) with `llama3.2`, then any installed tag from Ollama’s model list.

### 5.2 ADK Runtime Layout (`internal/agent/`)

| File | ADK role |
|---|---|
| `adk_model.go` | Per-request `model.LLM` factory (`gemini.NewModel`, `openaimodel.NewModel` with `APIChatCompletions`) plus the air-gapped two-turn `deterministicLLM`. No `model.Register` global state. |
| `adk_tools.go` | `herdADKTool` bridge (`tool.Tool` plus `Declaration`, `Run`, `ProcessRequest` via `toolutils.PackTool`), `BeforeToolCallback` exec-policy guard, `OnToolErrorCallback` rejection contract, `run_agent` recursion cap. |
| `engine.go` | `llmagent.New` with `InstructionProvider` (raw system prompt, no `{template}` injection) plus `runner.Runner` over the ADK database session service at `StateDir/sessions.db` with sliding-window compaction; reconstructs `ToolsCalled` matched by call ID in call order. |

System prompts travel via `InstructionProvider` so markdown containing `{curly_braces}` never triggers ADK session-state template errors. Keyless local endpoints receive the placeholder key `ollama` so `openai-go/v3` never fails on a missing `OPENAI_API_KEY`; the default `api.openai.com` endpoint still requires a real key. Stateful chat persists in the shared database service at `StateDir/sessions.db` only when the request carries a `SessionID`; one-shot prompts run on an ephemeral in-memory service that is discarded after the call, so the daemon never accumulates sessions. The runner enables ADK sliding-window compaction and wires ADK memory and artifact services; completed chat turns ingest into memory so `memory_recall` and the ADK `load_memory` and `preload_memory` tools read prior sessions without KV gossip writes.

---

## 6. Dynamic Configuration & Shared Memory via Distributed KV

Herd agents dynamically resolve their runtime configuration, security policies, and shared knowledge directly from the cluster's distributed LWW Key-Value store under the reserved `agent:*` namespace:

| Key Pattern | Scope | Purpose | Example Value |
| :--- | :--- | :--- | :--- |
| `agent:config:global` | Cluster-wide | Default model, base URL, temperature | `{"model":"llama3.2","base_url":"http://10.0.0.1:11434/v1"}` |
| `agent:config:nodes/<node>` | Per-Node Override | Edge hardware / node-specific overrides | `{"model":"qwen2.5-coder:1.5b"}` |
| `agent:policy:allowed_exec` | Cluster Policy | Whitelist filter for `cluster_exec` commands | `["df -h","uptime","systemctl status *","docker ps"]` |
| `agent:memory:<topic>/<key>` | Shared Memory | Cross-node agent collaborative knowledge | `{"pi5_status":"idle","last_backup":"2026-09-26T12:00:00Z"}` |
| `agent:prompts:<role>` | System Prompts | Reusable system prompt templates | `"You are a cluster SRE supervisor..."` |

### Setting Agent Configuration via CLI
```bash
# Set cluster-wide agent backend to a local Ollama server
herd kv set agent:config:global '{"model":"llama3.2","base_url":"http://192.168.1.50:11434/v1"}'

# Restrict remote exec commands to safe inspection tools
herd kv set agent:policy:allowed_exec '["uptime","df -h","free -m","cat /etc/os-release"]'

# Check active configuration
herd kv get agent:config:global
```

---

## 7. CLI Commands & Workflows

### 7.1 One-Shot Task Dispatch (`herd agent prompt`)
```bash
# Run a cluster-wide inspection task
herd agent prompt "Query the OS and uptime of all nodes, and store a markdown table in KV key 'cluster/report'"

# Run with a declarative role
herd agent prompt "Audit suspect nodes" --role ops
```

### 7.2 Interactive Chat Session (`herd agent chat`)
```bash
# Start an interactive CLI session with the local node agent
herd agent chat

# Chat directly with the agent on a remote node
herd agent chat --target node-2
```

The CLI generates one session UUID for the whole interactive loop and sends it as `session_id` on every IPC prompt. The daemon forwards it into the ADK database session service at `StateDir/sessions.db`, so each turn sees prior tool outputs and chats survive daemon restarts. Use `/new` to start a session, `/resume <id>` to resume one, and `/list` to list persisted sessions.

### 7.3 Agent Tools Schema Discovery (`herd agent tools`)
```bash
# Inspect all registered cluster tools available to the agent
herd agent tools
```

### 7.4 Actor-Style Messaging & Reactive Mailboxes

Nodes communicate asynchronously through KV-backed actor mailboxes (`mailbox:<target>/<msg_id>`). When a message arrives, the target node daemon reactively wakes up its embedded agent runtime, auto-acknowledges the entry, executes the task, and symmetrically delivers the reply back to the sender's mailbox (`mailbox:<sender>/<reply_id>`):

1. **Send Message / Task Request**:
```bash
# Using the first-class herd mail CLI command with wait-for-reply
herd mail send --topic sys_check --wait node-2 "Check uptime"

# Or deposit directly into the mailbox via KV:
herd kv set mailbox:node-2/task-123 '{"id":"task-123","from":"node-1","to":"node-2","topic":"sys_check","message":"Check uptime"}'
```

2. **Autonomous Remote Execution**:
The `node-2` daemon's mailbox watcher detects the gossip key, clears the message, invokes its local `AgentEngine`, and routes the reply to `mailbox:node-1/reply_task-123`.

3. **Inspect Mailbox**:
```bash
herd mail list
herd mail read --ack
```

---

## 8. Helper Automation Scripts

* **`scripts/set-agent-kv.sh`**: Easily configures `agent:config:global` (model, provider, and API key) in the distributed KV store from `.env` or CLI arguments.
  ```bash
  ./scripts/set-agent-kv.sh [API_KEY] [MODEL] [NODE_NAME]
  ```
* **`scripts/agent-mailbox-send.sh`**: Sends a task message to a target node's mailbox and waits for the remote agent to wake up, execute the task, and post its reply.
  ```bash
  ./scripts/agent-mailbox-send.sh <TARGET_NODE> "<MESSAGE>" [TOPIC] [TIMEOUT_SECS]
  ```


