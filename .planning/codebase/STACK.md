# Technology Stack

**Analysis Date:** 2026-02-23

## Languages

**Primary:**
- Go 1.25.6 - Core application (proxy server, CLI, API)
- TypeScript - Web UI (Svelte SPA)
- HTML/CSS - Static assets

**Secondary:**
- Shell script - Deployment and setup helpers (see README.md automation scripts)

## Runtime

**Environment:**
- Go 1.25.6 runtime (compiled binary)
- D-Bus daemon (Unix/Linux system service) - must be available on both server and laptop

**Package Manager:**
- Go modules (built-in)
- Deno (JavaScript runtime for frontend development and testing)

**Lockfiles:**
- `go.mod`, `go.sum` - Go dependencies
- `deno.lock` - Deno/Node dependencies for frontend

## Frameworks

**Core Backend:**
- Standard library `net/http` - HTTP server and routing
- `github.com/coder/websocket` v1.8.14 - WebSocket support for real-time updates
- `github.com/godbus/dbus/v5` v5.2.2 - D-Bus client library for Secret Service proxy

**Frontend:**
- Svelte v5.0.0 - UI framework (SPA)
- Vite v6.0.0 - Build tool and dev server
- TypeScript - Type safety

**Testing:**
- `go test` (built-in) - Go unit tests
- Playwright v1.58.2 - E2E browser tests for web UI
- `svelte-check` v4.0.0 - Frontend type checking

**Build/Dev:**
- Make - Build orchestration
- Deno - Frontend tooling runner (runs Vite, Playwright, etc.)
- Go build - Binary compilation

## Key Dependencies

**Critical:**
- `github.com/godbus/dbus/v5` v5.2.2 - D-Bus communication (core functionality - proxies Secret Service requests)
- `github.com/coder/websocket` v1.8.14 - WebSocket real-time updates (browser notifications and live request list)

**Logging & Configuration:**
- `github.com/lmittmann/tint` v1.1.3 - Colored structured logging (text output) - stderr sink
- `gopkg.in/yaml.v3` v3.0.1 - Configuration file parsing (XDG config paths)

**Utilities:**
- `github.com/fsnotify/fsnotify` v1.9.0 - File system watching (multi-socket mode - monitors socket directory)
- `github.com/google/uuid` v1.6.0 - UUID generation for request IDs and authentication tokens
- `golang.org/x/sys` v0.27.0 - System-level operations (signals, file operations)

## Configuration

**Environment Variables:**
- `XDG_CONFIG_HOME` - Config file location (defaults to `~/.config`)
- `XDG_STATE_HOME` - Authentication cookie storage (defaults to `~/.local/state`)
- `XDG_RUNTIME_DIR` - Runtime directory for sockets (required for multi-socket mode)
- `DBUS_SESSION_BUS_ADDRESS` - D-Bus session bus address (set by system, used by proxy)

**Config Files:**
- `~/.config/secrets-dispatcher/config.yaml` - Optional YAML configuration
  - `state_dir` - Override default state directory
  - `listen` - HTTP listen address (default: 127.0.0.1:8484)
  - `serve.sockets_dir` - Override multi-socket watch directory
  - `serve.remote_socket` - Single socket path (mutually exclusive with sockets_dir)
  - `serve.client` - Remote client name (for logging)
  - `serve.log_level` - debug/info/warn/error (default: info)
  - `serve.log_format` - text/json (default: text)
  - `serve.timeout` - Approval timeout duration (default: 5m)
  - `serve.history_limit` - Max resolved requests to keep (default: 100)
  - `serve.notifications` - Enable desktop notifications (default: true)

**Build Configuration:**
- `Makefile` - Build orchestration
  - `make build` - Full build (frontend + backend)
  - `make backend` - Go binary compilation
  - `make frontend` - Deno frontend build
  - `make test-go` - Go unit tests
  - `make test-e2e` - Playwright browser tests
- `web/vite.config.ts` - Vite build config (frontend bundling)
- `web/svelte.config.js` - Svelte preprocessor config

## Platform Requirements

**Development:**
- Go 1.25.6 or later
- Deno (for frontend development, testing, and building)
- D-Bus libraries (libdbus development headers for compilation)
- Unix/Linux environment (uses X11/Wayland desktop notifications, systemd units, XDG directories)

**Production:**
- Linux host with D-Bus available
- SSH access for tunneling (remote server → laptop)
- Laptop with local Secret Service running (GNOME Keyring, KeePassXC, gopass-secret-service, etc.)
- `dbus-daemon` available on remote server (started manually or by system)

**Deployment Target:**
- Linux binary (x86_64, ARM64, etc. - cross-compilable with Go)
- No external service dependencies (fully self-contained)
- Runs as unprivileged user

---

*Stack analysis: 2026-02-23*
