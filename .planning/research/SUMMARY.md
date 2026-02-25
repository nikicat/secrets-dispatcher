# Project Research Summary

**Project:** secrets-dispatcher v2.0 — Privilege Separation + VT Trusted I/O
**Domain:** Linux companion-user secret management with kernel-enforced trusted I/O
**Researched:** 2026-02-25
**Confidence:** HIGH (stack and architecture), MEDIUM (PAM/VT edge cases)

## Executive Summary

secrets-dispatcher v2.0 is a Linux-specific security hardening milestone that moves secret management and GPG signing approval out of the desktop user's process space and into a companion user (`secrets-nb`) isolated by a real OS UID boundary. The core insight: any software running as the same user as the daemon can read its memory, inject approvals, or observe secrets in flight. A separate UID makes this impossible at the kernel level — no ptrace, no `/proc/pid/mem` access across UIDs. The companion user owns the secret store, the GPG keyring, and a dedicated virtual terminal (VT8), which is the only trusted I/O channel: input on a VT bypasses the Wayland/X11 compositor entirely, giving a tamper-evident approval surface that no desktop process can observe or inject into.

The recommended architecture connects two worlds through a single, narrow interface: system D-Bus. The companion daemon registers `net.mowaka.SecretsDispatcher1` on the system bus; a small user-space agent running as the desktop user claims `org.freedesktop.secrets` on the session bus and proxies calls across the UID boundary. Desktop applications are unaware of the architecture change — they call the standard Secret Service API on the session bus exactly as before. GPG signing requests arrive via system D-Bus directly. All approval decisions are rendered on VT8 using a Bubble Tea v2 TUI, and optionally acknowledged via desktop notification action buttons for routine approvals. The HTTP/WebSocket/Web UI stack from v1.0 is dropped entirely.

The primary risks cluster around three areas: VT ownership (the ioctl-based VT_PROCESS mode has documented race conditions and can lock the display if the daemon crashes), PAM session lifecycle (the companion session must start asynchronously relative to PAM login — any blocking call in pam_exec locks all logins), and the cross-bus Secret Service proxy (D-Bus object paths are bus-relative; the user-agent must maintain a full path-mapping proxy, not a thin forwarder). These are all solvable with patterns established in the existing codebase and the research documents, but they require deliberate attention during implementation.

## Key Findings

### Recommended Stack

Four new Go dependencies are needed for v2.0. The v1.0 web stack (coder/websocket, Svelte, Deno, Playwright) is dropped entirely. All other existing dependencies are retained.

**Core technologies:**
- `charm.land/bubbletea/v2` v2.0.0 (released 2026-02-24): TUI framework for VT approval display — chosen over rivo/tview because Bubble Tea's Elm Architecture maps cleanly onto the 3-state approval machine (pending/approved/denied) and v2's "Cursed Renderer" supports custom `io.Writer` output (needed to write to `/dev/ttyN` instead of stdout)
- `github.com/msteinert/pam/v2` v2.1.0: PAM module development via CGo — needed if pam_exec proves insufficient for cross-user session lifecycle; start with pam_exec and escalate only if needed
- `github.com/coreos/go-systemd/v22` v22.7.0: logind API for companion session lifecycle — provides typed bindings to `org.freedesktop.login1` for tracking when all desktop user sessions end; using direct godbus would require hand-writing the same scaffolding
- `golang.org/x/sys` v0.41.0: upgrade from v0.27.0 — required by new dependencies; VT ioctl constants (`VT_ACTIVATE`, `VT_SETMODE`, etc.) are not in this package and must be defined inline from the stable Linux UAPI

**VT constants must be inlined:** `golang.org/x/sys/unix` does not define VT ioctl constants. They must be declared in an `internal/vt/consts.go` file using numeric literals from `<linux/vt.h>`. These values are stable Linux UAPI.

**Secret Service client:** Implemented directly on `godbus/dbus/v5` — no separate Secret Service library. Both available libraries (`go-libsecret`, `go-dbus-keyring`) are unmaintained.

See `.planning/research/STACK.md` for complete dependency set and build targets.

### Expected Features

The v2.0 milestone has a clear feature boundary. Every table-stakes item directly supports the core security model; removing any one of them breaks the isolation guarantee.

**Must have (table stakes):**
- Companion user (`secrets-nb`) with separate UID and 0700 home directory — foundation of all isolation
- System D-Bus interface with D-Bus policy file gating access by desktop user UID — the sole cross-UID IPC channel
- VT-based trusted I/O (VT8) with VT_SETMODE VT_PROCESS to resist unauthorized switching — the secure display
- VT TUI showing full approval context: requester process name, binary path, parent chain, secret path or commit details
- User-space agent claiming `org.freedesktop.secrets` on session bus, proxying to system D-Bus — transparent to apps
- PAM session hook to start/stop companion session on desktop login/logout — invisible to the user
- gopass-secret-service running on companion session bus (configuration change only — no code changes)
- GPG thin client updated from Unix socket/HTTP to system D-Bus method call
- Provisioning tool (`sd-provision`) with `--check` validation mode
- Three-layer test infrastructure: unit (mock interfaces), integration (private D-Bus daemon), VM E2E (real multi-user)

**Should have (differentiators):**
- Requester parent process chain displayed on VT (walk /proc PPid up to 5 levels)
- Desktop notification with requester identity in body ("Claude Code (pid 31245) wants openai/key")
- Structured audit log (slog JSON to companion home directory)
- `sd-provision --check` deployment validation
- `Lock()` method for explicit store lock without logout
- Graceful "companion not running" error with actionable desktop notification

**Defer to post-v2.0:**
- pam-gnupg automatic GPG passphrase presetting from login password
- Notification action button "Review on VT" -> chvt (requires CAP_SYS_TTY_CONFIG grant, privilege escalation concern)
- Non-git GPG signing (tag signing), SSH commit signing
- Audit log rotation

**Anti-features (deliberately excluded):**
- polkit authorization, graphical approval UI, Wayland secure surfaces — all route through the compositor, breaking the trust model
- HTTP/REST API, Web UI, WebSocket — dropped entirely; no reason to maintain them with D-Bus + VT covering all use cases
- Policy-based auto-approval, bulk approve — undermine human-in-the-loop model

See `.planning/research/FEATURES.md` for complete feature dependency graph and VT information hierarchy.

### Architecture Approach

The system splits cleanly across a UID boundary. The companion side owns all secrets and approval logic; the desktop side owns a thin agent that bridges the standard Secret Service API to the system bus. The existing `approval.Manager` is reused unchanged — it is transport-agnostic. The existing `internal/proxy` handlers are reused with direction inverted: v1.0 proxied remote session bus -> local session bus; v2.0 proxies system bus -> companion session bus. The HTTP/WebSocket/API layer is removed wholesale.

**Major components:**
1. **System D-Bus interface** (`internal/systemdbus/`) — registers `net.mowaka.SecretsDispatcher1` on system bus; accepts GetSecret/GPGSign/Approve/Deny from desktop user; calls `approval.Manager`; emits RequestCreated/RequestResolved signals
2. **VT manager + TUI** (`internal/vt/`) — opens `/dev/tty8`, sets VT_SETMODE VT_PROCESS, runs Bubble Tea TUI for approval display and keyboard input; hands fd to bubbletea via `tea.WithInput/WithOutput`
3. **User-space agent** (`cmd/user-agent/`) — runs as desktop user; claims `org.freedesktop.secrets` on session bus; maintains path-mapping proxy to system bus; listens to signals and calls notification daemon
4. **PAM hook** (`internal/pam/hook.go`) — fire-and-forget `pam_exec.so` call: starts companion session on `open_session`, decrements reference count and stops on `close_session`
5. **Provisioning tool** (`cmd/provision/`) — creates companion user, home dir, D-Bus policy file, systemd units, PAM config fragment; runs all gopass/GPG setup as `secrets-nb`; `--check` validates full setup

**Reused unchanged:** `internal/approval/`, `internal/gpgsign/commit.go`, `internal/gpgsign/gpg.go`, `internal/logging/`, `internal/proxy/session.go`, `internal/proxy/collection.go`, `internal/proxy/item.go`, `internal/testutil/mockservice.go`

**Dropped:** `internal/api/` (entire HTTP/WebSocket/Web UI layer), `internal/cli/` (HTTP client), `internal/gpgsign/daemon.go`

See `.planning/research/ARCHITECTURE.md` for full component inventory, data flow sequences, and system D-Bus interface definition.

### Critical Pitfalls

1. **D-Bus policy `<allow own>` missing for companion user** — the system bus defaults to deny-all; without an explicit `<policy user="secrets-nb"><allow own="net.mowaka.SecretsDispatcher1"/></policy>`, the daemon exits immediately on startup with no useful error. Write and test the policy file before writing daemon code. Use `<allow send_destination=...>` (not `send_interface`) for desktop user access rules.

2. **VT_SETMODE VT_PROCESS crash leaves VT frozen** — if the daemon dies while holding VT_PROCESS mode, the user cannot switch VTs (kernel waits for `VT_RELDISP` signal from a dead process). Prevention: install a `defer` cleanup calling `ioctl(VT_SETMODE, VT_AUTO)` on all exit paths, and a SIGUSR1 handler that calls `ioctl(VT_RELDISP, 1)`. Test with `kill -9` on the daemon.

3. **PAM `open_session` must not block** — a PAM module that waits for the companion session to be ready will hang `login`, `sshd`, and `sudo` for all users. The PAM hook must be fire-and-forget (`systemctl start --no-block`). All readiness logic belongs in the user-agent, which retries with backoff.

4. **Secret Service proxy cannot thin-forward D-Bus object paths** — the Secret Service protocol uses object paths as handles (session refs, collection refs). These paths are bus-relative and meaningless across bus boundaries. The user-agent must maintain a bidirectional path-mapping table and intercept all subsequent calls to proxied paths. Reuse the existing `internal/proxy` pattern.

5. **Companion user linger must be enabled** — without `loginctl enable-linger secrets-nb`, `systemd --user` for the companion user is torn down as soon as there are no logind sessions for that user (which is always, since the companion never logs in interactively). `/run/user/<uid>/` vanishes; the D-Bus socket disappears; the companion daemon cannot start. This is a required provisioning step.

6. **gpg-agent needs a TTY for pinentry** — the companion session has no DISPLAY or WAYLAND_DISPLAY. Without `GPG_TTY=/dev/tty8` and `pinentry-program /usr/bin/pinentry-tty` in gpg-agent.conf, GPG signing fails silently when the passphrase cache is cold.

See `.planning/research/PITFALLS.md` for all 18 documented pitfalls with detection and prevention details.

## Implications for Roadmap

Based on the dependency graph in ARCHITECTURE.md and the feature dependencies in FEATURES.md, the natural phase structure has 5 phases. FEATURES.md and ARCHITECTURE.md independently arrive at the same ordering, which is a strong signal the structure is correct.

### Phase 1: Foundation — Companion User + System D-Bus Skeleton

**Rationale:** Everything else depends on the companion user existing and the system D-Bus interface being defined. The interface definition (types, method signatures, policy file) is a zero-dependency work item that unblocks parallel work in later phases. The provisioning tool is needed to create the companion user before any companion-side code can be tested.

**Delivers:**
- Companion user (`secrets-nb`) created by provisioning tool with correct home dir permissions
- D-Bus policy file installed and verified: companion can own the name; desktop user can call methods
- System D-Bus interface skeleton: service registered, method stubs return errors, signals defined
- VT ioctl wrapper (`internal/vt/consts.go`, `internal/vt/ioctl.go`) — pure syscall layer with no business logic
- `sd-provision --check` validation command
- Interface definitions for VT, D-Bus, and Secret Service (needed for unit test mocks in Phase 2+)

**Avoids:** Pitfalls 1 (missing `<allow own>`), 2 (`send_interface` vs `send_destination`), 10 (dbus-broker compatibility), 18 (requester UID verification)

**Research flag:** Standard patterns — skip research-phase. D-Bus policy files, useradd, and systemd unit installation are well-documented.

### Phase 2: Core Request/Approval Flow

**Rationale:** The approval manager, system D-Bus wiring, and VT TUI form the core security-relevant logic. VT TUI is needed to test the approval flow interactively. The signal emitter (approval.Manager observer -> system D-Bus signals) is needed before the user-agent can be built in Phase 3. Secret access and GPG signing flows are the two primary use cases and should be validated together.

**Delivers:**
- Full system D-Bus interface: GetSecret, GPGSign, Approve, Deny, ListPending methods implemented
- `approval.Manager` wired to system D-Bus interface (port from v1.0 — goroutine blocks on approval)
- VT manager: opens `/dev/tty8`, sets VT_SETMODE VT_PROCESS, holds fd for lifetime
- VT TUI (Bubble Tea v2): renders pending requests (secret path + requester chain; commit details for GPG), keyboard y/n approval
- Signal emitter observer: approval.Manager events -> RequestCreated/RequestResolved signals on system bus
- Secret access flow end-to-end: system bus -> approval -> companion session bus -> gopass-secret-service
- GPG signing flow end-to-end: system bus -> approval -> gpg exec -> signature returned
- GPG companion session setup: `GPG_TTY=/dev/tty8`, pinentry-tty configured
- Integration tests with private D-Bus daemon verifying method signatures and signal delivery

**Uses:** `charm.land/bubbletea/v2`, `golang.org/x/sys` (ioctl), `godbus/dbus/v5` (system bus), reused `internal/approval/`, `internal/gpgsign/`

**Avoids:** Pitfalls 3 (VT_SETMODE crash -> VT frozen), 4 (VT_SETMODE race), 7 (gpg-agent no TTY), 12 (companion session D-Bus socket path hardcoded)

**Research flag:** VT_SETMODE edge cases (Pitfalls 3 and 4) warrant a focused research pass during Phase 2 planning. The documented race in Ubuntu Bug #290197 may require a workaround specific to the display manager in use. Verify Bubble Tea v2 custom `io.Writer` output on a raw VT fd early before building the full TUI.

### Phase 3: Desktop Integration — User Agent + PAM

**Rationale:** The user-agent depends on Phase 2's signal definitions and method interface being stable. PAM hook depends on the companion service being deployable. These are the two integration points between the companion and the desktop world and should be built together to validate the full round-trip.

**Delivers:**
- User-space agent (`cmd/user-agent/`): claims `org.freedesktop.secrets` on session bus with full path-mapping proxy to system bus
- Agent notification listener: subscribes to system bus signals, shows desktop notifications via `org.freedesktop.Notifications`; calls Approve/Deny on notification button action
- Signal startup race fixed: agent subscribes first, then queries ListPending to catch missed requests (Pitfall 13)
- GPG thin client updated: replaces Unix socket + HTTP client with system D-Bus GPGSign method call
- PAM hook: fire-and-forget `pam_exec.so` script; starts companion on open_session, reference-counts and stops on close_session
- Companion user linger enabled in provisioning tool
- Provisioning tool installs PAM fragment, user-agent systemd unit, GPG config for companion

**Avoids:** Pitfalls 5 (PAM timing), 6 (linger missing), 8 (object path proxy), 9 (GNUPGHOME ownership), 13 (notification race), 14 (PAM blocks login), 16 (gopass root ownership)

**Research flag:** PAM + systemd --user interaction for a different UID (Pitfalls 5 and 14) has sparse official documentation. The pam_exec + machinectl pattern for cross-user session start needs validation during planning. Budget time to test the exact PAM ordering constraints relative to pam_systemd.so.

### Phase 4: Hardening + Differentiators

**Rationale:** With the full request/approval/notification flow working, this phase adds the differentiating features and operational hardening. These are all independently implementable (no mutual dependencies) and can be prioritized by value.

**Delivers:**
- Requester parent process chain displayed on VT (walk /proc PPid, 5 levels)
- Desktop notification body includes requester identity
- Structured audit log (slog JSON, companion home dir)
- `Lock()` / `Unlock()` D-Bus methods wired to gpg-agent cache clear
- Graceful "companion not running" error handling in user-agent
- VT CLI mode for history and admin commands
- Store unlock prompt on companion session start (TUI shown on VT8 at startup if store is locked)

**Research flag:** Standard patterns — skip research-phase. All features here implement already-designed interfaces.

### Phase 5: VM E2E Validation

**Rationale:** The three-tier testing strategy requires a VM layer for anything involving real VT switching, real PAM hooks, and real multi-user logind sessions. This cannot be substituted by CI container tests (Pitfall 17). The VM E2E tests are the final gate before the v2.0 milestone is declared complete.

**Delivers:**
- QEMU VM test harness with full provisioning: companion user, PAM hook, D-Bus policy, systemd units
- E2E test: desktop login -> companion session starts -> secret request -> VT approval -> secret returned
- E2E test: git commit -> GPG signing request -> VT approval -> signed commit
- E2E test: desktop logout -> companion session stops
- Separate CI pipeline: unit + integration tests run in CI; VT E2E is a manual gate
- SKIP_VT_TESTS=1 environment gate for CI compatibility

**Research flag:** VM E2E test harness construction is worth a targeted research pass during Phase 5 planning. NixOS VM test framework or systemd-nspawn may be significantly simpler than raw QEMU.

### Phase Ordering Rationale

- Phase 1 before everything: companion user and D-Bus policy are prerequisites for all companion-side code to be testable
- Phase 2 before Phase 3: user-agent depends on signal interface and method signatures being stable; building them in parallel would require interface changes to propagate back
- Phase 3 before Phase 4: differentiators build on the baseline flow; no point adding requester chain display before the approval loop is proven
- Phase 5 last: VM E2E validates the complete integrated system; all components must be deployed together before the test is meaningful
- PAM hook and user-agent deliberately colocated in Phase 3: they are the two integration seams (companion <-> desktop) and should be validated together to catch lifecycle races early

### Research Flags

Needs focused research during planning:
- **Phase 2:** VT_SETMODE race conditions (Pitfall 4) and exact VT acquisition sequence on systems with GDM/SDDM; may need display-manager-specific workarounds
- **Phase 3:** PAM + `systemd --user` for a different UID — the pam_exec + machinectl cross-user pattern has sparse authoritative documentation; validate timing constraints and ordering relative to pam_systemd.so
- **Phase 5:** VM E2E harness selection — systemd-nspawn vs. QEMU vs. NixOS VM tests; significant effort difference between options

Standard patterns, skip research-phase:
- **Phase 1:** D-Bus policy files, useradd, systemd unit installation — all well-documented with official sources
- **Phase 4:** All differentiating features implement already-designed interfaces; no novel integration points

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All 4 new dependencies verified on pkg.go.dev with exact versions; VT constants verified against Linux kernel source; dropped dependencies confirmed |
| Features | HIGH | Table stakes and anti-features derived from Linux kernel docs (VT, D-Bus spec, Secret Service spec) and systemd official docs; novel combination has no prior art but the component-level claims are sound |
| Architecture | HIGH | Based on direct codebase analysis of existing `internal/proxy/`, `internal/approval/`, `internal/gpgsign/` plus official Linux VT and D-Bus documentation; component boundary decisions are well-motivated |
| Pitfalls | HIGH (documented), MEDIUM (novel combinations) | 14 of 18 pitfalls sourced from official docs or confirmed bug reports; 4 (PAM cross-user ordering, VT race mitigations) are from community sources and need implementation validation |

**Overall confidence:** HIGH for architectural approach; MEDIUM for PAM and VT edge case behavior in the specific companion-user-via-systemd-linger configuration

### Gaps to Address

- **PAM + systemd --user for companion user via linger:** The pam_exec + machinectl pattern for starting another user's systemd service is confirmed to work in principle but the exact timing constraints (when is pam_systemd.so done, when is `systemd --user` for the companion available) are not fully documented. Plan for iteration during Phase 3 implementation.

- **VT_SETMODE race with display manager:** The Ubuntu Bug #290197 race is documented but mitigations are workarounds. On systems with GDM or SDDM managing the same VT range, the companion daemon's VT_SETMODE may be reset. Accept as a documented limitation or test with the specific display manager in use.

- **Bubble Tea v2 on non-stdout tty:** The custom `io.Writer` output (`tea.WithOutput(tty)`) for a VT is confirmed in GitHub issue #860 but the v2.0 release is very new (2026-02-24). Verify early in Phase 2 that the rendering is correct on a raw VT fd before building the full approval TUI on top of it.

- **gopass-secret-service on companion session bus:** gopass-secret-service is assumed to work unchanged on the companion's session bus. This assumption should be verified in Phase 1 before building the proxy layer on top of it.

## Sources

### Primary (HIGH confidence)
- Linux VT ioctl: [man7.org/linux/man-pages/man2/ioctl_vt.2.html](https://man7.org/linux/man-pages/man2/ioctl_vt.2.html)
- Linux VT UAPI: [github.com/torvalds/linux/blob/master/include/uapi/linux/vt.h](https://github.com/torvalds/linux/blob/master/include/uapi/linux/vt.h)
- D-Bus policy spec: [dbus.freedesktop.org/doc/dbus-daemon.1.html](https://dbus.freedesktop.org/doc/dbus-daemon.1.html)
- D-Bus specification: [dbus.freedesktop.org/doc/dbus-specification.html](https://dbus.freedesktop.org/doc/dbus-specification.html)
- Secret Service API: [specifications.freedesktop.org/secret-service/latest/](https://specifications.freedesktop.org/secret-service/latest/)
- pam_systemd: [freedesktop.org/software/systemd/man/latest/pam_systemd.html](https://www.freedesktop.org/software/systemd/man/latest/pam_systemd.html)
- Bubble Tea v2.0.0: [pkg.go.dev/charm.land/bubbletea/v2](https://pkg.go.dev/charm.land/bubbletea/v2) — published 2026-02-24
- go-systemd v22.7.0: [pkg.go.dev/github.com/coreos/go-systemd/v22](https://pkg.go.dev/github.com/coreos/go-systemd/v22) — published 2026-01-27
- msteinert/pam v2.1.0: [github.com/msteinert/pam](https://github.com/msteinert/pam) — published 2025-05-13
- godbus/dbus v5.2.2: [github.com/godbus/dbus/releases](https://github.com/godbus/dbus/releases) — published 2024-12-29
- GnuPG Agent Options (pinentry): [gnupg.org/documentation/manuals/gnupg/Agent-Options.html](https://www.gnupg.org/documentation/manuals/gnupg/Agent-Options.html)
- dbus-broker deviations: [github.com/bus1/dbus-broker/wiki/Deviations](https://github.com/bus1/dbus-broker/wiki/Deviations)
- Ubuntu VT race bug: [bugs.launchpad.net/ubuntu/+source/linux/+bug/290197](https://bugs.launchpad.net/ubuntu/+source/linux/+bug/290197)
- systemd issue #2863 (PAM timing): [github.com/systemd/systemd/issues/2863](https://github.com/systemd/systemd/issues/2863)
- Arch Wiki systemd/User (linger): [wiki.archlinux.org/title/Systemd/User](https://wiki.archlinux.org/title/Systemd/User)
- Existing codebase: `internal/proxy/`, `internal/approval/`, `internal/gpgsign/`, `.planning/PROJECT.md`

### Secondary (MEDIUM confidence)
- How VT-switching works: [dvdhrm.wordpress.com/2013/08/24/how-vt-switching-works/](https://dvdhrm.wordpress.com/2013/08/24/how-vt-switching-works/)
- pam-gnupg: [github.com/cruegge/pam-gnupg](https://github.com/cruegge/pam-gnupg) — GPG passphrase presetting from login
- OpenSSH Privilege Separation: [citi.umich.edu/u/provos/ssh/privsep.html](http://www.citi.umich.edu/u/provos/ssh/privsep.html) — companion user pattern reference
- Bubble Tea issue #860: custom TTY output writer confirmed working on Linux
- pam_exec + machinectl cross-user pattern: Arch BBS, multiple community sources

### Tertiary (LOW confidence)
- VT_PROCESS edge cases on Wayland+GDM systems — inferred from Ubuntu bug and dvdhrm blog; real behavior with modern compositors unconfirmed
- Exact PAM ordering constraints for cross-user systemd --user start — community posts; needs validation during Phase 3

---
*Research completed: 2026-02-25*
*Ready for roadmap: yes*
