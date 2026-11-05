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

## Consequences

- Clients need no changes, and any spec-conforming backend works behind it.
- The dispatcher must win the bus name and keep it: a provider that re-grabs
  the name bypasses it silently (see [ADR 0009](0009-gnome-keyring-secrets-only-demotion.md)).
- Secret values stay encrypted end to end with the Secret Service DH session;
  the dispatcher decides on metadata (caller, item attributes), not values.
