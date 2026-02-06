# secrets-dispatcher Requirements

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

### R3: Client Authentication with Pairing
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
- Secrets encrypted with DH session key (Secret Service protocol)
- SSH tunnel for D-Bus transport
- Resistant to MITM attacks

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
**Decision**: TBD
Options:
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

1. **Desktop notifications** with D-Bus actions for quick approve/deny
2. **CLI commands** for scripting and power users
3. **TUI** (terminal UI) for interactive bulk approval
4. **Web dashboard** for visual overview (secured, see D6)

### D4: Session/Caching
**Decision**: TBD
After approving a secret:
- Allow same secret for N minutes without re-prompt?
- Allow same secret for duration of SSH session?
- Always require approval?

### D5: Storage Format
**Decision**: YAML config files (git-friendly)

```
~/.config/secrets-dispatcher/
├── config.yaml        # Main config + access rules
├── clients.yaml       # Paired clients and their keys
├── .cookie            # Auth cookie (mode 0600, regenerated on start)
└── logs/
    └── audit.log      # Access audit log
```

### D6: Web UI Authentication
**Decision**: Cookie file + one-time token exchange (bitcoind-style)

1. Daemon generates random secret on startup
2. Secret stored in `~/.config/secrets-dispatcher/.cookie` (mode 0600)
3. CLI tools read cookie file directly for API access
4. Browser auth via one-time token exchange:
   - User runs `secrets-dispatcher web auth`
   - CLI generates one-time token (30s expiry)
   - Opens browser with token in URL
   - Server validates, sets HttpOnly session cookie
   - Browser now authenticated

Security properties:
- Cookie file protected by filesystem permissions (0600)
- One-time token expires quickly, single-use
- Session cookie: HttpOnly, SameSite=Strict
- No persistent secrets in browser history

---

## Implementation Phases

### Phase 1: Basic Proxy (MVP)
- Connect to remote D-Bus via custom address
- Register as org.freedesktop.secrets
- Proxy all requests to local Secret Service
- Log all requests (no approval yet)

### Phase 2: Approval Prompts
- Show desktop notification for each GetSecret call
- Require click to approve
- Block until approved/denied

### Phase 3: Access Control Rules
- Config file for allow/deny/prompt rules
- Auto-approve matching rules
- Deny blocked paths

### Phase 4: Client Pairing
- Pairing flow with visual code verification
- Store paired client keys
- Per-client rules

---

## Interfaces

### Unix Socket API
Path: `/run/user/<UID>/secrets-dispatcher.sock` (mode 0600)

JSON-RPC or REST API for:
- Listing pending requests
- Approving/denying requests
- Querying status
- Audit log access

Used by: CLI tools, TUI

### HTTP API
Listens: `localhost:<PORT>` (configurable)

Same API as Unix socket, but requires authentication:
- Cookie file secret in `Authorization: Bearer <cookie>` header
- Or session cookie (obtained via one-time token exchange)

Used by: Web dashboard, browser

### D-Bus Interface
Bus: Session bus
Name: `org.freedesktop.SecretsDispatcher` (or similar)

Methods for desktop integration:
- Notification actions can call D-Bus directly
- Quick approve/deny without CLI

---

## CLI Command Structure

```bash
secrets-dispatcher
├── start                    # Start daemon (foreground)
├── daemon                   # Start daemon (background)
├── stop                     # Stop daemon
├── status                   # Show daemon status
│
├── pending                  # List pending requests
├── approve <id>             # Approve request
├── deny <id>                # Deny request
├── approve-all [--client X] # Approve all (optionally filtered)
│
├── client
│   ├── list                 # List paired clients
│   ├── pair                 # Start pairing flow
│   └── remove <name>        # Remove client
│
├── web
│   ├── auth                 # Authenticate browser (opens browser)
│   └── open                 # Auth + open dashboard (shortcut)
│
├── log [--follow]           # View audit log
└── config
    └── edit                 # Open config in $EDITOR
```

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

1. What happens if laptop is offline?
2. Mobile access - how would this work from phone?
3. Multiple concurrent clients - one process or multiple?
