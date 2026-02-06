# secrets-dispatcher

A secure proxy for dispatching secrets from a local Secret Service to remote servers.

## Problem

When accessing secrets from untrusted remote servers:
- Forwarding `gpg-agent` exposes ALL secrets (authorizes by key, not by secret)
- No visibility into which secret is requested or by which process
- Compromised server can silently bulk-decrypt everything

## Solution

`secrets-dispatcher` acts as a controlled gateway:
- Connects to remote server's D-Bus via SSH tunnel
- Registers as `org.freedesktop.secrets` on that bus
- Proxies requests to local Secret Service (gopass, gnome-keyring, etc.)
- Shows approval prompts with full context
- Enforces per-client access rules

## Architecture

```
SERVER (untrusted)                         LAPTOP (trusted)
┌─────────────────────────┐               ┌─────────────────────────────────┐
│                         │               │                                 │
│  App ──► local D-Bus ───┼── SSH ───────►│ secrets-dispatcher              │
│          (libsecret)    │   tunnel      │        │                        │
│                         │               │        ▼                        │
│  No secrets stored here │               │  Local D-Bus                    │
│                         │               │        │                        │
└─────────────────────────┘               │        ▼                        │
                                          │  Secret Service                 │
                                          │  (gopass/gnome-keyring/etc)     │
                                          └─────────────────────────────────┘
```

## Features

- [ ] Per-secret authorization (not per-key like gpg-agent)
- [ ] Full context display (secret path, client identity)
- [ ] Access control rules (allow/deny/prompt per client per path)
- [ ] Client pairing with visual verification (like Enpass)
- [ ] Audit logging
- [ ] Standard libsecret compatibility (no client-side changes)
- [ ] Secret Service DH encryption for secrets in transit

## Status

Early development. See `docs/REQUIREMENTS.md` for detailed specification.
