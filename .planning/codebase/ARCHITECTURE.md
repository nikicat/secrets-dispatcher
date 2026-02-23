# Architecture

**Analysis Date:** 2026-02-23

## Pattern Overview

**Overall:** Multi-layer proxy architecture with command/control separation

**Key Characteristics:**
- Dual-connection proxy pattern: connects to both remote D-Bus (untrusted) and local Secret Service (trusted)
- Observer/subscriber pattern for event propagation (approval events, client connect/disconnect)
- Request gating via approval manager with user interaction enforcement
- Two operational modes: single-socket (one remote client) and multi-socket (multiple remote clients)
- CLI and Web UI as alternate frontends to the same approval/status APIs

## Layers

**D-Bus Proxy Layer (`internal/proxy/`):**
- Purpose: Transparently proxy Secret Service requests from remote clients to local Secret Service
- Location: `internal/proxy/proxy.go`, `internal/proxy/service.go`, `internal/proxy/collection.go`, `internal/proxy/item.go`
- Contains: D-Bus interface handlers for `org.freedesktop.secrets` service, collection, and item objects
- Depends on: approval manager (for request gating), logging, dbus library
- Used by: Main entry point for single-socket mode; Manager for multi-socket mode

**Proxy Management Layer (`internal/proxy/manager.go`):**
- Purpose: Manages multiple concurrent proxy instances watching socket directories
- Location: `internal/proxy/manager.go`
- Contains: Socket file watching via fsnotify, proxy lifecycle management, client connection/disconnection events
- Depends on: Proxy layer, approval manager
- Used by: Main entry point for multi-socket mode, API server (for client discovery)

**Approval Management Layer (`internal/approval/`):**
- Purpose: Tracks pending secret access requests and enforces approval workflow
- Location: `internal/approval/manager.go`
- Contains: Request creation, approval/denial, timeout enforcement, observer subscription
- Depends on: UUID generation, time-based expiration
- Used by: Proxy layer (gating requests), API handlers (user actions), notification system

**HTTP API Layer (`internal/api/`):**
- Purpose: Provides REST API for Web UI and CLI clients to query status and approve/deny requests
- Location: `internal/api/server.go`, `internal/api/handlers.go`, `internal/api/websocket.go`
- Contains: Request routing, authentication middleware, JSON serialization, WebSocket for real-time updates
- Depends on: Approval manager, proxy manager (for multi-socket mode), auth system
- Used by: CLI clients, web browser clients

**Authentication Layer (`internal/api/auth.go`):**
- Purpose: Issues time-limited auth tokens for API access via secure cookie storage
- Location: `internal/api/auth.go`, `internal/api/jwt.go`
- Contains: Cookie file persistence, JWT token generation/validation, login URL generation
- Depends on: State directory management
- Used by: API server middleware

**CLI Layer (`internal/cli/`):**
- Purpose: Command-line client for querying and managing approvals
- Location: `internal/cli/client.go`, `internal/cli/format.go`
- Contains: HTTP client, output formatting (text/JSON), command dispatch
- Depends on: HTTP client, API types
- Used by: Main entry point CLI subcommands

**Notification Layer (`internal/notification/`):**
- Purpose: Sends desktop notifications when approval requests arrive
- Location: `internal/notification/desktop.go`
- Contains: D-Bus notification interface integration
- Depends on: Approval manager (as subscriber)
- Used by: Main during serve mode setup

## Data Flow

**Secret Access Request (Happy Path):**

1. Remote app calls `org.freedesktop.secrets` method (e.g., `SearchItems`)
2. Proxy layer receives via D-Bus subtree handler
3. Proxy extracts sender info and creates approval request (unless auto-approved)
4. Approval manager holds request in pending state, notifies subscribers
5. Desktop notifier sends OS notification to user
6. User opens Web UI or uses CLI to view request details
7. User clicks approve/deny via Web UI or CLI
8. Approval manager resolves request with decision
9. Proxy layer receives result channel signal
10. If approved: Proxy forwards D-Bus call to local Secret Service, returns result
11. If denied: Proxy returns `org.freedesktop.DBus.Error.AccessDenied`

**Multi-Socket Mode Setup:**

1. Main creates Manager with sockets directory
2. Manager scans existing sockets and starts watching directory via fsnotify
3. For each socket file, Manager creates a Proxy instance and runs in goroutine
4. Proxy connects to remote D-Bus socket and local session bus
5. Proxy registers as `org.freedesktop.secrets` on remote bus
6. Manager notifies observers (API server) of client connection
7. When socket removed, Manager stops proxy and notifies observers of disconnection

**Client-Initiated Approval:**

1. CLI/Web UI client makes HTTP request to `/api/v1/pending/{id}/approve`
2. API handler looks up request in approval manager
3. Approval manager signals the request's result channel
4. Proxy layer wakes up from channel read, checks result, proceeds accordingly
5. Proxy returns result to remote D-Bus client
6. Approval manager moves request to resolved history

## Key Abstractions

**Proxy:**
- Purpose: Represents a single connection to a remote D-Bus and the local Secret Service
- Examples: `internal/proxy/proxy.go`, handlers in `internal/proxy/service.go`, `collection.go`, `item.go`
- Pattern: Single Proxy per remote client; goroutine-safe via D-Bus connection handling

**Manager:**
- Purpose: Orchestrates multiple proxies and watches for socket additions/removals
- Examples: `internal/proxy/manager.go`
- Pattern: Maintains map of socket path → proxy instance, reacts to filesystem events

**Approval Request:**
- Purpose: Represents a pending secret access decision
- Examples: `internal/approval/manager.go` (Request struct)
- Pattern: Request struct with done channel for blocking; observers subscribe for event notifications

**D-Bus Handlers (Service, Collection, Item, SubtreeProperties):**
- Purpose: Implements Secret Service D-Bus interface methods
- Examples: `internal/proxy/service.go`, `internal/proxy/collection.go`, `internal/proxy/item.go`
- Pattern: Each handler object implements methods matching D-Bus interface signatures; calls registered on remote/local connections

**Session Manager:**
- Purpose: Creates and tracks Secret Service sessions for encryption/decryption operations
- Examples: `internal/proxy/session.go`
- Pattern: Maps session IDs to session metadata; delegates to local Secret Service

## Entry Points

**`main.go` - Subcommand Router:**
- Location: `main.go` lines 33-61
- Triggers: Program execution with subcommand argument
- Responsibilities: Parse subcommand, delegate to handler

**`runServe` - Server Entry Point:**
- Location: `main.go` lines 242-460
- Triggers: `secrets-dispatcher serve [args]`
- Responsibilities:
  - Parse config from file + flags
  - Initialize approval manager
  - Set up desktop notifications
  - Create proxy manager (multi-socket) OR single proxy (single-socket)
  - Start API server
  - Run proxy(s) until signal/context cancellation

**`runCLI` - CLI Entry Point:**
- Location: `main.go` lines 140-240
- Triggers: `secrets-dispatcher {list|show|approve|deny|history} [args]`
- Responsibilities:
  - Parse config and flags
  - Load auth token from state directory
  - Create CLI client with server address
  - Execute command and format output

**`runLogin` - Login URL Entry Point:**
- Location: `main.go` lines 79-138
- Triggers: `secrets-dispatcher login [args]`
- Responsibilities:
  - Load auth from state directory
  - Generate time-limited login URL
  - Attempt to open in browser

## Error Handling

**Strategy:** Layered error conversion with context preservation

**Patterns:**

**D-Bus Layer:**
- Internal errors wrapped in `dbus.Error` with appropriate D-Bus error names
- Example: `org.freedesktop.DBus.Error.Failed` for most errors
- Location: `internal/proxy/service.go` (lines 44-46), `collection.go` (lines 62-68)

**Request Gating:**
- Approval timeout → `ErrTimeout` returned to blocking caller
- Request denied → `ErrDenied` returned to proxy
- Request not found → `ErrNotFound` for lookup failures
- Location: `internal/approval/manager.go`

**API Layer:**
- HTTP 400/401/403/500 status codes with JSON error body
- Helper: `writeError()` in `internal/api/handlers.go`
- Authentication failures: 401 Unauthorized
- Approval failures: JSON error with HTTP 200 (legacy behavior)

**Main Entry Point:**
- CLI errors printed to stderr with exit code 1
- Server startup errors fatal with exit code 1
- Signal handling graceful shutdown without error exit

## Cross-Cutting Concerns

**Logging:**
- Approach: Structured logging via `log/slog` with `tint` colored formatter or JSON output
- Client name included in all logs for multi-socket tracing
- D-Bus method calls logged with arguments and result (success/error)
- Location: `internal/logging/audit.go`

**Validation:**
- D-Bus path validation (collection vs. item) in handlers
- Approval timeout validation at creation
- Config file validation on load
- CLI argument validation before API calls

**Authentication:**
- Token stored in state directory with restricted permissions (600)
- API middleware checks token presence and validity
- Login endpoint requires no token (bootstrap)
- Authorization: All-or-nothing (authenticated = full access)

**Session Management:**
- Sessions created per `OpenSession` call
- Tracked in per-proxy SessionManager
- Mapped by session path for secret encryption/decryption
- Auto-cleanup on proxy shutdown

**Client Disconnection Handling:**
- Tracker watches for D-Bus client disconnects
- Cancels any in-flight approval requests for that client
- Notifies multi-socket manager via observer pattern
- Location: `internal/proxy/tracker.go`

---

*Architecture analysis: 2026-02-23*
