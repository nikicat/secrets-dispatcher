# Domain Pitfalls: Linux Privilege Separation with VT Trusted I/O

**Domain:** Privilege-separated secret management daemon — companion user, system D-Bus, VT trusted I/O
**Researched:** 2026-02-25
**Milestone:** v2.0 — privilege separation + VT trusted I/O

---

## Critical Pitfalls

Mistakes that cause security model failures, silent functional breakage, or rearchitecting.

---

### Pitfall 1: D-Bus Policy File Missing `<allow own>` for the Companion User — Service Silently Rejected

**What goes wrong:** The companion user (`secrets-nb`) attempts to claim `net.nikicat.SecretsDispatcher` on the system bus. Without an explicit `<allow own="net.nikicat.SecretsDispatcher"/>` rule scoped to that user in a policy conf file, the bus returns `org.freedesktop.DBus.Error.AccessDenied`. The daemon exits. The error message gives no hint about where to add the policy.

**Why it happens:** The system bus defaults to deny-all for `own` and `send` method calls. Developers add `<allow own>` for root or the `messagebus` group but forget to scope it to the specific companion user. Or they add the rule to the wrong file scope — `<policy context="default">` instead of `<policy user="secrets-nb">`.

**Consequences:** The companion daemon fails to start. All secret requests block forever. Users see no desktop notification. The failure is silent from the desktop user's perspective.

**Prevention:**
- Write the policy file first, before the daemon code. Verify with `dbus-send` from the companion user before writing Go.
- Use `<policy user="secrets-nb">` scope, not `<policy context="default">`.
- Minimum viable policy: one `<allow own="net.nikicat.SecretsDispatcher"/>` rule and one `<allow send_destination="net.nikicat.SecretsDispatcher"/>` rule scoped to the desktop user (`nb`).
- After writing the conf file, reload with `sudo systemctl reload dbus` and verify: `busctl --system list | grep SecretsDispatcher`.

**Detection:** `dbus-daemon` logs "Connection ":1.X" is not allowed to own the service "net.nikicat.SecretsDispatcher"". In Go: `RequestName` returns `AccessDenied` error.

**Warning signs:** Daemon exits immediately at startup with a D-Bus error; `busctl --system status net.nikicat.SecretsDispatcher` shows "not running".

**Phase:** Phase 1 — system D-Bus interface design and provisioning tool.

---

### Pitfall 2: `send_destination` Policy Scoped to Interface Instead of Name — All Messages Blocked

**What goes wrong:** The policy file uses `<allow send_interface="net.nikicat.SecretsDispatcher.Requests"/>` instead of `<allow send_destination="net.nikicat.SecretsDispatcher"/>`. The interface field in D-Bus messages is optional. Any call without an explicit interface header — including introspection, property gets, and some method calls — is blocked by the interface-scoped rule but would be allowed by a destination-scoped rule.

**Why it happens:** Developers copy policy examples and see `send_interface` as more specific/secure. The D-Bus spec warning ("do NOT specify `<deny send_interface="..."/>`") is in the manual but easy to miss.

**Consequences:** Partial functionality — some method calls succeed, others fail with `AccessDenied`. Hard to diagnose because the symptom looks like a Go client bug rather than a policy bug.

**Prevention:**
- Use `<allow send_destination="net.nikicat.SecretsDispatcher"/>` exclusively. Never use `send_interface` in allow rules.
- Test every method call path against the running policy with `dbus-monitor --system` before declaring the policy complete.

**Detection:** Some method calls succeed; calls without interface headers (like `org.freedesktop.DBus.Introspectable.Introspect`) fail with `AccessDenied`.

**Phase:** Phase 1 — D-Bus policy file authoring.

---

### Pitfall 3: VT_SETMODE VT_PROCESS — Crash Leaves VT Unreleasable

**What goes wrong:** The companion daemon sets the approval VT to `VT_PROCESS` mode (to control VT switching during approvals). If the daemon crashes while in VT_PROCESS mode, the kernel waits for the release signal (`VT_RELDISP`) that the dead process will never send. The VT appears frozen — the user cannot switch away from the approval TUI screen using Ctrl+Alt+Fn.

**Why it happens:** When a process sets VT_PROCESS mode, it takes ownership of VT switching for that terminal. The kernel sends `relsig` (typically SIGUSR1) to the process when a switch away is requested. If that process is gone, the kernel waits indefinitely for acknowledgment. (Note: the kernel eventually force-resets the VT to `VT_AUTO` mode if the owning process dies, but this behavior is implementation-dependent and slow.)

**Consequences:** User cannot switch VTs. If the approval VT was showing during a crash, the display is stuck. Recovery requires switching to another TTY via SSH or SysRq.

**Prevention:**
- Install a signal handler for SIGUSR1 that immediately calls `ioctl(VT_RELDISP, 1)` when the daemon is not mid-approval. This releases the VT on request.
- Install a `defer` cleanup in Go that calls `ioctl(VT_SETMODE, VT_AUTO)` on any daemon exit path (normal or panic). The VT_AUTO mode hands control back to the kernel.
- Use `SA_RESETHAND` on the release signal so a double-crash doesn't loop.
- Test crash recovery explicitly: kill -9 the daemon while VT is in VT_PROCESS mode and verify the user can switch VTs within 5 seconds.

**Detection:** After daemon crash, Ctrl+Alt+F1 through Ctrl+Alt+F7 produce no VT switch.

**Warning signs:** Any process that sets VT_PROCESS without a cleanup handler is a potential VT-lock bomb.

**Phase:** Phase 2 — VT management implementation.

---

### Pitfall 4: VT_SETMODE Race — Another Process Resets VT_AUTO Between ACTIVATE and SETMODE

**What goes wrong:** The standard VT acquisition sequence is: (1) `ioctl(TIOCSCTTY)` to make the VT the controlling terminal, (2) `ioctl(VT_SETMODE, VT_PROCESS)`. Between these two steps, another process (e.g., a udev rule, systemd-logind, or gdm) can call `VT_SETMODE` on the same VT, resetting it to `VT_AUTO`. The daemon's VT_SETMODE call then silently succeeds but has no effect — the VT is back in auto mode and the kernel will switch away from it without asking.

**Why it happens:** The VT switching API is inherently racy by design (confirmed in Ubuntu bug #290197). There is no atomic "acquire and lock" primitive. udev and display managers do set VT modes as part of their own session management.

**Consequences:** The approval VT can be switched away from while the user is typing their approval input. An adversarial desktop process with `chvt` access could yank the user back to the desktop mid-approval, then switch back after the user has approved. This breaks the trusted I/O guarantee.

**Prevention:**
- After setting VT_PROCESS mode, perform a verification read: call `ioctl(VT_GETMODE)` and confirm the mode is still `VT_PROCESS`. If not, retry with a short backoff.
- Hold a file descriptor open on the VT device (`/dev/ttyN`) for the lifetime of the approval session. This prevents `TIOCSCTTY` by other processes.
- Accept that on Wayland-dominant systems, the compositor (running as the desktop user) has legitimate reasons to manage VTs. The VT isolation is a best-effort security measure, not a cryptographic guarantee.
- Document in the threat model: VT isolation protects against software in the desktop session that does not specifically target VT management. A process that deliberately calls `chvt` can still switch — this is a known kernel-level limitation.

**Detection:** VT switches happen during approval sessions when they should not. `ioctl(VT_GETMODE)` after `VT_SETMODE` returns `VT_AUTO`.

**Warning signs:** Display manager (GDM/SDDM) managing the same VT range as the companion daemon.

**Phase:** Phase 2 — VT management + security review.

---

### Pitfall 5: PAM `open_session` Timing — Companion Session Not Ready When Desktop Login Completes

**What goes wrong:** The PAM hook (pam_exec or a custom PAM module) triggers on `open_session` for the desktop user's login. It attempts to start the companion user's session. But the companion session start involves: starting `systemd --user` for `secrets-nb`, waiting for D-Bus session bus to be available, unlocking the gopass store (which may require passphrase). The PAM `open_session` call returns before any of this completes. The user's desktop loads and immediately tries to use Secret Service — but the companion session is still booting.

**Why it happens:** PAM does not wait for background services. `pam_exec` with a shell script that calls `systemctl start secrets-nb-session.service` returns as soon as systemctl accepts the request, not when the service is actually ready. `systemd-logind` confirmed this behavior (github.com/systemd/systemd issue #2863): pam_open_session blocks with a timeout, but the companion service itself is async.

**Consequences:** Desktop applications that call Secret Service at startup (GNOME Keyring clients, ssh-agent, browser password managers) receive "service unavailable" and cache that state. Some never retry. The user has to manually restart those applications after the companion session finishes starting.

**Prevention:**
- Do not make PAM block on companion session readiness. PAM has a hard timeout; blocking too long prevents login entirely.
- Instead: have the user-agent (running in the desktop session) poll or watch the system D-Bus for the companion service to appear. The user-agent should retry Secret Service calls with exponential backoff for the first 30 seconds.
- The companion session must publish a "ready" signal on the system D-Bus once the store is unlocked and gopass-secret-service is accepting requests.
- Add a `WantedBy=default.target` to the companion's user service file so it starts automatically without PAM involvement where possible.

**Detection:** `secret-tool lookup` immediately after login returns "No such secret" but succeeds 10 seconds later. journald shows companion session still initializing when desktop application first connects.

**Warning signs:** Any synchronous dependency between PAM login completion and companion session readiness is fragile.

**Phase:** Phase 3 — PAM hook + companion session lifecycle.

---

### Pitfall 6: `systemd --user` for Companion User Requires Linger — Session Dies on No Logins

**What goes wrong:** The companion user `secrets-nb` is never interactively logged in. Without linger enabled (`loginctl enable-linger secrets-nb`), systemd destroys the companion user's `systemd --user` instance and removes `/run/user/$(id -u secrets-nb)` as soon as there are no active logind sessions for that user. Since `secrets-nb` never logs in interactively, it never has a session, so `systemd --user` for `secrets-nb` never starts at all — or if started manually, is torn down.

**Why it happens:** `systemd --user` is designed as a per-session process, not a persistent daemon. The linger feature is specifically for background service users, but it must be explicitly enabled and is not the default.

**Consequences:** `XDG_RUNTIME_DIR=/run/user/<uid>` does not exist for `secrets-nb`. The companion session's D-Bus socket does not exist. `gopass-secret-service` has nowhere to put its socket. The companion daemon cannot start.

**Prevention:**
- The provisioning tool must call `loginctl enable-linger secrets-nb` during setup. This is a required provisioning step, not optional.
- Verify linger is enabled: `loginctl show-user secrets-nb | grep Linger`.
- In the provisioning tool's post-install verification, check that `/run/user/$(id -u secrets-nb)/bus` exists after a 5-second wait.
- Document: if the companion session mysteriously stops working after an upgrade or reboot, check linger first.

**Detection:** `systemctl --user --machine=secrets-nb@ status` fails with "Failed to connect to bus: No such file or directory".

**Warning signs:** `/run/user/<companion-uid>` directory absent.

**Phase:** Phase 3 — provisioning tool.

---

### Pitfall 7: gpg-agent for Companion User Has No VT TTY — Pinentry Cannot Display

**What goes wrong:** The companion user's gpg-agent needs to prompt for the GPG passphrase (on first use, or after cache expiry). The companion session has no graphical display (`$DISPLAY` and `$WAYLAND_DISPLAY` are unset). The companion's gpg-agent tries to invoke pinentry — either `pinentry-gnome3` fails because there is no Wayland compositor, or `pinentry-curses` fails because there is no controlling terminal. The passphrase prompt silently times out. GPG exits non-zero. The signing request fails.

**Why it happens:** pinentry requires either a graphical session (DISPLAY/WAYLAND_DISPLAY) or a TTY (`GPG_TTY` set to a real device). The companion user's systemd --user session has neither by default because it is started by linger/PAM, not by a TTY login.

**Consequences:** The companion session is functional for Secret Service requests (gopass handles cached secrets), but GPG signing fails whenever the gpg-agent passphrase cache is cold. First use after each boot fails silently.

**Prevention:**
- Configure pinentry for the companion user to use the approval VT: set `pinentry-program /usr/bin/pinentry-tty` in `~secrets-nb/.gnupg/gpg-agent.conf`.
- Set `GPG_TTY=/dev/tty8` (the companion's VT) in the companion session's environment (via `systemd --user` service `Environment=` or a `~/.profile` sourced by the session).
- Alternatively, configure the approval TUI to act as a pinentry — intercept passphrase prompts and display them in the TUI. This requires implementing the Assuan pinentry protocol, which is non-trivial.
- The simplest production approach: require the user to unlock the GPG agent manually on first boot (by running `gpg --card-status` or similar on the companion VT), then rely on long `default-cache-ttl-ssh` and `default-cache-ttl` in gpg-agent.conf.
- Test by stopping and restarting the companion session and verifying GPG signing works without any manual intervention.

**Detection:** `gpg: signing failed: Inappropriate ioctl for device` or `gpg: agent: agent did not return a passphrase` in companion session logs.

**Warning signs:** First signing request after boot always fails, subsequent ones succeed (cache was cold).

**Phase:** Phase 2 — GPG keyring setup + companion session configuration.

---

### Pitfall 8: Secret Service Proxy Cannot Forward D-Bus Object Paths Across Bus Boundaries

**What goes wrong:** The user-agent claims `org.freedesktop.secrets` on the desktop session bus and proxies calls to the companion's system D-Bus interface. The Secret Service protocol uses D-Bus object paths as session handles and collection references. When the proxy returns an object path like `/org/freedesktop/secrets/session/s1` to a caller on the session bus, the caller expects to be able to call methods on that path via `org.freedesktop.secrets` — on the same bus. But the real object lives on the companion session bus, not the desktop session bus. The proxy must intercept every subsequent call to that path and forward it, maintaining a mapping table.

**Why it happens:** D-Bus object paths are bus-relative references. They are not portable across bus instances. The Secret Service protocol is designed for single-bus use and assumes the service and client share a bus. The cross-bus proxy pattern requires the user-agent to be a full protocol-level proxy, not a thin forwarder.

**Consequences:** `session.Close()` calls fail — the caller tries to call a method on a path that only exists on the companion session bus, but the user-agent does not intercept that path. Sessions leak. `collection.SearchItems()` returns item paths that are unreachable on the session bus. libsecret clients get confused and fall back to plaintext storage.

**Prevention:**
- The user-agent must maintain a bidirectional path mapping: session bus paths → companion bus paths, and forward all calls.
- Study the existing `internal/proxy` package: it already implements this forwarding pattern (remote socket → local session bus). The v2.0 user-agent should reuse this architecture, but bridging system D-Bus → desktop session D-Bus instead.
- Write integration tests that exercise the full Secret Service flow through the proxy: open session, create item, get secret, close session. Verify path forwarding at each step.

**Detection:** `secret-tool store` works but `secret-tool lookup` fails with "No such object". D-Bus introspection on the desktop session bus shows paths that cannot be called.

**Warning signs:** Any proxy that returns object paths from the remote bus without rewriting them to local paths.

**Phase:** Phase 4 — user-agent Secret Service proxy implementation.

---

## Moderate Pitfalls

---

### Pitfall 9: GNUPGHOME Ownership Check Breaks Companion User's gopass

**What goes wrong:** The companion user `secrets-nb` has its GPG keyring at `~secrets-nb/.gnupg/`. Since GPG 2.2.17+, GPG performs an ownership check on `GNUPGHOME` and warns (and in some configurations, errors) if the directory or its contents are not owned by the running user. If the provisioning tool creates `~secrets-nb/.gnupg/` as root and does not `chown` it, all GPG operations under `secrets-nb` emit warnings that break non-interactive scripts.

**Prevention:**
- All provisioning operations that touch `~secrets-nb/.gnupg/` must be done as `secrets-nb` (via `sudo -u secrets-nb`) or must explicitly `chown secrets-nb:secrets-nb` the entire tree afterward.
- Test: `sudo -u secrets-nb gpg --list-keys` must produce no warnings about permissions.
- Never export `GNUPGHOME` pointing to another user's gnupg directory — GPG rejects this silently or with cryptic errors since v1.8.2 of gopass.

**Detection:** `gpg: WARNING: unsafe permissions on homedir '/home/secrets-nb/.gnupg'` in logs.

**Phase:** Phase 3 — provisioning tool.

---

### Pitfall 10: dbus-broker Rejects Deprecated Policy Attributes — `eavesdrop` and `at_console`

**What goes wrong:** On systems using dbus-broker (default on Arch Linux and Fedora as of 2024), D-Bus policy attributes `eavesdrop="true"` and `at_console="true"` are deprecated and ignored. If the policy file for the system D-Bus service uses either of these, dbus-broker logs warnings and the rules have no effect. Code relying on eavesdrop to monitor signals will receive nothing.

**Prevention:**
- Do not use `eavesdrop` in any policy rule. Use `BecomeMonitor()` for bus monitoring.
- Do not rely on `at_console` for access control. Use explicit `<policy user="...">` rules instead.
- Test the policy file against both dbus-daemon (traditional) and dbus-broker. Arch Linux uses dbus-broker by default; Debian/Ubuntu still use dbus-daemon.

**Detection:** dbus-broker logs: "Eavesdropping is deprecated and ignored". Monitoring subscriptions receive no signals.

**Phase:** Phase 1 — D-Bus policy file authoring.

---

### Pitfall 11: Private D-Bus Integration Tests Fail When Connecting as Root to the Test Bus

**What goes wrong:** Integration tests start a private `dbus-daemon --session --print-address` instance and set `DBUS_SESSION_BUS_ADDRESS`. Tests that run as root (e.g., in a CI container) may connect but find that the private bus rejects policy-gated operations — because the test bus was started without the companion user's policy files. Also, `dbus-run-session` is cleaner and more portable than manual `--print-address` parsing.

**Why it happens:** The private test bus uses a minimal default policy. Attempts to own names or send to specific destinations that would be allowed in production (via `/usr/share/dbus-1/system.d/*.conf`) are denied in the test bus because no conf was loaded.

**Prevention:**
- Launch the private test bus with an explicit config file: `dbus-daemon --config-file=testdata/test-bus.conf --print-address --fork`.
- The test config file should inherit from `session.conf` and add the specific `<allow own>` and `<allow send>` rules that production would have.
- Use `dbus-run-session -- go test ./...` for simple cases; use the explicit config approach only when testing policy-gated behavior.
- Test helper must wait for the bus socket to appear before connecting; do not assume it is immediately available after the process starts.

**Detection:** Test-only "org.freedesktop.DBus.Error.AccessDenied" errors that don't reproduce in production. Tests pass as root but fail as regular user or vice versa.

**Phase:** Phase 5 — integration test infrastructure.

---

### Pitfall 12: Companion Session D-Bus Socket Path Is Not Fixed — Cannot Hardcode It

**What goes wrong:** The companion user's session D-Bus socket is at a path that varies between dbus-daemon and dbus-broker, and between systemd versions. Hardcoding `unix:path=/run/user/<uid>/bus` works on modern systemd + dbus-broker but fails on systems where the session bus is at `unix:abstract=/tmp/dbus-XXXXXXX`. Any code that hardcodes the socket path instead of reading `DBUS_SESSION_BUS_ADDRESS` from the companion's environment will break on non-standard distributions.

**Prevention:**
- The companion session's `DBUS_SESSION_BUS_ADDRESS` must be passed via the systemd --user unit's environment. Use `systemctl --user --machine=secrets-nb@ show-environment` to verify.
- The system D-Bus daemon (running as the companion) must read `DBUS_SESSION_BUS_ADDRESS` from the environment, not construct it.
- In the provisioning tool, derive the socket path dynamically: `su -l secrets-nb -c 'echo $DBUS_SESSION_BUS_ADDRESS'` or read from `/proc/$(pgrep -u secrets-nb dbus-daemon)/environ`.

**Detection:** "Failed to connect to D-Bus: No such file or directory" errors when companion daemon tries to connect to the companion session bus.

**Phase:** Phase 2 — companion daemon startup and environment setup.

---

### Pitfall 13: User-Agent Notification Listener Race — Signal Subscribed After Approval Already Sent

**What goes wrong:** The user-agent subscribes to approval request signals on the system D-Bus. Between starting the subscription and the first signal arriving, an approval request comes in (e.g., git commit was running in the background). The signal was emitted before the subscription was active. The user sees no notification. The request expires unnoticed.

**Why it happens:** D-Bus signals are not queued for late subscribers. There is no "replay missed signals" mechanism. If the subscription is set up after the daemon emits the signal, the client misses it entirely.

**Prevention:**
- After subscribing to signals, the user-agent must immediately call a method on the system D-Bus service to enumerate pending requests and display notifications for any already-pending.
- The subscription setup and the initial pending-request query must happen atomically: subscribe first, then query. This ensures no signal emitted between the two is lost (it will be caught by the subscription).
- Test this explicitly: have a request pending before the user-agent starts and verify the user-agent picks it up.

**Detection:** Pending approval requests that receive no desktop notification. User must manually check the companion VT to see pending requests.

**Phase:** Phase 4 — user-agent notification listener.

---

### Pitfall 14: PAM Module Must Not Block — login/sshd Will Hang

**What goes wrong:** A custom PAM module for the `open_session` phase that blocks waiting for the companion session to be fully ready will cause `login`, `sshd`, and `sudo` to hang for all users during the wait period. PAM modules run synchronously in the caller's process. Any blocking I/O in `pam_sm_open_session` stalls the entire login.

**Why it happens:** PAM developers forget that their module runs in the authentication daemon's context. A 30-second wait for a service to start = 30-second login hang.

**Consequences:** SSH login times out. Console login appears frozen. If the companion session never starts (e.g., misconfiguration), all logins hang until PAM times out.

**Prevention:**
- The PAM session hook must be fire-and-forget: issue `systemctl start secrets-nb-session.service --no-block` and return `PAM_SUCCESS` immediately.
- All readiness waiting logic belongs in the user-agent, not in PAM.
- Add a timeout guard in any PAM module: if the service start call itself takes more than 2 seconds, log a warning and return `PAM_SUCCESS` anyway (do not block login for secret management).
- Test: SSH login must complete in under 3 seconds even if the companion session is slow to start.

**Detection:** SSH login hangs for 10–30 seconds when the companion session service is misconfigured.

**Phase:** Phase 3 — PAM module implementation.

---

## Minor Pitfalls

---

### Pitfall 15: chvt Requires CAP_SYS_TTY_CONFIG — Companion Daemon Cannot Switch VTs Without It

**What goes wrong:** Switching virtual terminals programmatically requires `ioctl(TIOCSTI)` is not needed, but `ioctl(VT_ACTIVATE)` requires the calling process to have `CAP_SYS_TTY_CONFIG` or to own the target TTY. The companion daemon runs as `secrets-nb`, which is an unprivileged user. Without explicit capability grants, `ioctl(VT_ACTIVATE, 8)` fails with `EPERM`.

**Prevention:**
- Grant `CAP_SYS_TTY_CONFIG` to the companion daemon's systemd service: `AmbientCapabilities=CAP_SYS_TTY_CONFIG`.
- Alternatively, configure the target VT (e.g., `/dev/tty8`) to be owned by `secrets-nb` via a udev rule: `KERNEL=="tty8", OWNER="secrets-nb"`.
- Test: verify `sudo -u secrets-nb chvt 8` succeeds after provisioning.

**Detection:** `ioctl VT_ACTIVATE: operation not permitted` in companion daemon logs when attempting to switch to the approval VT.

**Phase:** Phase 2 — VT management + provisioning.

---

### Pitfall 16: gopass Store Initialized Under Root — Wrong Ownership Prevents Companion Access

**What goes wrong:** During provisioning, if `gopass init` or `gopass setup` is run as root (e.g., in a provisioning script), the `.password-store/` directory and its files are root-owned. The companion user `secrets-nb` cannot read them. `gopass-secret-service` fails to open any collection.

**Prevention:**
- All gopass provisioning commands must run as `secrets-nb`: `sudo -u secrets-nb gopass init <key-id>`.
- After any provisioning step, verify: `sudo -u secrets-nb ls ~/.password-store/` succeeds.
- The provisioning tool must refuse to run gopass commands as root.

**Detection:** `gopass-secret-service` logs "permission denied" when opening the password store.

**Phase:** Phase 3 — provisioning tool.

---

### Pitfall 17: VM E2E Tests Need Nested VT — Most CI VMs Lack VT Devices

**What goes wrong:** The VM E2E test layer requires real VT switching and VT_SETMODE behavior. Most CI environments (GitHub Actions, GitLab CI) use virtualized environments where `/dev/tty1..8` may not exist, `chvt` fails, and VT ioctls return `ENOTTY`. Tests that assume a real VT are not runnable in standard CI.

**Prevention:**
- Separate VT-dependent E2E tests from the CI-runnable integration tests. VT E2E runs only in a dedicated QEMU VM, not in the CI container.
- Use `SKIP_VT_TESTS=1` environment variable to gate VT tests.
- The CI pipeline runs unit tests and private D-Bus integration tests only. VT E2E is a manual gate before release.
- Document this clearly in the test strategy: three layers, different runner requirements.

**Detection:** Tests fail in CI with "no such device" or "not a terminal" errors on VT ioctls.

**Phase:** Phase 5 — test infrastructure.

---

### Pitfall 18: `GetConnectionUnixUser` Returns Companion UID on System Bus — Requester Identity Lost

**What goes wrong:** On the system bus, when the companion daemon calls `GetConnectionUnixUser(sender)` to identify who is making a secret request, it may get back the UID of the connection owner — which, if the request came through the user-agent proxy, is the user-agent's UID (the desktop user). This is correct identity. But if the system D-Bus policy allows direct connections from other processes, and those processes connect under a different UID, the daemon must not assume all requesters are the desktop user.

**Prevention:**
- The system D-Bus policy must restrict `send_destination` to specific UIDs (`<policy user="nb">`) so only the legitimate desktop user's processes can connect.
- The companion daemon must always call `GetConnectionUnixUser` and verify the UID matches the expected desktop user before processing any request. Reject others with a D-Bus error.
- Treat the system bus as a trust boundary, not an implicit trust.

**Detection:** A process running as a different user successfully submits a secret request to the companion daemon.

**Phase:** Phase 1 — system D-Bus interface + authorization design.

---

## Phase-Specific Warnings

| Phase Topic | Likely Pitfall | Mitigation |
|-------------|---------------|------------|
| System D-Bus policy file authoring | Missing `allow own` for companion user (Pitfall 1) | Write and test policy file before daemon code |
| D-Bus policy rule type | `send_interface` instead of `send_destination` (Pitfall 2) | Use destination-scoped rules only |
| VT_SETMODE VT_PROCESS acquisition | Crash leaves VT stuck (Pitfall 3) | Install cleanup handlers; test kill -9 |
| VT_SETMODE and udev/logind interference | Race resets VT_AUTO (Pitfall 4) | Verify mode after set; accept partial guarantee |
| PAM open_session for companion lifecycle | PAM blocks login (Pitfall 14) | Fire-and-forget; readiness in user-agent |
| Companion session D-Bus readiness | User-agent starts before companion ready (Pitfall 5) | Retry with backoff; readiness signal on system bus |
| Companion user setup | Linger not enabled (Pitfall 6) | Provisioning tool must call `loginctl enable-linger` |
| GPG agent for companion user | No TTY for pinentry (Pitfall 7) | Configure `pinentry-tty` + GPG_TTY to approval VT |
| User-agent Secret Service proxy | Object paths not rewritten across buses (Pitfall 8) | Full path-mapping proxy, not thin forwarder |
| Provisioning gopass/GPG setup | Root ownership breaks companion access (Pitfalls 9, 16) | All setup must run as `secrets-nb` |
| dbus-broker policy compatibility | Deprecated `eavesdrop` attribute (Pitfall 10) | Test against both dbus-daemon and dbus-broker |
| Integration test D-Bus setup | Test bus missing production policy (Pitfall 11) | Load policy conf in test bus |
| User-agent notification startup | Signal arrives before subscription active (Pitfall 13) | Subscribe then query pending; handle both paths |
| VM E2E test infrastructure | VT ioctls unavailable in CI (Pitfall 17) | Separate VT tests; run only in QEMU |
| Requester authorization on system bus | UID not verified (Pitfall 18) | Always call GetConnectionUnixUser; verify against allowlist |

---

## Sources

- [ioctl_vt(2) — Linux manual page](https://man7.org/linux/man-pages/man2/ioctl_vt.2.html) — VT_SETMODE, VT_PROCESS, VT_RELDISP semantics (HIGH confidence)
- [How VT-switching works — dvdhrm blog](https://dvdhrm.wordpress.com/2013/08/24/how-vt-switching-works/) — VT ownership, signal-based switching, crash behavior (HIGH confidence)
- [Ubuntu Bug #290197 "VT switching API is inherently racy"](https://bugs.launchpad.net/ubuntu/+source/linux/+bug/290197) — race condition between VT_ACTIVATE and VT_SETMODE (HIGH confidence)
- [dbus-daemon(1) — freedesktop.org](https://dbus.freedesktop.org/doc/dbus-daemon.1.html) — policy file syntax, `send_destination`, `send_interface`, `allow own` semantics (HIGH confidence)
- [D-Bus Specification — freedesktop.org](https://dbus.freedesktop.org/doc/dbus-specification.html) — cross-bus behavior, signal delivery, object path semantics (HIGH confidence)
- [dbus-broker Deviations wiki](https://github.com/bus1/dbus-broker/wiki/Deviations) — `eavesdrop` and `at_console` deprecation in dbus-broker (HIGH confidence)
- [systemd issue #2863 — pam_systemd connection timeout](https://github.com/systemd/systemd/issues/2863) — PAM open_session timing problem with systemd --user (HIGH confidence)
- [Arch Wiki — systemd/User](https://wiki.archlinux.org/title/Systemd/User) — linger requirement for non-interactive companion users, XDG_RUNTIME_DIR lifecycle (HIGH confidence)
- [pam_systemd(8) — freedesktop.org](https://www.freedesktop.org/software/systemd/man/latest/pam_systemd.html) — PAM session lifecycle and logind integration (HIGH confidence)
- [GnuPG Common Problems](https://www.gnupg.org/documentation/manuals/gnupg/Common-Problems.html) — GPG_TTY requirement for pinentry, no-TTY failure modes (HIGH confidence)
- [GnuPG Agent Options](https://www.gnupg.org/documentation/manuals/gnupg/Agent-Options.html) — `pinentry-program` configuration (HIGH confidence)
- [gopass issue #907 — GNUPGHOME ownership warnings](https://github.com/gopasspw/gopass/issues/907) — GPG ownership check breaking companion user access (MEDIUM confidence)
- [pam_exec.so timing issue — Arch Forums](https://bbs.archlinux.org/viewtopic.php?id=180314) — pam_exec cannot reliably access systemd --user instance during PAM session phase (MEDIUM confidence)
- [Kicksecure Forums — chvt forced VT change](https://forums.kicksecure.com/t/chvt-change-foreground-virtual-terminal-vt-tty-prevent-malware-from-forced-tty-change/1274) — chvt authorization model, malware VT switching vectors (MEDIUM confidence)
- [System D-Bus ownership denied troubleshooting — linuxvox.com](https://linuxvox.com/blog/system-d-bus-does-not-allow-punching-out-ownership-with-conf-files/) — policy file pitfalls in practice (MEDIUM confidence)
- Existing codebase: `internal/proxy/proxy.go`, `internal/proxy/senderinfo.go` — cross-bus proxy pattern already implemented for SSH tunnel case; directly applicable to v2.0 user-agent design

---

*Research completed: 2026-02-25*
*Scope: v2.0 privilege separation + VT trusted I/O milestone*
*Previous PITFALLS.md (v1.0 GPG signing proxy pitfalls) superseded by this document*
