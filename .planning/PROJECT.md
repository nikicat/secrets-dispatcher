# Secrets Dispatcher

## What This Is

A privilege-separated secret management and approval system for Linux. Runs secrets (gopass) and approval logic under a dedicated companion user, isolated from the desktop user's memory space. Provides kernel-enforced trusted I/O via a dedicated VT for approvals — the user sees exactly what they're approving on a display that no userspace process in their desktop session can tamper with. Regular user apps request secrets and signatures via system D-Bus; approvals happen exclusively in the privileged environment.

## Core Value

The user always knows exactly what they're approving before it happens, and no process in their desktop session can observe or tamper with the approval.

## Current Milestone: v2.0 Privilege Separation

**Goal:** Move secrets-dispatcher and gopass-secret-service to a companion user with kernel-enforced trusted I/O, eliminating userspace attack surface.

**Target features:**
- Companion user architecture (secrets-nb for user nb)
- System D-Bus as sole external interface (request secrets/signatures only)
- VT-based trusted I/O for all approvals (persistent dedicated VT)
- User-space agent in desktop session (Secret Service proxy + notifications)
- gopass-secret-service as backend via standard Secret Service D-Bus protocol
- PAM-triggered companion session lifecycle
- Provisioning tool for companion user setup
- Three-layer testing: unit mocks, private D-Bus integration, VM E2E

## Requirements

### Validated

- ✓ Approval request pipeline with pending/approve/deny/expire/cancel flow — existing
- ✓ REST API with auth for request management — v1.0 (dropping in v2.0)
- ✓ WebSocket real-time event propagation — v1.0 (dropping in v2.0)
- ✓ Web UI for viewing and approving requests — v1.0 (dropping in v2.0)
- ✓ CLI for viewing and approving requests — existing
- ✓ Desktop notifications for incoming requests — existing
- ✓ Observer pattern for event subscribers — existing
- ✓ GPG signing requests flow through approval pipeline as `gpg_sign` request type — v1.0
- ✓ `gpg-sign` thin client intercepts `gpg.program`, sends commit data to daemon, blocks until approval — v1.0
- ✓ Signing context shows repo name, commit message, author/committer, changed files, key ID — v1.0
- ✓ Daemon calls real `gpg` after approval, returns signature to thin client — v1.0
- ✓ WebSocket delivers approval result and signature to thin client — v1.0 (replacing with D-Bus in v2.0)
- ✓ Web UI displays signing requests distinctly from secret access requests — v1.0 (dropping in v2.0)
- ✓ CLI displays signing request context in list/show/history — v1.0
- ✓ Desktop notifications with commit summary for signing requests — v1.0
- ✓ Session/client identity shown for parallel session disambiguation — v1.0
- ✓ Error propagation: daemon unreachable, GPG failures, request expiry — v1.0

### Active

- [ ] Companion user architecture with OS-level memory/filesystem isolation
- [ ] System D-Bus interface exposing only secret requests and signature requests
- [ ] VT-based trusted I/O: persistent dedicated VT with TUI for approvals
- [ ] VT displays rich approval context (secret path, requester PID/UID, or commit details)
- [ ] VT input isolation: VT_SETMODE to prevent unauthorized VT switching during approval
- [ ] CLI available on the privileged VT for history, rules, admin operations
- [ ] gopass-secret-service as backend via standard freedesktop Secret Service D-Bus protocol
- [ ] User-space agent: claims org.freedesktop.secrets on desktop session bus, proxies to system D-Bus
- [ ] User-space agent: subscribes to system D-Bus signals, shows desktop notifications
- [ ] PAM hook to start companion session on regular user login
- [ ] Companion session lifecycle: starts with store unlock, closes when all regular sessions end or on explicit lock
- [ ] Provisioning tool: creates companion user, home dir, systemd units, D-Bus policy files
- [ ] GPG thin client uses system D-Bus instead of Unix socket
- [ ] Drop HTTP/REST API, WebSocket, and Web UI
- [ ] Unit tests with interface mocks for VT, D-Bus, Secret Service
- [ ] Integration tests with private D-Bus daemon (runs as regular user)
- [ ] VM E2E tests validating real multi-user, VT, PAM, systemd deployment

### Out of Scope

- polkit integration — secrets-dispatcher owns all authorization/approval, D-Bus policy handles connection gating
- Handling the GPG private key directly — gpg-agent manages key protection
- Non-git GPG signing — only git commit signing (tags can be added later)
- SSH commit signing — staying with GPG signatures
- Policy-based auto-approval — undermines human-in-the-loop model
- Full diff content display — payload explosion, rendering complexity
- Graphical UI for approvals — VT TUI is the trusted display; GUI approvals are inherently untrusted

## Context

Shipped v1.0 with 12,520 LOC Go + Svelte.
v1.0 tech stack: Go, D-Bus (godbus), Svelte 5, WebSocket, GPG.
v2.0 drops Svelte/WebSocket/HTTP in favor of system D-Bus + VT TUI.

Related project: ~/src/gopass-secret-service — Go daemon implementing full freedesktop Secret Service D-Bus API over gopass. Currently runs on session bus with no authorization (trusts all session bus clients). In v2.0 it runs under the companion user where trust-everything is correct by isolation.

Threat model: any process running as the desktop user can ptrace or read /proc/pid/mem of other same-user processes. Moving secrets to a companion user with a separate UID provides OS-enforced memory and filesystem isolation. VT-based trusted I/O ensures the approval display and input never transit the desktop user's compositor or session processes.

Architecture:
```
Desktop user (nb)                  Companion user (secrets-nb)
├─ Compositor, apps                ├─ Session D-Bus (private)
├─ Session D-Bus                   │  ├─ gopass-secret-service (Secret Service API)
│  └─ user-agent                   │  └─ secrets-dispatcher (approval + rules)
│     ├─ org.freedesktop.secrets   ├─ secrets-dispatcher on system D-Bus (requests only)
│     │  (proxy → system D-Bus)    ├─ VT 8 (persistent TUI + CLI)
│     └─ notification listener     └─ GPG keyring, gopass store
│
└─ apps → system D-Bus → secrets-dispatcher → approval on VT → Secret Service → gopass
```

## Constraints

- **Protocol**: Must produce valid GPG/PGP signatures that git accepts
- **Compatibility**: D-Bus Secret Service spec requires org.freedesktop.secrets on session bus — user-agent proxies this
- **VT isolation**: Approval display and input must never transit desktop user's memory space
- **Dependency**: Companion session must be running for secret access and signing
- **Testing**: All three layers (unit, integration with private D-Bus, VM E2E) required
- **Backward compat**: GPG signing thin client interface changes (Unix socket → system D-Bus)

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Custom `gpg.program` over pinentry wrapper | Full access to commit context at the git-gpg boundary; avoids Assuan protocol interception | ✓ Good — clean separation, rich context display |
| Daemon calls gpg, not the CLI client | CLI is a thin pipe; daemon owns signing flow and gpg interaction | ✓ Good — enables server-side error handling and signature caching |
| New request type, not new approval system | Reuses existing approval manager, observers | ✓ Good — minimal new code, consistent UX |
| GPGRunner interface for testability | Enables unit test mocking without real gpg binary | ✓ Good — fast tests, no external deps |
| Companion user, not system user | Needs a real session (session D-Bus, systemd --user). Per-regular-user isolation. Not a singleton system service. | — Pending |
| VT for trusted I/O, not Wayland secure surface | Wayland has no protocol for privileged surfaces; compositor runs as desktop user and sees all input. VT is the only kernel-enforced isolated I/O path on Linux. | — Pending |
| No polkit | secrets-dispatcher owns all auth/approval logic. polkit can't display rich context. D-Bus policy files gate connections. | — Pending |
| System D-Bus only, drop HTTP/WS/Web UI | Minimal attack surface. No cross-user HTTP. All approval on VT. Web UI is untrusted by definition. | — Pending |
| Standard Secret Service protocol between dispatcher and gopass-secret-service | Universal interface. Swap gopass backend without changing dispatcher. Both on companion user's session bus. | — Pending |
| Single user-space agent for proxy + notifications | Minimal footprint in desktop session. Claims org.freedesktop.secrets, proxies to system D-Bus. Also listens for notification signals. | — Pending |
| PAM hook for companion session lifecycle | Automatic start on login. No manual intervention. Session closes when all regular user sessions end. | — Pending |
| Three-layer testing strategy | Unit tests with mocks (fast, `go test`), integration with private D-Bus (protocol correctness), VM E2E (deployment validation). Covers logic, protocol, and deployment separately. | — Pending |

---
*Last updated: 2026-02-25 after v2.0 milestone initialization*
