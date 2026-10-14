# Agent Quickstart (Local Ollama & Status)

Get the embedded Herd agent answering prompts with a **local Ollama** model in a few minutes, or verify cloud LLM configuration before you spend quota.

See also:
- [CLI Commands Reference](commands.md) — full `herd agent` flag list
- [Agent Runtime & Tools](adk-integration.md) — providers, tools, KV config
- [Security Deep Dive](security-deep-dive.md) — agent tool risks (`dns_lookup`, `fetch_url`, etc.)

---

## How prompts reach the LLM

`herd agent prompt` and `herd agent chat` talk to the **running `herd daemon`** over Unix IPC. The CLI binary and the daemon must be the **same build** after you upgrade Herd:

```bash
make build   # or: go build -o ./bin/herd ./cmd/herd
# Stop the old daemon, then start:
./bin/herd daemon
```

Use `herd agent status` (below) from the same install path you use for the daemon.

---

## Local Ollama in ~2 minutes

### 1. Install and start Ollama

Follow [ollama.com](https://ollama.com) for your OS, then:

```bash
ollama serve          # if not already running as a service
ollama pull llama3.2  # required: empty model list = no LLM replies
ollama list           # should show at least one model
```

Optional: `export OLLAMA_MODEL=your-model:tag` to prefer a specific tag. If the default name is missing, Herd picks the **first model** returned by `GET /api/tags`.

### 2. Start (or restart) Herd daemon

No cloud API keys are required. With no `GEMINI_API_KEY`, `OPENAI_API_KEY`, or `OPENAI_BASE_URL`, each prompt **auto-probes** `http://localhost:11434/v1` (OpenAI-compatible, with tools), then native `/api/chat`, then the deterministic fallback (`herd-builtin-coordinator`).

```bash
herd daemon
```

### 3. Check agent backend

```bash
herd agent status
herd agent status --probe --json
```

| `backend_class` | Meaning |
|-----------------|--------|
| `auto` | No cloud keys; runtime will probe local Ollama on each prompt |
| `llm` | Gemini or explicit OpenAI-compatible config |
| `ollama` | `provider=ollama` in KV or persona (Ollama required; errors if down) |
| `fallback` | No usable LLM path (misconfigured keys or Ollama unreachable) |

With `--probe`, **`auto`** and **`ollama`** backends call Ollama `GET /api/tags`. Probe fails with a clear error if the server is up but **no models** are installed.

### 4. Send a prompt

```bash
herd agent prompt "Say hello in one sentence."
```

Success: `Model:` in the footer is your Ollama model (not `herd-builtin-coordinator`).

Failure: response may include `Ollama auto-probe: ...` — follow that text (usually `ollama pull` or restart daemon).

### 5. DNS example (coordinator / ops / researcher personas)

Default `AGENTS.md` includes `dns_lookup` for troubleshooting:

```bash
herd agent prompt --role ops "Look up A and MX records for example.com and summarize."
```

---

## Cloud LLM (optional)

**Gemini (mesh-wide via KV):**

```bash
herd kv set agent:config:global \
  '{"provider":"gemini","model":"gemini-3.8-flash","api_key":"<GEMINI_API_KEY>"}'
herd agent status --probe
```

**OpenAI-compatible (Ollama on another host, vLLM, Groq, etc.):**

```bash
export OPENAI_BASE_URL=http://192.168.1.50:11434/v1
export OPENAI_API_KEY=ollama
export OPENAI_MODEL=llama3.2
herd agent status --probe
```

Restart the daemon after changing environment variables the daemon was started with.

---

## Cluster-wide agent status

```bash
herd agent status --cluster
```

Runs `herd exec <node> herd agent status --json` on each **alive** roster node. Remote nodes need `herd` on `PATH` and a reachable daemon.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|--------|----------------|-----|
| `Model: herd-builtin-coordinator` | Ollama down, no models, or old daemon | `ollama list`, `ollama pull`, rebuild & restart `herd daemon` |
| `unknown method: AgentStatus` | CLI newer than daemon | Rebuild and restart daemon |
| `Ollama is running but no models` | Empty `ollama list` | `ollama pull llama3.2` |
| Probe OK but prompt still fallback | Daemon not restarted after upgrade | Restart daemon from new binary |
| `--cluster` errors on workers | Remote `herd` missing or old | Install matching version on peers |

More detail: [adk-integration.md §5 Supported Model Providers](adk-integration.md#5-supported-model-providers).
