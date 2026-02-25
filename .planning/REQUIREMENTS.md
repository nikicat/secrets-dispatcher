# Requirements: Secrets Dispatcher v2.0

**Defined:** 2026-02-25
**Core Value:** The user always knows exactly what they're approving before it happens, and no process in their desktop session can observe or tamper with the approval.

## v1 Requirements

Requirements for v2.0 milestone. Each maps to roadmap phases.

### Companion User

- [x] **COMP-01**: Companion user (secrets-nb) exists as a real user with separate UID and 0700 home directory
- [x] **COMP-02**: gopass store and GPG keyring reside under companion user's home, inaccessible to desktop user
- [ ] **COMP-03**: Companion session starts automatically when desktop user logs in (PAM hook, fire-and-forget)
- [ ] **COMP-04**: Companion session stops when all desktop user sessions end
- [x] **COMP-05**: systemd linger enabled for companion user so systemd --user persists
- [ ] **COMP-06**: User can explicitly lock the store via Lock() D-Bus method (clears gpg-agent cache)
- [ ] **COMP-07**: User can explicitly unlock the store via Unlock() D-Bus method

### System D-Bus Interface

- [x] **DBUS-01**: Daemon registers on system bus, accepting requests from desktop user UID only
- [x] **DBUS-02**: D-Bus policy file gates access: companion user owns name, desktop user can call methods
- [ ] **DBUS-03**: Dispatcher exposes standard freedesktop Secret Service D-Bus protocol on system bus
- [ ] **DBUS-04**: GPG signing uses existing D-Bus protocol if available (research needed), otherwise custom interface
- [ ] **DBUS-05**: System D-Bus signals restricted to requesting user's processes only (D-Bus policy or unicast)
- [ ] **DBUS-06**: Request expiry/timeout enforced (same as v1.0 approval manager)
- [ ] **DBUS-07**: Graceful error when companion not running — actionable message to desktop user

### VT Trusted I/O

- [ ] **VT-01**: Companion session owns a persistent dedicated VT
- [ ] **VT-02**: VT_SETMODE VT_PROCESS blocks unauthorized VT switching during active approval
- [ ] **VT-03**: TUI displays rich context for secret requests (path, requester PID/UID/process name, working directory)
- [ ] **VT-04**: TUI displays rich context for GPG signing (repo, message, author, files, key ID)
- [ ] **VT-05**: TUI displays requester parent process chain (up to 5 levels via /proc PPid)
- [ ] **VT-06**: Keyboard approve/deny input on VT (y/n)
- [ ] **VT-07**: VT CLI mode available for history, pending requests, admin commands
- [ ] **VT-08**: Store unlock prompt displayed on VT at companion session start
- [ ] **VT-09**: Crash recovery: VT returns to VT_AUTO mode if daemon dies (cleanup handlers + signal handlers)

### User-Space Agent

- [ ] **AGENT-01**: Agent claims org.freedesktop.secrets on desktop session bus
- [ ] **AGENT-02**: Agent proxies Secret Service API calls to system D-Bus with full object path mapping
- [ ] **AGENT-03**: Agent subscribes to system D-Bus signals and shows desktop notifications
- [ ] **AGENT-04**: Desktop notification body includes requester process name and PID
- [ ] **AGENT-05**: Agent handles companion-not-running gracefully with actionable notification

### GPG Signing

- [ ] **GPG-01**: GPG thin client (runs as desktop user, invoked by git as gpg.program) uses system D-Bus to reach companion
- [ ] **GPG-02**: Companion's gpg-agent configured with pinentry-tty pointing to approval VT
- [ ] **GPG-03**: Companion daemon signs via existing GPGRunner interface (calls real gpg, same as v1.0)

### Provisioning

- [x] **PROV-01**: Provisioning tool creates companion user, home dir, D-Bus policy, systemd units
- [x] **PROV-02**: Provisioning tool installs PAM hook
- [x] **PROV-03**: Provisioning tool configures gopass and GPG under companion user
- [x] **PROV-04**: `sd-provision --check` validates full deployment (pass/fail per component)
- [x] **PROV-05**: Provisioning tool is idempotent (safe to re-run)

### Testing

- [ ] **TEST-01**: Unit tests with interface mocks for VT, D-Bus connections, Secret Service client
- [ ] **TEST-02**: Integration tests with private D-Bus daemon (runs as regular user, no root)
- [ ] **TEST-03**: VM E2E tests validating real multi-user, VT, PAM, systemd deployment
- [x] **TEST-04**: CI-compatible: unit + integration run in CI; VM E2E gated by environment variable

### Infrastructure

- [x] **INFRA-01**: All daemon logging goes to systemd-journald (structured slog)
- [x] **INFRA-02**: HTTP/REST API, WebSocket, and Web UI are opt-in (disabled by default), enabled via config flag or CLI option

## Future Requirements

Deferred to post-v2.0. Tracked but not in current roadmap.

### Lifecycle Enhancements

- **LIFE-01**: pam-gnupg automatic GPG passphrase presetting from login password
- **LIFE-02**: Notification action "Review on VT" button triggers chvt (requires CAP_SYS_TTY_CONFIG)

### Signing Extensions

- **SIGN-01**: Non-git GPG signing (tag signing)
- **SIGN-02**: SSH commit signing support

### Operational

- **OPS-01**: Audit log rotation policy

## Out of Scope

| Feature | Reason |
|---------|--------|
| polkit integration | secrets-dispatcher owns all authorization/approval; polkit can't display rich context on trusted display |
| Graphical approval UI | Any GUI runs inside desktop compositor — inherently untrusted for approval |
| Wayland secure surface | No standardized protocol; compositor is in desktop user's memory space |
| Policy-based auto-approval | Undermines human-in-the-loop model |
| Bulk approve all pending | Forces individual evaluation per request |
| Full diff content in signing | Payload explosion; user reviews diff in IDE |
| Handling GPG private key directly | gpg-agent manages key protection |
| Running companion as root | Removes memory isolation guarantee |
| Separate companion password | Users won't maintain a second password |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| COMP-01 | Phase 4 | Complete |
| COMP-02 | Phase 4 | Complete |
| COMP-03 | Phase 6 | Pending |
| COMP-04 | Phase 6 | Pending |
| COMP-05 | Phase 4 | Complete |
| COMP-06 | Phase 7 | Pending |
| COMP-07 | Phase 7 | Pending |
| DBUS-01 | Phase 4 | Complete |
| DBUS-02 | Phase 4 | Complete |
| DBUS-03 | Phase 5 | Pending |
| DBUS-04 | Phase 5 | Pending |
| DBUS-05 | Phase 5 | Pending |
| DBUS-06 | Phase 5 | Pending |
| DBUS-07 | Phase 5 | Pending |
| VT-01 | Phase 5 | Pending |
| VT-02 | Phase 5 | Pending |
| VT-03 | Phase 5 | Pending |
| VT-04 | Phase 5 | Pending |
| VT-05 | Phase 5 | Pending |
| VT-06 | Phase 5 | Pending |
| VT-07 | Phase 7 | Pending |
| VT-08 | Phase 7 | Pending |
| VT-09 | Phase 5 | Pending |
| AGENT-01 | Phase 6 | Pending |
| AGENT-02 | Phase 6 | Pending |
| AGENT-03 | Phase 6 | Pending |
| AGENT-04 | Phase 6 | Pending |
| AGENT-05 | Phase 6 | Pending |
| GPG-01 | Phase 6 | Pending |
| GPG-02 | Phase 5 | Pending |
| GPG-03 | Phase 5 | Pending |
| PROV-01 | Phase 4 | Complete |
| PROV-02 | Phase 4 | Complete |
| PROV-03 | Phase 4 | Complete |
| PROV-04 | Phase 4 | Complete |
| PROV-05 | Phase 4 | Complete |
| TEST-01 | Phase 5 | Pending |
| TEST-02 | Phase 5 | Pending |
| TEST-03 | Phase 8 | Pending |
| TEST-04 | Phase 4 | Complete |
| INFRA-01 | Phase 4 | Complete |
| INFRA-02 | Phase 4 | Complete |

**Coverage:**
- v1 requirements: 42 total
- Mapped to phases: 42
- Unmapped: 0 ✓

---
*Requirements defined: 2026-02-25*
*Last updated: 2026-02-25 — traceability filled after roadmap creation*
