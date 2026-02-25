# Architecture Patterns: v2.0 Privilege Separation

**Domain:** Linux privilege-separated secret management with VT trusted I/O
**Researched:** 2026-02-25
**Confidence:** HIGH — based on direct codebase analysis + Linux VT/D-Bus documentation

---

## Overview: What Changed from v1.0

v1.0 ran everything as the desktop user (`nb`): secrets-dispatcher daemon, gopass-secret-service, the D-Bus proxy, the HTTP/WebSocket server, and the web UI. Every component shared the same UID, so any process running as `nb` could ptrace or read `/proc/pid/mem` of any other `nb` process — including the daemon holding secrets in memory.

v2.0 splits the system across a UID boundary:

- **Companion user** (`secrets-nb`): owns secrets-dispatcher, gopass-secret-service, GPG keyring, VT 8
- **Desktop user** (`nb`): owns compositor, apps, and a small user-space agent

All approval logic and secret material lives with the companion user. The desktop user's processes can only request secrets via system D-Bus; they cannot inspect the companion user's memory.

---

## System Architecture Diagram

```
Desktop user (nb)                     Companion user (secrets-nb)
=================                     ===========================

Apps (browsers, tools)                secrets-dispatcher (approval core)
  │                                     │  ├─ approval.Manager (REUSED)
  │ Secret Service protocol             │  ├─ system D-Bus interface (NEW)
  ↓                                     │  ├─ VT TUI (NEW)
user-agent (NEW)                        │  └─ GPG runner (MODIFIED)
  ├─ org.freedesktop.secrets            │
  │  (claims on session bus)            gopass-secret-service (UNCHANGED)
  │  ↓ proxy call                         └─ session D-Bus, companion's session
  │
  │  system D-Bus (cross-UID)          GPG keyring + gopass store
  │  ══════════════════════════════════   (companion user's home dir, UID-isolated)
  │  ↑ signal: notification event
  │                                    VT 8 (kernel-enforced isolated I/O)
  └─ notification listener               ├─ TUI: pending request display
       ↓                                  ├─ approve/deny input
  libnotify / dunst                       └─ CLI: history, admin ops

gpg-sign thin client (MODIFIED)
  │ system D-Bus call (was: Unix socket + HTTP)
  └─ → secrets-dispatcher on system bus → approval → GPG sign

PAM hook (NEW)
  └─ on nb login: starts companion session via systemd --user for secrets-nb
```

---

## Component Inventory

### Components: REUSED Unchanged

| Component | Package | Why Unchanged |
|-----------|---------|---------------|
| `approval.Manager` | `internal/approval/` | Core pipeline unchanged — still tracks pending/approve/deny/expire/cancel; same observer pattern |
| `approval.Request` / `GPGSignInfo` | `internal/approval/` | Data model is transport-agnostic; works regardless of how requests arrive |
| `internal/gpgsign/commit.go` | `internal/gpgsign/` | Commit object parsing is unchanged |
| `internal/gpgsign/gpg.go` | `internal/gpgsign/` | GPG invocation logic unchanged; runs as companion user now, correct by design |
| `internal/logging/audit.go` | `internal/logging/` | Structured slog logging; works unchanged |
| `internal/dbus/types.go` | `internal/dbus/` | D-Bus type constants unchanged |
| `internal/proxy/session.go` | `internal/proxy/` | SessionManager for Secret Service sessions; companion's session bus |
| `internal/proxy/collection.go`, `item.go` | `internal/proxy/` | Collection/item D-Bus handlers |
| `internal/proxy/senderinfo.go` | `internal/proxy/` | Sender PID/UID resolver; works on system D-Bus too |
| `internal/proxy/tracker.go` | `internal/proxy/` | Client disconnect detection |
| `internal/proxy/unitpath.go` | `internal/proxy/` | D-Bus unit path decoding |
| `gopass-secret-service` | separate repo | Unchanged; runs on companion's session bus exactly as before |

### Components: MODIFIED

| Component | What Changes | Why |
|-----------|-------------|-----|
| `main.go` | Drop `serve` HTTP mode; add new subcommands: `agent`, `provision`, `vt-serve` | New entry points for companion-side daemon, desktop agent, provisioning |
| `internal/proxy/proxy.go` | Connect to **system** D-Bus (server side) and companion's **session** D-Bus (backend) instead of remote-socket → local-session pattern | Architecture inversion: was session→session proxy; now system→session proxy |
| `internal/proxy/service.go` | Same D-Bus method handlers but sender info resolved via **system bus** (`GetConnectionUnixProcessID` on system bus) | System bus sender lookup differs from session bus |
| `internal/gpgsign/run.go` | Replace Unix socket + HTTP with **system D-Bus method call** to companion | v1.0 used WebSocket/HTTP over Unix socket; v2.0 uses system D-Bus |
| `internal/gpgsign/daemon.go` | Remove `DaemonClient` (HTTP/WebSocket client); replace with D-Bus call | Entire `DaemonClient` struct is dropped |
| `internal/notification/desktop.go` | Notification handler moves to **user-agent** process; companion emits D-Bus signals; agent listens and calls libnotify | Previously ran in same process as daemon |
| `internal/config/config.go` | New config sections: VT number, companion user name, system D-Bus service name, session socket path | New deployment parameters |
| `internal/service/install.go` | Generate companion-user systemd units + D-Bus policy files instead of user service | Different deployment target |

### Components: DROPPED

| Component | Why Dropped |
|-----------|-------------|
| `internal/api/server.go` + `handlers.go` + `websocket.go` + `auth.go` + `jwt.go` | HTTP/WebSocket/Web UI removed entirely in v2.0 |
| `internal/api/static.go` + `embed.go` + `web/` assets | Web UI removed |
| `internal/cli/client.go` (HTTP client) | CLI now talks to VT TUI or system D-Bus directly, not HTTP |
| `internal/api/peercred.go` | Unix socket peer credential code (no Unix socket in v2.0) |
| `internal/api/gpgsign.go` + `api/gpgsign_test.go` | GPG HTTP endpoint removed |
| `web/` directory (Svelte source) | Entire web UI removed |

### Components: NEW

| Component | Location | Responsibility |
|-----------|----------|---------------|
| System D-Bus interface | `internal/systemdbus/` (new pkg) | Exports `net.mowaka.SecretsDispatcher1` on system bus; handles GetSecret, GPGSign method calls from desktop user's processes; calls approval.Manager |
| VT manager | `internal/vt/manager.go` (new pkg) | Opens persistent `/dev/ttyN`, sets VT_SETMODE, owns the trusted I/O channel |
| VT TUI | `internal/vt/tui.go` (new pkg) | Bubbletea-based approval UI on the VT; renders pending requests, handles keyboard approve/deny, integrates approval.Manager |
| User-space agent | `cmd/user-agent/main.go` (new binary) | Runs as desktop user; claims `org.freedesktop.secrets` on session bus; proxies to system D-Bus; subscribes to companion's signals; shows desktop notifications |
| Agent D-Bus proxy | `internal/agent/proxy.go` (new pkg) | Implements Secret Service interface on session bus; forwards calls to system bus; rewrites paths transparently |
| Agent notification listener | `internal/agent/notify.go` (new pkg) | Subscribes to companion's notification signals on system bus; calls notification daemon via session bus |
| PAM module/hook | `internal/pam/hook.go` (new pkg) | `pam_exec` script or shared library: starts companion session on desktop user login; stops it on logout |
| Provisioning tool | `cmd/provision/main.go` (new binary) | Creates companion user, home dir, gopass store, systemd units, D-Bus policy files, PAM config fragment |

---

## Component Boundaries

```
┌──────────────────────────────────────────────────────────────────────┐
│  COMPANION USER (secrets-nb)                                         │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │  secrets-dispatcher (companion-mode)                          │  │
│  │                                                               │  │
│  │  System D-Bus server    ←─── external interface               │  │
│  │  (internal/systemdbus)       accepts: GetSecret, GPGSign      │  │
│  │         │                    rejects: everything else          │  │
│  │         ↓                                                      │  │
│  │  approval.Manager       ←─── core: unchanged from v1.0        │  │
│  │         │                    blocks caller goroutine            │  │
│  │         │ events                                               │  │
│  │         ├─── VT TUI     ←─── displays, takes approve/deny      │  │
│  │         │    (internal/vt)   input from keyboard on VT 8       │  │
│  │         │                                                      │  │
│  │         └─── Signal emitter  emits D-Bus signals on system bus │  │
│  │              (new Observer)  for user-agent notification pickup │  │
│  │                                                               │  │
│  │  When GetSecret approved:                                     │  │
│  │  ↓                                                            │  │
│  │  internal/proxy (session-bus client)                          │  │
│  │  ↓         connects to companion's session bus               │  │
│  │  gopass-secret-service  ←─── companion's session bus          │  │
│  │                               unchanged Secret Service API     │  │
│  └────────────────────────────────────────────────────────────────┘  │
│                                                                      │
│  VT 8  (kernel, not userspace)                                       │
│  └─ /dev/tty8 — owned by secrets-nb, VT_SETMODE set               │  │
└──────────────────────────────────────────────────────────────────────┘

SYSTEM D-BUS (kernel-mediated, cross-UID)
══════════════════════════════════════════
  ├─ net.mowaka.SecretsDispatcher1 (owned by secrets-nb)
  │    methods: GetSecret, GPGSign
  │    signals: RequestCreated, RequestResolved, RequestExpired
  └─ policy file: allows nb to call methods, allows secrets-nb to own name

┌──────────────────────────────────────────────────────────────────────┐
│  DESKTOP USER (nb)                                                   │
│                                                                      │
│  user-agent (cmd/user-agent)                                         │
│  ├─ org.freedesktop.secrets on session bus (proxy to system bus)     │
│  └─ listens to system bus signals → calls libnotify                  │
│                                                                      │
│  gpg-sign thin client (modified)                                     │
│  └─ calls system D-Bus GPGSign method directly                       │
│                                                                      │
│  Applications (Firefox, etc.)                                        │
│  └─ call org.freedesktop.secrets on session bus → user-agent         │
└──────────────────────────────────────────────────────────────────────┘
```

---

## Data Flow: Secret Access Request (v2.0)

```
1. App (nb) calls org.freedesktop.secrets.Service.GetSecrets on session bus
2. user-agent (nb) receives call on session bus
3. user-agent calls net.mowaka.SecretsDispatcher1.GetSecret on SYSTEM bus
   — includes item path, requester PID/UID (extracted from session bus sender info)
4. secrets-dispatcher (secrets-nb) receives system bus call
5. Resolves sender info: GetConnectionUnixProcessID on system bus → real PID/UID
6. Creates approval.Request with full context
7. approval.Manager blocks the goroutine handling the system bus call
8. VT TUI (VT 8) displays pending request: item path, requester PID, unit name
9. User on VT 8 presses [A]pprove or [D]eny
10. VT TUI calls approval.Manager.Approve(id) or Deny(id)
11. Companion emits RequestResolved signal on system bus (user-agent gets notification update)
12. approval.Manager goroutine wakes; approval = true
13. secrets-dispatcher calls gopass-secret-service via companion's session bus
    (standard Secret Service protocol; gopass-secret-service returns secret)
14. secrets-dispatcher returns secret value in system bus method reply
15. user-agent receives system bus reply; returns to app's session bus call
16. App receives secret
```

## Data Flow: GPG Signing Request (v2.0)

```
1. git calls: secrets-dispatcher gpg-sign --status-fd=2 -bsau <keyID>
   (stdin: raw commit object)
2. gpg-sign (nb) parses commit, collects context (same as v1.0)
3. gpg-sign calls net.mowaka.SecretsDispatcher1.GPGSign on SYSTEM bus
   — payload: GPGSignInfo (repo, message, author, files, key ID, commit object)
   — this is a blocking D-Bus method call; it will block until approved or timeout
4. secrets-dispatcher (secrets-nb) receives system bus call
5. Creates gpg_sign approval.Request
6. VT TUI (VT 8) displays: repo name, commit message, author, changed files
7. User approves
8. approval.Manager unblocks goroutine
9. secrets-dispatcher executes real gpg (has access to companion's GPG keyring)
10. Returns signature in system bus method reply
11. gpg-sign writes signature to stdout, status lines to stderr
12. git commits with valid signature
```

## Data Flow: Desktop Notification (v2.0)

```
1. approval.Manager.notify(EventRequestCreated) fires
2. New observer: SignalEmitter calls conn.Emit on system bus:
   net.mowaka.SecretsDispatcher1.RequestCreated (with request metadata)
3. user-agent (nb) receives signal on system bus subscription
4. user-agent calls org.freedesktop.Notifications.Notify on nb's session bus
5. Notification daemon (dunst/mako) shows desktop notification
6. User clicks [Approve] or [Deny] button in notification
7. user-agent calls net.mowaka.SecretsDispatcher1.Approve(id) or Deny(id) on system bus
8. secrets-dispatcher approves/denies; approval.Manager resolves
   — Note: the approval is redundant with VT approval but provides convenience;
     both paths resolve the same approval.Manager request
```

---

## System D-Bus Interface Design

Service name: `net.mowaka.SecretsDispatcher1`
Object path: `/net/mowaka/SecretsDispatcher1`

```
interface net.mowaka.SecretsDispatcher1 {
  // Secret access
  method GetSecret(item_path: s, session: s) → (secret: ay, content_type: s)
  method SearchSecrets(attributes: a{ss}) → (items: as)

  // GPG signing
  method GPGSign(commit_object: ay, gpg_args: as, repo_name: s,
                 commit_msg: s, author: s, committer: s,
                 changed_files: as, key_id: s) → (signature: ay, status: ay)

  // Request management (callable by nb, gated by D-Bus policy)
  method Approve(request_id: s) → ()
  method Deny(request_id: s) → ()
  method ListPending() → (requests: a(ssst)) // id, type, summary, created_at

  // Lifecycle
  method Lock() → ()        // lock companion session
  method Unlock() → ()      // unlock (opens store unlock prompt on VT)

  // Signals (emitted by companion, received by user-agent)
  signal RequestCreated(request_id: s, type: s, summary: s, created_at: t)
  signal RequestResolved(request_id: s, result: s) // "approved"/"denied"/"expired"
  signal RequestExpired(request_id: s)
}
```

**D-Bus policy file** (`/etc/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf`):
```xml
<busconfig>
  <policy user="secrets-nb">
    <allow own="net.mowaka.SecretsDispatcher1"/>
  </policy>
  <policy user="nb">
    <allow send_destination="net.mowaka.SecretsDispatcher1"
           send_interface="net.mowaka.SecretsDispatcher1"/>
  </policy>
</busconfig>
```

Note: The policy file is per-regular-user (`nb`). The provisioning tool generates it. This is the only file that requires root to install.

---

## VT Manager Design

The VT manager allocates and owns a persistent virtual terminal for the companion user's trusted I/O.

**Allocation sequence:**
```
1. Open /dev/tty0 (requires CAP_SYS_TTY_CONFIG or group=tty)
2. ioctl(VT_OPENQRY) → find first free VT number (e.g., 8)
3. Close /dev/tty0
4. Open /dev/tty8
5. ioctl(VT_SETMODE, {mode: VT_PROCESS, relsig: SIGUSR1, acqsig: SIGUSR2})
   — prevents unauthorized VT switching during active approval
6. ioctl(VT_ACTIVATE) → switch to VT 8 on approval prompt
7. Hand fd to bubbletea program as stdin/stdout
```

**VT_SETMODE note:** Only one process per VT can call `VT_SETMODE`. The companion daemon holds this for its lifetime. VT switching back to the desktop is allowed after approval completes (VT_RELDISP).

**Go implementation:** Use `golang.org/x/sys/unix` for ioctl syscalls. Bubbletea accepts custom `io.ReadWriter` for the terminal, so the VT fd is passed directly as the program's input/output instead of os.Stdin/os.Stdout.

**Confidence:** MEDIUM — VT ioctl interface is well-documented (man2/ioctl_vt.2); the `golang.org/x/sys/unix` package provides `Syscall(SYS_IOCTL, ...)` for custom ioctl calls not yet in the stdlib wrapper. The VT_SETMODE pattern is used by Weston, X.org, and sway during VT acquisition; it's the canonical approach.

---

## User-Space Agent Design

The user-agent is a small, single-purpose binary that runs in the desktop user's session. It does two things:

1. **Secret Service proxy**: Claims `org.freedesktop.secrets` on the desktop session bus. When apps call Secret Service methods, the agent proxies them to the system bus. Session translation: the agent maintains session mappings so the app sees consistent session paths while the backend uses a separate set of sessions with the companion.

2. **Notification bridge**: Subscribes to system bus signals from the companion (`RequestCreated`, `RequestResolved`, `RequestExpired`). On `RequestCreated`, calls `org.freedesktop.Notifications.Notify` with approve/deny action buttons. On button click, calls `Approve()` or `Deny()` on the system bus.

The agent must handle the org.freedesktop.secrets session bus requirement: the spec requires the service to be on the session bus, not the system bus. Apps use `dbus.SessionBus()` and look for the well-known name `org.freedesktop.secrets`. The agent satisfies this requirement while keeping the actual secrets on the companion side.

**Existing proxy code reuse:** `internal/proxy/session.go` (SessionManager) and `internal/proxy/subtree.go` can be reused or adapted for the agent's session bus side. The direction is inverted: in v1.0 the proxy sat between a remote session bus and the local session bus; in v2.0 the agent sits between the local session bus (as server) and the system bus (as client).

---

## PAM Hook Design

The companion session must start when the desktop user logs in and stop when they log out.

**Mechanism:** `pam_exec.so` in the PAM session stack for common services (login, sshd, gdm, sddm).

**PAM config fragment** (`/etc/pam.d/secrets-dispatcher`):
```
session optional pam_exec.so /usr/lib/secrets-dispatcher/pam-hook.sh open_session
```

**Script behavior (open_session):**
```bash
# Called with PAM_USER set to the desktop user (e.g., "nb")
COMPANION="secrets-${PAM_USER}"
# Start companion's systemd --user instance
machinectl shell "${COMPANION}@" /usr/bin/systemctl --user start secrets-dispatcher.service
```

**Close session:** Decrements a reference count; stops companion when count hits 0.

**Alternative (cleaner):** A custom `pam_secrets_dispatcher.so` PAM module that directly manipulates the companion service without shell dependency. This is simpler to test and avoids machinectl process overhead on every login. The provisioning tool installs either approach.

**Confidence:** MEDIUM — pam_exec is reliable and documented; the machinectl approach is the same pattern used by several multi-user systemd service managers. The ordering constraint (pam_systemd.so must run first to establish the user's systemd instance) is a known footgun.

---

## Build Order (Dependency Graph)

The dependency graph below determines phase structure. Independent branches can be parallelized within a phase.

```
Level 0 (no dependencies):
  └─ D-Bus interface definition (IDL / Go types for system bus interface)
  └─ VT ioctl wrapper (internal/vt/ioctl.go) — pure syscall layer

Level 1 (depends on Level 0):
  └─ System D-Bus server skeleton (internal/systemdbus/) — depends on interface types
  └─ VT manager (internal/vt/manager.go) — depends on ioctl wrapper

Level 2 (depends on Level 1):
  └─ approval.Manager integration with system D-Bus server
     — system bus method call → approval request → block goroutine
  └─ VT TUI (internal/vt/tui.go) — depends on VT manager + approval.Manager

Level 3 (depends on Level 2):
  └─ Secret access flow: system bus → approval → session bus → gopass-secret-service
  └─ GPG sign flow: system bus → approval → gpg exec → return signature
  └─ Signal emitter observer (approval.Manager observer → system bus signals)

Level 4 (depends on Level 3):
  └─ user-agent (cmd/user-agent) — depends on system bus interface + signal definitions
  └─ gpg-sign thin client modification — depends on system bus interface

Level 5 (depends on Level 4):
  └─ Notification bridge in user-agent — depends on signals from Level 3
  └─ PAM hook — depends on companion service being deployable (Level 3)
  └─ Provisioning tool — depends on knowing all config file paths/formats

Level 6 (integration):
  └─ VM E2E tests — depend on everything above
```

**Phase mapping recommendation:**
- Phase 1: Level 0 + Level 1 (interface, VT ioctl, system bus skeleton, VT manager)
- Phase 2: Level 2 + Level 3 (approval integration, full secret + GPG flows, VT TUI)
- Phase 3: Level 4 + Level 5 (user-agent, modified gpg-sign, PAM, provisioning)
- Phase 4: Level 6 (VM E2E tests)

Alternatively Phase 1 and Phase 2 can merge if the VT TUI is considered a prerequisite for Phase 2 testing.

---

## Existing Code: Reuse vs. Replace Matrix

| Package | v1.0 Role | v2.0 Status | Notes |
|---------|-----------|------------|-------|
| `internal/approval/` | Core approval pipeline | **REUSED** | Entirely unchanged; transport-agnostic |
| `internal/proxy/` (handlers) | Session bus proxy | **MODIFIED** | Same D-Bus handlers; different connection targets |
| `internal/proxy/manager.go` | Multi-socket watcher | **DROPPED** | No longer needed; single system bus service |
| `internal/gpgsign/commit.go` | Commit parser | **REUSED** | Unchanged |
| `internal/gpgsign/gpg.go` | GPG runner | **REUSED** | Unchanged; runs under companion user correctly |
| `internal/gpgsign/run.go` | Thin client transport | **REPLACED** | HTTP/WebSocket → system D-Bus call |
| `internal/gpgsign/daemon.go` | HTTP daemon client | **DROPPED** | Replaced by system D-Bus method on companion |
| `internal/gpgsign/setup.go` | git config setup | **REUSED** | Unchanged |
| `internal/notification/` | Desktop notifier | **MOVED** | Moves to user-agent; companion only emits D-Bus signals |
| `internal/api/` | HTTP/WebSocket/Web UI | **DROPPED** | Entirely removed in v2.0 |
| `internal/cli/` | HTTP CLI client | **REPLACED** | New CLI talks to VT or system D-Bus |
| `internal/config/` | Config loader | **EXTENDED** | New fields for companion mode |
| `internal/logging/` | Structured logging | **REUSED** | Unchanged |
| `internal/dbus/types.go` | Secret Service types | **REUSED** | Unchanged |
| `internal/service/install.go` | Systemd user service | **REPLACED** | New: companion service + policy files |
| `internal/testutil/mockservice.go` | Mock Secret Service | **REUSED** | Reuse for integration tests |
| `cmd/mock-secret-service/` | Integration test helper | **REUSED** | Private D-Bus integration tests |
| `main.go` | Subcommand router | **SUBSTANTIALLY MODIFIED** | New subcommands; drop HTTP serve |

---

## Key Architectural Decisions

### Decision 1: System D-Bus as the sole cross-UID interface

System D-Bus is the correct IPC mechanism for cross-UID communication on Linux. It provides:
- Kernel-enforced access control via D-Bus policy files (no ptrace bypass possible)
- Standard sender identity (GetConnectionUnixProcessID, GetConnectionUnixUser)
- D-Bus activation for on-demand startup
- Well-understood security model; no new IPC primitives needed

Alternative considered: Unix sockets between users. Rejected because: requires setuid bits or group permissions, no built-in sender identity, no activation, harder to write policy files for.

### Decision 2: Approval happens on VT; notification action buttons are supplementary

The VT is the single authoritative approval path. A user pressing [A] or [D] on VT 8 is cryptographically trustworthy — no desktop userspace process can inject input into that VT. The notification action buttons are a convenience: they call `Approve()` or `Deny()` on the system bus, which is also correct. But if the notification daemon is compromised, it can only send D-Bus messages that the policy allows — it cannot forge VT input.

This means: do not require the user to switch to VT 8 for every approval. The notification buttons work for routine approvals. The VT is available for high-stakes review (view full context, CLI history, etc.).

### Decision 3: gopass-secret-service is unchanged, on companion's session bus

gopass-secret-service currently runs on session bus with no authorization (trusts all session bus clients). Under the companion user, this is correct: the only processes on the companion's session bus are the companion's own processes. No desktop-user process can access that bus.

The dispatcher talks to gopass-secret-service via the companion's session bus using the same `internal/proxy` handler code that currently talks to the desktop user's gopass-secret-service. The direction in the proxy now goes: system bus request → approval → companion session bus call → gopass-secret-service.

### Decision 4: user-agent is a separate binary, not a library

The user-agent has a completely different lifecycle (desktop session), different D-Bus connections (session bus as server + system bus as client), and different dependencies (libnotify path through session bus). Embedding it in the main binary creates unnecessary coupling and makes PAM/systemd unit management harder. A separate `user-agent` binary runs as a systemd user service for the desktop user (`systemctl --user enable secrets-dispatcher-agent.service`).

### Decision 5: The proxy's direction inverts but the handler code is reused

v1.0 proxy: remote session bus (untrusted client) → local session bus (local Secret Service)
v2.0 proxy: system bus (incoming call) → companion session bus (gopass-secret-service)

The Secret Service method handlers (`service.go`, `collection.go`, `item.go`) implement the same D-Bus interface and can be reused. The change is in `proxy.go`'s `Connect()`: instead of connecting to a remote socket and the local session bus, it connects to the system bus (as server, exporting the dispatcher interface) and to the companion's session bus (as client, calling gopass-secret-service).

---

## Cross-User Data Flow: Sender Identity

When an app calls through the user-agent proxy chain:

```
App (PID 1234, nb) → session bus → user-agent → system bus → companion dispatcher
```

The companion dispatcher sees only the user-agent's PID on the system bus. To get the real requester's identity, the user-agent must include it in the method call payload:
- `caller_pid: uint32` — extracted from the session bus sender at the user-agent
- `caller_uid: uint32` — extracted from the session bus sender
- `caller_unit: string` — systemd unit if available

The user-agent resolves these from the session bus (`GetConnectionUnixProcessID`, `GetConnectionUnixUser`) before forwarding to the system bus. The companion trusts these values because only `nb` is permitted to call the dispatcher on the system bus (enforced by D-Bus policy), and `nb`'s agent is the only caller in practice.

This is a deliberate trust model: the desktop user's agent vouches for the requester's identity. If the agent is compromised, it can lie about PID/UID. This is acceptable — the VT TUI shows the information from the method payload; a compromised agent could show wrong info on the desktop notification but the VT display is the authoritative one, and it comes from process-verified information.

---

## Scalability Considerations

| Concern | Current (v1.0) | v2.0 Impact |
|---------|---------------|-------------|
| Concurrent secret requests | One approval blocks one D-Bus session bus call | One approval blocks one system bus goroutine; same pattern; concurrent requests each get their own goroutine via godbus |
| VT display with multiple pending | Web UI shows list | VT TUI shows list with navigation |
| Multiple desktop users | Not supported (single-user tool) | One companion per desktop user (`secrets-nb` for `nb`, `secrets-alice` for `alice`); provisioning tool creates per-user companions |
| gopass-secret-service cold start | Already running | Companion service starts gopass-secret-service on demand via systemd unit dependency |
| VT allocation failure | N/A | Fallback: daemon starts without VT (approval via CLI from a terminal the user opens); degrade gracefully |

---

## Integration Tests: Existing Pattern Extended

v1.0 integration tests use a private D-Bus daemon (mock-secret-service, dbus-daemon started with `--session`). This pattern extends cleanly:

- **Unit tests** (unchanged pattern): mock `approval.Manager` with interface; test VT TUI with fake `io.ReadWriter`
- **Integration tests** (new): start private system D-Bus daemon (`dbus-daemon --system --nofork --print-address --config-file=test/dbus-system.conf`); run companion dispatcher against it; run user-agent against both the private system bus and a private session bus; verify full request flow without real VT (use pty or pipe)
- **VM E2E tests** (new): real multi-user Linux VM; real PAM; real VT switching; full deployment test

The existing `cmd/mock-secret-service/` mock can serve as the gopass-secret-service stand-in for integration tests.

---

## Sources

### Primary (HIGH confidence)
- Direct codebase analysis: `main.go`, `internal/proxy/proxy.go`, `internal/proxy/service.go`, `internal/approval/manager.go`, `internal/gpgsign/run.go`, `internal/gpgsign/daemon.go`, `internal/notification/desktop.go`, `internal/proxy/senderinfo.go`
- gopass-secret-service codebase: `internal/service/service.go`, `cmd/gopass-secret-service/main.go`
- `.planning/codebase/ARCHITECTURE.md` — existing v1.0 architecture
- `.planning/PROJECT.md` — v2.0 milestone requirements and key decisions
- [Linux ioctl_vt(2) man page](https://man7.org/linux/man-pages/man2/ioctl_vt.2.html) — VT_SETMODE, VT_OPENQRY, VT_ACTIVATE semantics
- [ioctl_console(2) man page](https://man7.org/linux/man-pages/man2/ioctl_console.2.html) — console ioctl reference
- [godbus/dbus v5 — SystemBus()](https://pkg.go.dev/github.com/godbus/dbus/v5) — system bus connection API
- [D-Bus daemon policy documentation](https://dbus.freedesktop.org/doc/dbus-daemon.1.html) — policy file syntax

### Secondary (MEDIUM confidence)
- [How VT-switching works — dvdhrm.wordpress.com](https://dvdhrm.wordpress.com/2013/08/24/how-vt-switching-works/) — VT_SETMODE/VT_PROCESS explained
- [Bubble Tea framework](https://pkg.go.dev/github.com/charmbracelet/bubbletea) — TUI framework for VT display
- [pam_systemd documentation](https://www.freedesktop.org/software/systemd/man/latest/pam_systemd.html) — PAM session lifecycle and ordering
- [D-Bus ArchWiki](https://wiki.archlinux.org/title/D-Bus) — session vs. system bus, per-user session buses, XDG_RUNTIME_DIR/bus path

### Tertiary (LOW confidence)
- WebSearch results for companion-user systemd patterns — no authoritative single source; pattern inferred from multiple sources
- pam_exec ordering constraints — community forum posts (Arch BBS); no official documentation

---

*Research completed: 2026-02-25*
*Ready for roadmap: yes*
