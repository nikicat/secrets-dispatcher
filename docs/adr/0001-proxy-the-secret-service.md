# ADR 0001: Proxy the Secret Service protocol in front of the real backend

Date: 2026-02-06 · Status: accepted

## Context

Secrets on a desktop are served over the freedesktop Secret Service D-Bus API
by gnome-keyring, KDE Wallet, KeePassXC or gopass-secret-service. Every client
(browsers, `secret-tool`, libsecret, AI agents) already speaks it, and none of
them can be changed to ask for approval first. The backends authorize per
collection unlock, not per secret, and say nothing about who is asking.

## Decision

secrets-dispatcher speaks the Secret Service protocol on both sides. It claims
`org.freedesktop.secrets` on the bus clients use and forwards each approved call
to the real backend, which it reaches on a private bus. Sessions are the only
objects it remaps; collection, item and prompt paths pass through 1:1, and
`Secret.Prompt` objects are forwarded so unlock prompts still reach the user.

The dispatcher terminates each client's session encryption itself (`plain` or a
DH key agreement) and always opens a `plain` session with the backend. The
backend is local IPC that the dispatcher already trusts, so encrypting that leg
adds no security.

## Consequences

- Clients need no changes, and any spec-conforming backend works behind it.
- The dispatcher must win the bus name and keep it: a provider that re-grabs
  the name bypasses it silently (see [ADR 0009](0009-gnome-keyring-secrets-only-demotion.md)).
- The dispatcher installs a D-Bus activation mask for the name, so the bus
  starts no other provider. The mask names the dispatcher's own unit as the
  provider. The dispatcher is restarted often while it is developed, and a call
  that arrives during a restart should wait for the new proxy, not fail. While
  no one owns the name, the bus holds such a call and has systemd start the
  unit, or join the restart already under way. Stopping the unit does not keep
  it stopped: the next call starts it again. To keep it down, run
  `systemctl --user mask secrets-dispatcher.service` or `service uninstall`.
- On a forwarded bus, an ssh forward that reconnects can find the name still
  held by its dead connection. There the dispatcher waits in the queue, and the
  bus gives it the name when that connection goes. On the session bus it fails
  instead, since a live provider holds the name there.
- On shutdown the dispatcher gives up the name before it closes anything else,
  so a client never reaches a proxy that is half closed. A Chromium or Electron
  os_crypt probe made during a restart once got "use of closed network
  connection" from the old proxy and kept a plain-text fallback for the rest of
  the process's life.
- Secret values travel encrypted between a DH client and the dispatcher; the
  dispatcher decides on metadata (caller, item attributes), not values.
