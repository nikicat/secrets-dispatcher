# ADR 0003: Remote clients are identified by their tunnel; pairing is deferred

Date: 2026-02-06 · Status: accepted

## Context

The first use case was an untrusted server that needs secrets kept on a trusted
laptop. The usual answer, forwarding gpg-agent and syncing the encrypted store,
authorizes per key: once the passphrase is cached, a compromised server can
decrypt every secret silently, and the user never sees which secret or which
process asked.

## Decision

Each server reaches the laptop's dispatcher over its own SSH tunnel to the
server's session bus (`serve --downstream socket:<path>`). The connection is
the identity: every request arriving on that tunnel is from that server, and is
approved per secret. Requests are shown and logged with the client they came
from.

Cryptographic client pairing (SRP, SPAKE2 or a visually confirmed code), which
would make the transport MITM-resistant independently of SSH, is deferred
(task T0002).

## Consequences

- Servers use standard libsecret; nothing changes on their side.
- Trust in a client is the trust in whoever set up its tunnel: the laptop user.
- When the laptop is offline, remote requests fail with a Secret Service
  error. Nothing is queued.
