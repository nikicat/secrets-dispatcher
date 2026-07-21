# secrets-dispatcher Requirements

> **Status / scope.** This is the original design & requirements doc for the
> **remote-server proxy** use case — the project's first target. The shipped
> product has since grown two more first-class capabilities not covered here:
> **local per-app access control** (wrap your own keyring, approve access per
> process) and **GPG commit-signing approval**. For the current feature set and
> user-facing docs, see the [README](../README.md) and
> [TARGET-AUDIENCE.md](TARGET-AUDIENCE.md). Sections below are annotated where
> they diverged from what shipped. The standard Secret Service DH **session**
> encryption (R6) is implemented; **client pairing (R3/D1) and the
> pairing-based MITM-resistant transport it enables remain planned**.

## Problem Statement

**Scenario**: User has trusted laptop + untrusted servers. Need to access secrets from servers without exposing all secrets to a compromised server.

**Current vulnerability with gpg-agent forwarding**:
1. All `.gpg` files synced to server via git
2. gpg-agent socket forwarded via SSH
3. If passphrase cached: attacker silently decrypts ALL secrets
4. If not cached: user approves one pinentry, attacker piggybacks to decrypt others
5. No visibility into WHICH secret is requested or by WHICH process

## Requirements

### R1: Per-Secret Authorization
- Each secret access requires explicit approval (or pre-authorization rule)
- Approving one secret does NOT authorize access to others
- Unlike gpg-agent which authorizes at key level, not secret level

### R2: Full Context Display
When approval is requested, user sees:
- Secret path (e.g., `servers/db-password`)
- Secret label (human-readable name)
- Requesting client identity (server name)
- Timestamp

### R3: Client Authentication with Pairing — ⏳ planned (not implemented)
- New clients must "pair" with the service (like Enpass browser extension)
- Pairing uses PAKE or SAS verification to prevent MITM
- User visually confirms matching code on both ends
- Paired clients stored with their public key

### R4: Access Control Rules
```yaml
clients:
  server-a:
    allow:
      - "servers/server-a/*"     # Auto-approve
    deny:
      - "personal/*"             # Never allow
    prompt:
      - "*"                      # Ask for everything else
  server-b:
    allow: []
    deny: ["*"]                  # Blocked entirely
```

### R5: Standard Protocol Compatibility
- Remote apps use standard libsecret/secret-tool
- No code changes required on server side
- D-Bus Secret Service protocol

### R6: Transport Security
- Secrets encrypted with DH session key (Secret Service protocol) — ✅ implemented
- SSH tunnel for D-Bus transport — ✅ (standard SSH `LocalForward`)
- Resistant to MITM attacks — ⏳ depends on client pairing (R3), planned

### R7: Audit Logging
- Log all secret access attempts (approved and denied)
- Include timestamp, client, secret path, decision

### R8: Backend Agnostic
- Uses system's Secret Service as backend (via local D-Bus)
- Works with any Secret Service implementation:
  - gopass-secret-service
  - gnome-keyring
  - KDE Wallet
  - KeePassXC

---

## Architecture

```
SERVER                                      LAPTOP
┌─────────────────────────────────┐        ┌─────────────────────────────────┐
│                                 │        │                                 │
│  App ──► local D-Bus            │        │   secrets-dispatcher            │
│              │                  │        │         │                       │
│              │ (server's bus)   │        │         │ registers as          │
│              │                  │        │         │ org.freedesktop.secrets│
│              └──────────────────┼────────┼─────────┘                       │
│                  SSH tunnel     │        │         │                       │
│                  LocalForward   │        │         ▼ proxies to            │
│                                 │        │   local D-Bus                   │
│                                 │        │         │                       │
│                                 │        │         ▼                       │
│                                 │        │   Backend Secret Service        │
│                                 │        │   (gopass/gnome-keyring/etc)    │
└─────────────────────────────────┘        └─────────────────────────────────┘
```

### Data Flow

1. App on server calls `secret_service_lookup()` via libsecret
2. libsecret talks to server's local D-Bus
3. `org.freedesktop.secrets` is provided by secrets-dispatcher (via SSH tunnel)
4. secrets-dispatcher receives request, identifies client as "server-a"
5. secrets-dispatcher checks access rules
6. If rule says "prompt": show approval dialog to user
7. If approved: proxy request to local Secret Service
8. Return secret (encrypted with DH session key) back through tunnel

---

## Design Decisions

### D1: Pairing Protocol
**Decision**: Deferred — **not implemented**. Remote clients are identified by
their SSH tunnel / D-Bus connection (D2) rather than a cryptographic pairing
handshake. If/when pairing is built, the candidate approaches are:
- SRP (Secure Remote Password) - like Enpass
- PAKE (SPAKE2) - like Magic Wormhole
- Simple visual code - display same code, user confirms match

### D2: Client Identification
**Decision**: By SSH tunnel / D-Bus connection
- Each server gets its own SSH tunnel
- secrets-dispatcher connects to that tunnel
- All requests on that connection = that server

### D3: Approval UI
**Decision**: Multi-interface approach

1. **Desktop notifications** with D-Bus actions for quick approve/deny — ✅ shipped
2. **CLI commands** for scripting and power users — ✅ shipped (`list`/`show`/`approve`/`deny`/`history`)
3. **Web dashboard** for visual overview (secured, see D6) — ✅ shipped
4. **TUI** (terminal UI) for interactive bulk approval — ⏳ only in the exploratory VT/privsep path, not shipped

### D4: Session/Caching
**Decision**: Per-request approval, with opt-in time-boxed auto-approve. The web
UI/notification "approve and auto-approve" action creates a temporary rule that
auto-approves the matching pattern for a configurable duration
(`auto_approve_duration_seconds`, default 120s). Concurrent requests arriving
within `approval_window` (default 2s) are batched into one prompt, and a request
expires after `timeout` (default 5m) if left unresolved. Durable auto-approval
is expressed as trust rules in `config.yaml`.

### D5: Storage Format
**Decision**: YAML config files (git-friendly)

```
~/.config/secrets-dispatcher/
├── config.yaml        # Main config + trust rules
└── .cookie            # Master auth token (mode 0600)

# clients.yaml (paired clients + keys) — planned, ships with R3 pairing
```

Audit records are written to **stderr** as structured JSON (captured by
systemd-journald when run as the user service), not to a dedicated log file.

### D6: Web UI Authentication
**Decision**: Cookie file + one-time token exchange (bitcoind-style)

1. Daemon generates random secret on startup
2. Secret stored in `~/.config/secrets-dispatcher/.cookie` (mode 0600)
3. CLI tools read cookie file directly for API access
4. Browser auth via one-time token exchange:
   - User runs `secrets-dispatcher login`
   - CLI generates a one-time token and prints/opens the login URL
   - Browser opens the URL with the token
   - Server validates, sets HttpOnly session cookie
   - Browser now authenticated

Security properties:
- Cookie file protected by filesystem permissions (0600)
- One-time token expires quickly, single-use
- Session cookie: HttpOnly, SameSite=Strict
- No persistent secrets in browser history

---

## Implementation Phases

### Phase 1: Basic Proxy (MVP) — ✅ shipped
- Connect to remote D-Bus via custom address (`serve --downstream socket:...`)
- Register as org.freedesktop.secrets
- Proxy all requests to local Secret Service
- Log all requests

### Phase 2: Approval Prompts — ✅ shipped
- Show desktop notification for each GetSecret call
- Require click to approve (also web UI + CLI)
- Block until approved/denied

### Phase 3: Access Control Rules — ✅ shipped
- Config file for approve/deny/ignore rules (matched on process + secret attrs)
- Auto-approve matching rules
- Deny blocked patterns

### Phase 4: Client Pairing — ⏳ planned (not implemented)
- Pairing flow with visual code verification
- Store paired client keys
- Per-client rules

> Beyond this original remote-proxy roadmap, the project also shipped local
> keyring takeover (`service install --mode local`), the one-command reversible
> `try` onboarding flow, and GPG commit-signing approval — see the README.

---

## Interfaces

*(As shipped.)*

### Secret Service (frontend)
Bus: Session bus
Name: `org.freedesktop.secrets`

The proxy claims the standard Secret Service name so unmodified libsecret
clients talk to it transparently. It forwards approved calls to the real backend
(gopass-secret-service, gnome-keyring, KeePassXC, or a tunneled remote bus).

### HTTP + WebSocket API
Listens: `127.0.0.1:8484` (configurable via `listen`)

REST endpoints (`/api/v1/...`) for listing/approving/denying requests plus a
WebSocket for real-time updates. Authentication:
- CLI/thin client: `Authorization: Bearer <token>` from the cookie file
- Browser: HttpOnly session cookie obtained via the one-time `login` token exchange

Used by: web dashboard, `list`/`approve`/`deny`/`history` CLI.

### Local Unix socket
An owner-only (mode 0600) Unix socket serving the same HTTP API, for same-user
thin clients without going through the TCP listener.

### Desktop notifications
Approve/Deny action buttons are delivered via the freedesktop
`org.freedesktop.Notifications` interface; the action is activated in-process
(no separate custom D-Bus service).

### System D-Bus daemon (exploratory privsep path)
The `daemon`/`provision` subcommands register `net.mowaka.SecretsDispatcher1` on
the **system** bus for the companion-user privilege-separation experiment — a
parallel path to the shipped session-bus proxy, not a replacement.

---

## CLI Command Structure

As shipped (`secrets-dispatcher <command>`; run `<command> -h` for options):

```bash
secrets-dispatcher
├── serve                    # Start the proxy server + API (foreground)
├── try                      # Reversible trial: take over the Secret Service,
│                            #   Ctrl-C restores everything (no config changes)
├── service                  # Manage the systemd user service
│   ├── install [--no-start] [--mode local|remote|full] [--backend ...] [--dry-run]
│   ├── uninstall            # Restore stock behavior exactly
│   └── status               # Doctor-style health report (name owner, units, takeover)
│
├── login                    # Print/open a one-time login URL for the web UI
├── list                     # List pending requests
├── show <id>                # Show a request (pending or resolved)
├── approve <id>             # Approve a pending request
├── deny <id>                # Deny a pending request
├── history                  # Show resolved requests
│
├── gpg-sign                 # GPG signing proxy (invoked by git as gpg.program)
│   └── setup                # Configure git to sign through secrets-dispatcher
│
├── config
│   ├── show [--defaults]    # Show current configuration
│   ├── edit                 # Edit in $EDITOR, optionally restart service
│   └── validate             # Validate config syntax and values
│
├── provision                # Provision companion user + artifacts (root; privsep path)
├── daemon                   # Run companion daemon on system D-Bus (privsep path)
└── version                  # Print version
```

> Client pairing (`client pair`/`list`/`remove`) is **not** implemented — it
> belongs to the planned R3 pairing work. There is no separate `stop` command;
> a foreground `serve`/`try` stops on Ctrl-C, and the systemd unit is managed
> via `service`.

---

## Bulk Request Handling

Secret Service protocol natively supports bulk requests via `GetSecrets(items[], session)`.

Flow for bulk approval:
1. Client calls `GetSecrets([item1, item2, item3], session)`
2. secrets-dispatcher receives batch request
3. Checks access rules for each item
4. Groups items by rule result (allow/deny/prompt)
5. Auto-approves "allow" items
6. Shows single approval prompt for all "prompt" items
7. User approves/denies the batch
8. Returns results

Notification for bulk request:
```
┌─────────────────────────────────────────────────────┐
│ 🔐 Bulk Secret Request from server-a                │
│                                                     │
│ Requesting 3 secrets:                               │
│   • servers/server-a/db-password                    │
│   • servers/server-a/api-key                        │
│   • servers/server-a/redis-password                 │
│                                                     │
│ [Approve All]  [Review in TUI]  [Deny All]         │
└─────────────────────────────────────────────────────┘
```

---

## Open Questions

1. **Laptop offline** — a remote request simply fails (backend unreachable); the
   requesting app sees a Secret Service error. No offline queueing.
2. **Mobile access** — out of scope.
3. **Multiple concurrent clients** — resolved: a single `serve` process handles
   multiple downstream connections; client identity is tracked per connection
   and shown in the UI for disambiguation.
