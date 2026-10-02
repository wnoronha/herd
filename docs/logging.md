# Unified Logging Architecture

Developer guide and architecture specification for structured, leveled logging across Herd.

See also:
- [Architecture Overview](architecture.md)
- [CLI Commands Reference](commands.md)
- [Testing Process](testing-process.md)

---

## 1. Motivation & Architecture

Prior to Herd 1.0, subsystems utilized divergent logging mechanisms:
- Daemon entrypoints used `go.uber.org/zap` for colored or JSON console logging.
- Internal daemon coordinators logged via standard library `*log.Logger` (`d.Logger.Printf`).
- Memberlist gossip logged through `log.New(os.Stderr, "[node-gossip] ", ...)`.
- Tailcat DERP listeners defaulted to unformatted `log.Printf` timestamps (`2026/09/30 14:23:28 [v1] magicsock: ...`).

This created confusing, mismatched log lines with conflicting timestamp formats, missing node identifiers, and un-filterable log levels.

Herd solves this with a **single unified logging architecture** centered around an abstract interface: [`logger.Logger`](../internal/logger/logger.go) backed by **Uber Zap**.

```mermaid
graph TD
    ENV["Environment Variables (HERD_LOG_LEVEL, HERD_LOG_FORMAT)"] --> INIT["logger.NewLogger(level, format)"]
    INIT --> CORE["Zap Core (JSON or CapitalColor Console Encoder)"]
    CORE --> WRAPPER["logger.Logger (zapWrapper)"]

    WRAPPER -->|Daemon Context| DAEMON["Daemon (d.Logger.WithNode(name))"]
    DAEMON -->|Named('transport')| TAILCAT["Tailcat Transport (Logf adapter)"]
    DAEMON -->|Named('gossip')| GOSSIP["Memberlist Gossip (ToStdLogger adapter)"]
    DAEMON -->|Named('agent')| AGENT["AI Agent Engine (Infof / Errorf)"]
    DAEMON -->|Named('mailbox')| MAILBOX["Mailbox Watcher (Infof / Errorf)"]
    DAEMON -->|Named('file_stream')| FILES["File Stream Subsystem (Warnf)"]
```

---

## 2. The `logger.Logger` Interface

Defined in `internal/logger/logger.go`, `logger.Logger` exposes both strongly-typed Zap field logging and convenient Printf-style leveled logging:

```go
type Logger interface {
    // Structured leveled logging with zap.Field arguments
    Debug(msg string, fields ...zap.Field)
    Info(msg string, fields ...zap.Field)
    Warn(msg string, fields ...zap.Field)
    Error(msg string, fields ...zap.Field)

    // Formatted leveled logging (Printf-style)
    Debugf(format string, args ...any)
    Infof(format string, args ...any)
    Warnf(format string, args ...any)
    Errorf(format string, args ...any)
    Printf(format string, args ...any) // Aliased to Infof

    // Contextual annotations & sub-loggers
    With(fields ...zap.Field) Logger
    Named(component string) Logger
    WithNode(nodeName string) Logger
    WithComponent(component string) Logger
    WithPeer(peer string) Logger

    // Adapters for third-party libraries
    Desugar() *zap.Logger
    Sugar() *zap.SugaredLogger
    ToStdLogger() *log.Logger
    Logf() Logf
    Sync() error
}
```

---

## 3. Subsystem Namespacing & Adapters

Every subsystem and background loop MUST inherit from the root logger with a descriptive component name:

```go
// 1. Tagging a specific subsystem
subsysLog := d.Logger.Named("agent")
subsysLog.Infof("Prompt completed in %dms (model: %s)", execMs, model)

// 2. HashiCorp Memberlist adapter
// Memberlist requires a standard library *log.Logger
mcfg := memberlist.DefaultWANConfig()
mcfg.Logger = d.Logger.Named("gossip").ToStdLogger()

// 3. Tailscale / Tailcat adapter
// Tailcat Server requires a func(format string, args ...any) (tslogger.Logf)
tcServer := &tcat.Server{
    Key:          nodePriv,
    PresharedKey: psk,
    Logf:         tslogger.Logf(d.Logger.Named("tailcat").Logf()),
}
```

---

## 4. Developer Guidelines & Anti-Patterns

To ensure consistent logs across the mesh, all developers and AI agents writing Herd code must adhere to the following rules:

### 4.1 Strict Anti-Patterns

| Anti-Pattern | Why It Is Forbidden | Correct Replacement |
|---|---|---|
| `fmt.Println(...)` / `fmt.Printf(...)` | Unstructured, unformatted, bypasses log level filtering and log routing. | `logger.Infof(...)` or `logger.Debugf(...)` |
| `log.Println(...)` / `log.Printf(...)` | Emits plain stderr text with uncoordinated timestamps, bypassing Zap. | `logger.Infof(...)` |
| `log.New(os.Stderr, ...)` | Creates unmanaged standard library loggers with fixed prefixes. | `logger.Named("subsys").ToStdLogger()` |
| Concrete `*zap.Logger` on structs | Binds components directly to Zap concrete structs, breaking mocking and decoupling. | `Logger logger.Logger` |
| Dual-logger pattern (`Logger` + `ZapLogger`) | Causes divergent output formats and inconsistent logging paths. | Single `Logger logger.Logger` |

### 4.2 Best Practices

1. **Always Accept `logger.Logger` in Constructors**:
   ```go
   func NewSubsystem(cfg Config, log logger.Logger) (*Subsystem, error) {
       if log == nil {
           log = logger.NewNop()
       }
       return &Subsystem{log: log.Named("subsystem")}, nil
   }
   ```
2. **Default to `logger.NewNop()` If Nil**:
   Never let a `nil` logger panic. Always fall back cleanly to `logger.NewNop()`.
3. **Use Leveled Logging Appropriately**:
   - `Debug` / `Debugf`: High-frequency packets, handshake traces, and ephemeral transitions.
   - `Info` / `Infof`: Lifecycle events, startup/shutdown, cluster joins, task completions.
   - `Warn` / `Warnf`: Recoverable errors, failed retries, degraded network fallbacks.
   - `Error` / `Errorf`: Action failures, command errors, critical network disruptions.

---

## 5. Configuration & Environment Overrides

Herd logging is configured at runtime via standard environment variables:

| Environment Variable | Allowed Values | Default | Purpose |
|---|---|---|---|
| `HERD_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` | Minimum log severity to emit |
| `HERD_LOG_FORMAT` | `console`, `json` | `console` | Output encoder format |

### 5.1 Console Format (`console`)
Human-readable, colored output with ISO8601 timestamps and component tags:
```text
2026-09-30T15:20:00.123-0400	INFO	[node-1]	Starting Herd daemon for node 'node-1' (Tailcat Addr: tcpGFw...)...
2026-09-30T15:20:00.150-0400	INFO	[node-1.mailbox]	Waking up agent 'node-1' to process task msg-101
```

### 5.2 JSON Format (`json`)
Machine-readable structured JSON suitable for CloudWatch, Datadog, Vector, or FluentBit:
```json
{"level":"info","timestamp":"2026-09-30T15:20:00.123-0400","node":"node-1","message":"Starting Herd daemon for node 'node-1'..."}
{"level":"info","timestamp":"2026-09-30T15:20:00.150-0400","node":"node-1","component":"mailbox","message":"Waking up agent 'node-1' to process task msg-101"}
```

---

## 6. Testing Practices

When writing unit and integration tests:

1. **Use `logger.NewNop()`**: Keep test output clean and avoid polluting CI logs:
   ```go
   d, err := daemon.New(cfg, logger.NewNop())
   if err != nil {
       t.Fatalf("daemon.New error: %v", err)
   }
   ```
2. **Testing Log Output**: When verifying that a specific log message was emitted, use `logger.NewLogger` with a test buffer or Zap observer:
   ```go
   var buf bytes.Buffer
   log, err := logger.NewLogger("debug", "json")
   // Assert on expected log strings or fields
   ```
