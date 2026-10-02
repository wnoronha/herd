# Cross-Platform Building & Deployment

Guide to building, cross-compiling, and running Herd on Linux, macOS (Apple Silicon & Intel), Windows, and ARM devices (Raspberry Pi).

See also:
- [Architecture Overview](architecture.md)
- [CLI Commands Reference](commands.md)
- [Testing & Verification](testing-process.md)

---

## 1. Supported Platforms & Architectures

Herd is written in pure Go and compiles as a **statically linked single binary** with zero external C library dependencies (`CGO_ENABLED=0`):

| Operating System | Architecture | Binary Name | Target Hardware |
|---|---|---|---|
| **Linux** | `arm64` (`aarch64`) | `herd-linux-arm64` | Raspberry Pi 5 / 4 (64-bit), ARM64 servers, AWS Graviton |
| **Linux** | `amd64` (`x86_64`) | `herd-linux-amd64` | Standard Linux servers, Ubuntu, Debian, Alpine, Fedora |
| **macOS** | `arm64` | `herd-darwin-arm64` | Apple Silicon Macs (M1, M2, M3, M4) |
| **macOS** | `amd64` | `herd-darwin-amd64` | Intel-based Macs |
| **Windows** | `amd64` | `herd-windows-amd64.exe` | Windows 10/11, Windows Server |

---

## 2. Cross-Compilation Commands

To compile release binaries for all supported platforms:

```bash
mkdir -p bin/release

# macOS (Apple Silicon)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/release/herd-darwin-arm64 ./cmd/herd

# macOS (Intel)
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/release/herd-darwin-amd64 ./cmd/herd

# Linux (Raspberry Pi 5 / ARM64)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/release/herd-linux-arm64 ./cmd/herd

# Linux (x86_64)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/release/herd-linux-amd64 ./cmd/herd

# Windows (x86_64)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o bin/release/herd-windows-amd64.exe ./cmd/herd
```

---

## 3. Makefile Targets

The [`Makefile`](../Makefile) provides automated shortcuts:

```bash
# Build local native binary
make build

# Install to ~/.local/bin/herd
make install

# Run complete quality gate (vet, lint, race tests)
make check

# Cross-compile release binaries for all platforms
make build-all

# Clean build artifacts
make clean
```

---

## 4. Cross-Platform Shell Execution (`herd exec`)

Herd adapts its command execution shell to the target operating system automatically at runtime:

- **Unix / Linux / macOS**: Commands are executed via `/bin/sh -c "<command>"`.
- **Windows**: Commands are executed via `powershell.exe -Command "<command>"` (with automatic fallback to `cmd.exe /c`).

This ensures cross-platform commands run reliably regardless of which operating system hosts the target node.
