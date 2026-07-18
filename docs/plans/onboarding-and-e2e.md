# Plan: onboarding ("install & try") + Ubuntu e2e testing

Status: **PR A + PR B landed** (2026-07-18); **PR C is the live roadmap** —
future sessions pick up from the PR C section below. Companion issue **#1**
(GNOME bypass + prompt forwarding) is closed, fixed by PR A + PR B.

Landed:
- **PR A** (#13) — prompt forwarding + Tier-1 container e2e (`e2e/gnome/fast.sh`).
- **PR B** (#14) — provider detection, `--backend` keywords, secrets-only
  gnome-keyring demotion, Tier-2 desktop-VM harness (`e2e/gnome/vm/`,
  `make test-e2e-gnome-vm`). The VM gate runs on every PR (~1.5 min with the
  weekly image cache) and is a required check on master.
- **#15** — `go install` ships the full web UI (US-2's Go path; committed dist
  + embed-freshness CI guard).
- **Spike resolved (was PR B step 3)**: *secrets-only demotion*, NOT
  whole-daemon masking — with the units masked, pam_gnome_keyring respawns an
  unmanaged `--daemonize --login` daemon at next login that re-grabs the name,
  and pkcs11 dies. Demotion = user drop-in (`--components=pkcs11`) + Hidden=true
  autostart shadow + the D-Bus activation mask; all user-level files, exactly
  reversible. See `internal/service/gnomekeyring.go`.

## The reframe that drives everything

secrets-dispatcher's job is to **insert itself in front of the session's Secret
Service and forward to the real backend**. So "install" is *not* "copy a
binary" — the meaningful install is the takeover of `org.freedesktop.secrets`
plus a private, forwarded backend. Onboarding has to make that **safe,
observable, and reversible**, or nobody runs it near their keyring.

The binary copy still matters, but only as a prerequisite: `service install`
bakes the resolved `os.Executable()` path into the systemd unit
(`internal/service/install.go:153-160`), so the binary must first sit at a
stable path (`~/.local/bin`) — that's all `make install` is for.

## Where things stand (done this session)

- **#6** — per-request approval tracking (concurrent approvals no longer cancel
  each other) + `senderName`/`requestID`/`ErrFailed`/`pathOf`/`senderOf`/
  `upstream()`/`senderResolver()` cleanups. **Merged, PR #10.**
- **#4** — `make install` target (stages binary to `~/.local/bin`,
  `PREFIX`/`DESTDIR`-overridable). **Merged, PR #11.**
- **#1** — still open; the core of the plan below (PR A + PR B).

## User stories (full set; US-4/5/6 are the core)

North-star first run — one reversible command:

```
$ secrets-dispatcher try
✓ Detected GNOME Keyring owning org.freedesktop.secrets
✓ secrets-dispatcher now in front of it (GNOME Keyring → private backend)
✓ Web UI: http://127.0.0.1:8484
→ See it work:  secret-tool lookup service foo   (Ctrl-C to stop & fully restore)
```

- **US-1** Know what it will change before it runs (`--dry-run`).
- **US-2** One obvious "get the binary" path (curl → `~/.local/bin`; from-source `make install` [#4 ✓]).
- **US-3** Packager-friendly `make install PREFIX=… DESTDIR=…`, no web-UI rebuild [#4 ✓].
- **US-4** *Take over the bus, keep my keyring* — one command puts the dispatcher in front of the current provider, demoted to a private forwarded backend. [#1-A]
- **US-5** *Works on my actual desktop* — auto-detect GNOME/gnome-keyring and do the right masking/topology; don't make the user hand-edit units. [#1-A]
- **US-6** *Don't lock me out of locked secrets* — forward `org.freedesktop.Secret.Prompt` so unlock prompts still reach me. [#1-B]
- **US-7** Make something happen — first-run tells me how to trigger a request; I see an approval with the process chain; notification doesn't auto-dismiss. [#1 notif]
- **US-8** Approve/deny from web UI, desktop notification, or CLI.
- **US-9** Trust I can undo it — stopping the trial fully restores the original Secret Service, nothing persisted/masked.
- **US-10** Make it permanent deliberately — `service install --start` (distinct from the trial).
- **US-11** Know I'm actually in front — `status`/`doctor` confirms name ownership + backend health, warns on re-grab.

## Implementation plan

### PR A — Forward `Secret.Prompt` objects (US-6, #1-B) — ✅ shipped (#13)

Small, self-contained, fixes a correctness lockout (locked collection →
`Object does not implement …Prompt` → user locked out of their own secret).

Current state (master): handlers forward to `backendConn`, export on
`frontConn` — `proxy.go` `ConnectWith` L77-146. Exports at L99-128 cover
Service / Collection / Item / SubtreeProperties; **nothing** at
`/org/freedesktop/secrets/prompt`. `PromptInterface` const exists
(`internal/dbus/types.go:16`), unused. Prompt paths are returned opaque from
Unlock/Lock/CreateCollection (`service.go`), Delete/CreateItem
(`collection.go`), Delete (`item.go`). `Prompt.Completed` signals **already
forwarded** by `signals.go:54` (prefix `org.freedesktop.Secret.`).

Steps:
1. New `internal/proxy/prompt.go`: `PromptHandler{ localConn *dbus.Conn; logger }`
   (mirror the minimal `SubtreePropertiesHandler`), `NewPromptHandler(backendConn, logger)`,
   an `upstream(path)` method, and `isPromptPath(path)` (prefix
   `/org/freedesktop/secrets/prompt/`) beside `isItemPath`/`isCollectionPath`.
   Two methods reusing `pathOf`/`ErrFailed`:
   - `Prompt(msg, windowID string)`: `path := pathOf(msg)`; non-prompt →
     `ErrObjectNotFound`; else `h.upstream(path).Call(PromptInterface+".Prompt", 0, windowID)`;
     error → `ErrFailed(call.Err)`.
   - `Dismiss(msg)`: same with `.Dismiss()`.
2. `proxy.go` `ConnectWith`: add `prompt` field (~L25), construct
   `p.prompt = NewPromptHandler(p.backendConn, p.logger)` (~L96), and after the
   item subtree export (L128):
   `p.frontConn.ExportSubtree(p.prompt, "/org/freedesktop/secrets/prompt", dbustypes.PromptInterface)`.
3. Completed already forwarded — add a test asserting it. **No path rewriting**
   (only *sessions* are remapped via SessionManager; collection/item/prompt
   paths are 1:1).
4. **Enabling test fixture:** extend `cmd/mock-secret-service` (currently has no
   Prompt/Lock/Unlock) to produce the prompt flow — a lockable collection whose
   `Unlock` returns a prompt path + a `…/prompt/*` object implementing
   `Prompt()/Dismiss()` emitting `Completed`.
5. `internal/proxy/prompt_test.go`: front-bus `Prompt.Prompt` reaches the mock
   backend and `Completed` propagates back.

Effort ~S–M; main cost is the mock extension; low product risk (purely additive).

### PR B — Keep secrets-dispatcher the owner on GNOME (US-4 + US-5, #1-A) — ✅ shipped (#14)

Current state: name grab = `maskDBusActivation()` + one-shot
`stopDBusActivatedService()` SIGTERM (`install.go:472`) +
`RequestName(BusName, NameFlagReplaceExisting)` (`proxy.go:135`). **No** masking
of gnome-keyring's *systemd* units, **no** provider/DE detection, `--backend` is
a raw path (default gopass-secret-service, `main.go:905`).

Steps:
1. `internal/service/detect.go` — `DetectProvider()`: resolve current
   `org.freedesktop.secrets` owner (GetNameOwner → PID → exe) + read
   `XDG_CURRENT_DESKTOP`; return `{gnome-keyring|gopass|kwallet|unknown}`. Inject
   D-Bus/exec seams like the existing `systemctlFunc`/`lookPathFunc`.
2. `--backend gnome-keyring` shortcut (`install.go:202-204`, `main.go:905`):
   non-path `--backend` values → command templates.
   `gnome-keyring` → `gnome-keyring-daemon --foreground --components=secrets --control-directory=%t/…`.
   Keep raw paths + `gopass` default. Small keyword→template table.
3. **Mask gnome-keyring systemd units** — new `maskProvider`/`unmaskProvider`:
   for local/full + provider gnome-keyring,
   `systemctl --user mask --now gnome-keyring-daemon.service gnome-keyring-daemon.socket`,
   **recording prior state so Uninstall reverses exactly** (model the state,
   don't reconstruct it). Wire unmask into `Uninstall()` (`install.go:318`).
   - ⚠️ **SPIKE (do before shipping):** masking the whole daemon also kills
     gnome-keyring's ssh/pkcs11/gpg components. We only want to demote its
     *secrets* component. Resolve *secrets-only* vs *whole-daemon-private*. This
     is the crux of US-5 and of US-9 reversibility. **The spike and the
     desktop-VM harness (below) are the same investigation** — build them together.
4. Detection-driven default backend: `--backend` unset + provider gnome-keyring
   → default private backend to gnome-keyring so stock-GNOME just works.
5. Post-install owner check: print who owns `org.freedesktop.secrets`, warn if
   not us (seeds US-11).

Effort ~M–L. Risks: reversibility of user-unit masking; the step-3 spike;
distro unit-name variance.

### PR C — `try` command + `status`/doctor (US-9 + US-11) — ⏭ NEXT (the live roadmap)

The reversible trial (north-star) + "am I actually in front" check. A/B are
landed, so `try` composes proven primitives: `DetectProvider()` (detect.go),
demote/restore (gnomekeyring.go), `reportOwnership()` (the US-11 seed in
install.go), and the Tier-2 scenario as its acceptance template
(`e2e/gnome/vm/scenario.sh` already exercises install→verify→uninstall).

Notes for the implementing session:
- `try` ≈ install(local, detection-driven backend) + live ownership/status
  output + Ctrl-C → uninstall-equivalent full restore; `--dry-run` prints the
  exact file/unit changes (US-1) — the demotion state model makes this easy
  (the managed files ARE the plan).
- `status`/`doctor`: name owner (+ exe classification), backend health,
  masked/demoted state consistency, warn on re-grab (extend reportOwnership).
- Autologin caveat (spike finding): on autologin desktops the login collection
  starts locked; `try`'s "see it work" hint must not assume an unlocked
  keyring — prefer a `secret-tool store`+`lookup` pair or surface the unlock
  prompt flow (PR A forwards it).
- Acceptance: extend `e2e/gnome/vm/scenario.sh` with a `try` leg (trial → 
  Ctrl-C → assert stock state byte-exact, mirroring the uninstall leg).

## E2E testing plan

### Fidelity ladder (server VM was rejected — see below)

- **Tier 0 (exists):** mock-secret-service + Playwright — proxy logic, web UI, every PR.
- **Tier 1 — fast, container (no VM):** `dbus-run-session` + real gnome-keyring
  in an Ubuntu container. Exercises proxy/prompt-forwarding *logic* + approval
  plumbing + `NameOwnerChanged` cheaply. Great for PR A. **Not** the takeover
  acceptance test.
- **Tier 2 — fidelity, real GNOME desktop VM (nightly/label/pre-release):** the
  only place US-5 re-grab, gcr-prompter (US-6 real UX), gnome-shell
  notifications, and snap-Firefox get honest coverage.

### Why NOT a server VM (important — decided this session)

A server/headless session is a false-economy middle tier: neither the
container's speed nor the desktop's fidelity, and it **misses exactly the
desktop-only behavior secrets-dispatcher fights against**:

| Desktop-only behavior | Story | Server misses it? |
|---|---|---|
| gnome-keyring (re)started via `gnome-session` + XDG autostart (`/etc/xdg/autostart/gnome-keyring-*.desktop`), *on top of* systemd `.socket` + D-Bus activation | US-5 / #1-A | **Yes — the big one.** Masking can pass on server while `gnome-session` re-grabs on real desktop. |
| Unlock prompt rendered by `gcr-prompter` (GUI) | US-6 / #1-B | Partly (D-Bus forwarding testable; real prompter isn't). |
| Notification server = gnome-shell (its auto-dismiss is what `expire_timeout=0` targets) | #1 notif | Yes. |
| Browsers are **snaps**; snapd mediates their Secret Service access | Firefox row in README | Yes. |
| Session bus = **dbus-broker** (tracker leans on `NameOwnerChanged` timing) | #6 code | Minor; match it. |

Exact gnome-keyring start/re-grab vectors **shift by Ubuntu release** — itself
the argument for running the real desktop, not modeling it on server.

### VM tool decision: **raw QEMU + Ubuntu cloud image + cloud-init**

Chosen for true local↔CI parity (same script/image/accel both places), no
daemon/snap, and full-distro fidelity (real systemd). Confirmed:
`ubuntu-latest` GitHub-hosted runners expose `/dev/kvm`.

| Tool | Parity | CI-friendly | Full-distro fidelity | Verdict |
|---|---|---|---|---|
| **raw QEMU + cloud-init** | ✅ identical | ✅ `/dev/kvm` + `sg kvm`, no daemon | ✅ real systemd | **Pick** |
| multipass | ⚠️ snap + `multipassd` in CI | ⚠️ | ✅ | Optional *local* convenience only, against the same `user-data` |
| quickemu | ⚠️ | ⚠️ interactive-desktop oriented | ✅ | Local human desktop use, not CI |
| smolvm | ✅ portable | ✅ fast | ❌ **microVM sandbox (libkrun)** — no full init/desktop stack | Wrong class of tool |

Concrete CI pattern (confirmed working):
- Ubuntu **Desktop** image (or minimal `gdm3 + gnome-session + gnome-shell + gnome-keyring` on server base); qcow2 **overlay** over an immutable/cacheable base.
- `cloud-localds seed.iso user-data meta-data`; `user-data` installs the keyring stack, sets **gdm autologin** for the test user, `XDG_CURRENT_DESKTOP=ubuntu:GNOME`, writes a `cloud-init-ready` marker.
- Headless GNOME via QEMU virtual display **or** `mutter --headless --virtual-monitor` (real gnome-shell + gcr-prompter + notifications, no monitor).
- Boot under `sg kvm -c 'qemu-system-x86_64 -enable-kvm … -netdev hostfwd=tcp::2222-:22 …'`.
- Readiness: poll `nc -z 127.0.0.1 2222` then `ssh … test -f /var/tmp/cloud-init-ready`.
- Runner packages: `qemu-system-x86 qemu-utils cloud-image-utils genisoimage netcat-openbsd`.
- Web-UI steps: existing Playwright against forwarded `:8484`.

### Notification auto-dismiss — how to test it (mostly instrumentable, no pixels)

`org.freedesktop.Notifications` broadcasts:
- **`NotificationClosed(id, reason)`** — reason `1`=expired, `2`=user-dismissed,
  `3`=CloseNotification, `4`=undefined.
- **`ActionInvoked(id, action_key)`**.

Strategy, layered:
- **Fast tier:** a capture stub implementing `org.freedesktop.Notifications`
  records `Notify()` args → assert we send `expire_timeout=0`, `urgency=2`,
  actions present, body carries the process chain. (Tests *our* side only.)
- **Fidelity tier (real gnome-shell):**
  - bus monitor (`busctl --user monitor org.freedesktop.Notifications` or an
    `AddMatch`) → assert **no `NotificationClosed(id, expired=1)`** within the
    request window when `expire_timeout=0` (the deterministic core; the old `-1`
    would emit it).
  - **AT-SPI** (`org.a11y.Bus`, via `dogtail`/`pyatspi`) → assert the banner +
    Approve/Deny buttons are still *presented*, **and drive Approve
    programmatically** (`action.doAction`) — makes the notification approval
    path fully automatable on real GNOME.
  - GNOME Shell `Eval` (unsafe-mode, test-VM only) as a fallback to introspect
    `Main.messageTray`.
- **Probe first:** fire one notification with `-1` and with `0` on real
  gnome-shell, capture what actually gets emitted (close reason? banner hidden
  but still open?), so we anchor assertions on real behavior. The tool sends
  `urgency=critical (2)`, so the failure is *likely* the expiry path
  (D-Bus-observable) — but confirm, don't assume.

The only genuinely-fuzzy thing is subjective "is it visually prominent enough,"
which isn't a regression test anyway.

### Shared scenario (maps 1:1 to the stories)

One `e2e/gnome/scenario.sh`, run in both tiers (extra desktop-only steps in Tier 2):
1. Baseline: `busctl --user list` shows gnome-keyring owns the name; `secret-tool store/lookup` round-trips.
2. `service install --backend gnome-keyring --start` → secrets-dispatcher now owns the name; `secret-tool lookup` still returns the secret. (US-4)
3. Restart the user session / re-arm the socket → gnome-keyring **doesn't** re-grab. (US-5) *(Tier 2 only — this is the point of the desktop VM.)*
4. Lock the collection; `secret-tool lookup` → prompt forwarded → approve → secret returned, not the interface error. (US-6)
5. Drive a request through a renamed wrapper (`fake-agent → secret-tool`) → approval shows the process chain; actionable via CLI `approve <id>`, web UI (Playwright), and the notification path (AT-SPI). (US-7/8)
6. `service uninstall` → gnome-keyring owns the name again, masks removed, `secret-tool` still works. (US-9)

### Components to build

1. `e2e/gnome/provision.sh` (or cloud-init `user-data`) — bring a box to
   Ubuntu-default state (keyring stack, autologin, socket-activated
   gnome-keyring, `XDG_CURRENT_DESKTOP`).
2. `e2e/gnome/notif-stub` — tiny D-Bus service owning
   `org.freedesktop.Notifications` that records `Notify()` (fast-tier only).
3. `e2e/gnome/scenario.sh` — the shared journey above.
4. `.github/workflows/e2e-gnome.yml` — `fast` job (Tier 1, every PR) + `full`
   job (Tier 2 desktop VM: `schedule` nightly, `e2e-gnome` label, pre-release).
   *(Landed better than planned: the `full` job proved cheap enough — ~1.5 min
   with the weekly image cache — to run on EVERY PR, and is a required check
   on master; nightly + dispatch remain, the label opt-in was dropped.)*

The shared script + notif-stub are independent of the product fixes (work even
against the mock), so they can land first and unblock everything.

## Sequencing

1. (optional) shared `scenario.sh` + notif-stub — notif-stub still open (goes
   with the US-7 notification work).
2. ✅ **PR A** (prompt forwarding) + Tier-1 container test — #13.
3. ✅ **PR B** spike — verdict: secrets-only demotion; VM harness built,
   scenario steps 2/3/6 are the acceptance gate (`e2e/gnome/vm/scenario.sh`).
4. ✅ **PR B** proper (detection, `--backend gnome-keyring`, demotion,
   reversibility) — #14.
5. ⏭ **PR C** (`try` + `status`/doctor) — see the PR C section for the
   session-ready notes.
6. Then: US-7 notification work (auto-dismiss fix + notif-stub + AT-SPI
   fidelity tier — strategy below remains the plan of record).

## Decisions locked this session (don't re-litigate)

- #4 shipped as a real `make install` (not README-only); it's the "get the
  binary" prerequisite, not "install."
- `try` command → PR C, after A/B.
- PR B step-3 mask approach → **spike** it (secrets-only vs whole-daemon).
- VM tool → **raw qemu + cloud-init** (multipass = optional local only; skip
  quickemu/smolvm).
- **Server VM rejected** — Tier 1 = container, Tier 2 = real-GNOME desktop VM.
- Notification auto-dismiss is testable via `NotificationClosed`/`ActionInvoked`
  on D-Bus + AT-SPI on real gnome-shell; probe real behavior first.
