# Phase 4: Foundation - Research

**Researched:** 2026-02-25
**Domain:** Linux user provisioning, system D-Bus policy, Go daemon skeleton, systemd integration
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

#### Provisioning tool UX
- Subcommand of main binary: `secrets-dispatcher provision` and `secrets-dispatcher provision --check`
- Non-interactive — scriptable, no prompts, fails with clear messages on precondition failures
- Requires root — must be run as root (sudo secrets-dispatcher provision), fails immediately if not root
- `--check` output: human-readable checklist with pass/fail per component and actionable fix hints on failures

#### Companion user placement
- Home directory: `/var/lib/secret-companion/{username}` (e.g., `/var/lib/secret-companion/nb`)
- Parent `/var/lib/secret-companion/` owned by root with 0755
- User home subdir owned by companion user with 0700
- Username: configurable via `--companion-name` flag, default `secrets-{username}`
- Shell: /usr/sbin/nologin — no interactive login, companion session managed by systemd

#### Daemon skeleton scope
- Structured skeleton: D-Bus name registration + empty handler interfaces + slog logging + signal handling + sd-notify readiness notification
- Runs as systemd user service under companion user (unit file installed by provisioning)
- Same binary, subcommand: `secrets-dispatcher daemon`
- Extends existing cobra CLI alongside provisioning subcommand
- Stub methods return canned responses — proves D-Bus policy works, Phase 5 adds real logic

#### HTTP/WebSocket handling
- Keep existing HTTP/WS/Web UI code untouched
- New `daemon` subcommand simply does not initialize HTTP listeners
- Old `serve` command continues to work as before
- HTTP/WS kept as opt-in alternative permanently (not planned for removal)

#### Testing approach
- All tests fully automated — no manual steps, no user-assisted testing, no "run this and verify"
- Three test layers: unit (mocks), integration (private D-Bus daemon), VM E2E
- Every layer runs with `go test` or equivalent CI-compatible command
- Tests must prove success criteria without human intervention

### Claude's Discretion
- D-Bus policy XML structure and exact method signatures for stubs
- systemd unit file details (dependencies, ordering, environment)
- Signal handler implementation
- Test framework and mock strategy for Phase 4 tests
- Exact cobra command tree restructuring

### Deferred Ideas (OUT OF SCOPE)
- SSH agent forwarding to companion — move SSH private keys to companion user's vault, forward desktop user SSH requests via proxy. Same privilege separation pattern as GPG. Capture as future milestone (v2.1 or v3).

</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| COMP-01 | Companion user (secrets-nb) exists as a real user with separate UID and 0700 home directory | useradd + mkdir + chown pattern; idempotency via os/user.Lookup |
| COMP-02 | gopass store and GPG keyring reside under companion user's home, inaccessible to desktop user | Directory structure under 0700 home; Phase 4 creates dirs, actual gopass/GPG init is Phase 5 scope |
| COMP-05 | systemd linger enabled for companion user so systemd --user persists | `loginctl enable-linger {user}` via os/exec; idempotent by nature |
| DBUS-01 | Daemon registers on system bus, accepting requests from desktop user UID only | godbus/dbus/v5 ConnectSystemBus() + RequestName(); policy enforces UID restriction |
| DBUS-02 | D-Bus policy file gates access: companion user owns name, desktop user can call methods | D-Bus policy XML with user="companion-name" for own, user="desktop-name" for send; installed to /usr/share/dbus-1/system.d/ |
| PROV-01 | Provisioning tool creates companion user, home dir, D-Bus policy, systemd units | All via os/exec (useradd, loginctl) + os.MkdirAll + os.WriteFile; root-check at start |
| PROV-02 | Provisioning tool installs PAM hook | Writes PAM config snippet to /etc/pam.d/secrets-dispatcher; pam_exec.so with systemctl start --no-block; actual PAM integration tested in VM E2E |
| PROV-03 | Provisioning tool configures gopass and GPG under companion user | Phase 4 scope: create directory skeleton (~/.config/gopass/, ~/.gnupg/ under companion home); actual gopass init + GPG keygen deferred to Phase 5 |
| PROV-04 | `sd-provision --check` validates full deployment (pass/fail per component) | checklist struct with per-component check functions; each returns pass/fail + fix hint |
| PROV-05 | Provisioning tool is idempotent (safe to re-run) | os/user.Lookup before useradd; MkdirAll; overwrite policy + unit files; loginctl enable-linger is idempotent |
| INFRA-01 | All daemon logging goes to systemd-journald (structured slog) | slog.NewJSONHandler(os.Stderr) → journald captures stderr automatically for system services; no extra library needed |
| INFRA-02 | HTTP/REST API, WebSocket, and Web UI are opt-in (disabled by default) | daemon subcommand simply does not call api.NewServer(); existing serve command unchanged |
| TEST-04 | CI-compatible: unit + integration run in CI; VM E2E gated by environment variable | unit tests: mocked os/exec; integration tests: private dbus-daemon (no root); policy rejection test → VM E2E only |

</phase_requirements>

## Summary

Phase 4 has three distinct technical domains: (1) Linux user provisioning via os/exec syscalls (useradd, loginctl, chmod), (2) system D-Bus policy XML authoring and integration test infrastructure for policy-enforced buses, and (3) a Go daemon skeleton using godbus/dbus/v5 with sd-notify, slog, and signal handling. All three are well-understood domains with existing patterns in the project or standard Linux tooling.

The key architectural insight is that ALL new Go code for this phase lives in two new packages: `internal/companion` (provisioning logic) and `internal/daemon` (daemon skeleton), plus two new `main.go` subcommand handlers (`provision` and `daemon`). The existing switch-based CLI dispatch in `main.go` is extended — cobra is NOT added (it is not a current dependency and the existing stdlib flag pattern is sufficient for this scope).

The critical testing constraint — no root, no real companion user, no manual steps — is achievable by: (a) using dependency injection / function variable overrides for os/exec calls in provisioning unit tests (same pattern as `internal/service/install_test.go`), and (b) extending the existing `startDBusDaemon()` integration test helper with a `startDBusDaemonWithPolicy()` variant that writes a temp config file. The policy denial test (wrong UID rejected) cannot be automated without root and belongs in VM E2E scope.

**Primary recommendation:** Implement provisioning as a thin orchestrator over injectable system call functions. The integration test spins a private dbus-daemon with a policy config file mirroring the system bus defaults, which lets us validate D-Bus name registration under policy enforcement without root.

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| github.com/godbus/dbus/v5 | v5.2.2 (already in go.mod) | System D-Bus connection, name registration, object export | Already the project's D-Bus library; provides ConnectSystemBus(), RequestName(), Export() |
| log/slog (stdlib) | Go 1.25.6 | Structured daemon logging | Already used in runServe(); journald captures stderr output automatically |
| os/exec (stdlib) | Go 1.25.6 | Invoke useradd, loginctl, chown | Standard approach; injectable for unit tests |
| os/user (stdlib) | Go 1.25.6 | Check if Linux user exists before creating | os/user.Lookup() returns error if user not found |
| os/signal + syscall | Go 1.25.6 | SIGTERM/SIGINT handler in daemon | Already used in runServe() |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/lmittmann/tint | v1.1.3 (already in go.mod) | Colored slog for non-journald output | Already used; daemon detects INVOCATION_ID env var to suppress color/timestamps |
| net (stdlib) Unix datagram | Go stdlib | sd-notify (write READY=1 to NOTIFY_SOCKET) | 10-line implementation; no new dependency needed |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| stdlib flag switch | github.com/spf13/cobra v1.10.2 | Cobra adds auto-help, persistent flags, and structured subcommands but also adds ~10k LOC dependency. The existing switch statement handles the current 8 subcommands fine. Phase 4 adds 2 more. Cobra's value is real at 15+ subcommands or when flag inheritance across levels matters. **Recommendation: stay with stdlib flag.** |
| os/exec for user ops | cgo + libc getpwnam | cgo complicates cross-compilation; os/exec is testable and clear |
| coreos/go-systemd/v22 for sd-notify | Write to NOTIFY_SOCKET directly | go-systemd is 50k LOC for a protocol that is `echo "READY=1" > $NOTIFY_SOCKET`. Roll the 10-line version. |

**Cobra flag (IMPORTANT):** CONTEXT.md says "Extends existing cobra CLI alongside provisioning subcommand" but the codebase has NO cobra dependency — it uses stdlib flag with a manual switch. This appears to be forward-looking language in the CONTEXT, not a description of current reality. **The planner should use stdlib flag and NOT add cobra.** If the user intended cobra to be added in this phase, they must clarify.

## Architecture Patterns

### Recommended Project Structure

```
internal/
├── companion/           # NEW: Provisioning logic
│   ├── provision.go     # Orchestrator; calls system functions
│   ├── provision_test.go
│   ├── check.go         # --check checker components
│   ├── check_test.go
│   └── sysfuncs.go      # Injectable system call vars (useradd, chown, etc.)
├── daemon/              # NEW: Daemon skeleton
│   ├── daemon.go        # Main daemon run loop
│   ├── daemon_test.go   # Integration test with private dbus-daemon
│   ├── dispatcher.go    # D-Bus object with stub methods
│   └── notify.go        # sd-notify implementation (10 lines)
├── service/             # EXISTING: systemd user service management (unchanged)
└── dbus/                # EXISTING: D-Bus type definitions (add daemon constants)

assets/
└── net.mowaka.SecretsDispatcher1.conf   # NEW: D-Bus policy file (embedded via go:embed)

main.go                  # EXTEND: add "provision" and "daemon" cases to switch
```

### Pattern 1: Injectable System Calls for Testable Provisioning

**What:** Replace direct `exec.Command("useradd", ...)` calls with package-level function variables that unit tests can swap out. Identical to the pattern in `internal/service/install_test.go` (which swaps `systemctlFunc`).

**When to use:** Any provisioning function that calls os/exec or syscalls that require root.

**Example:**

```go
// internal/companion/sysfuncs.go

// Swappable by tests
var (
    userAddFunc    = defaultUserAdd
    loginctlFunc   = defaultLoginctl
    chmodFunc      = os.Chmod
    chownFunc      = os.Lchown
    mkdirAllFunc   = os.MkdirAll
    writeFileFunc  = os.WriteFile
)

func defaultUserAdd(username, homeDir, shell string) error {
    cmd := exec.Command("useradd",
        "--home-dir", homeDir,
        "--no-create-home",
        "--shell", shell,
        "--system",
        username,
    )
    return cmd.Run()
}

func defaultLoginctl(args ...string) error {
    return exec.Command("loginctl", args...).Run()
}
```

```go
// internal/companion/provision_test.go

func TestProvisionCreatesUser(t *testing.T) {
    var addedUser string
    origUserAdd := userAddFunc
    userAddFunc = func(username, homeDir, shell string) error {
        addedUser = username
        return nil
    }
    t.Cleanup(func() { userAddFunc = origUserAdd })
    // ... same pattern as install_test.go
}
```

### Pattern 2: Private dbus-daemon with Policy Config for Integration Tests

**What:** Extend the existing `startDBusDaemon()` helper to write a temporary policy config file and start dbus-daemon with `--config-file`. This enforces policy without requiring root.

**When to use:** Integration tests for DBUS-01 (can register) and any positive-path policy verification.

**Example:**

```go
// internal/daemon/daemon_test.go

func startDBusDaemonWithPolicy(t *testing.T, companionUser, desktopUser string) (*exec.Cmd, string) {
    t.Helper()
    tmpDir := t.TempDir()
    sockPath := filepath.Join(tmpDir, "test.sock")

    confPath := filepath.Join(tmpDir, "policy.conf")
    conf := fmt.Sprintf(`<?xml version="1.0"?>
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=%s</listen>
  <policy context="default">
    <allow user="*"/>
    <deny own="*"/>
    <deny send_type="method_call"/>
    <allow send_type="signal"/>
    <allow send_requested_reply="true" send_type="method_return"/>
    <allow send_requested_reply="true" send_type="error"/>
    <allow receive_type="method_call"/>
    <allow receive_type="method_return"/>
    <allow receive_type="error"/>
    <allow receive_type="signal"/>
    <allow send_destination="org.freedesktop.DBus"/>
  </policy>
  <policy user="%s">
    <allow own="net.mowaka.SecretsDispatcher1"/>
    <allow send_destination="net.mowaka.SecretsDispatcher1"/>
  </policy>
</busconfig>`, sockPath, companionUser)

    os.WriteFile(confPath, []byte(conf), 0600)

    cmd := exec.Command("dbus-daemon", "--config-file="+confPath, "--nofork")
    cmd.Start()
    // Wait for socket...
    return cmd, "unix:path=" + sockPath
}
```

**Critical implementation note (VERIFIED):** The `user=` attribute on `<policy>` takes a username OR numeric UID. Numeric UID is confirmed working. The `user_id=` attribute does NOT exist and causes dbus-daemon to reject the config. Using `user="1000"` (numeric) is valid.

**Policy rejection testing limitation:** To test that a WRONG UID gets rejected, you need two different UIDs in one test. This is not achievable without root in CI. The policy-denial test lives in VM E2E scope only.

### Pattern 3: D-Bus Stub Dispatcher

**What:** A Go struct with Phase 4 stub methods, exported via `conn.Export()`. Methods return canned responses. Phase 5 replaces with real implementations.

**When to use:** Proving D-Bus policy works and the wire protocol is correct end-to-end.

**Example:**

```go
// internal/daemon/dispatcher.go

const (
    BusName       = "net.mowaka.SecretsDispatcher1"
    ObjectPath    = "/net/mowaka/SecretsDispatcher1"
    Interface     = "net.mowaka.SecretsDispatcher1"
)

type Dispatcher struct {
    version string
}

// Ping is a health-check stub. Phase 5 replaces with real methods.
func (d *Dispatcher) Ping() (string, *dbus.Error) {
    return "pong", nil
}

// GetVersion returns the daemon version.
func (d *Dispatcher) GetVersion() (string, *dbus.Error) {
    return d.version, nil
}
```

```go
// internal/daemon/daemon.go - registration

func (d *Daemon) Run(ctx context.Context) error {
    conn, err := dbus.Connect(d.busAddr) // or dbus.ConnectSystemBus() in production
    if err != nil {
        return fmt.Errorf("connect to system bus: %w", err)
    }
    defer conn.Close()

    dispatcher := &Dispatcher{version: d.version}
    if err := conn.Export(dispatcher, ObjectPath, Interface); err != nil {
        return fmt.Errorf("export dispatcher: %w", err)
    }
    if err := conn.Export(dbus.DefaultIntrospectHandler(dispatcher, ObjectPath), ObjectPath, "org.freedesktop.DBus.Introspectable"); err != nil {
        return fmt.Errorf("export introspect: %w", err)
    }

    reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
    if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
        return fmt.Errorf("request bus name %q: reply=%d err=%w", BusName, reply, err)
    }

    slog.Info("daemon ready", "bus_name", BusName)
    sdNotify("READY=1")

    <-ctx.Done()
    return nil
}
```

### Pattern 4: sd-notify (No New Dependency)

**What:** Write "READY=1" to the Unix datagram socket at `$NOTIFY_SOCKET`. The entire implementation is 15 lines.

**When to use:** After daemon startup completes, before blocking in the event loop.

**Example:**

```go
// internal/daemon/notify.go

import "net"

// sdNotify sends a notification to systemd via NOTIFY_SOCKET.
// Silently succeeds if NOTIFY_SOCKET is not set (non-systemd environment).
func sdNotify(state string) {
    socket := os.Getenv("NOTIFY_SOCKET")
    if socket == "" {
        return
    }
    conn, err := net.Dial("unixgram", socket)
    if err != nil {
        slog.Warn("sd-notify dial failed", "err", err)
        return
    }
    defer conn.Close()
    conn.Write([]byte(state))
}
```

### Pattern 5: D-Bus Policy File Structure (VERIFIED)

The policy file MUST mirror the system bus defaults from `/usr/share/dbus-1/system.conf`. The system bus default policy is:

```xml
<!-- /usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf -->
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <!-- Companion user owns the bus name -->
  <policy user="secrets-{username}">
    <allow own="net.mowaka.SecretsDispatcher1"/>
    <allow send_destination="net.mowaka.SecretsDispatcher1"/>
  </policy>

  <!-- Desktop user can call methods -->
  <policy user="{username}">
    <allow send_destination="net.mowaka.SecretsDispatcher1"/>
  </policy>
</busconfig>
```

**Key facts (VERIFIED from system.conf and man dbus-daemon):**
- The system bus already denies `own="*"` and `send_type="method_call"` by default
- The system bus already allows all `receive_type` by default
- Service policy files only need to PUNCH HOLES (add allow rules)
- `<policy user="username">` takes a username string or numeric UID string
- `user_id="..."` is NOT a valid attribute (dbus-daemon rejects config with it)
- The file goes in `/usr/share/dbus-1/system.d/` (standard location, no `/etc/dbus-1/system.d/` override needed)

The provisioning tool writes a rendered version of this template with real usernames substituted in. The desktop user is detected from `SUDO_USER` environment variable (set by sudo).

### Pattern 6: Companion User Creation (Idempotent)

```go
// internal/companion/provision.go

func ensureCompanionUser(username, homeDir string) error {
    // Check if user already exists
    if _, err := user.Lookup(username); err == nil {
        slog.Info("companion user already exists", "username", username)
        return nil
    }

    // Create parent dir first (root-owned, 0755)
    if err := mkdirAllFunc(filepath.Dir(homeDir), 0755); err != nil {
        return fmt.Errorf("create parent dir: %w", err)
    }

    // Create user (--no-create-home because we control the home dir)
    if err := userAddFunc(username, homeDir, "/usr/sbin/nologin"); err != nil {
        return fmt.Errorf("useradd: %w", err)
    }

    // Create home dir, owned by companion user, 0700
    companionUser, err := user.Lookup(username)
    if err != nil {
        return fmt.Errorf("lookup after create: %w", err)
    }
    uid, _ := strconv.Atoi(companionUser.Uid)
    gid, _ := strconv.Atoi(companionUser.Gid)

    if err := mkdirAllFunc(homeDir, 0700); err != nil {
        return fmt.Errorf("create home dir: %w", err)
    }
    return chownFunc(homeDir, uid, gid)
}
```

### Anti-Patterns to Avoid

- **Calling useradd blindly:** Always check user existence with `os/user.Lookup()` first. Calling `useradd` on an existing user returns exit code 9 and prints to stderr — this is an avoidable error.
- **Blocking PAM in provisioning test:** The PAM hook installs a config file, not a binary. Do NOT try to test PAM behavior in unit/integration tests. PAM behavior is VM E2E only.
- **Using abstract D-Bus sockets in tests:** Use `unix:path=` (filesystem sockets), not `unix:abstract=`. Abstract sockets have kernel-global namespace and can collide between parallel test runs.
- **Forgetting IntrospectHandler:** Export `org.freedesktop.DBus.Introspectable` alongside your interface. Without it, `busctl introspect` gives opaque errors and other D-Bus clients may misbehave.
- **receive_from in dbus-daemon config:** The attribute is `receive_sender`, not `receive_from`. Using `receive_from` causes dbus-daemon to reject the config silently (exit with error).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| sd-notify protocol | coreos/go-systemd/v22 (50k LOC) | 15-line NOTIFY_SOCKET write | The protocol is literally one line to a datagram socket; no library needed |
| D-Bus policy enforcement | Custom UID check in Go | dbus-daemon's built-in policy engine | Policy is enforced at the kernel/daemon level; checking UID in application code is defense-in-depth only and NOT the required policy isolation |
| Linux user management | Parse /etc/passwd directly | `os/user.Lookup()` + `exec.Command("useradd")` | os/user handles all the NSS routing correctly; useradd handles shadow, uid allocation, etc. |

**Key insight:** The D-Bus policy file is the security enforcement mechanism, not Go code. Any UID-checking in the Go daemon is belt-and-suspenders; the policy file is the actual gate that DBUS-02 requires.

## Common Pitfalls

### Pitfall 1: D-Bus Policy Receive Direction
**What goes wrong:** Writing a test dbus-daemon config with `<deny own="*"/>` and `<deny send_destination="..."/>` but forgetting that the system bus also has explicit `<allow receive_type="..."/>` rules. Without those allows, the daemon's replies to the bus itself (e.g., RequestName response, NameAcquired signal) are rejected.
**Why it happens:** The system bus default config is not "deny all except explicit allows" — it's a mix of specific denies on sends with explicit allows on receives. The policy context="default" section from system.conf must be replicated in test configs.
**How to avoid:** Copy the full default policy block from `/usr/share/dbus-1/system.conf` into your test config template. This was VERIFIED via live testing: the correct template is documented in Pattern 5 above.
**Warning signs:** dbus-daemon logs "Rejected receive message, 0 matched rules" for method_return or NameAcquired signal.

### Pitfall 2: useradd --system vs Regular User
**What goes wrong:** Using `--system` flag creates a system user with a UID in the system range (typically <1000) which may interfere with systemd --user. System users don't get a full PAM session and systemd --user may not start for them.
**Why it happens:** "companion user" sounds like a system account, but needs a full user session for systemd --user to work.
**How to avoid:** Create as a regular user (no `--system` flag) with `--no-create-home` and `--shell /usr/sbin/nologin`. The nologin shell prevents interactive login while still allowing systemd --user to run.
**Warning signs:** `loginctl enable-linger` succeeds but `systemctl --user --machine=secrets-nb@` fails.

### Pitfall 3: Missing COMP-05 Causes Daemon Death on Logout
**What goes wrong:** Companion daemon starts fine, but stops when the desktop user logs out because `systemd --user` for the companion is terminated.
**Why it happens:** Without linger enabled, systemd kills the --user instance when the last user session ends.
**How to avoid:** `loginctl enable-linger {companion-username}` is a REQUIRED provisioning step (COMP-05). Include it in the --check output as a separate check item.
**Warning signs:** Daemon works fine interactively but disappears after logout.

### Pitfall 4: SUDO_USER Not Set
**What goes wrong:** Provisioning detects `os.Getenv("SUDO_USER")` as empty, can't determine which desktop user to create the companion for.
**Why it happens:** The user ran the binary as root directly (not via sudo), or the binary is SUID root.
**How to avoid:** If SUDO_USER is empty, require `--user {username}` flag explicitly. Fail with clear error if neither is available.
**Warning signs:** Companion user named "secrets-root" or provisioning fails with no useful message.

### Pitfall 5: cobra CLI Mismatch
**What goes wrong:** CONTEXT.md says "Extends existing cobra CLI" but the codebase has no cobra dependency and uses stdlib flag with a manual switch.
**Why it happens:** The CONTEXT language appears to be forward-looking ("will use cobra") rather than descriptive of current state.
**How to avoid:** Do NOT add cobra in Phase 4. Extend the existing `switch os.Args[1]` in `main.go` with "provision" and "daemon" cases. If the user explicitly wants cobra migration, they must say so.

### Pitfall 6: PROV-03 gopass/GPG Init Scope Creep
**What goes wrong:** Phase 4 tries to run `gopass init` and `gpg --gen-key` as the companion user, which requires a full user session, interactive prompts, and possibly a passphrase.
**Why it happens:** PROV-03 ("Provisioning tool configures gopass and GPG under companion user") is in Phase 4's requirement list, but the Phase 4 Success Criteria say nothing about a working gopass store.
**How to avoid:** Phase 4 PROV-03 scope = create directory skeleton (`$HOME/.config/gopass/`, `$HOME/.gnupg/`) with correct permissions. Actual gopass init + GPG key generation is Phase 5 scope where the companion daemon is running and can be invoked.

## Code Examples

Verified patterns from official sources and live testing:

### Claiming a D-Bus name with godbus/dbus/v5

```go
// Source: godbus/dbus/v5 conn.go + verified against existing proxy.go pattern
import "github.com/godbus/dbus/v5"

conn, err := dbus.ConnectSystemBus() // production
// OR for tests:
conn, err := dbus.Connect("unix:path=/tmp/test.sock")
if err != nil {
    return fmt.Errorf("connect: %w", err)
}
defer conn.Close()

reply, err := conn.RequestName("net.mowaka.SecretsDispatcher1", dbus.NameFlagDoNotQueue)
if err != nil {
    return fmt.Errorf("request name: %w", err)
}
if reply != dbus.RequestNameReplyPrimaryOwner {
    return fmt.Errorf("not primary owner (reply=%d); policy rejected or name taken", reply)
}
```

### Exporting a Go object as D-Bus interface

```go
// Source: godbus/dbus/v5 export docs + existing testutil/mockservice.go pattern
type Dispatcher struct{}

func (d *Dispatcher) Ping() (string, *dbus.Error) { return "pong", nil }
func (d *Dispatcher) GetVersion() (string, *dbus.Error) { return "0.1.0", nil }

// Register on the connection
if err := conn.Export(d, "/net/mowaka/SecretsDispatcher1", "net.mowaka.SecretsDispatcher1"); err != nil {
    return err
}
// Always export Introspectable too
if err := conn.Export(dbus.DefaultIntrospectHandler(d, "/net/mowaka/SecretsDispatcher1"),
    "/net/mowaka/SecretsDispatcher1", "org.freedesktop.DBus.Introspectable"); err != nil {
    return err
}
```

### Checking user existence with os/user

```go
import "os/user"

if _, err := user.Lookup("secrets-nb"); err != nil {
    // User does not exist, create it
} else {
    // User exists, skip creation (idempotent)
}
```

### Private dbus-daemon for integration tests (VERIFIED working)

```go
// Extends existing startDBusDaemon() pattern from proxy_test.go
func startDBusDaemonWithPolicy(t *testing.T, companionUsername string) (cmd *exec.Cmd, addr string) {
    t.Helper()
    dir := t.TempDir()
    sockPath := filepath.Join(dir, "test.sock")
    confPath := filepath.Join(dir, "policy.conf")

    conf := fmt.Sprintf(policyConfigTemplate, sockPath, companionUsername)
    if err := os.WriteFile(confPath, []byte(conf), 0600); err != nil {
        t.Fatal(err)
    }

    cmd = exec.Command("dbus-daemon", "--config-file="+confPath, "--nofork")
    if err := cmd.Start(); err != nil {
        t.Fatalf("start dbus-daemon: %v", err)
    }
    t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

    for range 50 {
        if _, err := os.Stat(sockPath); err == nil {
            return cmd, "unix:path=" + sockPath
        }
        time.Sleep(100 * time.Millisecond)
    }
    t.Fatal("dbus-daemon did not start in time")
    return nil, ""
}
```

### PAM hook config file content (for pam_exec.so)

```
# /etc/pam.d/secrets-dispatcher
# Start/stop companion session on desktop user login/logout
session optional pam_exec.so quiet seteuid /usr/local/bin/secrets-dispatcher-pam-hook
```

Or more correctly using systemctl directly:
```
session optional pam_exec.so quiet /usr/bin/systemctl start --no-block secrets-dispatcher-companion@%u.service
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| /etc/dbus-1/system.d/ for service policy files | /usr/share/dbus-1/system.d/ for packaged service policies | dbus 1.10 (2015) | Phase 4 installs policy to /usr/share/dbus-1/system.d/; /etc/ is for admin overrides only |
| coreos/go-systemd for sd-notify | Write to NOTIFY_SOCKET directly | Ongoing community pattern | Less dependency bloat; protocol is trivially simple |
| useradd --system for service accounts | Regular user with --no-create-home --shell nologin | When systemd --user started being used for service accounts | systemd --user requires a regular user with a UID in the normal range |

**Deprecated/outdated:**
- `dbus.SystemBus()` (shared connection): Prefer `dbus.ConnectSystemBus()` (dedicated connection). The shared version is harder to test and may have unexpected lifecycle coupling.

## Open Questions

1. **cobra migration scope**
   - What we know: CONTEXT.md says "Extends existing cobra CLI" but cobra is not in go.mod or go.sum. The existing CLI uses stdlib flag with switch.
   - What's unclear: Did the user intend cobra adoption in Phase 4, or was this language describing a future state?
   - Recommendation: Planner should NOT add cobra. Extend existing switch statement. Flag this explicitly in PLAN.md for user confirmation.

2. **PROV-03 exact scope in Phase 4**
   - What we know: PROV-03 is in Phase 4 requirements but Phase 4 Success Criteria don't mention gopass/GPG. Actual gopass init requires an active user session.
   - What's unclear: Does Phase 4 PROV-03 mean "create directory skeleton" or "fully initialize gopass store"?
   - Recommendation: Planner should scope PROV-03 to directory skeleton only (create ~/.config/gopass/, ~/.gnupg/ with correct ownership) and note that `gopass init` + `gpg --gen-key` are Phase 5 tasks.

3. **Systemd unit file location for companion daemon**
   - What we know: CONTEXT says "Runs as systemd user service under companion user". Companion home is at `/var/lib/secret-companion/{username}`. XDG_CONFIG_HOME for companion would be `/var/lib/secret-companion/{username}/.config`.
   - What's unclear: Does provisioning write the unit file to `/var/lib/secret-companion/{username}/.config/systemd/user/` (companion's XDG), or does it install as a system-level service at `/etc/systemd/system/` running as the companion user?
   - Recommendation: Use companion's XDG path for the unit file since it runs under systemd --user. Provisioning writes to that path with correct ownership.

## Sources

### Primary (HIGH confidence)
- `man dbus-daemon` (local) - policy syntax, `user=` attribute, system bus default-deny behavior
- `/usr/share/dbus-1/system.conf` (local) - authoritative system bus default policy template
- `/home/nb/src/secrets-dispatcher/proxy_test.go` (codebase) - existing private dbus-daemon test infrastructure
- `/home/nb/src/secrets-dispatcher/internal/service/install_test.go` (codebase) - injectable function var pattern for testable os/exec calls
- `/home/nb/src/secrets-dispatcher/internal/testutil/mockservice.go` (codebase) - godbus object export pattern
- `/home/nb/src/secrets-dispatcher/go.mod` (codebase) - confirmed godbus/dbus/v5 v5.2.2 in deps; cobra NOT present
- Live dbus-daemon testing (this session) - VERIFIED policy config syntax, user= attribute behavior, system.conf template

### Secondary (MEDIUM confidence)
- `useradd --help` (local) - confirmed `--no-create-home`, `--shell`, `--home-dir` flags
- `loginctl` (local) - confirmed `enable-linger` subcommand behavior
- `go list -m github.com/spf13/cobra@latest` - confirmed cobra v1.10.2 available (not adding it)
- `go list -m github.com/coreos/go-systemd/v22@latest` - confirmed v22.7.0 available (not adding it)

### Tertiary (LOW confidence)
- None. All critical claims verified from primary sources.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - godbus already in deps; stdlib flag verified in codebase; all other tools confirmed present
- Architecture: HIGH - patterns verified from existing codebase + live dbus-daemon testing
- Pitfalls: HIGH - D-Bus policy pitfalls discovered and verified through live testing; companion user pitfalls from systemd docs

**Research date:** 2026-02-25
**Valid until:** 2026-05-25 (90 days — stable Linux/D-Bus APIs, low churn)
