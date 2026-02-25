# Phase 5: Core Flow - Research

**Researched:** 2026-02-26
**Domain:** bubbletea TUI, Linux VT ioctls, D-Bus Secret Service, GPG pinentry, process chain traversal
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**TUI framework & layout**
- Use bubbletea (Elm architecture) + lipgloss for the VT TUI
- Fullscreen two-pane layout: narrow left pane (request list), wide right pane (request detail)
- Left pane items show type badge + short description: `[SECRET] github token`, `[SIGN] fix: auth flow`
- Arrow keys to navigate the list; selected item's full detail shown in right pane
- New requests appear in list without stealing focus — user navigates to them when ready
- Resolved requests fade to a "Recent" history section at bottom of list (dimmed, showing outcome)
- Live countdown timer shown per item in the left pane (not the detail pane)

**TUI detail pane content**
- Secret requests: secret path, requester PID/UID/process name, working directory, parent process chain (up to 5 levels)
- GPG signing requests: repo name, commit message, author, changed file names + stat summary line (e.g. "3 files changed, +47, -12"), key ID
- Process chain displayed for both request types

**Approval interaction**
- y/n keystrokes act immediately — no confirmation dialog
- y/n only active when VT is in locked state (for manual and auto lock modes)
- Three configurable VT lock modes:
  1. **No locking** — y/n work anytime, no VT_PROCESS mode
  2. **Manual lock/unlock** — user explicitly locks VT, can only approve/deny while locked
  3. **Auto lock on select** — VT locks when a pending request is selected, unlocks when cursor moves away or request is resolved
- Esc to unlock VT (in manual and auto modes), then normal Ctrl+Alt+Fn to switch

**VT allocation & lifecycle**
- VT number configurable (flag/config), defaults to VT8
- Daemon claims VT (opens /dev/ttyN, initializes TUI) on startup, not lazily on first request
- Idle state: same two-pane layout with empty list and status info in right pane (daemon uptime, companion user, lock mode, last activity)
- No desktop notification in Phase 5 — user manually switches to VT to check; Phase 6 agent handles awareness

**Crash recovery**
- If daemon crashes while holding VT_PROCESS mode, cleanup handlers + signal handlers return VT to VT_AUTO
- User can switch VTs normally after crash

**Caller-side experience**
- Denied requests return custom D-Bus error with reason (e.g. `net.mowaka.Error.Denied` with "Request denied by user")
- Timed-out requests return error mentioning VT: "Request timed out — approve on VT8 (Ctrl+Alt+F8)"
- D-Bus activation file installed so bus attempts to start daemon; returns actionable error if daemon can't start
- GPG thin client prints single status line to stderr while blocking: "Waiting for approval on VT8..."

### Claude's Discretion

- D-Bus method signatures and interface design (freedesktop Secret Service subset vs custom)
- bubbletea model/update/view architecture and component breakdown
- Process chain retrieval implementation (/proc traversal)
- Exact lipgloss styling (colors, borders, spacing)
- Integration test strategy for VT interactions (mock TTY)
- D-Bus activation file format and error messaging details
- How lock mode configuration is stored/switched

### Deferred Ideas (OUT OF SCOPE)

None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| DBUS-03 | Dispatcher exposes standard freedesktop Secret Service D-Bus protocol on system bus | Secret Service spec verified; v1 proxy already implements full interface; Phase 5 routes calls to companion's gopass store |
| DBUS-04 | GPG signing uses existing D-Bus protocol if available (research needed), otherwise custom interface | No standard D-Bus protocol for GPG signing exists; v1 uses custom HTTP/WS; Phase 5 uses custom `net.mowaka.SecretsDispatcher1` methods |
| DBUS-05 | System D-Bus signals restricted to requesting user's processes only | D-Bus policy (existing DBUS-02 file) + unicast signals using Emit with destination |
| DBUS-06 | Request expiry/timeout enforced (same as v1.0 approval manager) | Existing `approval.Manager` with timeout goroutine; reuse directly |
| DBUS-07 | Graceful error when companion not running — actionable message to desktop user | D-Bus activation file + custom `net.mowaka.Error.NotRunning` error |
| VT-01 | Companion session owns a persistent dedicated VT | Open `/dev/ttyN` at daemon startup; bubbletea `WithInput`/`WithOutput` on that fd |
| VT-02 | VT_SETMODE VT_PROCESS blocks unauthorized VT switching during active approval | `unix.Syscall(unix.SYS_IOCTL, fd, VT_SETMODE, ...)` with `vt_mode{mode: VT_PROCESS, relsig: SIGUSR1, acqsig: SIGUSR2}` |
| VT-03 | TUI displays rich context for secret requests | bubbletea detail pane renders path, PID/UID, process name, working dir |
| VT-04 | TUI displays rich context for GPG signing | bubbletea detail pane renders repo, message, author, files, stat summary, key ID |
| VT-05 | TUI displays requester parent process chain (up to 5 levels via /proc PPid) | `/proc/{pid}/status` PPid traversal; already partially done in v1 senderinfo.go |
| VT-06 | Keyboard approve/deny input on VT (y/n) | bubbletea `tea.KeyMsg` in `Update()` |
| VT-09 | Crash recovery: VT returns to VT_AUTO if daemon dies | `defer ioctl(VT_SETMODE, VT_AUTO)` + signal handler for SIGTERM/SIGINT/SIGKILL (SIGKILL uncatchable — kernel self-recovers) |
| GPG-02 | Companion's gpg-agent configured with pinentry-tty pointing to approval VT | `pinentry-program /usr/bin/pinentry-tty` in companion's `~/.gnupg/gpg-agent.conf`; `GPG_TTY=/dev/ttyN` set in companion's systemd unit |
| GPG-03 | Companion daemon signs via existing GPGRunner interface (calls real gpg, same as v1.0) | `internal/gpgsign` package already has `Run()` + `GPGRunner`; reuse; daemon calls gpg on companion's behalf |
| TEST-01 | Unit tests with interface mocks for VT, D-Bus connections, Secret Service client | Mock VT (write to bytes.Buffer), mock D-Bus connection (interface injection) |
| TEST-02 | Integration tests with private D-Bus daemon (extends daemon_test.go pattern) | Established pattern in `internal/daemon/daemon_test.go`; extend for Secret Service and GPG signing methods |
</phase_requirements>

## Summary

Phase 5 transforms the daemon skeleton from Phase 4 into a fully functional approval engine. The three main technical domains are: (1) the bubbletea-based VT TUI that renders requests and captures y/n decisions, (2) the Linux VT ioctl machinery for trusted I/O isolation, and (3) the D-Bus method implementations that bridge callers to the approval flow.

The TUI runs entirely on a dedicated VT (`/dev/tty8` by default), opened at daemon startup. Bubbletea supports arbitrary `*os.File` objects via `WithInput`/`WithOutput`, so opening `/dev/ttyN` and passing the fd to bubbletea is straightforward. VT_PROCESS mode is engaged/released via raw `ioctl` syscalls using constants from `<linux/vt.h>` (VT_SETMODE=0x5602, VT_AUTO=0x00, VT_PROCESS=0x01) and `golang.org/x/sys/unix`. The kernel self-recovers VT to text mode if the owning process dies, but the daemon must also register cleanup handlers for clean shutdown.

The D-Bus layer in Phase 5 replaces the stub dispatcher with real methods: `RequestSecret` (returns secret value or blocks until approval) and `RequestSign` (triggers GPG signing on approval). The freedesktop Secret Service protocol is already fully implemented in `internal/proxy/` — Phase 5 does not re-implement it but instead adds a server-side handler that the companion daemon exposes. GPG signing reuses `internal/gpgsign` and the existing `approval.Manager` with its observer pattern.

**Primary recommendation:** Wire bubbletea → approval.Manager → D-Bus dispatcher in three steps: (a) implement the D-Bus handler methods that create approval requests and block, (b) implement the TUI as a bubbletea program on /dev/ttyN, (c) connect the Manager's observer to the TUI's message channel so new requests appear without blocking the D-Bus goroutine.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| github.com/charmbracelet/bubbletea | v1.2.x (latest v1; v2 also available at charm.land/bubbletea/v2) | TUI event loop, Elm architecture | Charmbracelet's flagship TUI framework; industry standard for Go TUIs |
| github.com/charmbracelet/lipgloss | v1.1.0 | Terminal layout styling, two-pane layout | Pairs with bubbletea; JoinHorizontal for two-column layout |
| github.com/charmbracelet/bubbles/list | (part of charmbracelet/bubbles) | Pre-built list component with navigation | Provides cursor, selection, filtering out of the box |
| github.com/godbus/dbus/v5 | v5.2.2 (already in go.mod) | D-Bus method implementation | Already present; Phase 4 uses it |
| golang.org/x/sys | v0.27.0 (already in go.mod) | VT ioctl syscalls | Already present; provides unix.Syscall for raw ioctls |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/charmbracelet/bubbles/timer | (part of charmbracelet/bubbles) | Countdown timer component | Per-item countdown display in left pane |
| github.com/charmbracelet/bubbles/key | (part of charmbracelet/bubbles) | Key binding definitions | Custom y/n/Esc keymap |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| bubbletea v1 | bubbletea v2 (charm.land/bubbletea/v2) | v2 has new module path and Cursed Renderer but is still RC; v1 is stable and well-documented |
| bubbles/list | Hand-rolled list | bubbles/list is 800+ lines with pagination, filtering, and keyboard navigation; not worth duplicating |
| lipgloss v1 | lipgloss v2 | v2 is in dev; v1.1.0 is stable March 2025 |

**Installation:**
```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/charmbracelet/bubbles@latest
```

## Architecture Patterns

### Recommended Package Structure
```
internal/
├── daemon/
│   ├── daemon.go          # Config, Run() — extended from Phase 4
│   ├── dispatcher.go      # D-Bus object — Phase 5 replaces stubs with real methods
│   ├── dispatcher_test.go # Integration tests (extends daemon_test.go pattern)
│   ├── handler.go         # NEW: RequestSecret(), RequestSign() D-Bus method handlers
│   └── notify.go          # sd-notify (unchanged)
├── tui/
│   ├── model.go           # NEW: bubbletea Model — root model, routes messages
│   ├── list_pane.go       # NEW: left pane (request list, countdown, history)
│   ├── detail_pane.go     # NEW: right pane (full request context)
│   ├── vt.go              # NEW: VT ioctl helpers (open tty, VT_SETMODE, VT_RELDISP)
│   ├── styles.go          # NEW: lipgloss style definitions
│   └── model_test.go      # NEW: unit tests with mock TTY/approvals
└── procchain/
    └── procchain.go       # NEW: /proc PPid traversal (up to 5 levels)
```

### Pattern 1: bubbletea on a Custom VT

The daemon opens `/dev/ttyN` at startup and runs bubbletea on that fd, completely independent of the daemon's stdin/stdout.

```go
// Source: bubbletea WithInput/WithOutput API (pkg.go.dev/github.com/charmbracelet/bubbletea)
func StartTUI(ctx context.Context, vtPath string, mgr *approval.Manager) error {
    f, err := os.OpenFile(vtPath, os.O_RDWR, 0)
    if err != nil {
        return fmt.Errorf("open vt %s: %w", vtPath, err)
    }
    // bubbletea accepts any *os.File as input/output
    p := tea.NewProgram(
        newModel(mgr),
        tea.WithInput(f),
        tea.WithOutput(f),
        tea.WithAltScreen(),
    )
    _, err = p.Run()
    return err
}
```

**Key insight:** bubbletea's `WithInput`/`WithOutput` accept any `*os.File`, so `/dev/tty8` is a drop-in for stdin/stdout. The daemon's D-Bus goroutine and the TUI goroutine run concurrently; they communicate via `p.Send(msg)` from outside the TUI and via `tea.Cmd` returning messages inside the TUI.

### Pattern 2: VT_PROCESS Mode via Raw ioctl

VT_PROCESS causes the kernel to ask the owning process for permission before any VT switch. The daemon responds via SIGUSR1 handler (release ack). The key constants come from `<linux/vt.h>`:

```go
// Source: /usr/include/linux/vt.h (verified on this machine)
const (
    VT_SETMODE  = 0x5602
    VT_RELDISP  = 0x5605
    VT_AUTO     = 0x00
    VT_PROCESS  = 0x01
)

// vt_mode mirrors struct vt_mode from <linux/vt.h>
type vtMode struct {
    mode   uint8
    waitv  uint8
    relsig int16
    acqsig int16
    frsig  int16
}

func lockVT(fd uintptr) error {
    m := vtMode{
        mode:   VT_PROCESS,
        relsig: int16(syscall.SIGUSR1), // kernel sends this to ask us to release
        acqsig: int16(syscall.SIGUSR2), // kernel sends this when we've acquired
    }
    _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, VT_SETMODE, uintptr(unsafe.Pointer(&m)))
    if errno != 0 {
        return errno
    }
    return nil
}

func unlockVT(fd uintptr) error {
    m := vtMode{mode: VT_AUTO}
    _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, VT_SETMODE, uintptr(unsafe.Pointer(&m)))
    if errno != 0 {
        return errno
    }
    return nil
}

// Respond to kernel's VT-release request (SIGUSR1 handler):
func reldisp(fd uintptr) error {
    _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, VT_RELDISP, 1)
    if errno != 0 {
        return errno
    }
    return nil
}
```

The lock/unlock transitions map to the three VT lock modes (no-lock, manual, auto-on-select). In no-lock mode, `lockVT` is never called. The SIGUSR1 handler must call `reldisp` to acknowledge the release — if it doesn't, the VT switch hangs indefinitely for the user.

### Pattern 3: bubbletea Model Architecture

The root model holds the list pane, detail pane, VT lock state, and references the `approval.Manager` via a message channel (not direct reference — bubbletea is single-threaded in Update/View).

```go
// Source: bubbletea Elm architecture pattern (pkg.go.dev docs)
type model struct {
    list      list.Model         // bubbles/list for left pane
    detail    detailPane         // custom component for right pane
    history   []approval.HistoryEntry
    lockMode  LockMode           // NoLock | ManualLock | AutoLock
    vtLocked  bool
    vtFD      uintptr            // file descriptor of /dev/ttyN
    width     int
    height    int
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        // Critical: store dimensions, pass proportional widths to each pane
        m.width, m.height = msg.Width, msg.Height
        leftW := m.width / 3
        rightW := m.width - leftW
        m.list.SetSize(leftW, m.height)
        m.detail.SetSize(rightW, m.height)
    case NewRequestMsg:
        // Injected via p.Send() from D-Bus goroutine
        m.list.InsertItem(0, requestItem{req: msg.Request})
    case tea.KeyMsg:
        switch msg.String() {
        case "y":
            if m.canApprove() {
                return m, approveCmd(m.selectedRequest())
            }
        case "n":
            if m.canApprove() {
                return m, denyCmd(m.selectedRequest())
            }
        }
    }
    // ... delegate to list, detail
}
```

External injection uses `p.Send()` — this is the bridge between the D-Bus goroutine and the TUI:

```go
// From D-Bus handler goroutine (outside bubbletea event loop):
p.Send(NewRequestMsg{Request: req})
// From approval.Manager observer callback:
p.Send(RequestResolvedMsg{ID: req.ID, Resolution: resolution})
```

### Pattern 4: D-Bus Method Handlers

Phase 5 adds real methods to the dispatcher. The handlers block until the approval manager resolves the request:

```go
// On net.mowaka.SecretsDispatcher1 interface (custom, not Secret Service)
func (d *Dispatcher) RequestSecret(sender dbus.Sender, path string) (string, *dbus.Error) {
    senderInfo := d.resolver.Resolve(string(sender))
    procChain := procchain.Walk(senderInfo.PID, 5)

    req := &ApprovalRequest{Path: path, SenderInfo: senderInfo, ProcChain: procChain}

    // Inject into TUI
    d.program.Send(NewRequestMsg{Request: req})

    // Block until TUI resolves it
    result, err := d.approvalMgr.Wait(req.ID)
    if err != nil {
        return "", &dbus.Error{Name: "net.mowaka.Error.Timeout",
            Body: []interface{}{"Request timed out — approve on VT8 (Ctrl+Alt+F8)"}}
    }
    if !result {
        return "", &dbus.Error{Name: "net.mowaka.Error.Denied",
            Body: []interface{}{"Request denied by user"}}
    }
    secret, err := d.store.Get(path)
    if err != nil {
        return "", &dbus.Error{Name: "net.mowaka.Error.StoreError", Body: []interface{}{err.Error()}}
    }
    return secret, nil
}
```

**DBUS-03 clarification:** The freedesktop Secret Service protocol is already fully implemented in `internal/proxy/`. The daemon in Phase 5 does NOT re-expose the full Secret Service protocol on the system bus (that would require implementing Collection/Item object paths, which is Phase 7 territory). Instead, Phase 5 adds a simpler custom method `RequestSecret(path string) -> (secret string)` that the desktop-side caller (future Phase 6 thin client) calls. The existing `internal/dbus/types.go` structures remain available.

### Pattern 5: Process Chain Traversal

```go
// Source: Linux /proc filesystem layout (verified)
// /proc/{pid}/status contains "PPid: N" — the parent PID
func Walk(pid uint32, maxDepth int) []ProcInfo {
    var chain []ProcInfo
    current := pid
    for i := 0; i < maxDepth && current > 1; i++ {
        info, err := readProcInfo(current)
        if err != nil {
            break
        }
        chain = append(chain, info)
        current = info.PPid
    }
    return chain
}

func readProcInfo(pid uint32) (ProcInfo, error) {
    // Read /proc/{pid}/status for Name and PPid
    // Read /proc/{pid}/comm for short command name
    // Read /proc/{pid}/cwd (symlink) for working directory
    data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
    // parse "Name:", "PPid:" fields
    ...
}
```

The v1 codebase already has `SenderInfoResolver` using D-Bus for PID. Phase 5 adds `/proc` traversal on top of that. Note: `readlink /proc/{pid}/cwd` may fail for processes of other users even as the same UID — handle gracefully.

### Pattern 6: GPG-02 (pinentry-tty on the VT)

The companion's gpg-agent needs its pinentry to use the approval VT. Set in the companion's `~/.gnupg/gpg-agent.conf`:

```
pinentry-program /usr/bin/pinentry-tty
```

And in the companion's systemd user unit (`Environment=` stanza):

```
Environment=GPG_TTY=/dev/tty8
```

With `--keep-tty` (or `keep-tty` in gpg-agent.conf), the agent will always use VT8 for pinentry regardless of which TTY triggered the signing request. This means the passphrase prompt appears on the VT TUI when GPG needs it.

**GPG-03:** The existing `internal/gpgsign` package already has a `Run()` function that invokes real gpg. The daemon reuses this as-is — on approval, it calls gpg with the companion's keyring (`GNUPGHOME` pointing to companion's `~/.gnupg`).

### Pattern 7: D-Bus Activation File (DBUS-07)

To return an actionable error when the daemon is not running:

```
# /usr/share/dbus-1/system-services/net.mowaka.SecretsDispatcher1.service
[D-BUS Service]
Name=net.mowaka.SecretsDispatcher1
Exec=/usr/bin/secrets-dispatcher daemon
User=secrets-nb
```

The dbus-daemon will attempt to start the service when a client calls a method. If it fails to start (e.g. companion user not provisioned), the caller receives `org.freedesktop.DBus.Error.SpawnFailed` or the daemon's own startup error. For a better user experience, the daemon's startup code should emit a descriptive error to journald before exiting.

### Anti-Patterns to Avoid

- **Blocking Update/View:** Never block `Update()` or `View()` — D-Bus blocking waits must run in a separate goroutine; communicate results to TUI via `p.Send()`.
- **Direct model mutation from D-Bus goroutine:** bubbletea is NOT goroutine-safe inside Update/View. Only `p.Send()` is safe to call from outside goroutines.
- **Opening /dev/ttyN for O_RDONLY:** Must open O_RDWR — bubbletea needs to write escape sequences to the same fd.
- **Forgetting VT_RELDISP on SIGUSR1:** If the SIGUSR1 handler does not call `ioctl(fd, VT_RELDISP, 1)`, the VT switch hangs forever (keyboard and mouse become unresponsive on adjacent VTs).
- **Calling VT_SETMODE on wrong fd:** Must call it on the fd for `/dev/ttyN`, not on `/dev/tty` (which redirects to the current controlling terminal).
- **SIGKILL and VT_PROCESS:** SIGKILL cannot be caught. However, the Linux kernel automatically releases VT_PROCESS mode when the owning process dies — no recovery handler needed for kill -9. The `defer unlockVT()` handles clean shutdown; SIGKILL is handled by the kernel.
- **WindowSizeMsg accounting:** lipgloss borders add 2 to each dimension. When setting pane widths from `WindowSizeMsg`, subtract border widths before calling `list.SetSize()` to avoid horizontal overflow.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| TUI list navigation | Custom arrow-key list | `bubbles/list` | Handles page overflow, filtering, keyboard bindings, item selection |
| Countdown timer | time.After + model.tick | `bubbles/timer` | Handles tick command lifecycle, expiry, reset |
| Key bindings | String comparison on raw keys | `bubbles/key` | Composable keymaps with help text generation |
| D-Bus structs | Custom type marshaling | Existing `internal/dbus/types.go` | Already defined and tested; Secret type, error constructors |
| Approval request tracking | Custom channel/map | Existing `approval.Manager` | 400-line tested implementation with timeout, history, observers |
| GPG signing | Re-implement gpg invocation | Existing `internal/gpgsign` | Already handles commit object parsing, status fd, exit code propagation |
| Process name lookup | Custom /proc parser | Simple `readProcInfo()` helper | Only 4 fields needed: Name, PPid, comm, cwd — 30 lines total |

**Key insight:** This project already has a mature v1 implementation. The primary job of Phase 5 is wiring existing packages together, not building new infrastructure.

## Common Pitfalls

### Pitfall 1: VT_SETMODE Race with Display Manager

**What goes wrong:** On systems with GDM/SDDM running on VT1 and desktop on VT2, a display manager may attempt to reclaim VTs on login/logout. If the daemon calls `VT_SETMODE(VT_PROCESS)` on VT8 before or during a display manager operation, the ioctl may be denied or cause a deadlock.

**Why it happens:** The DM also uses `VT_SETMODE` to manage its VT; conflicting `VT_PROCESS` holders prevent switches.

**How to avoid:** Only call `VT_SETMODE(VT_PROCESS)` when a request is actively pending (not at daemon startup). Validate VT8 is not already owned by another process before calling. Log and continue without VT lock if the ioctl fails — degraded to "no locking" mode.

**Warning signs:** `ioctl(VT_SETMODE)` returns EPERM or EACCES; VT switching hangs after daemon starts; note STATE.md already flags this as "Phase 5: VT_SETMODE race with display manager (Ubuntu Bug #290197)".

**Mitigation:** Start in "no locking" mode; let the admin enable VT_PROCESS mode explicitly; validate with an integration test that ioctl call on a real ttyN succeeds.

### Pitfall 2: bubbletea goroutine safety

**What goes wrong:** The D-Bus handler calls into the bubbletea model directly, causing data races on model state.

**Why it happens:** bubbletea's `Update` and `View` run on a single goroutine. Any mutation from another goroutine is a race.

**How to avoid:** The D-Bus handler goroutine only calls `p.Send(msg)` — never accesses model fields. The `approval.Manager.Observer` callback also only calls `p.Send()`.

**Warning signs:** `-race` flag failures; TUI shows stale state; cursor jumps.

### Pitfall 3: Missing VT_RELDISP acknowledgment

**What goes wrong:** User presses Ctrl+Alt+Fn to switch VTs, but the system freezes — no VT switching works until daemon is killed.

**Why it happens:** In VT_PROCESS mode, the kernel sends SIGUSR1 to the owning process when a switch is requested. The process must call `ioctl(fd, VT_RELDISP, 1)` to grant the switch. If the signal handler is not registered or doesn't call reldisp, the switch request stalls indefinitely.

**How to avoid:** Register `signal.Notify(ch, syscall.SIGUSR1)` immediately after calling `VT_SETMODE(VT_PROCESS)`. The SIGUSR1 goroutine calls `reldisp(fd)` unconditionally (the TUI has already snapshotted state by the time the switch completes). SIGUSR2 (acquire signal) can be used to re-enter lock mode if VT returns.

**Warning signs:** Keyboard stops responding on all VTs; only a hard reset recovers.

### Pitfall 4: Default bubbletea output goes to /dev/tty, not /dev/ttyN

**What goes wrong:** bubbletea renders to the controlling terminal of the daemon process, not VT8. The VT8 stays blank.

**Why it happens:** Without `WithOutput(f)`, bubbletea uses `os.Stdout` or opens `/dev/tty` — which resolves to the daemon's controlling terminal, likely a pty or the terminal where the daemon was launched.

**How to avoid:** Always pass `WithInput(vtFile)` AND `WithOutput(vtFile)` where `vtFile` is the opened `/dev/ttyN` fd.

**Warning signs:** TUI output appears in the launch terminal; VT8 shows only a blank cursor.

### Pitfall 5: pinentry-tty can't access /dev/ttyN as companion user

**What goes wrong:** gpg-agent calls pinentry-tty but gets "Permission denied" opening `/dev/ttyN`.

**Why it happens:** VT device files are owned by `root:tty`, mode 0620. The companion user must be in the `tty` group to write to VT devices.

**How to avoid:** Provisioning (Phase 4 `companion.Provision`) must add the companion user to the `tty` group. Verify with `stat /dev/tty8` and `id secrets-nb | grep tty`. Add a check in `internal/companion/check.go`.

**Warning signs:** gpg-agent logs "pinentry-tty: cannot open tty '/dev/tty8': Permission denied"; GPG signing hangs silently.

### Pitfall 6: D-Bus method blocks, starves the event loop

**What goes wrong:** While a D-Bus method (e.g. `RequestSecret`) is blocked waiting for user approval, no new D-Bus messages can be processed — a second simultaneous request hangs.

**Why it happens:** By default, godbus/dbus routes all method calls serially on one goroutine.

**How to avoid:** Use `conn.Export(obj, path, iface)` with goroutine-safe methods, and confirm that godbus dispatches each method call in its own goroutine (it does — godbus v5 calls handler methods concurrently). The `approval.Manager.Wait()` blocking is per-request, so multiple concurrent callers each block independently. Verify with a concurrent integration test.

**Warning signs:** Second D-Bus call never returns while first is pending.

## Code Examples

Verified patterns from official sources and existing codebase:

### Opening /dev/ttyN and running bubbletea

```go
// Source: bubbletea pkg.go.dev API, os.OpenFile stdlib
func StartVT(vtPath string, m tea.Model) (*tea.Program, error) {
    f, err := os.OpenFile(vtPath, os.O_RDWR, 0)
    if err != nil {
        return nil, fmt.Errorf("open %s: %w", vtPath, err)
    }
    p := tea.NewProgram(m,
        tea.WithInput(f),
        tea.WithOutput(f),
        tea.WithAltScreen(),
    )
    return p, nil
}
```

### Two-pane lipgloss layout from WindowSizeMsg

```go
// Source: lipgloss pkg.go.dev JoinHorizontal, Style.Width/Height
func (m model) View() string {
    leftW := m.width / 3
    rightW := m.width - leftW

    left := lipgloss.NewStyle().
        Width(leftW - 2).  // subtract 2 for border
        Height(m.height - 2).
        Border(lipgloss.NormalBorder()).
        Render(m.list.View())

    right := lipgloss.NewStyle().
        Width(rightW - 2).
        Height(m.height - 2).
        Border(lipgloss.NormalBorder()).
        Render(m.detail.View())

    return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}
```

### VT_SETMODE using syscall (no external deps beyond stdlib + x/sys)

```go
// Source: /usr/include/linux/vt.h (verified on this machine)
// Constants confirmed: VT_SETMODE=0x5602, VT_AUTO=0, VT_PROCESS=1, VT_RELDISP=0x5605
import (
    "syscall"
    "unsafe"
)

type vtMode struct {
    mode   uint8
    waitv  uint8
    relsig int16
    acqsig int16
    frsig  int16
}

const (
    vtSetMode = 0x5602
    vtRelDisp = 0x5605
    vtModeAuto    = 0x00
    vtModeProcess = 0x01
)

func engageVTProcess(fd uintptr) error {
    m := vtMode{
        mode:   vtModeProcess,
        relsig: int16(syscall.SIGUSR1),
        acqsig: int16(syscall.SIGUSR2),
    }
    _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, vtSetMode, uintptr(unsafe.Pointer(&m)))
    if err != 0 {
        return err
    }
    return nil
}

func restoreVTAuto(fd uintptr) error {
    m := vtMode{mode: vtModeAuto}
    _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, vtSetMode, uintptr(unsafe.Pointer(&m)))
    if err != 0 {
        return err
    }
    return nil
}

func acknowledgeVTRelease(fd uintptr) error {
    _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, vtRelDisp, 1)
    if err != 0 {
        return err
    }
    return nil
}
```

### approval.Manager as bubbletea observer bridge

```go
// Source: existing internal/approval/manager.go Observer pattern
type tuiObserver struct {
    program *tea.Program
}

func (o *tuiObserver) OnEvent(e approval.Event) {
    switch e.Type {
    case approval.EventRequestCreated:
        o.program.Send(NewRequestMsg{Request: e.Request})
    case approval.EventRequestExpired, approval.EventRequestApproved, approval.EventRequestDenied:
        o.program.Send(RequestResolvedMsg{ID: e.Request.ID})
    }
}
```

### Process chain traversal

```go
// Source: Linux /proc filesystem (verified)
type ProcInfo struct {
    PID  uint32
    PPid uint32
    Name string // from /proc/pid/comm
    CWD  string // from readlink /proc/pid/cwd
}

func Walk(startPID uint32, maxDepth int) []ProcInfo {
    var chain []ProcInfo
    current := startPID
    seen := map[uint32]bool{}
    for i := 0; i < maxDepth; i++ {
        if current <= 1 || seen[current] {
            break
        }
        seen[current] = true
        info, err := readProc(current)
        if err != nil {
            break
        }
        chain = append(chain, info)
        current = info.PPid
    }
    return chain
}
```

### D-Bus activation service file

```ini
# /usr/share/dbus-1/system-services/net.mowaka.SecretsDispatcher1.service
# Source: dbus.freedesktop.org/doc/system-activation.txt (verified)
[D-BUS Service]
Name=net.mowaka.SecretsDispatcher1
Exec=/usr/bin/secrets-dispatcher daemon
User=secrets-nb
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| bubbletea v0.x (pre-stable) | bubbletea v1.x (stable, v2 in RC) | Nov 2024 (v1.0) / Oct 2025 (v2.0) | v1 API is stable; v2 has new module path `charm.land/bubbletea/v2` |
| lipgloss v0.x | lipgloss v1.1.0 | Mar 2025 | `v2` also available but v1 is production-ready |
| godbus session bus only | godbus system bus (v5.2.2) | already used in Phase 4 | No change needed |

**Deprecated/outdated:**
- lipgloss `.Copy()` method: deprecated in v1 in favor of method chaining from scratch (`lipgloss.NewStyle().Bold(true)...` not `.Copy().Bold(true)`)
- bubbletea v2 module path `github.com/charmbracelet/bubbletea/v2` was moved to `charm.land/bubbletea/v2` — don't use the github path for v2

## Open Questions

1. **VT_PROCESS mode + display manager race (Ubuntu Bug #290197)**
   - What we know: GDM and SDDM may interfere with VT_SETMODE on non-console VTs; the bug is confirmed in Ubuntu's tracker
   - What's unclear: Whether this affects VT8 specifically (DMs typically manage VT1-7); whether Arch Linux (this system: Linux 6.18.8-zen2-1-zen) exhibits the issue
   - Recommendation: Default to "no locking" mode; make VT_PROCESS opt-in; add an integration test that validates VT_SETMODE succeeds on `/dev/tty8` using a kernel ioctl before engaging it in production

2. **godbus concurrent method dispatch**
   - What we know: godbus v5 dispatches method calls; the docs don't explicitly state goroutine-per-call behavior
   - What's unclear: Whether multiple concurrent D-Bus callers each get their own goroutine (confirmed behavior from v1's proxy package which handles concurrent callers)
   - Recommendation: Add a concurrent integration test with two simultaneous `RequestSecret` calls; verify both resolve independently

3. **bubbletea rendering to /dev/tty8 without a controlling terminal**
   - What we know: bubbletea uses `WithInput`/`WithOutput` with raw file handles; the daemon has no controlling terminal (runs under systemd)
   - What's unclear: Whether bubbletea's terminal size detection (`WindowSizeMsg`) works when input/output is a VT rather than a pty
   - Recommendation: The VT device supports `TIOCGWINSZ` (same ioctl bubbletea uses for size detection) — should work. Validate in the first test run with a manual `stty size < /dev/tty8` equivalent.

4. **companion user tty group membership**
   - What we know: `/dev/ttyN` is mode 0620 owned by `root:tty`; the companion user needs `tty` group membership
   - What's unclear: Whether provisioning (Phase 4) already adds the companion to the `tty` group
   - Recommendation: Check `internal/companion/provision.go`; add `tty` group membership if missing; add to `check.go` companion checks

## Sources

### Primary (HIGH confidence)
- `/usr/include/linux/vt.h` — VT ioctl constants verified directly: VT_SETMODE=0x5602, VT_RELDISP=0x5605, VT_AUTO=0, VT_PROCESS=1, struct vt_mode layout
- `pkg.go.dev/github.com/charmbracelet/bubbletea` — Model interface, WithInput/WithOutput, tea.NewProgram, tea.WindowSizeMsg API
- `pkg.go.dev/github.com/charmbracelet/lipgloss` — JoinHorizontal, Style.Width/Height, border API (v1.1.0)
- `pkg.go.dev/github.com/charmbracelet/bubbles/list` — list.New, list.Item interface, SelectedItem(), SetSize() API
- `specifications.freedesktop.org/secret-service/0.2` — Secret Service D-Bus method signatures (confirmed match existing internal/dbus/types.go)
- `dbus.freedesktop.org/doc/system-activation.txt` — D-Bus service activation file format: [D-BUS Service] with Name, Exec, User fields
- Existing codebase: `internal/approval/manager.go`, `internal/daemon/daemon.go`, `internal/gpgsign/run.go`, `internal/proxy/service.go`, `internal/proxy/senderinfo.go`

### Secondary (MEDIUM confidence)
- `man7.org/linux/man-pages/man2/ioctl_vt.2.html` — VT_PROCESS, VT_RELDISP semantics (cross-confirmed with linux/vt.h)
- `dottedmag.net/blog/02-de-vt/` — VT_PROCESS signal mechanism and crash recovery (kernel auto-recovers on process death)
- `gnupg.org GPG-AGENT(1)` — `--pinentry-program` and `--keep-tty` options for directing pinentry to VT
- `leg100.github.io/en/posts/building-bubbletea-programs/` — WindowSizeMsg layout calculations, component tree pattern

### Tertiary (LOW confidence)
- Ubuntu Bug #290197 mention — VT_SETMODE race with display manager (flagged in STATE.md; not independently verified for current kernel 6.18.8)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries verified via pkg.go.dev and existing go.mod/go.sum
- Architecture: HIGH — patterns derived from existing v1 codebase + verified API docs
- VT ioctl: HIGH — constants verified from `/usr/include/linux/vt.h` on this machine
- GPG pinentry: MEDIUM — gnupg docs verified; runtime behavior with companion user requires validation
- Pitfalls: MEDIUM — most from source docs; VT race bug is LOW (flagged as open question)

**Research date:** 2026-02-26
**Valid until:** 2026-08-26 (bubbletea v1 API is stable; VT kernel API is stable; 6-month window)
