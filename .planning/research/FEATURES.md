# Feature Landscape

**Domain:** Linux privilege-separated secret management with VT trusted I/O
**Researched:** 2026-02-25
**Milestone:** v2.0 — Companion user architecture + VT trusted approval display
**Confidence:** HIGH (kernel VT API, D-Bus spec, systemd docs from official sources), MEDIUM (PAM session hook patterns from ArchWiki and upstream), LOW (novel combination — no prior art for this exact design)

---

## Context: What This Milestone Adds

v1.0 shipped a working approval proxy: gopass-secret-service + secrets-dispatcher run as the desktop user, approval via Web UI / CLI / desktop notifications. The attack surface is the desktop user's memory space — any process running as the same user can ptrace or read the daemon's memory, observe secrets in flight, or inject approvals.

v2.0 adds kernel-enforced isolation:

1. **Companion user isolation** — secrets and approval logic run under a separate UID (`secrets-nb`), OS-enforced memory and filesystem boundary. No desktop user process can ptrace or read companion memory.

2. **VT trusted I/O** — approval display and keyboard input live on a dedicated virtual terminal (VT8), owned by the companion user. The desktop compositor, Wayland/X11 stack, and all desktop user processes are blind to this VT. No userspace process in the desktop session can read or inject into it.

3. **System D-Bus as the sole external interface** — requests cross the UID boundary via the system D-Bus, with explicit policy files gating which UIDs may call which methods. This is the smallest possible attack surface.

The prior v1.0 HTTP/WebSocket/Web UI approval stack is dropped entirely.

---

## Table Stakes

Features whose absence makes the system insecure or unusable. Missing any of these and the privilege separation goal fails.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Companion user with separate UID | Foundation of all isolation guarantees. Without a distinct UID the OS cannot enforce memory/filesystem separation. | Low (usermod/useradd) | Naming convention: `secrets-<username>`, e.g. `secrets-nb`. Must be a real user (not a system user) to get a session D-Bus and home directory. Must NOT be in the desktop user's supplementary groups. |
| Companion user home directory with restricted permissions | gopass store and GPG keyring live here. Desktop user must have zero access. | Low | `~secrets-nb/` with mode 0700, owned by `secrets-nb`. Standard Linux filesystem permissions enforce this. |
| System D-Bus interface for secret requests | Only inter-user IPC channel. Without this, app clients have no way to request secrets from the companion user. | Med | Register `net.secretsdispatcher.Secrets1` on the system D-Bus. Policy file grants `send_destination` to the desktop user's UID only. |
| System D-Bus interface for GPG signing requests | Same isolation requirement as secrets — signing must cross the UID boundary through the narrowest possible channel. | Med | Reuses the same system D-Bus service; adds a separate method/interface for signing requests. |
| D-Bus policy file gating | Without explicit `allow` rules for the desktop UID and `deny` rules for all others, any local user can call the companion service. | Low | `/etc/dbus-1/system.d/secrets-dispatcher.conf`. Must allow only the target desktop user. Allow list by UID, not group. |
| VT-based trusted I/O (dedicated VT) | Approval display that no desktop process can observe or tamper with. Core security property of v2.0. | High | Companion user session owns VT8 (or another high-numbered VT). The companion's approval TUI runs there. Requires companion user session to hold the VT. |
| VT_SETMODE VT_PROCESS to resist switching | In VT_AUTO mode any process or user can switch away from the approval VT, breaking trust. VT_PROCESS allows the approval process to block switches during active approval. | Med | Use `VT_SETMODE` ioctl with `mode=VT_PROCESS`, `relsig=SIGUSR1`, `acqsig=SIGUSR2`. On release signal, deny or defer switch. On completion, restore VT_AUTO. |
| VT displays rich approval context | User must see what they are approving. Same "clear signing" principle from v1.0 but now on the trusted display. | Med | For secret requests: path, requester PID/UID/process name. For signing: repo, commit message, author, changed files, key ID. Must be unambiguous — the VT is the authoritative display. |
| Keyboard input for approve/deny on VT | User interacts on the trusted VT. If input goes through the compositor it can be intercepted. | Med | Terminal input on the raw VT — keyboard goes directly from kernel to the companion's tty fd. No Wayland/X11 input layer involved. |
| Request expiry / timeout | Companion cannot block forever if user walks away from the VT. | Low | Same timeout logic as v1.0 approval manager, now enforced on the VT TUI side. |
| gopass-secret-service as backend | Actual secret storage. Must run under companion user. Currently runs on session bus with no authz (trust-everything is correct in companion isolation). | Low (configuration change) | gopass-secret-service already exists at `~/src/gopass-secret-service`. Runs on companion's session D-Bus. No code changes needed — isolation is provided by the companion UID. |
| User-space agent claims `org.freedesktop.secrets` on desktop session bus | The Secret Service spec requires `org.freedesktop.secrets` on the session bus. Applications (e.g., libsecret clients) expect this. Without it, libsecret falls back to other providers or fails. | Med | Agent runs as desktop user. Claims the name. Proxies to system D-Bus. Any call it receives on the session bus it forwards to the companion via the system D-Bus method. |
| User-space agent delivers desktop notifications | Approval result notifications must reach the desktop user's notification daemon, which runs on the session D-Bus. Only the desktop user's session can send to its own notification daemon. | Low | Agent subscribes to system D-Bus signals from the companion. On `RequestCreated` / `RequestResolved` signals, sends `org.freedesktop.Notifications.Notify` on the session bus. |
| PAM session hook to start companion session | Without automatic start, the user must manually start the companion before using secrets. This is fragile and error-prone. GNOME Keyring sets the standard: unlock on login, invisible to the user. | High | `pam_exec.so` or custom PAM module in `/etc/pam.d/system-login`. On `session open` for user `nb`, `su`/`machinectl` or `systemd-run` starts companion session under `secrets-nb`. |
| Companion session lifecycle tied to desktop session | If companion stays running after the desktop user logs out, secrets are exposed without an owner watching the VT. | Med | PAM `session close` hook stops the companion. Also: session close when all regular user sessions end (logind session tracking). |
| Provisioning tool (`sd-provision`) | Without a repeatable setup tool, deployment is manual, error-prone, and undocumented. System administrators and the user themselves must be able to set up the companion cleanly. | Med | Creates companion user, home directory, copies gopass store config, installs systemd units, writes D-Bus policy file, sets permissions. Idempotent. Must be run as root. |
| GPG thin client uses system D-Bus | v1.0 thin client used Unix socket to reach the daemon running as the desktop user. v2.0 daemon is the companion — unreachable via desktop user's socket. | Low | Replace Unix socket call with system D-Bus method call to `net.secretsdispatcher.Secrets1`. |
| Unit tests with interface mocks for VT, D-Bus, Secret Service | Required for CI. VT and D-Bus cannot be used in `go test` without mocks. Without this, no fast feedback loop. | High | Define interfaces for VT operations (open, write, ioctl VT_SETMODE), D-Bus connections, and Secret Service. Mock all three. |
| Integration tests with private D-Bus daemon | Unit tests verify logic. Integration tests verify protocol correctness — that the system D-Bus method signatures, signals, and policy files work. Cannot rely on system D-Bus in CI. | High | `dbus-daemon --session --print-address` spawned per test, running as the test user. Tests connect to it. No root required. |
| VM E2E tests validating multi-user deployment | The companion user, PAM hooks, VT ownership, and D-Bus policy files can only be validated in a real multi-user environment. No substitutes. | High | Spawn a VM (qemu or nspawn), run provisioning tool, login as desktop user, verify secret requests flow through companion and appear on VT. |

---

## Differentiators

Features that make the system meaningfully better than the functional minimum. Not table stakes — but their absence would make v2.0 feel incomplete or operationally awkward.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| VT CLI available for history and admin | The approval TUI is the primary interface on the VT. But ops tasks (view history, check pending, manually approve/deny stuck requests) need a CLI. | Low | The companion's VT process runs a TUI with embedded CLI mode. Tab or command-mode switches to a readline-style prompt. History and admin commands available without switching away from the trusted VT. |
| VT displays requester process name and binary path | PID and UID are not human-readable. "python3 /home/nb/.venv/lib/site-packages/openai/_client.py" is immediately understandable. Helps detect unexpected requesters. | Med | Read `/proc/<pid>/exe` (symlink) and `/proc/<pid>/comm`. Requester PID is available from D-Bus sender credentials (kernel-provided, unforgeable). |
| VT displays requester parent process chain | A subprocess of a subprocess of a terminal is different from a direct application. The chain "terminal → python3 → openai_client" explains why the secret is being requested. | Med | Walk `/proc/<pid>/status` `PPid` field up to init (pid 1) or a session leader. Limit to 5 levels. Include comm name at each level. |
| Approval with session identity shown (for parallel sessions) | Multiple desktop user sessions (e.g., two Claude Code sessions) both requesting signing simultaneously. User must know which is which. | Low | D-Bus sender credentials give PID. Walk /proc to find tty or XDG_SESSION_ID. Show session number or tty name prominently. |
| Desktop notification includes requester identity | Companion signals `RequestCreated` with requester metadata. Agent shows it in the notification body: "Claude Code (pid 31245) wants access to openai/key". User can decide whether to switch to VT or deny. | Low | Signal payload includes process name, path, and pid. Agent formats notification body. |
| Notification action: switch to approval VT | Desktop notification has an action button "Review on VT". Clicking it switches the active VT to the approval VT (VT8). | Med | Agent sends `org.freedesktop.Notifications.Notify` with `actions = ["switch-vt", "Review on VT"]`. On action signal, send VT switch ioctl via `/dev/console` or `chvt`. Requires CAP_SYS_TTY_CONFIG or the right group. |
| Store unlock prompt on companion session start | When companion session starts (on PAM login), it needs the gopass store password / GPG passphrase to unlock. This must happen on the trusted VT, not on the desktop. | Med | Companion's session startup script runs the TUI immediately, which shows an unlock prompt on VT8. User switches to VT8, enters passphrase, switches back. Or: use pam-gnupg to preset the GPG agent passphrase from the login password. |
| Structured audit log for all requests and decisions | Who requested what, when, and what the decision was. This is the security event record. Required for any deployment where someone else might review access. | Low | Structured slog JSON to a file under companion's home directory. Rotated daily. Include: timestamp, requester PID/UID/process, secret path or signing context, decision, decision time. |
| Graceful handling of companion-not-running | If user requests a secret before logging in, or after an unexpected companion crash, the error should be clear and actionable, not a generic D-Bus error. | Low | Agent detects when system D-Bus name `net.secretsdispatcher.Secrets1` is not present. Returns `org.freedesktop.DBus.Error.ServiceUnknown` with a human-readable body. Show a desktop notification: "Secrets Dispatcher companion is not running." |
| `sd-provision --check` for deployment validation | After setup, user wants to verify everything is connected. Without a check command, debugging is manual. | Low | Provisioning tool `--check` mode: verify companion user exists, home dir permissions, D-Bus policy file, systemd units registered, PAM hook present. Print pass/fail for each. |
| Lock command (explicit store lock) | User wants to lock the companion's GPG agent and store before walking away, without logging out. | Low | System D-Bus method `Lock()` on `net.secretsdispatcher.Secrets1`. Companion clears GPG agent cache (send SIGHUP to gpg-agent or use `gpg-connect-agent reloadagent`). |

---

## Anti-Features

Things to deliberately NOT build in this milestone. Each has a clear reason and an alternative.

| Anti-Feature | Why Avoid | What to Do Instead |
|--------------|-----------|-------------------|
| polkit for authorization | polkit cannot display rich approval context on a trusted display. It routes through the compositor for graphical dialogs — exactly what we're replacing. secrets-dispatcher owns all approval logic. | D-Bus policy files gate connection (which UIDs can call the service). secrets-dispatcher itself makes the approval decision after the connection is allowed. |
| Graphical (GUI) approval UI | Any graphical UI runs inside the desktop user's compositor, which runs as the desktop user. The compositor can read any pixel and inject any input. A graphical approval is inherently untrusted. | VT-based TUI is the only trusted display on Linux. VT input bypasses the compositor entirely. |
| Wayland secure surface / secure input protocol | Wayland has no standardized protocol for privileged surfaces that survive compositor compromise. The compositor itself would need to be in the TCB. | VT is the kernel-enforced isolation boundary. The compositor is irrelevant once the VT is in use. |
| Persistent HTTP/REST API | Dropped in v2.0. Expands attack surface. Any process on the network (or the local machine) can reach it. No good reason to keep it when D-Bus + VT cover all use cases. | System D-Bus for external interface. VT CLI for local admin. |
| Web UI | Runs in the desktop user's browser (same UID). Cannot be the approval interface — it is not trusted. Also: dropped with the HTTP API. | VT TUI is the approval interface. Desktop notifications are the alert mechanism. |
| WebSocket | Infrastructure for the Web UI. Dropped with the Web UI. | D-Bus signals for event delivery. Agent subscribes and shows desktop notifications. |
| Policy-based auto-approval | Undermines the human-in-the-loop model. Any rule that approves without user interaction defeats the purpose of the gate. | If the user trusts a particular application unconditionally, they configure their system not to use secrets-dispatcher for that application. |
| Bulk approve all pending | Same as auto-approval — forces the user to evaluate each request individually. | One approval per request. Parallel requests are shown in a queue on the VT. |
| Full diff content in signing approval | Payload explosion. A 10,000-line diff makes the VT TUI unusable. Rendering a full diff in a terminal requires a pager and significant UI complexity. | Show filename list. User reviews diff in their IDE before approving. This is the v1.0 decision and remains correct. |
| Handling GPG private key directly in companion | The companion would need to hold the private key in memory and implement passphrase handling. This is the gpg-agent's job. Moving it to the companion does not add security — it just re-implements gpg-agent badly. | Companion's gpg-agent handles key protection. Dispatcher calls real `gpg` after approval. |
| SSH commit signing | Different mechanism, different protocol. Out of scope for v2.0; was out of scope for v1.0. | Document as a future milestone. |
| Non-git GPG signing | Tag signing has a different object format. Out of scope per PROJECT.md. | Document as a future milestone after v2.0 ships. |
| Running companion as root or a system service | A system service (no real session, no XDG_RUNTIME_DIR) cannot own a session D-Bus, cannot easily get a VT, and cannot run `systemd --user`. Root removes the memory isolation guarantee. | Companion is a real user with a real session. OS session management (logind) provides the isolation. |
| Storing secrets in system memory (shared with root) | System-level memory is accessible to root. The companion user model does not protect against root compromise, but it eliminates desktop-user-level attack surface. Clarify this in documentation. | Document the threat model boundary explicitly: companion user protects against other-desktop-processes, not against root/kernel compromise. |
| PAM module that asks for a separate companion password | Users will not remember or maintain a second password. Defeats the "invisible to the user" session start goal. | pam-gnupg pattern: preset the GPG agent passphrase from the login password. Or: companion store uses a key protected only by the companion's GPG agent, which is unlocked on session start automatically. |

---

## Feature Dependencies

```
Companion user (useradd)
  → Companion home directory (0700 permissions)
  → gopass store config under companion home
  → GPG keyring under companion home
  → Companion session D-Bus (XDG_RUNTIME_DIR for secrets-nb)

Companion session D-Bus
  → gopass-secret-service (backend, no code changes)
  → secrets-dispatcher companion process (approval + rules)

System D-Bus policy file
  → System D-Bus interface (RequestSecret, RequestSign methods)
  → Desktop user can call companion service

VT dedicated to companion
  → Companion user owns the VT (opens /dev/tty8, sets permissions)
  → VT TUI approval display (reads from tty8 fd)
  → VT_SETMODE VT_PROCESS (blocks unauthorized switches during approval)
  → VT keyboard input for approve/deny

PAM session hook
  → Companion session lifecycle (starts on nb login)
  → Store unlock prompt on companion VT
  → Companion session closes when all nb sessions end

User-space agent (desktop user process)
  → Claims org.freedesktop.secrets on session bus (proxies to system D-Bus)
  → Subscribes to system D-Bus signals (notification delivery)
  → Notification action "Review on VT" → chvt to companion VT

GPG thin client (system D-Bus)
  → Replaces v1.0 Unix socket with system D-Bus method call
  → Signing request flows to companion approval manager
  → Approval on VT → companion calls real gpg → returns signature

Provisioning tool (sd-provision)
  → All of the above: creates companion user, home dir, D-Bus policy, PAM hook, systemd units
  → --check validates the complete setup

Three-layer testing
  → Unit tests: VT interface mock, D-Bus connection mock, Secret Service mock
  → Integration tests: private D-Bus daemon (no root), protocol verification
  → VM E2E: real multi-user deployment, real PAM, real VT, real logind lifecycle
```

---

## Information Hierarchy for the VT Approval Display

Ordered by importance to the user's decision. Must be legible on an 80-column terminal without scrolling for items 1-5.

**For secret access requests:**
1. Request type: "SECRET ACCESS REQUEST"
2. Requester: process name + binary path (from /proc/pid/exe)
3. Secret path(s) being requested
4. Requester parent chain (who spawned the requesting process)
5. Session identity (which desktop session — for parallel sessions)
6. Request timestamp and expiry countdown
7. [y/n] prompt

**For GPG signing requests:**
1. Request type: "GPG SIGNING REQUEST"
2. Repository name and path
3. Commit message (truncated to ~120 chars if needed)
4. Changed files list (up to 20 files; "and N more" if over)
5. Author / committer
6. Key fingerprint
7. Session identity (which Claude Code session)
8. [y/n] prompt

---

## MVP Recommendation

Phases ordered by dependency and risk:

**Phase 1: Companion user architecture foundation**
- Provisioning tool: companion user, home dir, D-Bus policy file, systemd units
- Companion session D-Bus: gopass-secret-service running under companion user (configuration only)
- System D-Bus interface skeleton: service registration, method stubs, policy file
- `--check` validation command
- Unit test infrastructure: interface definitions for VT, D-Bus, Secret Service

**Phase 2: System D-Bus request/approval flow**
- Full system D-Bus interface: RequestSecret, RequestSign methods + RequestCreated/RequestResolved signals
- Companion approval manager wired to D-Bus interface (port from v1.0 approval manager)
- Desktop user-space agent: claims org.freedesktop.secrets on session bus, proxies to system D-Bus
- Desktop notifications via agent (RequestCreated signal → Notify)
- GPG thin client updated to use system D-Bus
- Integration tests: private D-Bus daemon, protocol correctness

**Phase 3: VT trusted I/O**
- Companion session acquires VT8 (opens /dev/tty8 from companion user context)
- VT TUI approval display: request context rendered on tty8
- VT_SETMODE VT_PROCESS: blocks VT switching during active approval
- Keyboard input on VT: y/n approval
- VT CLI mode: history, pending, admin commands
- Store unlock prompt on companion session start

**Phase 4: PAM lifecycle**
- PAM session hook: companion session starts on nb login
- Companion session close: on nb session close / last nb session ends
- Lifecycle integration tests: simulate login/logout via PAM test harness

**Phase 5: VM E2E validation**
- Full deployment in a VM: provisioning tool, PAM hook, companion session, VT, system D-Bus
- Validate secret request flow end-to-end as desktop user
- Validate GPG signing flow end-to-end
- Validate lifecycle: companion starts on login, closes on logout

**Defer to post-v2.0:**
- pam-gnupg / automatic GPG passphrase presetting from login password (complexity vs. reward unclear)
- Notification action "Review on VT" → chvt (requires CAP_SYS_TTY_CONFIG — privilege escalation concern)
- Non-git GPG signing, SSH signing
- Audit log rotation (basic logging is in scope; rotation is operational detail)

---

## Sources

**HIGH confidence (official docs / kernel interfaces):**
- [systemd Password Agents Specification](https://systemd.io/PASSWORD_AGENTS/) — inotify-based password agent protocol; pattern for system-level approval with multiple agents
- [ioctl_vt(2) — Linux manual page](https://man7.org/linux/man-pages/man2/ioctl_vt.2.html) — VT_SETMODE, VT_PROCESS, VT_AUTO, VT_RELDISP, relsig/acqsig
- [pam_systemd(8) — systemd](https://www.freedesktop.org/software/systemd/man/latest/pam_systemd.html) — session lifecycle, XDG_RUNTIME_DIR creation/cleanup, user@.service management
- [D-Bus Specification — freedesktop.org](https://dbus.freedesktop.org/doc/dbus-specification.html) — system bus policy, UID-based access control
- [Secret Service API Draft — freedesktop.org](https://specifications.freedesktop.org/secret-service/latest/) — org.freedesktop.secrets bus name ownership, session semantics
- [dbus-daemon(1)](https://linux.die.net/man/1/dbus-daemon) — policy file syntax: `<allow user=...>`, `<deny>`, `send_destination`, `own`
- [How VT-switching works — dvdhrm](https://dvdhrm.wordpress.com/2013/08/24/how-vt-switching-works/) — VT_SETMODE, VT_PROCESS mode, signal-based acknowledge protocol

**MEDIUM confidence (verified via multiple community/project sources):**
- [GNOME/Keyring — ArchWiki](https://wiki.archlinux.org/title/GNOME/Keyring) — PAM login unlock pattern, gnome-keyring-daemon --login lifecycle
- [pam-gnupg — GitHub](https://github.com/cruegge/pam-gnupg) — GPG passphrase presetting from login password via PAM; lifecycle notes (must come after pam_systemd)
- [systemd/User — ArchWiki](https://wiki.archlinux.org/title/Systemd/User) — user@.service, XDG_RUNTIME_DIR, session D-Bus lifecycle
- [OpenSSH Privilege Separation](http://www.citi.umich.edu/u/provos/ssh/privsep.html) — reference design for companion user pattern (unprivileged child via separate UID + chroot)
- [Bubbletea TUI framework](https://github.com/charmbracelet/bubbletea) — Go TUI library, v1.0 released 2024, production-quality for persistent terminal applications on a dedicated VT
- [tview — rivo/tview](https://github.com/rivo/tview) — alternative Go TUI with rich widgets; good alternative to bubbletea for approval display
- [op-secret-manager (2026)](https://bexelbie.com/2026/02/06/op-secret-manager) — contemporary example of SUID+service-account privilege separation for secret distribution

**LOW confidence (training data + contextual inference):**
- PAM session hook via `pam_exec.so` for cross-user session management — pattern is well-known but exact interaction with systemd --user for a different UID requires validation during Phase 4
- VT_PROCESS blocking behavior during ongoing approval: the kernel will send relsig on any switch request; the process decides whether to call VT_RELDISP — but edge cases (VT owner process crashes, signal handling under load) need validation during Phase 3

---

*Research completed: 2026-02-25*
*Feeds: v2.0 roadmap, phase planning*
