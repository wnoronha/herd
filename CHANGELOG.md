# Changelog

This file records notable project items.
The format follows Keep a Changelog 1.1.0.

## [Unreleased]

### Added
- Persist chat sessions in StateDir sessions.db with ADK database service.
- Add chat list command for persisted sessions.
- Enable ADK sliding-window compaction for chat sessions.
- Add memory recall tool backed by ADK memory service.
- Wire ADK load and preload memory tools plus artifact load tools.
- List versioned artifact entries through ADK artifact service.

### Removed
- Remove in-memory TTL eviction for chat sessions.

## [0.2.1] - 2026-10-24

### Added
- Add streaming agent responses over IPC (Inter-Process Communication).
- Add yes-or-no approval prompts for destructive chat tools.
- Add chat commands for tools, history, and sessions.
- Add Agent Development Kit (ADK) v2 engine with session runners.
- Add Ollama auto-probe with deterministic fallback for air-gapped prompts.
- Add session TTL (Time-To-Live) and cap eviction for persisted chat sessions.

### Fixed
- Skip history fold when a session persists context.
- Retry local models without tool support through a tool-less call.

## [v0.2.0] - 2026-10-20

### Added
- Add mDNS discovery with TXT codec and tests.
- Add provider interface with join pipeline and dedup.
- Add daemon wiring with LAN-IP advertisement flags.
- Add home-LAN quickstart guide with trust model.
- Add dns_lookup agent tool for record resolution.
- Add herd agent status with probe modes.
- Add agent quickstart docs with debug steps.
- Add Cobra CLI with native command structure.
- Add distributed KV docs and actor mailbox guides.

### Changed
- Run CI on ubuntu-26.04 explicitly.
- Tighten gocyclo threshold via extraction refactors.
- Tidy pflag dependency for Cobra commands.
- Refresh default persona template with DNS guidance.
- Restructure agent and daemon layout with expanded CI.

### Fixed
- Drop link-local addrs from mDNS candidates.
- Gate live-DNS behind HERD_LIVE_DNS flag.
- Isolate agent env from Ollama leakage.
- Fix IPC map with probe timeouts and checks.
- Fix CLI arg validators and docs flag conflict.
- Fix forward tunnel reservation with port checks.
- Chunk transport writes to 10MB frames.
- Harden daemon shutdown with race-safe snapshots.
- Pin govulncheck and hygiene checks on PR.

### Security
- Enforce fail-closed exec policy on corrupt KV.
- Enforce policy checks in deterministic dispatch.
- Normalize model names without silent fallback.
