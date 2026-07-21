# Compatibility & status

## Tested environments

| Environment | Status |
|---|---|
| Ubuntu 24.04 & 26.04 LTS · GNOME on Wayland | ✅ Full-desktop end-to-end tested in CI on every PR |
| Arch Linux | ✅ [AUR package](https://aur.archlinux.org/packages/secrets-dispatcher) · primary dev environment |
| gnome-keyring (drop-in-place takeover) | ✅ Tested |
| [gopass-secret-service](https://github.com/nikicat/gopass-secret-service) | ✅ Tested |
| KeePassXC · KDE Wallet · other Secret Service backends | 🟡 Standard protocol — expected to work, not yet verified |
| KDE Plasma · Sway / other wlroots · X11 sessions | 🟡 Expected to work, not yet verified |

Running it somewhere that isn't listed?
**[Tell us whether it worked](https://github.com/nikicat/secrets-dispatcher/issues)** —
compatibility reports are exactly the kind of feedback that decides where this
project goes next.

## Works with

**Any Secret Service backend** — it adds access control regardless of which
keyring you use:

- [gopass-secret-service](https://github.com/nikicat/gopass-secret-service) · GNOME Keyring · KDE Wallet · KeePassXC

**Any Secret Service client:**

- Firefox, Chromium / Chrome, Electron apps
- `secret-tool`, Python `secretstorage`
- AI coding agents (Claude Code, Codex, …)
- anything using libsecret

### Works great with gopass-secret-service

Together they form a complete stack: gopass-secret-service provides the Secret
Service backend (secrets in GPG-encrypted, git-synced GoPass), and
secrets-dispatcher adds per-operation approval and audit logging on top.

```
App → secrets-dispatcher → gopass-secret-service → GoPass → GPG
       (access control)     (Secret Service API)    (store)  (encryption)
```

## Feature status

| Feature | Status |
|---------|--------|
| Secret Service proxy (local & remote) | Working — proxy, audit logging, trust rules |
| Git GPG commit signing | Working — setup, signing flow, approval UI, auto-approve |
| Web UI | Working — real-time updates, approve/deny, history, trust rules |
| Desktop notifications | Working — inline approve/deny actions |
| CLI | Working — list, approve, deny, history |
| Process chain detection | Working — full ancestry with exe, CWD, systemd unit |
| Trust rules engine | Working — process + secret matching with globs |
| Client pairing (remote) | Planned |
| DH encryption for secrets in transit | Planned |
