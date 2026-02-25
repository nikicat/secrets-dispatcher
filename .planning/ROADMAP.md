# Roadmap: Secrets Dispatcher

## Milestones

- ✅ **v1.0 GPG Commit Signing** — Phases 1-3 (shipped 2026-02-24)
- 🚧 **v2.0 Privilege Separation** — Phases 4-8 (in progress)

## Phases

<details>
<summary>✅ v1.0 GPG Commit Signing (Phases 1-3) — SHIPPED 2026-02-24</summary>

- [x] Phase 1: Data Model and Protocol Foundation (3/3 plans) — completed 2026-02-24
- [x] Phase 2: Core Signing Flow (4/4 plans) — completed 2026-02-24
- [x] Phase 3: UI and Observability (3/3 plans) — completed 2026-02-24

See: `.planning/milestones/v1.0-ROADMAP.md` for full details.

</details>

### 🚧 v2.0 Privilege Separation (In Progress)

**Milestone Goal:** Move secrets-dispatcher and gopass-secret-service to a companion user with kernel-enforced trusted I/O, eliminating userspace attack surface.

- [x] **Phase 4: Foundation** — Companion user + system D-Bus policy + daemon skeleton (2/2 plans done)
- [ ] **Phase 5: Core Flow** — Approval manager wired to system D-Bus + VT TUI end-to-end
- [ ] **Phase 6: Desktop Integration** — User-space agent + PAM hook + GPG thin client update
- [ ] **Phase 7: Hardening** — Differentiating features and operational polish
- [ ] **Phase 8: VM E2E** — Full deployment validation in a real multi-user VM

## Phase Details

### Phase 4: Foundation
**Goal**: Companion user exists, system D-Bus policy is verified, and the provisioning tool creates the full deployment skeleton — all before any companion-side daemon code is written
**Depends on**: Nothing (first phase of v2.0)
**Requirements**: COMP-01, COMP-02, COMP-05, DBUS-01, DBUS-02, PROV-01, PROV-02, PROV-03, PROV-04, PROV-05, INFRA-01, INFRA-02, TEST-04
**Success Criteria** (what must be TRUE):
  1. Running `sd-provision` creates the `secrets-nb` user with a 0700 home directory that the desktop user cannot read
  2. Running `sd-provision --check` reports pass/fail for each deployment component with actionable output
  3. The companion daemon can register `net.mowaka.SecretsDispatcher1` on the system bus; a desktop-user process can call stub methods; a process running as any other UID receives a policy rejection
  4. Unit and integration tests compile and run without root, real VT, or real companion user
  5. All daemon output routes to systemd-journald; HTTP/REST/WebSocket are disabled by default
**Plans**: 2 plans
Plans:
- [x] 04-01-PLAN.md — Provisioning tool: companion user creation, D-Bus policy, systemd unit, PAM hook, check validator
- [x] 04-02-PLAN.md — Daemon skeleton: D-Bus stub registration, sd-notify, integration tests with private dbus-daemon

### Phase 5: Core Flow
**Goal**: End-to-end secret request and GPG signing flows work through the VT TUI: request arrives on system D-Bus, appears on VT8, keyboard y/n resolves it, result is returned to the caller
**Depends on**: Phase 4
**Requirements**: DBUS-03, DBUS-04, DBUS-05, DBUS-06, DBUS-07, VT-01, VT-02, VT-03, VT-04, VT-05, VT-06, VT-09, GPG-02, GPG-03, TEST-01, TEST-02
**Success Criteria** (what must be TRUE):
  1. A secret request sent via system D-Bus appears on VT8 with the secret path, requester PID/UID/process name, and parent process chain
  2. A GPG signing request appears on VT8 with repo name, commit message, author, changed files, and key ID
  3. Pressing `y` on VT8 approves the request and unblocks the requesting process; pressing `n` denies it with an error returned to the caller
  4. If the daemon crashes while holding VT_PROCESS mode, the VT returns to VT_AUTO and the user can switch VTs normally
  5. Integration tests with a private D-Bus daemon verify method signatures and signal delivery without root
**Plans**: TBD

### Phase 6: Desktop Integration
**Goal**: Desktop applications transparently use the companion's secret store via the user-space agent; git commits trigger VT approval via the updated GPG thin client; PAM automatically starts and stops the companion session
**Depends on**: Phase 5
**Requirements**: COMP-03, COMP-04, AGENT-01, AGENT-02, AGENT-03, AGENT-04, AGENT-05, GPG-01
**Success Criteria** (what must be TRUE):
  1. An application calling the standard Secret Service API on the session bus receives secrets from gopass-secret-service running as `secrets-nb`, with no application-level changes required
  2. A desktop notification appears when a secret request arrives, showing the requester process name and PID
  3. `git commit -S` from the desktop session triggers a GPG signing request on VT8; the commit completes with a valid signature after keyboard approval
  4. Logging in as the desktop user automatically starts the companion session; logging out stops it after all sessions end
  5. When the companion is not running, the user-space agent shows an actionable notification rather than a silent hang
**Plans**: TBD

### Phase 7: Hardening
**Goal**: All differentiating features and operational polish are in place: store lock/unlock, VT CLI mode, and store unlock prompt on companion start
**Depends on**: Phase 6
**Requirements**: COMP-06, COMP-07, VT-07, VT-08
**Success Criteria** (what must be TRUE):
  1. Calling `Lock()` via D-Bus clears the gpg-agent passphrase cache; the store is inaccessible until unlocked
  2. Calling `Unlock()` via D-Bus shows the passphrase prompt on VT8 and restores secret access after correct entry
  3. The companion session start shows a store unlock prompt on VT8 when the passphrase cache is cold
  4. Switching VT8 to CLI mode shows pending request history and accepts admin commands
**Plans**: TBD

### Phase 8: VM E2E
**Goal**: A real VM with the full multi-user deployment passes all end-to-end scenarios: login starts companion, secret approval works on VT, GPG signing completes, logout stops companion
**Depends on**: Phase 7
**Requirements**: TEST-03
**Success Criteria** (what must be TRUE):
  1. A VM provisioned with `sd-provision` and rebooted starts the companion session automatically when the desktop user logs in, with no manual steps
  2. A secret request from a desktop application flows through system D-Bus, appears on VT8, and returns the secret after keyboard approval
  3. A `git commit -S` in the VM produces a valid signed commit after VT8 approval showing the correct commit context
  4. When the desktop user logs out, the companion session stops and the D-Bus name is released
**Plans**: TBD

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1. Data Model and Protocol Foundation | v1.0 | 3/3 | Complete | 2026-02-24 |
| 2. Core Signing Flow | v1.0 | 4/4 | Complete | 2026-02-24 |
| 3. UI and Observability | v1.0 | 3/3 | Complete | 2026-02-24 |
| 4. Foundation | v2.0 | 1/2 | In progress | - |
| 5. Core Flow | v2.0 | 0/? | Not started | - |
| 6. Desktop Integration | v2.0 | 0/? | Not started | - |
| 7. Hardening | v2.0 | 0/? | Not started | - |
| 8. VM E2E | v2.0 | 0/? | Not started | - |
