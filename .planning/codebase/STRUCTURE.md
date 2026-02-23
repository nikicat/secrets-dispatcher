# Codebase Structure

**Analysis Date:** 2026-02-23

## Directory Layout

```
secrets-dispatcher/
├── main.go                 # Entry point: subcommand router (serve, login, list, show, approve, deny, history)
├── proxy_test.go           # Top-level integration tests for proxy behavior
├── go.mod                  # Module declaration and dependencies
├── go.sum                  # Dependency checksums
├── Makefile                # Build and test targets
├── README.md               # User-facing documentation
├── TODO.md                 # Task tracking
├── docs/                   # Additional documentation
├── cmd/
│   └── mock-secret-service/  # Mock Secret Service for testing
│       └── main.go
├── internal/
│   ├── proxy/              # D-Bus proxy core logic
│   ├── approval/           # Request approval/denial workflow
│   ├── api/                # HTTP REST API server
│   ├── cli/                # Command-line client
│   ├── config/             # Configuration file handling
│   ├── logging/            # Structured audit logging
│   ├── notification/       # Desktop notification integration
│   ├── dbus/               # D-Bus type definitions
│   ├── testutil/           # Test utilities and mocks
│   └── api/web/            # Embedded web UI assets
├── web/                    # Web UI source (Svelte, not compiled)
├── test-results/           # Test output artifacts
└── .planning/codebase/     # GSD codebase analysis documents (this directory)
```

## Directory Purposes

**Root:**
- Purpose: Main module and entry point
- Contains: Go source files, build configuration, documentation
- Key files: `main.go`, `go.mod`, `README.md`

**`internal/proxy/`:**
- Purpose: D-Bus proxy implementation connecting remote D-Bus to local Secret Service
- Contains:
  - `proxy.go`: Main Proxy struct and lifecycle (Connect, Run, Close)
  - `service.go`: Implements `org.freedesktop.Secret.Service` interface methods
  - `collection.go`: Handles collection objects (SearchItems, Delete, etc.)
  - `item.go`: Handles secret items (GetSecret, Delete, etc.)
  - `subtree.go`: Subtree handler registration for D-Bus paths
  - `manager.go`: Watches sockets directory and manages multiple proxy instances
  - `session.go`: SessionManager for Secret Service session tracking
  - `tracker.go`: Tracks D-Bus clients and detects disconnections
  - `senderinfo.go`: Resolves process information (PID, UID, unit name) from D-Bus sender
  - `unitpath.go`: D-Bus object path utilities

**`internal/approval/`:**
- Purpose: Manages pending secret access requests and approval workflow
- Contains:
  - `manager.go`: Tracks requests, enforces timeouts, notifies observers
  - `types.go`: Type definitions (Request, Event, ItemInfo, etc.)

**`internal/api/`:**
- Purpose: HTTP REST API server and Web UI serving
- Contains:
  - `server.go`: HTTP server setup, listener management
  - `handlers.go`: API endpoints for status, pending list, approve/deny actions
  - `websocket.go`: WebSocket handler for real-time client/request updates
  - `auth.go`: Cookie-based authentication, token generation
  - `jwt.go`: JWT token creation/validation
  - `types.go`: API request/response types
  - `static.go`: SPA handler for web UI assets
  - `version.go`: Version information endpoint
  - `embed.go`: Asset embedding metadata
  - `web/`: Compiled web UI assets (Svelte)

**`internal/cli/`:**
- Purpose: Command-line client for API interaction
- Contains:
  - `client.go`: HTTP client abstraction (List, Show, Approve, Deny, History)
  - `format.go`: Output formatting (text table, JSON)

**`internal/config/`:**
- Purpose: YAML configuration file handling
- Contains:
  - `config.go`: Config structs and loader (respects XDG_CONFIG_HOME)

**`internal/logging/`:**
- Purpose: Structured audit logging
- Contains:
  - `audit.go`: Audit logger with D-Bus method call tracking

**`internal/notification/`:**
- Purpose: Desktop notification integration via D-Bus
- Contains:
  - `desktop.go`: D-Bus notification sender

**`internal/dbus/`:**
- Purpose: D-Bus type and constant definitions
- Contains:
  - `types.go`: Constants like BusName, ServicePath, interface names

**`internal/testutil/`:**
- Purpose: Shared test utilities
- Contains:
  - `mockservice.go`: Mock Secret Service for unit tests

**`cmd/mock-secret-service/`:**
- Purpose: Standalone mock Secret Service for integration testing
- Contains:
  - `main.go`: Mock service implementation

**`web/`:**
- Purpose: Web UI source code (pre-build)
- Contains: Svelte source files (not included in built binary)
- Note: Built assets end up in `internal/api/web/`

## Key File Locations

**Entry Points:**
- `main.go`: Main function and subcommand dispatch (lines 33-61)
  - `runServe()`: Server initialization (lines 242-460)
  - `runCLI()`: CLI command execution (lines 140-240)
  - `runLogin()`: Login URL generation (lines 79-138)

**Configuration:**
- `internal/config/config.go`: Config loading from `$XDG_CONFIG_HOME/secrets-dispatcher/config.yaml`

**Core Logic:**
- `internal/proxy/proxy.go`: Main proxy lifecycle
- `internal/approval/manager.go`: Request tracking and decision enforcement
- `internal/api/handlers.go`: HTTP endpoints

**Testing:**
- `proxy_test.go`: Top-level integration tests
- `internal/*/\*_test.go`: Package-level unit tests

## Naming Conventions

**Files:**
- Go source: `lowercase.go` (no underscores except in test files)
- Test files: `{name}_test.go` (e.g., `proxy_test.go`, `manager_test.go`)
- Build output: `secrets-dispatcher` (main binary), `mock-secret-service` (test helper)

**Functions:**
- Exported: PascalCase (e.g., `NewProxy`, `Connect`, `HandleApprove`)
- Unexported: camelCase (e.g., `newProxyInstance`, `writeError`)
- Constructors: `New{Type}()` pattern (e.g., `NewProxy`, `NewManager`)
- D-Bus handlers: Match interface method names (e.g., `SearchItems`, `GetSecret`)

**Variables:**
- Exported: PascalCase (e.g., `ErrDenied`, `EventRequestCreated`)
- Unexported: camelCase (e.g., `defaultListenAddr`, `progName`)
- Package-level constants: SCREAMING_SNAKE_CASE or camelCase per Go convention

**Types:**
- Structs: PascalCase (e.g., `Proxy`, `Manager`, `Request`)
- Interfaces: PascalCase with -er suffix where appropriate (e.g., `Observer`, `ClientProvider`, `ClientObserver`)
- Methods: PascalCase (e.g., `Connect`, `Run`, `Subscribe`)

## Where to Add New Code

**New D-Bus Method Support:**
- Core handler: Add method to appropriate handler struct (`Service`, `CollectionHandler`, `ItemHandler`) in `internal/proxy/{service,collection,item}.go`
- Logging: Add log method to `internal/logging/audit.go`
- Approval: If method accesses secrets, add request creation in proxy handler before local D-Bus call
- Tests: Add test case in corresponding `{handler}_test.go`

**New API Endpoint:**
- Handler: Add method to `Handlers` struct in `internal/api/handlers.go`
- Route: Register pattern in `newServerWithHandlers()` in `internal/api/server.go`
- Types: Add request/response types in `internal/api/types.go` if needed
- Tests: Add test in `internal/api/handlers_test.go`

**New CLI Command:**
- Main dispatch: Add case in `main.go` switch (line 39-51)
- CLI logic: Add method to `Client` in `internal/cli/client.go`
- Formatting: Add formatter method in `internal/cli/format.go` if output format needed
- Tests: Add test in `internal/cli/client_test.go`

**New Configuration Option:**
- Config struct: Add field to `Config` or `ServeConfig` in `internal/config/config.go` with YAML tag
- Main.go: Add flag and config merge logic in appropriate `run*()` function
- Tests: Add test case in `internal/config/config_test.go`

**Utility/Helper Functions:**
- Location: Create in appropriate package or in `internal/testutil/` if test-only
- No catch-all utils package; keep helpers close to where they're used

## Special Directories

**`.planning/codebase/`:**
- Purpose: GSD codebase analysis documents
- Generated: Yes (via `/gsd:map-codebase` command)
- Committed: Yes
- Contents: ARCHITECTURE.md, STRUCTURE.md, etc.

**`test-results/`:**
- Purpose: Test execution artifacts
- Generated: Yes (via `make test`)
- Committed: No (in `.gitignore`)
- Contents: Coverage reports, test logs

**`internal/api/web/`:**
- Purpose: Compiled web UI assets
- Generated: Yes (build step copies from `web/dist/`)
- Committed: Yes (binary includes embedded assets)
- Contains: `dist/` with HTML, CSS, JS

**`web/node_modules/`:**
- Purpose: Web UI dependencies
- Generated: Yes (via `npm install` during web build)
- Committed: No (in `.gitignore`)

## Build and Development

**Build:**
```bash
make build              # Builds main secrets-dispatcher binary
make build-mock         # Builds mock-secret-service for testing
```

**Tests:**
```bash
make test               # Runs all Go tests with coverage
make test-race          # Runs with race detector
```

**Configuration Precedence (CLI & Server):**
1. Explicit command-line flags (highest priority)
2. Config file values from `config.yaml`
3. Environment variables (XDG_* paths)
4. Built-in defaults (e.g., `127.0.0.1:8484`)

---

*Structure analysis: 2026-02-23*
