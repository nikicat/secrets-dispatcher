# ADR 0005: Trust rules match the caller's full process chain, keyed on `exe`

Date: 2026-07-21 · Status: accepted

## Context

The immediate D-Bus caller is often a generic tool (`secret-tool`, `node`,
`dbus-send`) started by the application that actually wants the secret. A rule
on the immediate caller either approves every user of that tool or nothing.
Process identifiers also differ in how much they can be trusted: `comm` and
argv are set by the process itself.

## Decision

- The dispatcher resolves the caller's **full process ancestry** for every
  request, and rules match anywhere in that chain.
- Rules live under `serve.rules` in `~/.config/secrets-dispatcher/config.yaml`
  (YAML, so they can be versioned) and take effect on restart. Patterns are
  globs (`path.Match`), and all non-empty matchers are AND-ed. Actions are
  `approve`, `deny`, and `ignore` (writes only).
- **`exe`**, the kernel-resolved `/proc/PID/exe`, is the identifier for
  security-relevant rules. **`unit`**, resolved with systemd's `GetUnitByPID`,
  is authoritative for managed services. **`name`** (`comm`, 15 characters) and
  **`args`** are self-reported and advisory only.
- GPG signing has its own allow-list, `trusted_signers`, keyed on executable
  path.

## Consequences

- A rule can tell "Firefox wants the GitHub token" from
  "unknown-script → curl → dbus-send wants the GitHub token".
- A `deny` written on `name` or `args` is not a control; the docs and the
  `secrets-rule` skill steer rules to `exe`.
- Requests that arrive over a tunnel have no local process chain, so local
  rules cannot match them ([ADR 0011](0011-remote-signing-through-per-remote-sockets.md)).
