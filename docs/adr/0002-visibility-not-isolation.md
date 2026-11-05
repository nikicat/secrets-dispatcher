# ADR 0002: Same-user visibility and control, not a privilege boundary

Date: 2026-03-12 · Status: accepted

## Context

The dispatcher runs as the desktop user, like the keyring and gpg-agent behind
it. Any process of that user can also read the backend's files, talk to
gpg-agent directly, or ptrace the dispatcher. Isolating secrets from the user's
own processes would need a separate uid holding the keys.

## Decision

secrets-dispatcher is a same-user proxy. Its job is to make access to the
keyring and the GPG signing key visible and approvable, and to log it, not to
contain a process that is determined to go around it. Bypassing an approval
through the dispatcher's own interfaces is a vulnerability; bypassing it by
going around the dispatcher (reading files, using gpg-agent directly) is out of
scope.

The companion-user `daemon`/`provision` path on the system bus is an
exploratory experiment toward real privilege separation, not the shipped
product.

## Consequences

- Rules that block an application must match on identifiers a process cannot
  forge ([ADR 0005](0005-trust-rules-on-the-process-chain.md)).
- Anything that hands another machine raw access to the user's agents (a
  forwarded gpg-agent or ssh-agent socket) skips the dispatcher entirely; remote
  access goes through the dispatcher instead ([ADR 0003](0003-remote-clients-identified-by-their-tunnel.md),
  [ADR 0011](0011-remote-signing-through-per-remote-sockets.md)).
