# secrets-dispatcher

[![CI](https://github.com/nikicat/secrets-dispatcher/actions/workflows/check.yml/badge.svg)](https://github.com/nikicat/secrets-dispatcher/actions/workflows/check.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/nikicat/secrets-dispatcher)](https://goreportcard.com/report/github.com/nikicat/secrets-dispatcher)
[![Release](https://img.shields.io/github/v/release/nikicat/secrets-dispatcher)](https://github.com/nikicat/secrets-dispatcher/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Your AI coding agent runs as you — so it can read every secret in your keyring, silently.** Claude Code, Cursor, Codex, any script you launch: they call the Linux [Secret Service](https://specifications.freedesktop.org/secret-service/) and read any unlocked credential with no prompt and no log. Usually it isn't malice — just an agent taking a wrong path to a task — but you never see it happen.

**secrets-dispatcher** is the checkpoint that makes it visible. When something reads a secret, you see *what* it touched and the full process chain (`claude-code → node → secret-tool`), and you approve, deny, or auto-allow — the tools you trust fade into rules, everything else has to ask. Every access is logged, and the same gate covers what gets committed and signed as you (`git commit -S`). A drop-in proxy: same keyring, same data, **[reversible in one command](#quick-start)** — Ctrl-C undoes everything.

![secrets-dispatcher sits between the apps that request secrets — browser, AI agents, CLI tools, git commit -S — and your keyring / GPG signing key, adding per-request approval, full process-chain detection, trust rules, and an audit log](docs/diagram/architecture.png)

> **Honest scope.** This is visibility and control — *not* a sandbox or a privilege
> boundary. It runs as your user, and it gates the **keyring** and **GPG signing**, not
> `.env` files or arbitrary disk reads. A smoke detector, not a vault door: pair it with a
> sandbox if you need to *contain* an agent; use this to *see and decide* when the agents,
> apps, and scripts you run natively touch your secrets or signing key. [More on the security
> model →](SECURITY.md)

## Why

**No keyring access control.** Any process running as your user can call the Secret Service D-Bus API and read any unlocked secret — browsers, Electron apps, CLI tools, and AI coding agents (Claude Code, Codex, Cursor). There's no audit trail, no per-app permissions, and no way to know which process accessed what.

**Git signing is blind.** `git commit -S` invokes GPG with no human-visible context. When AI agents or CI pipelines make commits, arbitrary content gets signed without review. There's no GPG signing approval step.

**gpg-agent forwarding is all-or-nothing.** Forwarding a GPG agent over SSH gives the remote machine blanket access to decrypt *any* secret. No per-secret control.

secrets-dispatcher adds a controlled gateway with:
- **Full process chain visibility** — not just "dbus-daemon asked" but `claude-code → node → secret-tool`
- **Per-operation approval** via web UI, desktop notifications, or CLI
- **Trust rules** — auto-approve known-safe patterns, prompt for everything else
- **Audit logging** — JSON log of every access attempt with process info and decision

## Verified on

| Environment | Status |
|---|---|
| Ubuntu 24.04 & 26.04 LTS · GNOME on Wayland | ✅ Full-desktop end-to-end tested in CI on every PR |
| Arch Linux | ✅ [AUR package](https://aur.archlinux.org/packages/secrets-dispatcher) · primary dev environment |
| gnome-keyring (drop-in-place takeover) | ✅ Tested |
| [gopass-secret-service](https://github.com/nikicat/gopass-secret-service) | ✅ Tested |
| KeePassXC · KDE Wallet · other Secret Service backends | 🟡 Standard protocol — expected to work, not yet verified |
| KDE Plasma · Sway / other wlroots · X11 sessions | 🟡 Expected to work, not yet verified |

Running it somewhere that isn't listed? **[Tell us whether it worked](https://github.com/nikicat/secrets-dispatcher/issues)** — compatibility reports are exactly the kind of feedback that decides where this project goes next.

## Quick Start

You need a running Secret Service keyring (gnome-keyring, KeePassXC, KDE Wallet, [gopass-secret-service](https://github.com/nikicat/gopass-secret-service)…) and/or GPG for signing — `try` and `service install` auto-detect what you already have.

### Install

**Prebuilt binary** — each [release](https://github.com/nikicat/secrets-dispatcher/releases) ships static Linux binaries (amd64/arm64):

```bash
curl -Lo ~/.local/bin/secrets-dispatcher \
  https://github.com/nikicat/secrets-dispatcher/releases/latest/download/secrets-dispatcher-linux-amd64
chmod +x ~/.local/bin/secrets-dispatcher
```

**With Go** (full build, web UI included — the compiled frontend is committed):

```bash
go install github.com/nikicat/secrets-dispatcher@latest
```

**From source** (requires Go and npm for the embedded web UI):

```bash
git clone https://github.com/nikicat/secrets-dispatcher.git
cd secrets-dispatcher
make build && make install   # installs to ~/.local/bin
```

### Try it (fully reversible)

The fastest way to see the approval flow on your real desktop — one command,
no commitment:

```bash
secrets-dispatcher try
```

`try` detects your current Secret Service (e.g. gnome-keyring), slips the
dispatcher in front of it (same keyring data, demoted to a private backend),
and prints the web UI address. Then make something ask for a secret and watch
it surface:

```bash
secret-tool store --label=demo service demo   # store a throwaway secret
secret-tool lookup service demo                # → an approval prompt appears
```

**Ctrl-C ends the trial and restores everything exactly** — every file and unit
it touched is reverted, and your config is never modified. Run `secrets-dispatcher
try --dry-run` first to see the precise file and unit changes, and `secrets-dispatcher
service status` any time to confirm the dispatcher is really in front and the
takeover state is consistent.

### Secret Access Control (local)

```bash
# Start the daemon
secrets-dispatcher serve &

# Or install as a systemd user service (auto-start on login)
secrets-dispatcher service install --start

# On GNOME: put the dispatcher in front of your existing keyring. Detects
# gnome-keyring, demotes only its secrets component to a private backend
# (pkcs11/ssh/PAM unlock keep working), same keyring data. `service
# uninstall` restores stock behavior exactly.
secrets-dispatcher service install --mode local --start

# Open the web UI
secrets-dispatcher login

# Now any secret access triggers an approval prompt
secret-tool lookup service smtp   # → you'll see a notification
```

### Git Commit Signing

```bash
# One-time setup
secrets-dispatcher gpg-sign setup
git config --global commit.gpgsign true

# Now every signed commit requires approval
git commit -S -m "my signed commit"
# → desktop notification with repo, message, changed files
# → approve or deny before GPG signs
```

## Approval Interfaces

When a request isn't already covered by a rule, you get the full picture before deciding — what's asking (the whole process chain), for which secret, and Approve / Deny:

![secrets-dispatcher web UI: a pending secret-access request showing the full process chain, with Approve and Deny buttons](docs/screenshots/webui-overview.png)

Approve or deny requests through any of:

- **Web UI** — real-time dashboard at `http://127.0.0.1:8484` (open with `secrets-dispatcher login`)
- **Desktop notifications** — inline Approve/Deny action buttons
- **CLI** — `secrets-dispatcher list`, `secrets-dispatcher approve <id>`, `secrets-dispatcher deny <id>`

All three update in real-time — approve via notification and the web UI reflects it instantly.

## Trust Rules

Auto-approve known-safe patterns instead of prompting for every request. Add to `~/.config/secrets-dispatcher/config.yaml`:

```yaml
serve:
  rules:
    # Auto-approve Firefox accessing any secret
    - name: firefox
      action: approve
      process:
        exe: "/usr/lib/firefox/firefox"

    # Auto-approve tools running from your project directory
    - name: my-project
      action: approve
      process:
        cwd: "/home/me/src/my-project/*"

    # Auto-approve a shell-script wrapper (exe is the interpreter, so
    # identify the script via its argv)
    - name: logcli-wrapper
      action: approve
      process:
        exe: "/usr/bin/bash"
        args: "/home/me/.local/bin/logcli"

    # Ignore Chrome's dummy secret probe
    - name: chrome-probe
      action: ignore
      request_types: [write]
      process:
        exe: "*chrome*"

    # Auto-approve deploy script accessing deploy secrets
    - name: deploy
      action: approve
      process:
        exe: "/usr/bin/ansible-playbook"
      secret:
        collection: "deploy"

  # Auto-approve GPG signing from specific editors
  trusted_signers:
    - exe_path: /usr/bin/nvim
```

Rules match on process attributes (exe, name, args, CWD, systemd unit) and secret attributes (collection, label, custom attributes). All patterns support globs. Process matching checks the full process chain, not just the immediate caller. `args` is matched against each individual cmdline argument of each process in the chain — useful for interpreter-run scripts, whose exe is the interpreter (`/usr/bin/bash`) while the script path only appears in argv.

For security-relevant rules — especially `deny` — match on **`exe`**: it compares the kernel-resolved `/proc/PID/exe` and cannot be spoofed. **`name`** matches the process `comm`, which any process can set freely (`prctl(PR_SET_NAME)`); treat it as advisory only and never rely on it to block an application. **`args`** is likewise self-reported — a process can rewrite its argv after exec — so it is advisory only too. **`unit`** matches the caller's real systemd unit (resolved via `GetUnitByPID`), which is authoritative for systemd-managed services.

## Process Chain Detection

When a request comes in, secrets-dispatcher resolves the full process ancestry:

```
Request: GetSecrets → collection/login/github-token
Process chain: claude-code → node → dbus-send
Unit: user@1000.service
```

This means you can write rules that match on the actual originating process, not just the D-Bus sender. Useful for distinguishing "Firefox wants my GitHub token" from "unknown-script → curl → dbus-send wants my GitHub token."

## Secret Service Proxy (Remote Servers)

For accessing secrets on remote servers without forwarding your GPG agent:

```
SERVER (untrusted)                         LAPTOP (trusted)
┌─────────────────────────┐               ┌─────────────────────────────────┐
│                         │               │                                 │
│  App ──► local D-Bus ───┼── SSH ───────►│ secrets-dispatcher              │
│          (libsecret)    │   tunnel      │        │                        │
│                         │               │        ▼                        │
│  No secrets stored here │               │  Local Secret Service           │
│                         │               │  (gopass/gnome-keyring/etc)     │
└─────────────────────────┘               └─────────────────────────────────┘
```

```bash
# SSH with tunnel (laptop)
ssh -L /run/user/1000/secrets-dispatcher/myserver.sock:/run/user/1001/bus user@server

# Start secrets-dispatcher (laptop)
secrets-dispatcher serve --downstream socket:/run/user/1000/secrets-dispatcher/myserver.sock

# Use secrets on server — no changes needed, apps use standard D-Bus
secret-tool lookup service myapp
```

See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for the remote-proxy design and requirements (threat model, access-control rules, transport security).

## Audit Logging

All secret access is logged to stderr in structured JSON:

```json
{"time":"2025-03-09T14:22:01Z","level":"INFO","msg":"dbus_call","method":"GetSecrets","items":["collection/login/github-token"],"process_chain":["claude-code","node","dbus-send"],"result":"approved"}
```

## Configuration

Config file: `~/.config/secrets-dispatcher/config.yaml`

```yaml
listen: "127.0.0.1:8484"          # Web UI address
state_dir: "~/.local/state/secrets-dispatcher"

serve:
  log_level: info                  # debug, info, warn, error
  timeout: 5m                      # approval request timeout
  approval_window: 2s              # batch concurrent requests
  notification_delay: 1s           # suppress short-lived requests
  notifications: true              # desktop notifications
  ignore_chrome_dummy_secret: true # suppress Chrome's probe
  rules: []                        # trust rules (see above)
  trusted_signers: []              # GPG signing auto-approve
```

## Compatibility

**Works with** any Secret Service backend — adds audit logging and access control regardless of which keyring you use:
- [gopass-secret-service](https://github.com/nikicat/gopass-secret-service)
- GNOME Keyring
- KDE Wallet
- KeePassXC

**Works with** any Secret Service client:
- Firefox, Chromium/Chrome, Electron apps
- `secret-tool`, Python `secretstorage`
- AI coding agents (Claude Code, Codex, etc.)
- Any application using libsecret

### Works great with [gopass-secret-service](https://github.com/nikicat/gopass-secret-service)

Together they form a complete stack: gopass-secret-service provides the Secret Service backend (storing secrets in GPG-encrypted, git-synced GoPass), and secrets-dispatcher adds per-operation approval and audit logging on top.

```
App → secrets-dispatcher → gopass-secret-service → GoPass → GPG
       (access control)     (Secret Service API)    (store)  (encryption)
```

## Status

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

## Development

```bash
make build          # Build with embedded frontend
make test-go        # Run Go tests
make test-e2e       # Run Playwright E2E tests
make pre-commit     # Lint + format + staticcheck
make demo           # Record demo videos in the Tier-2 GNOME VM -> .build/demos/
```

`make demo` boots the same Ubuntu desktop VM the e2e suite uses and screen-records
the install/try arc being typed into a real GNOME terminal (see
`e2e/gnome/vm/demo.sh`). Videos are build artifacts, never committed; the
manual [Demos workflow](.github/workflows/demos.yml) records them in CI and
uploads the result as a workflow artifact.

## Documentation

- [Architecture](docs/ARCHITECTURE.md) — how a request is decided (with the flow diagram)
- [Remote-proxy design & requirements](docs/REQUIREMENTS.md)
- [Target Audience & User Personas](docs/TARGET-AUDIENCE.md)
- [Contributing](CONTRIBUTING.md)

## License

MIT License
