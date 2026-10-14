# Security Deep Dive

This guide describes Herd attack surfaces, failure modes, and mitigations. For a short trust summary, see [architecture.md](architecture.md) §4. This text follows ASD-STE100 (Simplified Technical English) in a relaxed form: short sentences and active voice.

See also:
- [Wire Protocol & Transport](protocol.md)
- [CLI Commands Reference](commands.md)
- [Testing Process](testing-process.md)

---

## 1. IPC methods (local trust boundary)

All methods use JSON-RPC on a Unix socket. The socket file mode is `0600`. Any local user that can open the socket can call any method.

| Method family | Capability | Remote blast radius |
|---|---|---|
| `GetStatus`, `GetRoster` | Read cluster metadata | None (read-only) |
| `JoinNode`, `LeaveCluster` | Change local membership | Indirect (join exposes node to mesh) |
| `ExecCommand` | Shell on local + remote targets | **High** — arbitrary command on any reachable peer |
| `ForwardPort` | Local listen → peer port | **Medium** — exposes internal services via localhost tunnel |
| `CopyFile` | File read/write across nodes | **High** — data exfiltration or overwrite |
| KV (`KVGet`, `KVSet`, …) | Distributed store | **Medium** — poison cluster state, mailbox injection |
| Mail (`MailSend`, …) | Actor mailbox | **Medium** — triggers agent workloads on peers |
| Agent (`AgentPrompt`, …) | LLM + tools on daemon | **High** — tools wrap exec/KV/mail |
| `Stop` | Daemon shutdown | Local DoS |

**Mitigation today:** Use socket path permissions and OS user isolation. **Not provided:** Peer credential checks, mTLS on IPC, or sudo-style elevation.

---

## 2. Mesh stream handlers (peer trust boundary)

| Stream type | Handler | Abuse scenario |
|---|---|---|
| `StreamTypeExec` | Local shell | Any joined peer runs commands without IPC |
| `StreamTypeAgent` | Agent engine | Prompt injection → tool calls |
| `StreamTypeFile` | File transfer | Unauthenticated file push/pull |
| Gossip / KV delegate | State sync | Malicious KV entries, roster spam |

Crypto protects data on the wire. Crypto does not authorize the peer.

---

## 3. Transport failure modes

- **Discovery key fallback** — Shared across all Herd builds; if used on the wire, passive observers with source code can decrypt those packets. Prefer ECDH paths once peer keys are known.
- **DERP / NAT relay** — Traffic may traverse third-party relays (Tailcat). Treat relay operators as network observers.
- **Memberlist WAN tuning** — Aggressive suspicion can mark nodes `left` under partition; lenient tuning slows failure detection. Tune `ProbeInterval`, `SuspicionMult` when bridging high-latency links.
- **Partial shutdown** — Canceling `runCtx` stops in-flight exec/agent streams; gossip leave is best-effort with timeout.

---

## 4. Memberlist tuning reference

Defaults in `daemon.Start` (WAN profile):

- `TCPTimeout`: 10s
- `ProbeTimeout`: 5s
- `ProbeInterval`: 5s
- `SuspicionMult`: 6

Under asymmetric partitions, expect false suspicions before convergence. For lab clusters on loopback, convergence tests use ~12s deadlines (`test/cluster_integration_test.go`).

---

## 5. Agent tool abuse surface

| Tool | Risk |
|---|---|
| `cluster_exec` | Arbitrary shell; optional KV policy `agent:policy:allowed_exec` prefix whitelist |
| `cluster_cp` | Cross-node file copy |
| `shared_kv` | Write arbitrary keys (including `mailbox:*`) |
| `agent_send_mail` / `agent_broadcast` | Wake remote agents |
| `fetch_url` / `google_search` | SSRF / exfiltration if model is attacker-controlled |

**Mitigation today:** `agent:policy:allowed_exec` KV key (OpenAI tool path). Gemini path should honor the same policy where tool dispatch is shared. Restrict API keys and agent definitions on untrusted nodes.

---

## 6. Recommended hardening roadmap

1. Join tokens propagated in join RPC and validated before accepting streams.
2. mTLS or WireGuard identity binding peer node name to roster entries.
3. Exec/cp allowlists default-deny on production profiles.
4. Per-cluster PSK distributed at join (replace global discovery fallback for multi-tenant labs).
5. Audit log for IPC and remote exec with structured logger fields.
