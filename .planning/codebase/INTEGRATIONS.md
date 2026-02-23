# External Integrations

**Analysis Date:** 2026-02-23

## APIs & External Services

**D-Bus Secret Service:**
- Local Secret Service (GNOME Keyring, KeePassXC, gopass-secret-service)
  - SDK/Client: `github.com/godbus/dbus/v5` v5.2.2
  - Interface: `org.freedesktop.Secret` service, registered methods:
    - `CreateCollection()` - Create secret collections
    - `OpenSession()` - Open secrets session
    - `GetSecrets()` - Retrieve secret values
    - `SearchItems()` - Search for secrets by attributes
    - `SetAlias()` - Set collection aliases
    - `Lock()/Unlock()` - Lock/unlock collections
  - Used by: `internal/proxy/service.go`, `internal/proxy/collection.go`, `internal/proxy/item.go`
  - Authentication: D-Bus session bus authentication (handled by system)

**Remote D-Bus Session Bus:**
- Remote server's D-Bus session bus (accessed via SSH tunnel)
  - Connection: Unix socket forwarded via SSH local port forward
  - Mode 1 (Single-socket): Direct connection to single remote D-Bus socket
  - Mode 2 (Multi-socket): File system watching for socket files in `$XDG_RUNTIME_DIR/secrets-dispatcher/`
  - Used by: `internal/proxy/proxy.go`, `internal/proxy/manager.go`
  - Protocol: D-Bus protocol over Unix socket

## Data Storage

**Databases:**
- Not applicable - stateless proxy service
- In-memory state only (approval requests, client tracking)

**File Storage:**
- Configuration file: `~/.config/secrets-dispatcher/config.yaml` (YAML format)
- State/Cookies: `~/.local/state/secrets-dispatcher/` directory
  - `secrets-dispatcher.cookie` - HTTP authentication token file (UUID-based)
  - Client connection metadata stored in memory only

**Caching:**
- None - all requests proxied through in real-time

## Authentication & Identity

**Auth Provider:**
- Custom session-based authentication
  - Implementation: `internal/api/auth.go`
  - Method: Cookie-based JWT tokens (signed with HMAC secret)
  - Token generation: `api.NewAuth()` creates 32-byte random secret
  - Token verification: `Auth.Middleware()` checks X-Auth-Token header
  - Token expiration: Generated tokens included in login URL, valid until session ends
  - Cookie storage: Persistent file at `~/.local/state/secrets-dispatcher/secrets-dispatcher.cookie`
  - Usage: `POST /api/v1/auth` endpoint accepts token from login URL

**D-Bus Authentication:**
- System-level D-Bus message authentication (handled by dbus-daemon)
- Sender verification via Unix socket peer credentials (PID, UID)
- Used for: Process tracking, user identification (see `internal/proxy/senderinfo.go`)

## Monitoring & Observability

**Error Tracking:**
- Not integrated with external error tracking
- Errors logged to stderr

**Logs:**
- Structured logging via `log/slog` (Go standard library) with custom handler
- Output format: Colored text (default) or JSON (configurable)
- Sink: `os.Stderr`
- Audit logging: JSON format with fields: `time`, `level`, `msg`, `client`, `method`, `items`, `result`
- Example log: `{"time":"...","level":"INFO","msg":"dbus_call","client":"myserver","method":"GetSecrets","items":["/org/freedesktop/secrets/collection/login/1"],"result":"ok"}`
- Levels: debug, info, warn, error (configurable via `--log-level`)
- Time format: `time.TimeOnly` when using tint handler

**Desktop Notifications:**
- Integration: D-Bus `org.freedesktop.Notifications` interface
- Implementation: `internal/notification/desktop.go`
- Triggered by: New approval requests, request resolution
- Content: Client name, process info, secret details
- Delivery: Via `org.freedesktop.Notifications.Notify()` method call
- Library: `github.com/godbus/dbus/v5` v5.2.2

## CI/CD & Deployment

**Hosting:**
- Self-hosted binary (laptop user runs locally)
- No cloud deployment
- Single machine deployment per user

**CI Pipeline:**
- Not integrated (no external CI/CD service)
- Local testing: `make test-go`, `make test-e2e`
- Manual build: `make build`

## Environment Configuration

**Required env vars:**
- `XDG_RUNTIME_DIR` - Must be set for multi-socket mode; used for socket directory creation
- `DBUS_SESSION_BUS_ADDRESS` - Set by system; used to connect to local Secret Service
- Optional: `XDG_CONFIG_HOME`, `XDG_STATE_HOME` (fall back to `~/.config` and `~/.local/state`)

**Secrets location:**
- Authentication tokens stored in: `~/.local/state/secrets-dispatcher/secrets-dispatcher.cookie`
- No API keys or external service credentials required
- File permissions: 0600 (user-only read/write)

## Webhooks & Callbacks

**Incoming:**
- None - stateless HTTP API only
- `/api/v1/auth` - Authentication endpoint (generates login URL)
- No external webhook ingestion

**Outgoing:**
- None - no outbound API calls to external services
- Desktop notifications sent to local D-Bus notifier only
- SSH tunnel (user-initiated, not application-initiated)

## WebSocket Communications

**Real-Time Updates:**
- Protocol: WebSocket (RFC 6455) over HTTP
- Endpoint: `/api/v1/ws` (requires authentication via X-Auth-Token header)
- Library: `github.com/coder/websocket` v1.8.14 (Coder's WebSocket fork)
- Message format: JSON
  - `snapshot` - Initial state (all pending requests, clients, history)
  - `request_created` - New approval request received
  - `request_resolved` - Request approved/denied/expired
  - `client_connected` - New remote client connected (multi-socket mode)
  - `client_disconnected` - Client disconnected
  - `history_entry` - New history entry added
- Used by: Web UI in `web/src/lib/websocket.ts`
- Ping/Pong: 30-second ping period to keep connection alive
- Max message size: 512 bytes

---

*Integration audit: 2026-02-23*
