# Changelog

This file records notable project items.
The format follows Keep a Changelog 1.1.0.

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
