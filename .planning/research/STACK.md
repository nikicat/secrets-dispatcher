# Technology Stack: v2.0 Privilege Separation Milestone

**Project:** secrets-dispatcher — VT trusted I/O, system D-Bus, PAM hooks, companion user
**Researched:** 2026-02-25
**Scope:** Stack additions for the v2.0 milestone ONLY. Existing dependencies (godbus/dbus v5.2.2, golang.org/x/sys v0.27.0, google/uuid, lmittmann/tint, yaml.v3, fsnotify) remain in place unless noted. The v1.0 dependencies (coder/websocket, Svelte, Deno, Playwright) are DROPPED in v2.0.

---

## Summary Recommendation

Four new Go dependencies are needed for v2.0:

1. **`charm.land/bubbletea/v2`** — TUI framework for the VT approval interface
2. **`github.com/msteinert/pam/v2`** — PAM module development via CGo (for the session hook)
3. **`github.com/coreos/go-systemd/v22`** — systemd logind API for companion session lifecycle management
4. **`golang.org/x/sys`** upgrade to **v0.41.0** — VT ioctl constants are defined raw; the upgrade brings the current version in line with the rest of the dependency tree

The VT ioctl constants (`VT_ACTIVATE`, `VT_SETMODE`, etc.) are **not exposed as named constants in golang.org/x/sys/unix**. They must be defined inline as numeric literals (the values are stable Linux ABI). This is the correct approach used by all Linux projects (mpv, X.org, psplash) that do VT management without a C layer.

The Secret Service client (dispatcher → gopass-secret-service) is implemented directly on top of the existing `github.com/godbus/dbus/v5` — no additional Secret Service client library is needed or appropriate.

The private D-Bus daemon for integration testing uses `dbus-run-session` launched as an `exec.Cmd` in `TestMain` — no additional Go dependency needed.

---

## New Dependencies

### 1. TUI Framework

| Technology | Version | Module | Purpose |
|------------|---------|--------|---------|
| Bubble Tea v2 | v2.0.0 | `charm.land/bubbletea/v2` | VT approval TUI |
| Lip Gloss v2 | v2.x | `charm.land/lipgloss/v2` | Terminal styling |
| Bubbles v2 | v2.x | `charm.land/bubbles/v2` | Reusable TUI components |

**Why Bubble Tea v2, not v1:** v2.0.0 was released 2026-02-24 (stable, MIT, `charm.land/bubbletea/v2`). It ships the "Cursed Renderer" built from the ncurses rendering algorithm — orders of magnitude faster repaint on custom output writers, which matters when writing to `/dev/ttyN` instead of stdout. The Elm Architecture (Model/Update/View) maps cleanly onto the approval state machine (pending → approved/denied/expired). v1 is still functional but v2 is the current stable release.

**Why not rivo/tview:** tview is an immediate-mode widget toolkit (object-oriented, stateful widgets). It is more complex for a use case that is essentially a state machine with a small number of views. Bubble Tea's functional approach is simpler to test in isolation. tview has no equivalent of v2's improved non-stdout rendering.

**VT output redirection:** Bubble Tea v2 supports custom `io.Writer` output via `tea.WithOutput(tty)` where `tty` is a `*os.File` opened on `/dev/tty8`. The program also needs `tea.WithInput(tty)` so keyboard input comes from the VT rather than the process's stdin. This pattern is confirmed working on Linux in the project's GitHub issue tracker (issue #860, verified 2026-02-25).

**Note on import path change:** v2 uses `charm.land/bubbletea/v2` not `github.com/charmbracelet/bubbletea`. The charm.land domain is a vanity redirect maintained by Charmbracelet.

```go
import tea "charm.land/bubbletea/v2"
import "charm.land/lipgloss/v2"

tty, err := os.OpenFile("/dev/tty8", os.O_RDWR, 0)
// ...
p := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(tty))
```

**Confidence: HIGH** — verified against official release page and pkg.go.dev (v2.0.0, published 2026-02-24).

---

### 2. PAM Module (Session Lifecycle Hook)

| Technology | Version | Module | Purpose |
|------------|---------|--------|---------|
| msteinert/pam | v2.1.0 | `github.com/msteinert/pam/v2` | PAM session module via CGo |

**Why msteinert/pam:** This is the standard Go wrapper for the Linux PAM C API. v2.1.0 was released 2025-05-13. It exposes `pam_sm_open_session` and `pam_sm_close_session` callbacks via CGo, allowing a Go binary to be compiled as a PAM shared library (`pam_secrets_dispatcher.so`). The alternative — a pure C PAM module — is correct but means maintaining C code in a Go project, adding complexity.

**Architecture:** The PAM hook is compiled as a separate CGo shared library (`cmd/pam-module/main.go`), not linked into the main daemon binary. It is a small shim that calls `systemctl --user start secrets-dispatcher-session.service` (or a companion-user equivalent via `machinectl`) on `open_session` and `stop` on `close_session`.

**Alternative considered — pam_exec:** `pam_exec` is a built-in PAM module that runs an external command. It is simpler to deploy (no custom .so) and sufficient for the use case: `pam_exec.so /usr/local/bin/sd-session-start`. Use pam_exec if the PAM hook is just a one-liner script. Use msteinert/pam if the hook needs Go logic (e.g., detecting the last session closing requires checking `loginctl` state). The tradeoff: pam_exec is simpler to deploy; msteinert/pam is more capable. **Flag for implementation phase:** start with pam_exec, escalate to msteinert/pam if pam_exec proves insufficient.

**Known issue — CGo PAM + signal handling:** There is a documented SIGCHLD interaction between CGo-compiled PAM modules and systemd's pam_systemd. This is a known problem in the Go community (golang-nuts thread, 2016). msteinert/pam v2 addresses this in its documentation. Test carefully with signal-heavy scenarios.

**pam_systemd timing:** When a PAM session hook runs via pam_exec after `pam_systemd.so`, the `systemd --user` instance for the target user may not be ready. The hook must either retry with backoff or use `systemctl is-system-running --wait` before attempting to control user units. This is a known timing issue in pam + systemd integration (systemd/systemd issue #2863).

**Confidence: MEDIUM** — library confirmed active and versioned, known CGo caveats require validation during implementation.

```bash
go install github.com/msteinert/pam/v2
# compile as shared library:
# go build -buildmode=c-shared -o pam_secrets_dispatcher.so ./cmd/pam-module/
```

---

### 3. systemd Login Manager Integration

| Technology | Version | Module | Purpose |
|------------|---------|--------|---------|
| go-systemd/v22 | v22.7.0 | `github.com/coreos/go-systemd/v22` | logind API, session tracking |

**Why go-systemd:** The companion session lifecycle needs to detect when the last regular-user session closes (to shut down the companion user session). The `login1` package in go-systemd wraps the `org.freedesktop.login1.Manager` D-Bus interface, which is the correct API for listing sessions, subscribing to session removal events, and querying whether any sessions remain for a given UID.

**What it provides:**
- `login1.New()` — connect to logind D-Bus interface
- `login1.Manager.ListSessions()` — enumerate active sessions
- `login1.Manager.SessionRemoved` signal — fire-and-forget companion shutdown trigger

v22.7.0 was published 2026-01-27. The library is actively maintained by CoreOS/Red Hat.

**Alternative:** Direct D-Bus calls to `org.freedesktop.login1` via the existing `godbus/dbus/v5`. This is functionally equivalent but requires hand-writing all the method call scaffolding. go-systemd provides type-safe bindings to logind, reducing error surface. Given the project already uses godbus for Secret Service proxy work, go-systemd wrapping logind in a typed API is worth the additional dependency.

**Note:** go-systemd/v22 depends on godbus/dbus/v5 internally, so there is no version conflict.

**Confidence: HIGH** — official CoreOS library, actively maintained, version confirmed on pkg.go.dev (2026-01-27).

---

### 4. golang.org/x/sys Upgrade

| Technology | Current | Target | Reason |
|------------|---------|--------|--------|
| golang.org/x/sys | v0.27.0 | v0.41.0 | Current release (2026-02-08), VT ioctl work requires latest version |

The upgrade is routine. v0.41.0 is the current stable release (published 2026-02-08). The go.mod currently pins v0.27.0. go-systemd/v22 and other new dependencies will require a newer version anyway — the upgrade happens naturally during `go mod tidy`.

**Confidence: HIGH** — verified on pkg.go.dev.

---

## VT Management (No New Dependency)

VT management is implemented directly using `unix.Syscall(unix.SYS_IOCTL, ...)` with inline numeric constants. The constants (`VT_ACTIVATE`, `VT_SETMODE`, etc.) are **not defined as named constants in golang.org/x/sys/unix** (verified by searching zerrors_linux.go and zerrors_linux_amd64.go in the golang/sys repo). They must be declared in the project's own VT package.

These constants are part of the stable Linux UAPI (`include/uapi/linux/vt.h`) and do not change:

```go
// internal/vt/consts.go — Linux VT ioctl constants from include/uapi/linux/vt.h
// These are stable Linux UAPI values; they do not change between kernel versions.
const (
    VT_OPENQRY  = 0x5600 // find available vt
    VT_GETMODE  = 0x5601 // get current vt mode
    VT_SETMODE  = 0x5602 // set vt mode
    VT_GETSTATE = 0x5603 // get console state
    VT_RELDISP  = 0x5605 // release display
    VT_ACTIVATE = 0x5606 // switch to vt N
    VT_WAITACTIVE = 0x5607 // wait until vt N is active

    VT_AUTO    = 0x00 // auto VT switching
    VT_PROCESS = 0x01 // process controls switching
    VT_ACKACQ  = 0x02 // acknowledge VT acquisition
)
```

**VT_SETMODE data structure:** The `vt_mode` struct is not defined in golang.org/x/sys/unix. Define it locally:

```go
// VtMode matches C struct vt_mode from <linux/vt.h>
type VtMode struct {
    Mode   byte  // VT_AUTO or VT_PROCESS
    Waitv  byte  // if set, hang on writes if not active
    Relsig int16 // signal to raise on release
    Acqsig int16 // signal to raise on acquisition
    Frsig  int16 // unused (set to 0)
}
```

**VT operations required:**
- `VT_OPENQRY` on `/dev/tty0` — find a free VT number (used if we don't reserve a fixed VT)
- `VT_ACTIVATE` on `/dev/tty0` or `/dev/console` — switch to the companion VT
- `VT_SETMODE` — set `VT_PROCESS` mode to block unauthorized switching during approval
- `VT_RELDISP` — release the display (acknowledge switch-away when approval completes)
- Open `/dev/ttyN` directly — the companion process opens its VT as its controlling terminal

**Permissions:** `VT_ACTIVATE` requires either root or the calling process to own the active VT (or hold `CAP_SYS_TTY_CONFIG`). The companion user process running on VT 8 can call `VT_ACTIVATE` on `/dev/ttyN` it owns. The provisioning tool grants the companion user appropriate group membership (`tty` group) or a udev rule for the specific VT device.

**Confidence: HIGH** — VT ioctl constants verified against `include/uapi/linux/vt.h` in the Linux kernel source (torvalds/linux, 2026-02-25). Ioctl approach confirmed by chvt source, mpv, and psplash implementations.

---

## System D-Bus Service (No New Dependency)

The system D-Bus service (secrets-dispatcher on the system bus) uses the existing `github.com/godbus/dbus/v5`. The key difference from v1.0 is connecting to the **system bus** instead of the session bus, and registering a well-known name with a D-Bus policy file.

**System bus connection:**
```go
conn, err := dbus.ConnectSystemBus()
```

**Policy file** (`/etc/dbus-1/system.d/secrets-dispatcher.conf`):
```xml
<!DOCTYPE busconfig PUBLIC
  "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
  "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <policy user="secrets-nb">
    <allow own="io.nb.SecretsDispatcher"/>
  </policy>
  <policy context="default">
    <allow send_destination="io.nb.SecretsDispatcher"/>
    <allow receive_sender="io.nb.SecretsDispatcher"/>
  </policy>
</busconfig>
```

**Service file** (`/usr/share/dbus-1/system-services/io.nb.SecretsDispatcher.service`):
```ini
[D-BUS Service]
Name=io.nb.SecretsDispatcher
Exec=/usr/local/bin/secrets-dispatcher-companion
User=secrets-nb
```

The service file enables D-Bus activation (dbus-daemon starts the service on demand). For this project's lifecycle model (PAM starts the companion session which starts the service), D-Bus activation may not be needed — but the service file is correct to have for manual starts and tooling compatibility.

**Confidence: HIGH** — pattern verified against godbus documentation and Linux D-Bus policy documentation (pkg.go.dev/github.com/godbus/dbus/v5, published 2024-12-29).

---

## Private D-Bus for Integration Testing (No New Dependency)

Integration tests that exercise the D-Bus protocol layer use a private `dbus-daemon` started as a subprocess via `exec.Cmd`. No additional Go dependency needed — `dbus-run-session` is available on all Arch/Debian/Ubuntu/Fedora systems.

```go
// TestMain setup pattern
func TestMain(m *testing.M) {
    // Start private session bus
    cmd := exec.Command("dbus-run-session", "--", "env")
    out, _ := cmd.Output()
    // parse DBUS_SESSION_BUS_ADDRESS from out
    // set os.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
    // run tests
    // cleanup
}
```

Alternatively, start `dbus-daemon --session --print-address --fork` directly with a temp config file (`mktemp`) pointing to a session config. This gives more control over service search paths (needed to load test service files without installing to system directories).

**Confidence: HIGH** — dbus-run-session is the standard tool for this (documented in freedesktop.org test plan, verified against dbus-run-session manpage).

---

## Secret Service Client (No New Dependency)

The dispatcher calls gopass-secret-service (running on the companion user's session D-Bus) using the freedesktop Secret Service API. This uses the existing `github.com/godbus/dbus/v5` — no additional Secret Service client library is warranted.

**Why not use go-libsecret or go-dbus-keyring:**
- `gsterjov/go-libsecret` — 2 total commits, created 2016, unmaintained. Do not use.
- `ppacher/go-dbus-keyring` — marginally maintained, limited scope, wraps godbus anyway.

Both libraries are thin wrappers over godbus that add a fixed abstraction on top of the Secret Service spec. Since the project already uses godbus directly and has Secret Service D-Bus expertise (internal/proxy/), writing the 4-6 method calls against gopass-secret-service directly is cleaner and keeps the dependency count down.

**The calls needed (from Secret Service spec v0.2 DRAFT, 2025-11-26):**
- `org.freedesktop.Secret.Service.OpenSession` — establish PLAIN or DH session
- `org.freedesktop.Secret.Service.GetSecrets` — retrieve secrets by item path
- `org.freedesktop.Secret.Collection.SearchItems` — find items by attribute
- `org.freedesktop.Secret.Session.Close` — clean up session

This is roughly 100 lines of direct godbus usage with the existing patterns already present in `internal/proxy/`.

**Confidence: HIGH** — Secret Service spec verified at freedesktop.org (2025-11-26 draft). Library staleness verified on GitHub.

---

## Dropped Dependencies (v1.0 → v2.0)

| Dependency | v1.0 Use | v2.0 Status |
|------------|----------|-------------|
| `github.com/coder/websocket v1.8.14` | Real-time web UI updates | DROPPED — no web UI in v2.0 |
| Svelte 5, Vite, TypeScript | Web UI frontend | DROPPED — web UI removed |
| Playwright | E2E browser tests | DROPPED — no browser |
| Deno | Frontend tooling | DROPPED — no frontend |

Running `go mod tidy` after removing websocket import sites will clean the go.mod.

---

## Complete v2.0 Dependency Set

After adding new and removing dropped:

```go
// go.mod (projected v2.0)
require (
    charm.land/bubbletea/v2         v2.0.0
    charm.land/lipgloss/v2          v2.x.x   // added transitively by bubbletea
    charm.land/bubbles/v2           v2.x.x   // optional, confirm during implementation
    github.com/coreos/go-systemd/v22 v22.7.0
    github.com/fsnotify/fsnotify    v1.9.0   // retained
    github.com/godbus/dbus/v5       v5.2.2   // retained
    github.com/google/uuid          v1.6.0   // retained
    github.com/lmittmann/tint       v1.1.3   // retained
    github.com/msteinert/pam/v2     v2.1.0
    golang.org/x/sys                v0.41.0  // upgraded
    gopkg.in/yaml.v3                v3.0.1   // retained
    // DROPPED: github.com/coder/websocket
)
```

---

## Build Targets

v2.0 produces multiple build artifacts (unlike v1.0's single binary):

| Target | Type | Description |
|--------|------|-------------|
| `secrets-dispatcher` | binary | Companion-side daemon (system D-Bus + VT TUI) |
| `sd-user-agent` | binary | Desktop session agent (org.freedesktop.secrets proxy + notification listener) |
| `sd-gpg-sign` | binary | GPG thin client (replaces v1.0 gpg-sign subcommand, now uses system D-Bus) |
| `pam_sd_session.so` | CGo shared lib | PAM session hook (built with `-buildmode=c-shared`) |
| `sd-provision` | binary | Companion user setup tool |

The PAM module requires CGo and a C toolchain. The other binaries do not require CGo and cross-compile normally.

```makefile
# PAM module requires CGo
pam_sd_session.so:
    CGO_ENABLED=1 go build -buildmode=c-shared -o $@ ./cmd/pam-module/

# Other binaries: CGo optional (go-systemd uses CGo for some features, check --tags)
secrets-dispatcher:
    go build -o $@ ./cmd/dispatcher/
```

**Flag:** go-systemd v22 can be built without CGo on most paths. Verify with `CGO_ENABLED=0 go build` during initial setup — if it fails, the PAM module is the only CGo requirement.

---

## What NOT to Use

### phoenix-tui/phoenix — DO NOT USE
A newer TUI framework advertised as "modern alternative to Bubbletea". 29 stars on GitHub as of research date. No stable release. Unproven in production. Use Bubble Tea v2 which is mature, well-maintained by Charmbracelet, and has the custom output writer support this project needs.

### rivo/tview — DO NOT USE for this project
Excellent library for traditional TUI applications with widget trees. The wrong fit here: the approval TUI is a state machine with 3-4 views, not a dashboard with independent widgets. tview's object-oriented widget model adds conceptual overhead for a focused single-purpose TUI. Bubble Tea's functional model maps directly onto the approval state machine.

### gsterjov/go-libsecret — DO NOT USE
2 commits total, 2016, effectively dead. No releases. Avoid.

### ppacher/go-dbus-keyring — DO NOT USE
Thin wrapper over godbus. Last meaningful activity years ago. No advantage over direct godbus usage for this project's needs.

### pam_script (jeroennijhof/pam_script) — DO NOT USE as Go replacement
pam_script is a C PAM module that runs shell scripts. It is a valid lightweight alternative to a custom PAM module for the session lifecycle hook. Evaluate against pam_exec first. Neither is a "Go library" — both are system-level components. The PAM hook does not need to be written in Go if a shell script suffices.

### golang.org/x/crypto/openpgp — ALREADY EXCLUDED
Deprecated. Not needed. See v1.0 STACK.md.

---

## Sources

- Bubble Tea v2.0.0 release: [github.com/charmbracelet/bubbletea/releases/tag/v2.0.0](https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.0) — release date 2026-02-24, import path `charm.land/bubbletea/v2` (HIGH confidence)
- pkg.go.dev Bubble Tea v2: [pkg.go.dev/charm.land/bubbletea/v2](https://pkg.go.dev/charm.land/bubbletea/v2) — v2.0.0, published 2026-02-24 (HIGH confidence)
- Bubble Tea WithOutput issue: [github.com/charmbracelet/bubbletea/issues/860](https://github.com/charmbracelet/bubbletea/issues/860) — custom TTY output writer confirmed working (MEDIUM confidence)
- msteinert/pam v2.1.0: [github.com/msteinert/pam](https://github.com/msteinert/pam) — v2.1.0 released 2025-05-13 (HIGH confidence)
- go-systemd v22.7.0: [pkg.go.dev/github.com/coreos/go-systemd/v22](https://pkg.go.dev/github.com/coreos/go-systemd/v22) — published 2026-01-27 (HIGH confidence)
- golang.org/x/sys v0.41.0: [pkg.go.dev/golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) — published 2026-02-08 (HIGH confidence)
- Linux VT ioctl constants: [github.com/torvalds/linux/blob/master/include/uapi/linux/vt.h](https://github.com/torvalds/linux/blob/master/include/uapi/linux/vt.h) — stable UAPI, verified 2026-02-25 (HIGH confidence)
- VT constants NOT in golang.org/x/sys: verified by inspecting zerrors_linux.go and zerrors_linux_amd64.go in golang/sys repo (HIGH confidence — absence confirmed)
- ioctl_vt(2) Linux man page: [man7.org/linux/man-pages/man2/ioctl_vt.2.html](https://man7.org/linux/man-pages/man2/ioctl_vt.2.html) — authoritative VT ioctl semantics (HIGH confidence)
- godbus/dbus v5.2.2: [github.com/godbus/dbus/releases](https://github.com/godbus/dbus/releases) — published 2024-12-29, current (HIGH confidence)
- Secret Service spec v0.2 DRAFT: [specifications.freedesktop.org/secret-service/latest/](https://specifications.freedesktop.org/secret-service/latest/) — updated 2025-11-26 (HIGH confidence)
- go-libsecret status: [github.com/gsterjov/go-libsecret](https://github.com/gsterjov/go-libsecret) — 2 commits total, unmaintained (HIGH confidence)
- pam_systemd timing issue: [github.com/systemd/systemd/issues/2863](https://github.com/systemd/systemd/issues/2863) — known pam_exec + systemd --user timing race (MEDIUM confidence)
- dbus-run-session: [manpages.debian.org/testing/dbus-daemon/dbus-run-session.1.en.html](https://manpages.debian.org/testing/dbus-daemon/dbus-run-session.1.en.html) — standard test isolation tool (HIGH confidence)
