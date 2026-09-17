# RFC: Secure-Local Backend Isolation

- Status: Proposed
- Target: Post-v0.6
- Historical basis: `b7533d1`, `c039f44`, `fc60d82`
- Primary platform: systemd/logind Linux with GNOME and GNOME Keyring

## Summary

Add an opt-in `secure-local` mode that moves the Secret Service backend into a
separate operating-system user and places a root-managed authorization broker
between desktop applications and that backend.

Desktop applications continue to use `org.freedesktop.secrets` on their normal
session bus. The backend bus, keyring files, backend process memory, trusted
policy, and approval authority are inaccessible to the desktop user. A desktop
agent provides the existing Web UI, notifications, and CLI integration, but it
cannot access the backend or approve requests by itself.

This is a separate security mode, not a transparent hardening flag for current
`local` mode.

## Motivation

Current local mode improves visibility and control but is not a privilege
boundary. A malicious process running as the desktop user can inspect other
same-user processes and may discover private transport details or modify
user-owned policy.

Secure-local is intended to provide a meaningful boundary against arbitrary
code already running as the desktop user while retaining standard Secret
Service compatibility and GNOME's native unlock experience.

## Historical Findings

The historical implementation established useful foundations:

- a distinct backend UID and private backend bus;
- a root-owned broker and root-owned policy/state;
- credential-switched bus connections;
- explicit desktop/backend UID checks;
- initial provisioning and VM coverage.

It is not production-ready and must not be replayed as-is:

- the desktop had no usable, strongly authorized approval path;
- backend unlock and GNOME prompter behavior were incomplete;
- the implementation used an obsolete whole-daemon masking strategy;
- lifecycle, migration, rollback, and multi-user behavior were unfinished;
- root path checks did not verify ownership, writable ancestors, or symlinks;
- tests did not prove an approved, locked secret read end to end.

Target-credential D-Bus connections must explicitly authenticate with
`dbus.AuthExternal(targetUID)`. Socket credentials alone must not be assumed to
override godbus's parent-EUID authentication behavior.

## Goals

- Prevent the desktop UID from accessing backend storage, process memory, or
  the private backend bus.
- Prevent user-writable binaries, configuration, units, and tokens from
  becoming trusted broker authority.
- Require fresh human authorization before releasing secret material.
- Preserve standard Secret Service clients and GNOME's native unlock dialog.
- Preserve current GNOME provider detection, reversible takeover, prompt
  forwarding, and SystemPrompter bridge behavior.
- Support isolated instances for multiple desktop users.
- Provide transactional provisioning, migration, rollback, status, and
  deprovisioning.

## Threat Model

The primary attacker is arbitrary malicious code already running as the
desktop UID. It may:

- invoke any public Secret Service method;
- connect to the user's session bus;
- read and modify user-owned files;
- call the user-facing HTTP API;
- choose process names, arguments, paths, and ancestry;
- race for user-bus names;
- create misleading notifications or desktop windows;
- kill user processes and cause denial of service.

The trusted computing base is the kernel, root, systemd, logind, Polkit, the
package manager, the secure-local broker, GNOME Keyring under the backend UID,
and GNOME Shell's system prompter.

Without an authorized human decision, desktop-user malware must not be able to:

- reach the backend transport or storage;
- approve its own request through a user-readable token;
- alter trusted rules or broker configuration;
- pass attacker-controlled plaintext credentials to an unlock helper;
- replace any root-executed component.

Process attribution remains display context, not a strong application identity.

## Architecture

```text
desktop applications
        |
        | session D-Bus / org.freedesktop.secrets
        v
root-managed secure-local broker
        |
        | private backend D-Bus
        v
GNOME Keyring under backend UID
        |
        v
backend-only keyring store

desktop secure agent
  |-- notifications
  |-- Web UI on loopback
  |-- CLI adapter
  `-- system-bus control calls -> broker -> Polkit

GNOME Keyring SystemPrompter
  <-> brokered opaque exchange
  <-> GNOME Shell SystemPrompter
```

### Components

| Component | UID | Responsibility |
| --- | --- | --- |
| Secure launcher and broker | root | Own the front name, enforce policy, supervise the backend |
| Private `dbus-daemon` | backend UID | Isolated backend transport |
| GNOME Keyring | backend UID | Secret Service provider and encrypted store |
| Secure desktop agent | desktop UID | Web, notification, and CLI presentation only |
| GNOME Shell prompter | desktop UID | Native unlock UI and encrypted exchange |

### Trusted Paths

```text
/etc/secrets-dispatcher/secure/<uid>.yaml
/var/lib/secrets-dispatcher/secure/<uid>/
/var/lib/secrets-dispatcher/backends/<uid>/
/run/secrets-dispatcher/secure/<uid>/
/usr/lib/systemd/system/secrets-dispatcher-secure@.service
/usr/lib/systemd/user/secrets-dispatcher-secure-agent.service
```

Use the numeric desktop UID as the instance key. Trusted configuration records
the expected username and UID and startup rejects mismatches or UID reuse.

## Broker Startup

The launcher must:

1. Require effective UID 0.
2. Parse a canonical decimal desktop UID.
3. Verify the desktop identity against trusted provisioning data.
4. Resolve a non-root backend UID distinct from the desktop UID.
5. Create per-instance runtime paths with exact ownership and modes.
6. Start a private bus and GNOME Keyring as the backend UID with supplementary
   groups cleared and a fixed minimal environment.
7. Connect to desktop and backend buses with target credentials and explicit
   external authentication for those credentials.
8. Start proxying only after both buses and the backend are ready.
9. Supervise all children as one process group and terminate them together.
10. Fail closed on policy, ownership, authentication, or readiness errors.

The backend environment is limited to fixed `PATH`, `HOME`,
`XDG_RUNTIME_DIR`, and `DBUS_SESSION_BUS_ADDRESS`. It must not inherit desktop
display, loader, language-runtime, or agent variables.

## Authorization and UI

The broker exposes a narrow system-bus control interface separate from the
Secret Service data plane. Read methods expose status, pending summaries, and
history. Mutation methods approve, deny, cancel, or alter trusted policy.

Every secret-releasing or policy-mutating operation requires a Polkit action:

```text
net.mowaka.secrets-dispatcher.secure-local.decide
```

Requirements:

- use `auth_self` for an active local session;
- do not use authorization caching;
- bind authorization to the exact request ID, decision, item set, and operation;
- atomically verify that the request is still pending;
- reject replayed and stale decisions.

The desktop agent may host the current Web UI and display notifications, but a
browser cookie or user-owned bearer token is never sufficient authority.
Notification actions and CLI decisions invoke the same Polkit-checked broker
methods.

Secure-local v1 supports root-owned deny rules and otherwise prompts. Existing
user-owned automatic approve rules are not imported. Same-user process metadata
cannot safely authorize release against the primary threat model.

## Unlock and Prompt Ownership

Unlock continues to use Secret Service and GNOME protocols:

1. The client calls `Unlock`.
2. The backend returns a `Secret.Prompt` object.
3. The broker binds the prompt path to the originating D-Bus sender.
4. Only that sender may call `Prompt` or `Dismiss` for the path.
5. The broker claims `org.gnome.keyring.SystemPrompter` on the private backend
   bus before unlock can activate a fallback.
6. Prompter calls are forwarded to GNOME Shell's real session-bus prompter.
7. The encrypted exchange remains opaque to the broker.
8. `Completed` is forwarded and prompt ownership is removed.

Prompt ownership is also removed on disconnect or timeout. Unlock fails closed
when the real desktop prompter is unavailable. Exchange payloads are never
logged, and plaintext passwords never pass through the Web API.

A cross-UID SystemPrompter spike is a release-blocking prerequisite.

## GNOME Lifecycle

The implementation must preserve current upstream behavior:

- detect the provider before takeover;
- demote GNOME Keyring to non-secret components rather than masking the whole
  daemon;
- shadow autostart and mask public Secret Service activation;
- retain PAM startup and PKCS#11 functionality;
- back up and restore pre-existing files exactly;
- forward Secret Prompt objects and `Completed` signals;
- bridge `org.gnome.keyring.SystemPrompter` across the backend bus.

Whole-daemon masking is rejected because PAM can respawn an unmanaged daemon
and because masking breaks non-secret components.

The desktop secure agent acts as a graphical-session lease. Final lease loss
stops GNOME Keyring and destroys private runtime state. A lingering user manager
must not keep an unlocked backend alive.

## Provisioning and Commands

Proposed flow:

```bash
sudo secrets-dispatcher secure-local provision --user "$USER"
secrets-dispatcher secure-local migrate --dry-run
secrets-dispatcher secure-local migrate
secrets-dispatcher service install --mode secure-local --start
secrets-dispatcher service status
```

Removal:

```bash
secrets-dispatcher service uninstall
sudo secrets-dispatcher secure-local deprovision --user "$USER"
```

Provisioning creates trusted artifacts but does not perform takeover.
Deprovisioning refuses while secure-local is enabled and retains backend data
unless `--purge` is explicit.

## Migration

Migration uses Secret Service APIs. It must never copy or `chown` GNOME Keyring
files.

The migration process must:

1. Unlock source and destination through normal prompt flows.
2. Enumerate source collections and items.
3. Present a dry-run with counts, conflicts, and unsupported metadata.
4. Copy labels, attributes, collection mapping, content type, and secret value.
5. Keep a root-owned resumable journal containing identifiers and status, never
   secret values.
6. Enter a maintenance cutover that blocks new source mutations.
7. Perform a final delta scan and verify destination contents.
8. Transfer public ownership only after verification.
9. Roll back takeover if any cutover step fails.

Conflict handling is explicit: `fail`, `skip`, or `replace`, with `fail` as the
default. Reverse migration is provided before returning to ordinary local mode.

## Root Path Validation

Lexical path-prefix checks are insufficient. Root-trusted artifacts must:

- use fixed package-managed paths in production;
- be opened without following symlinks or magic links;
- have only root-owned, non-writable ancestors;
- be root-owned regular files that are not group- or world-writable;
- reject control characters, traversal, systemd specifier injection, and
  pre-created destination symlinks;
- be written atomically with same-directory temporary files, `fsync`, and
  rename;
- be revalidated at every broker startup.

General `--binary` and `--home-base` overrides are not available in production
secure-local mode.

## Testing

### Security Spikes

- Explicit target-UID D-Bus authentication with both `dbus-daemon` and
  `dbus-broker`.
- SystemPrompter forwarding across desktop, root, and backend UIDs.
- Non-cached Polkit authorization from Web, notification, and CLI flows.
- Graphical lease behavior across logout, relogin, and linger.

### Unit and Integration Tests

- UID separation, minimal environment, and root path validation.
- Polkit required for every mutation, including stale/replay rejection.
- Prompt ownership, completion, timeout, and disconnect cleanup.
- Approved requests reach the backend; denied requests never do.
- Desktop UID cannot connect to the backend transport.
- Broker restart cancels pending requests and prompts safely.
- Interrupted migrations resume without duplicate writes.

### GNOME VM Tests

Extend the current `e2e/gnome/vm/` harness on Ubuntu 24.04 and 26.04:

- secure provisioning, takeover, and exact uninstall restoration;
- backend files and bus inaccessible to the desktop UID;
- real GNOME unlock across UIDs;
- Polkit-backed Web, notification, and CLI approval;
- relogin, logout, linger, suspend, and resume behavior;
- forward and reverse migration with conflict and interruption coverage;
- two desktop users with separate backends, stores, buses, and approvals.

## Delivery Milestones

1. Security spikes.
2. Fixed-path provisioning and validation.
3. Trusted broker and private backend transport.
4. Current GNOME takeover and graphical-session lifecycle integration.
5. Polkit-authorized desktop agent and UI.
6. Cross-UID prompt and SystemPrompter support.
7. Forward and reverse migration.
8. Multi-user, adversarial, packaging, and recovery hardening.

Each milestone must remain independently testable. No public secure-local mode
is advertised until unlock, authorization, migration, rollback, and GNOME VM
gates are complete.

## Acceptance Criteria

- The desktop UID cannot access backend storage, memory, or transport.
- No user-readable token can release a secret or mutate trusted policy.
- Every release receives fresh Polkit-backed human authorization unless denied
  by root-owned policy.
- Native GNOME unlock works through the encrypted prompter exchange.
- GNOME Keyring cannot re-grab the public name across login.
- The backend terminates with the graphical session lease.
- Provisioning rejects user-writable and symlinked trusted paths.
- Migration is explicit, resumable, verifiable, and reversible.
- Multiple users remain isolated.
- Existing local, remote, full, GPG, and GNOME lifecycle tests remain green.

## Non-Goals

- Protect plaintext `.env` files, browser storage, clipboard contents, process
  memory, or secrets after approved delivery.
- Sandbox arbitrary desktop applications.
- Prevent denial of service, all bus-name races, or desktop phishing.
- Treat process ancestry or executable paths as strong identity.
- Support automatic approve rules in secure-local v1.
- Support non-GNOME backends in the first release.
- Support systems without systemd, logind, and Polkit.
- Add macOS or Windows support.
- Add GPG-signing or SSH-agent privilege separation.
- Transparently synchronize old and secure keyring stores.
- Copy or change ownership of existing GNOME Keyring files.
- Keep an unlocked backend alive solely because the user manager lingers.
