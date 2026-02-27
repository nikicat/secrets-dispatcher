# Phase 6: Desktop Integration - Research

**Researched:** 2026-02-27
**Domain:** D-Bus cross-user session bus access, Secret Service proxy, GPG thin client (D-Bus), PAM/systemd lifecycle
**Confidence:** MEDIUM-HIGH

## Summary

Phase 6 has three distinct technical problems: (1) migrating the companion daemon from system bus to session bus and enabling cross-user access from the desktop proxy, (2) implementing a Secret Service proxy that claims `org.freedesktop.secrets` on the desktop session bus and forwards to the companion's session bus, and (3) wiring PAM/systemd lifecycle so the companion starts/stops automatically with the desktop user's sessions.

The existing codebase already contains a working proxy implementation in `internal/proxy/` that handles the Secret Service API against the system bus. Phase 6 repoints that proxy at the companion's session bus socket instead. The key technical risk is cross-user D-Bus authentication: the companion's session bus uses `EXTERNAL` auth that verifies the connecting UID, so the desktop user proxy cannot connect directly without either (a) ACL/group permissions on the socket path, or (b) running a custom dbus-daemon configuration that allows cross-user connects. Option (a) — using a shared Unix group with `setfacl` or group-owned socket — is the standard Linux pattern for this and does not require daemon source changes.

The GPG thin client already exists in `internal/gpgsign/` but connects to the daemon over HTTP/WebSocket. Phase 6 replaces that transport with a D-Bus call to the companion's session bus (matching the CONTEXT.md decision that all user↔companion IPC goes through the companion's session bus). This is a narrowly scoped rewrite of `gpgsign/run.go`.

PAM lifecycle is simpler than it looks: linger is already enabled (Phase 4), and companion start via D-Bus socket activation eliminates explicit startup. Stop on last session requires the companion's systemd unit to have `PartOf=` or `BindsTo=` the desktop user's logind session scope — confirmed by systemd documentation.

**Primary recommendation:** Keep the existing proxy architecture unchanged; repoint `backendConn` at the companion's session bus. The cross-user access problem is solved by setting appropriate filesystem permissions on the companion's `/run/user/<companion-uid>/bus` socket using a shared Unix group or setfacl.

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

#### System bus → session bus migration
- Phase 5's system bus interface (RequestSecret, RequestSign) is **deprecated**
- Companion daemon migrates to its own session bus — all user↔companion IPC goes through the companion's session bus
- No system bus involvement for user↔companion communication
- D-Bus socket activation on the companion's session bus starts the daemon on first connection

#### Secret Service proxy
- Thin D-Bus forwarder running as desktop user, claims `org.freedesktop.secrets` on desktop session bus
- Forwards Secret Service API calls directly to companion's session bus (not via system bus)
- **Practical subset** of freedesktop Secret Service spec — what libsecret/Chrome/Firefox actually call (OpenSession plain, SearchItems, GetSecrets, CreateItem), not full spec
- **Backend-agnostic** — works with any Secret Service implementation on the companion side, not committed to gopass-secret-service specifically
- VT approval required for secret requests (same as GPG signing)
- Existing pipeline architecture (upstream → filter → downstream) handles both proxy and VT-approval cases via configuration

#### Companion session bus access
- Proxy connects to companion user's session bus directly
- Permissions on companion's session bus socket adjusted to allow the corresponding desktop user
- D-Bus socket activation handles first-connection transparently — no retry logic in proxy

#### GPG thin client
- Signing-only scope: handles --sign, --detach-sign, --status-fd, --armor, and key selection flags git passes during commit/tag signing
- Connects to companion's session bus (same path as Secret Service proxy)
- Stderr status line while blocking: "Waiting for approval on VT8..."
- Non-zero exit + stderr message on failure (companion not running, approval timeout, denial)
- Does not mimic gpg status-fd format — simple exit code + human-readable error

#### PAM lifecycle
- **Start**: Socket activation via systemd — companion starts on first D-Bus connection to its session bus. No explicit PAM start hook needed (linger already enabled in Phase 4)
- **Stop**: systemd-logind dependency — companion service stops automatically when desktop user's last session ends
- D-Bus socket activation eliminates startup race conditions entirely

#### Desktop notifications
- **Deferred** — not part of minimal flow
- Silent block for secret requests (desktop app waits until VT approval)
- Pending request awareness (monitoring companion state, notifying desktop user) is a separate concern from the proxy
- Companion-not-running: proxy returns D-Bus error immediately, no notification

### Claude's Discretion
- Companion session bus socket permission mechanism (group-based ACL, bind-mount, etc.)
- Secret Service method mapping details (which D-Bus methods/signals to implement)
- systemd-logind watcher implementation for companion stop
- D-Bus socket activation service file format
- How the pipeline configuration distinguishes proxy mode vs VT-approval mode
- Integration test strategy for cross-user session bus communication

### Deferred Ideas (OUT OF SCOPE)
- Desktop notifications for pending VT approval — later plan within Phase 6 or Phase 7
- Notification action button "Review on VT" triggering chvt — tracked as LIFE-02 in future requirements
- Broader gpg shim (verify, encrypt, decrypt) — only if other tools besides git need it
- Full Secret Service spec compliance (encrypted transport, prompts API, locking) — only if practical subset proves insufficient
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| COMP-03 | Companion session starts automatically when desktop user logs in | Socket activation via D-Bus service file on companion's session bus; no explicit PAM hook needed beyond linger (already enabled) |
| COMP-04 | Companion session stops when all desktop user sessions end | `PartOf=` or `BindsTo=` `session-c<N>.scope` or `graphical-session.target` in companion user unit — stops when last desktop session closes |
| AGENT-01 | Agent claims `org.freedesktop.secrets` on desktop session bus | Proxy already exists in `internal/proxy/`; Phase 6 adds a startup command `sd serve` that runs proxy against companion's session bus socket |
| AGENT-02 | Agent proxies Secret Service API calls to system D-Bus with full object path mapping | Existing `internal/proxy/service.go`, `collection.go`, `item.go` implement this; Phase 6 repoints backend connection to companion session bus |
| AGENT-03 | Agent subscribes to system D-Bus signals and shows desktop notifications | Deferred per CONTEXT.md — not in Phase 6 minimal flow |
| AGENT-04 | Desktop notification body includes requester process name and PID | Deferred per CONTEXT.md |
| AGENT-05 | Agent handles companion-not-running gracefully with actionable notification | Proxy returns D-Bus error immediately when companion not running (socket activation fails or times out); notification deferred |
| DBUS-05 | System D-Bus signals restricted to requesting user's processes only | Moot now — IPC migrated off system bus entirely; companion session bus is naturally scoped per-user |
| GPG-01 | GPG thin client uses system D-Bus to reach companion | Spec says "system D-Bus" but CONTEXT.md overrides: thin client connects to companion's session bus. Replaces HTTP/WebSocket transport in `gpgsign/run.go` |
</phase_requirements>

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/godbus/dbus/v5` | v5.2.2 (already in go.mod) | Session bus connection, method export, RequestName | Already used throughout codebase |
| `golang.org/x/sys` | v0.38.0 (already in go.mod) | `setfacl` equivalent: `unix.Acl*` or just `os.Chmod` + group membership | Already in go.mod |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `setfacl` (system tool, not Go) | system-provided | Set ACL on companion's session bus socket to allow desktop user | Called from provisioning step, not from Go at runtime |
| `github.com/godbus/dbus/v5/introspect` | bundled with godbus | Introspectable interface on the companion daemon's session bus exports | Companion daemon needs it when exporting methods |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Group-based socket ACL | bind-mount companion's `/run/user/<uid>/bus` into desktop user's namespace | ACL is simpler, no mount namespace changes |
| `setfacl` at provision time | Custom dbus-daemon config with `allow_anonymous` | `allow_anonymous` is a security downgrade; ACL is precise |
| godbus `Connect("unix:path=...")` | `dbus.ConnectSessionBus()` with UID switch | `Connect()` with explicit path is cleaner than UID manipulation |

## Architecture Patterns

### Recommended Project Structure

Phase 6 adds/modifies these files:
```
internal/
├── daemon/
│   ├── daemon.go           # Migrates from system bus → session bus
│   ├── activation.go       # Add session bus socket activation file content
│   └── templates.go        # Add sessiond unit template with BindsTo desktop sessions
├── gpgsign/
│   └── run.go              # Replace HTTP/WS transport with D-Bus call to companion session bus
│   └── dbusclient.go       # NEW: D-Bus client for companion session bus (replaces daemon.go HTTP client)
└── companion/
    ├── provision.go        # Add: setfacl call on socket, session bus socket activation file
    └── templates.go        # Add: session bus socket activation .service file template
cmd/
└── serve.go / daemon.go    # Wire proxy to connect to companion's session bus socket path
```

### Pattern 1: Session Bus Connection to Companion

**What:** The desktop proxy and GPG thin client both connect to the companion's session bus using the companion UID's `XDG_RUNTIME_DIR`.

**When to use:** Any time the desktop user process needs to call companion daemon methods.

```go
// Source: godbus documentation + XDG_RUNTIME_DIR convention
func companionBusAddress(companionUID int) string {
    // Standard path: systemd sets XDG_RUNTIME_DIR=/run/user/<uid>
    // The session bus socket is always at <XDG_RUNTIME_DIR>/bus
    return fmt.Sprintf("unix:path=/run/user/%d/bus", companionUID)
}

func connectToCompanion(companionUID int) (*dbus.Conn, error) {
    addr := companionBusAddress(companionUID)
    return dbus.Connect(addr)
    // This works IF the socket has ACL permissions for the desktop user.
}
```

**Confidence:** HIGH — `/run/user/<uid>/bus` is the standard systemd user bus socket path (verified by multiple sources including ArchWiki, godbus issues, and dbus-daemon docs).

### Pattern 2: Cross-User Socket ACL

**What:** At provision time, grant the desktop user read/write permission on the companion's session bus socket using `setfacl`.

**When to use:** One-time setup during `sd-provision` (Phase 4 provisioning flow, extended in Phase 6).

```bash
# Source: POSIX ACL standard, setfacl manpage
# Set at provision time; re-applied if needed at login time via systemd tmpfiles.d or ExecStartPost
setfacl -m u:${DESKTOP_USER}:rw /run/user/${COMPANION_UID}/bus
```

**Practical concern (LOW confidence — needs validation):** The socket at `/run/user/<companion-uid>/bus` is created fresh at each login by systemd. The ACL must be re-applied after each companion login. Options:
1. Add `ExecStartPost=setfacl -m u:${DESKTOP_USER}:rw /run/user/%i/bus` to the companion systemd unit
2. Use `tmpfiles.d` to set ACL on the path
3. Use a group: add both users to a shared group (e.g., `secrets-<desktopuser>-shared`) and set group ownership + mode on the socket path via dbus-daemon config

The cleanest approach is option 1 (ExecStartPost in the companion unit) because it runs every time the companion starts, making it idempotent.

**Alternative — group-based socket:** Set the companion's dbus-daemon to listen on a socket owned by the shared group. This requires a custom dbus-daemon config for the companion's session bus. More invasive.

**Recommendation:** ExecStartPost setfacl. Simple, precise, tested pattern per the quantum5.ca article.

### Pattern 3: Companion Daemon on Session Bus

**What:** Companion daemon exports its D-Bus interface on its own session bus (replacing the system bus registration from Phase 4-5).

**When to use:** New companion startup flow.

```go
// Source: godbus documentation
// Replace ConnectSystemBus() with ConnectSessionBus() in daemon/daemon.go
conn, err := dbus.ConnectSessionBus()
// OR if running headless with DBUS_SESSION_BUS_ADDRESS not set:
conn, err := dbus.Connect("unix:path=/run/user/" + companionUID + "/bus")
```

**Interface constants:** The `net.mowaka.SecretsDispatcher1` bus name, object path, and interface remain unchanged. Only the bus (system → session) changes.

**Session bus activation file:** Placed in `~/.local/share/dbus-1/services/` (companion's home):
```ini
[D-BUS Service]
Name=net.mowaka.SecretsDispatcher1
Exec=/usr/local/bin/secrets-dispatcher daemon
```

This is different from the system bus activation file in Phase 4 (`/usr/share/dbus-1/system-services/`). Session bus activation files live in the user's `XDG_DATA_HOME/dbus-1/services/`.

**Confidence:** MEDIUM — session bus activation file path is documented in dbus-daemon man page (`$XDG_DATA_HOME/dbus-1/services` or `~/.local/share/dbus-1/services`); the format is identical to system bus activation files.

### Pattern 4: D-Bus Socket Activation (Session Bus)

**What:** When a client connects to `net.mowaka.SecretsDispatcher1` on the companion's session bus and no service is running, dbus-daemon auto-starts it.

The D-Bus service activation file is placed in the companion's `~/.local/share/dbus-1/services/` (or `/usr/share/dbus-1/session-services/` for system-wide install). This is distinct from systemd socket activation. Phase 6 uses D-Bus service activation (not systemd socket activation).

```
# Installed to companion's XDG_DATA_HOME/dbus-1/services/
[D-BUS Service]
Name=net.mowaka.SecretsDispatcher1
Exec=/usr/local/bin/secrets-dispatcher daemon
```

**Confidence:** MEDIUM — D-Bus service activation for session bus is documented but less commonly implemented than system bus activation. Needs validation that the session bus daemon actually picks up user-level service files.

### Pattern 5: Companion Stop on Desktop Logout (COMP-04)

**What:** Companion user's `secrets-dispatcher-companion.service` depends on the desktop user's logind session being active, so it stops when the last desktop session ends.

**Key insight from research:** The `user@.service` for a user (e.g., `user@1001.service` for companion) "will survive as long as there is some session for that user, and will be killed as soon as the last session for the user is closed" — BUT only if linger is NOT enabled. Since Phase 4 enables linger for the companion user so that `user@<companion-uid>.service` persists without active sessions, we cannot rely on the companion's own user manager stopping automatically.

**Solution:** Make the companion's daemon service itself depend on the desktop user's logind session scope. The companion daemon service unit should include:
```ini
[Unit]
# Bind lifetime to the desktop user's graphical session
# session-c<N>.scope is too dynamic; use user-<DESKTOP_UID>.slice instead
PartOf=user-1000.slice
After=user-1000.slice

[Service]
ExecStart=/usr/local/bin/secrets-dispatcher daemon
```

**Actual mechanism (MEDIUM confidence — needs validation):** When the desktop user's `user@<desktop-uid>.service` stops (last session ends, no linger), systemd propagates the stop to all units in `user-<desktop-uid>.slice`. A service in a different user's context cannot directly bind to another user's slice. The correct mechanism is:

Option A: The companion daemon watches for a logind signal (`SessionRemoved` from `org.freedesktop.login1.Manager`) and exits when the desktop user has no active sessions.

Option B: A system service (running as root) monitors logind and stops the companion service when the desktop user logs out (`systemctl stop secrets-dispatcher-companion@<desktop-user>`). This is what `pam_exec.so` provides on logout — but PAM hooks are not guaranteed to run on all logout paths.

Option C: Use `systemd-logind`'s `linger` — since Phase 4 enables linger for the companion, the companion persists indefinitely. Phase 6 may need to disable linger on logout or use a different mechanism.

**Recommended approach:** The companion daemon subscribes to `org.freedesktop.login1.Manager.SessionRemoved` on the system bus and exits when the desktop user has no remaining sessions. This is self-contained and reliable.

**Confidence:** LOW for exact implementation — the systemd slice dependency across user boundaries is not well-documented. The logind signal subscription approach is more reliable and verified by how gnome-keyring handles similar lifecycle.

### Pattern 6: GPG Thin Client Migration (D-Bus transport)

**What:** Replace the HTTP/WebSocket transport in `gpgsign/run.go` with direct D-Bus calls to the companion's session bus.

```go
// Source: godbus documentation
// Before (Phase 5): HTTP POST + WebSocket to Unix socket
// After (Phase 6): D-Bus method call to companion session bus

conn, err := dbus.Connect(companionBusAddress(companionUID))
if err != nil {
    fmt.Fprintf(os.Stderr, "secrets-dispatcher: companion not running: %v\n", err)
    return 2
}
defer conn.Close()

obj := conn.Object("net.mowaka.SecretsDispatcher1", "/net/mowaka/SecretsDispatcher1")

// Blocking call — returns when VT approval completes
var sig, status []byte
fmt.Fprintf(os.Stderr, "secrets-dispatcher: Waiting for approval on VT8...\n")
err = obj.Call("net.mowaka.SecretsDispatcher1.RequestSign", 0,
    repoName, commitMsg, author, committer, keyID, changedFiles, string(commitBytes),
).Store(&sig, &status)
```

**Key design point:** The D-Bus call is blocking. `godbus` dispatches each method call in its own goroutine on the server side, and the client call blocks until the method returns. This is simpler than the WebSocket polling pattern currently in `run.go`.

**Companion UID lookup:** The thin client needs to know the companion UID to construct the session bus path. This can be looked up via `user.Lookup("secrets-" + os.Getenv("USER"))` at runtime.

**Confidence:** HIGH — D-Bus blocking call pattern is well-established; the existing `daemon/handler.go` already implements the server side.

### Anti-Patterns to Avoid

- **Retry loop on companion-not-running:** Per CONTEXT.md, proxy returns D-Bus error immediately if socket activation fails. No retry.
- **ANONYMOUS auth on companion bus:** The companion's session bus uses EXTERNAL auth by default. Do not add `<allow_anonymous>` — it would be a security downgrade.
- **UID switching in Go to connect:** The `syscall.Seteuid()` approach (found in godbus issue #246) works but is fragile in multithreaded programs (Go goroutines can run on different OS threads). The ACL-on-socket approach is safer and correct.
- **System bus for user↔companion:** CONTEXT.md explicitly deprecated this; do not use system bus.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Secret Service API proxy | Custom implementation | Existing `internal/proxy/` package | Already implemented and tested in Phase 1-5 |
| Session object path mapping | Custom path table | Existing `internal/proxy/session.go` | Session bidirectional map already exists |
| D-Bus connection pooling | Custom connection pool | Single persistent connection per process | D-Bus connections are cheap; one per process is standard |
| Companion UID lookup | Custom `/etc/passwd` parser | `os/user.Lookup()` | stdlib handles this |
| Logind session monitoring | Custom `/proc` polling | Subscribe to `org.freedesktop.login1.Manager.SessionRemoved` signal via godbus | Reliable signal vs polling |

**Key insight:** The proxy architecture in `internal/proxy/` is already production-quality. Phase 6 is primarily about rewiring connections (system bus → companion session bus) and adding a new startup mode, not reimplementing the proxy.

## Common Pitfalls

### Pitfall 1: Session Bus Socket Lifetime
**What goes wrong:** The ACL on `/run/user/<companion-uid>/bus` is lost when the companion user logs out and back in — systemd recreates the socket without the ACL.
**Why it happens:** `tmpfs` at `/run/user/<uid>/` is recreated on each login; ACLs are not persisted.
**How to avoid:** Apply ACL in `ExecStartPost=` of the companion's systemd unit, not just at provision time.
**Warning signs:** Proxy gets `Permission denied` connecting to companion bus after a companion restart.

### Pitfall 2: D-Bus Service Activation File Location
**What goes wrong:** Placing the D-Bus session bus activation file at `/usr/share/dbus-1/system-services/` (system bus location) instead of the session bus location.
**Why it happens:** The paths are similar; Phase 4 installed a system bus activation file and it's easy to copy the pattern.
**How to avoid:** Session bus activation files go in `$XDG_DATA_HOME/dbus-1/services/` (typically `~/.local/share/dbus-1/services/` for the companion user) or `/usr/share/dbus-1/session-services/`.
**Warning signs:** Connecting to companion session bus succeeds but name activation fails silently.

### Pitfall 3: Companion UID Needed at Runtime
**What goes wrong:** The proxy and GPG thin client need the companion UID to construct the session bus address, but it's not stored anywhere accessible.
**Why it happens:** The provisioning tool creates the user but doesn't persist the UID in a way the proxy can read.
**How to avoid:** The proxy can call `user.Lookup("secrets-" + currentUser)` at startup. Alternatively, store the companion UID in a config file during provisioning.
**Warning signs:** Proxy cannot find companion bus address.

### Pitfall 4: Linger Interaction with COMP-04
**What goes wrong:** Linger is enabled for the companion user (Phase 4), so the companion's `user@<uid>.service` persists forever — even after the desktop user logs out. COMP-04 (companion stops when desktop user sessions end) appears to work against linger.
**Why it happens:** Linger is needed so the companion's systemd instance persists between sessions; but it also means the companion daemon itself won't stop on desktop logout.
**How to avoid:** The companion daemon itself must watch for the desktop user's sessions ending (via logind signals) and self-terminate. The companion's `user@.service` can remain alive (for future reconnect), but the daemon process exits.
**Warning signs:** After desktop logout, the companion daemon is still visible in `ps` or `systemctl --user status` under the companion user.

### Pitfall 5: godbus EXTERNAL Auth Across UIDs
**What goes wrong:** The desktop user process tries to connect to the companion's session bus but receives `Authentication failed` because EXTERNAL auth sends the connecting process's UID, which doesn't match the companion's UID that owns the bus.
**Why it happens:** dbus-daemon verifies the connecting UID matches the bus owner's UID for session buses (default policy).
**How to avoid:** The companion's session bus configuration must explicitly allow the desktop user's UID to connect. This requires either:
  (a) A custom dbus-daemon config with `<policy user="<desktop-uid>"><allow />...</policy>` — but this requires running dbus-daemon with a custom config for the companion's session
  (b) If using dbus-broker (modern replacement for dbus-daemon on most distros): check if it has different default policies
  (c) Accept that the desktop user needs a socket-level permission AND must be allowed by the bus policy
**Warning signs:** `dbus.Connect()` returns immediately with auth error.

**This is the highest-risk unknown in Phase 6.** The exact mechanism for allowing cross-user session bus connections needs empirical testing.

### Pitfall 6: git's gpg.program invocation
**What goes wrong:** git invokes the binary set as `gpg.program` with specific flags. If the binary exits non-zero for any reason, git aborts the commit with a confusing error.
**Why it happens:** git passes `--status-fd=2 -bsau <keyID>` and reads stdout for the signature. Any deviations cause commit failure.
**How to avoid:** The thin client already handles this correctly in `gpgsign/run.go`. The migration to D-Bus transport only changes the IPC mechanism, not the git protocol interface.
**Warning signs:** `git commit -S` fails with `gpg: signing failed`.

## Code Examples

### Connecting to Companion Session Bus

```go
// Source: godbus documentation, standard XDG_RUNTIME_DIR convention
import (
    "fmt"
    "os/user"
    "github.com/godbus/dbus/v5"
)

func connectCompanionBus(desktopUsername string) (*dbus.Conn, error) {
    companionName := "secrets-" + desktopUsername
    u, err := user.Lookup(companionName)
    if err != nil {
        return nil, fmt.Errorf("lookup companion user %q: %w", companionName, err)
    }
    addr := fmt.Sprintf("unix:path=/run/user/%s/bus", u.Uid)
    return dbus.Connect(addr)
}
```

### ExecStartPost ACL in Companion Systemd Unit

```ini
[Unit]
Description=Secrets Dispatcher Companion Daemon
After=dbus.service
Requires=dbus.service

[Service]
Type=notify
ExecStart=/usr/local/bin/secrets-dispatcher daemon
ExecStartPost=/usr/bin/setfacl -m u:{{.DesktopUID}}:rw /run/user/{{.CompanionUID}}/bus
Environment=HOME={{.CompanionHome}}
Environment=XDG_RUNTIME_DIR=/run/user/{{.CompanionUID}}
```

### Logind Session Watch (Companion Self-Stop)

```go
// Source: godbus signal subscription pattern
// Companion daemon watches for desktop user session removal
func watchDesktopSessions(ctx context.Context, conn *dbus.Conn, desktopUID int, onLogout func()) {
    conn.AddMatchSignal(
        dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
        dbus.WithMatchMember("SessionRemoved"),
    )
    ch := make(chan *dbus.Signal, 10)
    conn.Signal(ch)

    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case sig := <-ch:
                if sig.Name == "org.freedesktop.login1.Manager.SessionRemoved" {
                    // Check if desktop user has any remaining sessions
                    if !desktopUserHasSessions(conn, desktopUID) {
                        onLogout()
                        return
                    }
                }
            }
        }
    }()
}
```

### D-Bus Session Bus Activation File (Companion User)

```ini
# Install to: ~/.local/share/dbus-1/services/net.mowaka.SecretsDispatcher1.service
# (companion user's XDG_DATA_HOME)
[D-BUS Service]
Name=net.mowaka.SecretsDispatcher1
Exec=/usr/local/bin/secrets-dispatcher daemon
```

### Secret Service Method Subset Required

Based on the official spec and what libsecret/Chrome/Firefox call:

| Method | Interface | Required | Notes |
|--------|-----------|----------|-------|
| `OpenSession(algorithm, input)` | Service | YES | Chrome/Firefox/libsecret all start here |
| `SearchItems(attributes)` | Service | YES | Used by libsecret for lookup |
| `GetSecrets(items, session)` | Service | YES | Bulk secret fetch |
| `Unlock(objects)` | Service | YES | Required even if always returns unlocked |
| `CreateItem(properties, secret, replace)` | Collection | YES | For password save flows |
| `SearchItems(attributes)` | Collection | YES | Per-collection search |
| `GetSecret(session)` | Item | YES | Single item fetch |
| `Lock(objects)` | Service | NO | Can stub returning empty |
| `CreateCollection(properties, alias)` | Service | NO | Can stub |
| `Prompt.*` | Prompt | NO | Can return "/" (no prompt needed) |

Existing `internal/proxy/service.go`, `collection.go`, `item.go` already implement the required methods. Phase 6 doesn't need to add new Secret Service methods — only repoint the backend connection.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| System bus for companion IPC | Session bus (Phase 6) | Phase 6 | Eliminates need for system bus policy file for user↔companion; better isolation |
| HTTP/WebSocket transport for GPG | D-Bus session bus call (Phase 6) | Phase 6 | Unified transport: one IPC mechanism for all companion calls |
| D-Bus system activation file | Session bus activation file (user-level) | Phase 6 | Companion starts on demand via D-Bus session bus activation |
| gpgsign/daemon.go (HTTP client) | New dbusclient.go (D-Bus client) | Phase 6 | `DaemonClient` struct replaced; `run.go` rewritten for D-Bus |

**Deprecated/outdated in Phase 6:**
- `internal/daemon/activation.go`: Contains system bus activation file content — needs replacement or supplement with session bus variant
- `companion/templates.go` `pamConfigTemplate`: PAM hook to start companion — may become no-op since socket activation replaces explicit start
- `gpgsign/daemon.go`: The HTTP/WebSocket `DaemonClient` — replaced by D-Bus client

## Open Questions

1. **Cross-user dbus-daemon policy enforcement**
   - What we know: dbus-daemon EXTERNAL auth checks that the connecting process UID matches an allowed user; by default only the same UID as the bus owner is allowed
   - What's unclear: Can the standard session dbus-daemon be configured to allow a different specific UID without running a completely custom config? Or does the companion need to run its own dbus-daemon with a custom config file?
   - Recommendation: Test empirically. If standard session bus doesn't support cross-user policy, the companion runs its own dbus-daemon with `--config-file=<custom>` that includes `<policy user="<desktop-uid>">` rules. This is the most likely path given EXTERNAL auth constraints.

2. **D-Bus session activation file pickup**
   - What we know: Files in `$XDG_DATA_HOME/dbus-1/services/` are picked up by the session bus daemon
   - What's unclear: Does the systemd-managed session bus (`dbus.socket`/`dbus.service`) look in the user's home? Or does it only use `/usr/share/dbus-1/session-services/`?
   - Recommendation: Test both locations. If user-home location doesn't work, install to `/usr/share/dbus-1/session-services/` (requires root at provision time).

3. **Companion self-stop mechanism for COMP-04**
   - What we know: Linger keeps companion `user@.service` alive; systemd slice dependency across users is not straightforward
   - What's unclear: Whether `PartOf=user-<desktop-uid>.slice` in the companion's unit file actually works cross-user
   - Recommendation: Use logind signal subscription (SessionRemoved) in the daemon itself. This is self-contained and doesn't require systemd cross-user dependencies.

4. **DBUS-05 disposition**
   - What we know: DBUS-05 says "system D-Bus signals restricted to requesting user's processes only" — but the system bus is deprecated for user↔companion IPC in Phase 6
   - What's unclear: Does DBUS-05 still need implementation, or is it vacuously satisfied by removing system bus usage?
   - Recommendation: Mark DBUS-05 as satisfied-by-design since no signals are emitted on the system bus in Phase 6.

## Sources

### Primary (HIGH confidence)
- [godbus/dbus v5 pkg.go.dev](https://pkg.go.dev/github.com/godbus/dbus/v5) — Connect(), ExportSubtree(), ExportMethodTable() API
- [freedesktop Secret Service API](https://specifications.freedesktop.org/secret-service/latest-single/) — Method signatures for Service, Collection, Item interfaces
- [org.freedesktop.Secret.Service methods](https://specifications.freedesktop.org/secret-service/0.2/org.freedesktop.Secret.Service.html) — Complete method list
- Existing codebase: `internal/proxy/` — current proxy implementation
- Existing codebase: `internal/gpgsign/run.go` — current GPG thin client
- Existing codebase: `internal/daemon/daemon.go` — current daemon bus connection pattern

### Secondary (MEDIUM confidence)
- [ArchWiki Systemd/User](https://wiki.archlinux.org/title/Systemd/User) — user@.service lifecycle, linger behavior
- [godbus issue #246](https://github.com/godbus/dbus/issues/246) — cross-user session bus connection patterns
- [quantum5.ca Unix socket sharing](https://quantum5.ca/2021/06/05/sharing-unix-sockets-between-multiple-users/) — ACL and group approaches for cross-user socket access
- [dbus-daemon man page](https://dbus.freedesktop.org/doc/dbus-daemon.1.html) — auth mechanisms, listen configuration, multiple addresses

### Tertiary (LOW confidence — needs validation)
- Cross-user dbus-daemon session bus policy enforcement details
- Session bus activation file pickup from user XDG_DATA_HOME
- `PartOf=user-<uid>.slice` cross-user systemd dependency behavior

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new dependencies needed, existing godbus v5 covers all needs
- Architecture (proxy repoint): HIGH — existing proxy is reused unchanged; only connection target changes
- Architecture (cross-user auth): LOW — empirical testing required; EXTERNAL auth behavior with different UIDs is the main unknown
- Architecture (lifecycle): MEDIUM — logind signal approach is reliable but needs implementation
- Pitfalls: MEDIUM — based on D-Bus spec + experience with similar projects

**Research date:** 2026-02-27
**Valid until:** 2026-09-27 (stable ecosystem — godbus, dbus-daemon, systemd change rarely)
