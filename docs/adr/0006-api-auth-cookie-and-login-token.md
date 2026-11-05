# ADR 0006: API auth: a persisted master token, and one-time login for browsers

Date: 2026-02-07 · Status: accepted

## Context

The HTTP/WebSocket API approves and denies requests, so whoever can call it can
approve their own secret access. It is used by the CLI, the `gpg-sign` thin
client, and the web UI in a browser, which cannot read files.

## Decision

- **Master token.** A random 256-bit token in
  `$XDG_STATE_HOME/secrets-dispatcher/.cookie` (mode 0600, bitcoind-style). It
  is generated once and loaded on every later start, so clients keep working
  across restarts. CLI tools and the thin client send it as
  `Authorization: Bearer`.
- **Local unix socket.** The same API is also served on an owner-only socket,
  `$XDG_RUNTIME_DIR/secrets-dispatcher/api.sock`, for same-user clients.
- **Browsers** authenticate with `secrets-dispatcher login`: the CLI mints a
  one-time, short-lived token and opens the login URL; the server exchanges it
  for an HttpOnly, SameSite=Strict session cookie. Sessions are independent
  random IDs, never the master token, so a leaked session cookie does not leak
  it.

## Consequences

- The master token grants the whole API, approvals included. It must never be
  handed to anything whose requests it would then approve, such as a remote
  machine ([ADR 0011](0011-remote-signing-through-per-remote-sockets.md)).
- Filesystem permissions are the only protection of the token, which is in
  line with [ADR 0002](0002-visibility-not-isolation.md).
