# secrets-dispatcher

A secure approval gateway for secret operations. Proxies [Secret Service](https://specifications.freedesktop.org/secret-service/latest/) requests from remote servers and intercepts git GPG commit signing — both with per-operation approval prompts showing full context.

## Problem

**Remote secrets:** Forwarding `gpg-agent` to untrusted servers exposes ALL secrets (authorizes by key, not by secret). A compromised server can silently bulk-decrypt everything with no visibility into what's being accessed.

**Git commit signing:** GPG signing happens silently — `git commit -S` invokes gpg with no human-visible context about what's being signed. Automated or compromised environments can sign arbitrary commits.

## Solution

`secrets-dispatcher` acts as a controlled gateway for both:

**Secret Service proxy:**
- Connects to remote server's D-Bus via SSH tunnel
- Registers as `org.freedesktop.secrets` on that bus
- Proxies requests to local Secret Service (gopass, gnome-keyring, etc.)

**Git GPG signing proxy:**
- Replaces `gpg` as git's `gpg.program`
- Intercepts signing requests and shows commit context (repo, author, message, changed files)
- Delegates to real `gpg` only after explicit approval

Both modes share:
- Web UI, CLI, and desktop notification approval
- Audit logging
- Configurable timeout (default 5 minutes)

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

**Secret Service proxy:**
- [ ] Per-secret authorization (not per-key like gpg-agent)
- [ ] Full context display (secret path, client identity)
- [ ] Access control rules (allow/deny/prompt per client per path)
- [ ] Client pairing with visual verification (like Enpass)
- [x] Audit logging
- [x] Standard libsecret compatibility (no client-side changes)
- [ ] Secret Service DH encryption for secrets in transit

**Git GPG commit signing:**
- [x] Per-commit approval with full context (repo, author, message, changed files)
- [x] Web UI with approve/deny buttons
- [x] Desktop notifications with inline approve/deny actions
- [x] CLI approve/deny (`secrets-dispatcher approve <id>`)
- [x] One-command git setup (`secrets-dispatcher gpg-sign setup`)
- [x] Graceful cancellation (Ctrl+C cancels the pending request)
- [ ] Auto-accept/reject rules

## Git GPG Commit Signing

### Prerequisites

- GPG key pair configured (`gpg --list-secret-keys`)
- `secrets-dispatcher serve` running (or installed as a systemd service)

### Setup

**1. Configure git to use secrets-dispatcher as the GPG program:**

```bash
secrets-dispatcher gpg-sign setup
```

This creates a wrapper script at `~/.local/bin/secrets-dispatcher-gpg` and sets `gpg.program` in your global git config. Use `--local` for per-repo configuration instead.

**2. Enable commit signing in git** (if not already):

```bash
git config --global commit.gpgsign true
```

### Usage

Sign commits as usual — secrets-dispatcher intercepts transparently:

```bash
git commit -S -m "my signed commit"
```

When git invokes the signing program, secrets-dispatcher:

1. Parses the commit object to extract context (repo name, author, commit message, changed files)
2. Sends a signing request to the daemon via Unix socket
3. Blocks until you approve or deny

Approve via any of:
- **Web UI** — open with `secrets-dispatcher login`
- **Desktop notification** — click Approve/Deny
- **CLI** — `secrets-dispatcher list` then `secrets-dispatcher approve <id>`

If you interrupt (`Ctrl+C`), the pending request is automatically cancelled.

### How It Works

```
git commit -S
    │
    ▼
git calls: secrets-dispatcher-gpg --status-fd=2 -bsau <keyID>
    │                                      stdin: raw commit object
    ▼
secrets-dispatcher gpg-sign (thin client)
    │  Parses commit: repo, author, message, changed files
    │  Connects to daemon via Unix socket
    ▼
secrets-dispatcher serve (daemon)
    │  Creates approval request
    │  Notifies: web UI, desktop notification
    │  Waits for user decision
    ▼
User approves ──► daemon runs real gpg ──► signature returned to git
User denies   ──► git commit fails (exit 1)
```

### Debugging

Set `SECRETS_DISPATCHER_DEBUG=1` to see verbose output from the thin client:

```bash
SECRETS_DISPATCHER_DEBUG=1 git commit -S -m "test"
```

## Secret Service Proxy

### Prerequisites

- A local Secret Service running on your laptop (e.g., gnome-keyring, KeePassXC, or gopass-secret-service)
- SSH access to the remote server
- `dbus-daemon` installed on the server

### Quick Start

**1. Build secrets-dispatcher on your laptop:**

```bash
go build -o secrets-dispatcher .
```

**2. Start a D-Bus daemon on the server (if not already running):**

```bash
# On the server - start D-Bus at the standard session bus location
# Skip this if the server already has a session bus running
dbus-daemon --session --nofork --address="unix:path=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/bus" &
```

**3. SSH to server with local forward (on laptop):**

```bash
# Create runtime directory for secrets-dispatcher sockets
mkdir -p "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/secrets-dispatcher"

# Forward the server's D-Bus session bus to laptop (adjust remote UID as needed)
ssh -L "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/secrets-dispatcher/myserver.sock:/run/user/1001/bus" user@server
```

**4. Start secrets-dispatcher (on laptop, in another terminal):**

```bash
./secrets-dispatcher \
    --remote-socket "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/secrets-dispatcher/myserver.sock" \
    --client myserver
```

**5. Use secrets on the server:**

```bash
# On the remote server - no special configuration needed!
# Apps use the standard D-Bus session bus at /run/user/<UID>/bus
secret-tool lookup service myapp username myuser
```

### How It Works (Secret Service)

```
LAPTOP                                  SERVER
┌────────────────────────────────┐     ┌──────────────────────────┐
│                                │     │                          │
│  secrets-dispatcher            │     │   dbus-daemon            │
│        │                       │     │   (session bus)          │
│        │                       │     │        ▲                 │
│        ▼                       │     │        │                 │
│  $XDG_RUNTIME_DIR/             │     │  $XDG_RUNTIME_DIR/bus    │
│   secrets-dispatcher/ ─────────┼─────┼───► (standard location)  │
│     myserver.sock        SSH -L│     │        ▲                 │
│        │                       │     │        │                 │
│        ▼                       │     │   secret-tool / apps     │
│  Local Session D-Bus           │     │   (use D-Bus normally)   │
│        │                       │     │                          │
│        ▼                       │     │                          │
│  Secret Service                │     │                          │
│  (gnome-keyring/gopass/etc)    │     │                          │
└────────────────────────────────┘     └──────────────────────────┘
```

1. A dedicated D-Bus daemon runs on the server for secrets communication
2. SSH local forward (`-L`) tunnels that socket to your laptop
3. secrets-dispatcher connects to the tunneled socket and registers as `org.freedesktop.secrets`
4. Apps on the server connect to the same D-Bus and find secrets-dispatcher
5. secrets-dispatcher proxies requests to your local Secret Service
6. All access is logged for audit

### SSH Config Setup

Add to `~/.ssh/config`:

```
Host myserver
    HostName server.example.com
    User myuser

    # Forward remote D-Bus socket to local secrets-dispatcher directory
    # Note: SSH config doesn't expand variables, use absolute paths
    # Local UID 1000, remote UID 1001 - adjust as needed
    LocalForward /run/user/1000/secrets-dispatcher/myserver.sock /run/user/1001/bus

    # Remove existing socket before binding (handles reconnects)
    StreamLocalBindUnlink yes

    # Keep connection alive for long sessions
    ServerAliveInterval 60
    ServerAliveCountMax 3
```

### Automation Script

Create `~/bin/secrets-connect` on your laptop:

```bash
#!/bin/bash
set -e

SERVER="$1"
RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
SOCK_DIR="$RUNTIME_DIR/secrets-dispatcher"
LOCAL_SOCK="$SOCK_DIR/$SERVER.sock"

# Create socket directory
mkdir -p "$SOCK_DIR"

# Start SSH with tunnel in background
# StreamLocalBindUnlink removes existing socket on reconnect
# Get remote user's UID via ssh (or hardcode if known)
REMOTE_UID=$(ssh "$SERVER" 'id -u')
ssh -f -N -o StreamLocalBindUnlink=yes \
    -L "$LOCAL_SOCK:/run/user/$REMOTE_UID/bus" "$SERVER"

# Wait for socket to be created
for i in {1..10}; do
    [ -S "$LOCAL_SOCK" ] && break
    sleep 0.5
done

# Start secrets-dispatcher
exec secrets-dispatcher --remote-socket "$LOCAL_SOCK" --client "$SERVER"
```

Usage: `secrets-connect myserver`

### Server-Side Setup

Add to your server's `~/.bashrc` or `~/.zshrc`:

```bash
# Start D-Bus session daemon if not running (for headless servers)
if [ -z "$DBUS_SESSION_BUS_ADDRESS" ]; then
    _bus_path="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/bus"
    if [ ! -S "$_bus_path" ]; then
        dbus-daemon --session --fork --address="unix:path=$_bus_path"
    fi
    export DBUS_SESSION_BUS_ADDRESS="unix:path=$_bus_path"
    unset _bus_path
fi
```

No special configuration needed for apps - they use the standard D-Bus session bus automatically.

### Audit Logging

All secret access is logged to stderr in JSON format:

```json
{"time":"...","level":"INFO","msg":"dbus_call","client":"myserver","method":"GetSecrets","items":["/org/freedesktop/secrets/collection/login/1"],"result":"ok"}
```

Redirect to a file for persistent logging:

```bash
./secrets-dispatcher --remote-socket /path/to/sock --client myserver 2>> ~/.local/log/secrets-dispatcher.log
```

### Troubleshooting

**"Connection refused" on server:**
- Check that the SSH tunnel is active: `ls -la /run/user/$(id -u)/secrets.sock`
- Ensure secrets-dispatcher is running on the laptop

**"No such object" errors:**
- Verify your local Secret Service is running: `secret-tool search --all`
- Check secrets-dispatcher logs for errors

**Socket permission denied:**
- Ensure the remote socket path is in your user's runtime dir (`/run/user/$(id -u)/`)

## Status

- **Secret Service proxy:** Basic proxy with audit logging is complete.
- **Git GPG commit signing:** Fully functional — setup, signing flow, web UI, desktop notifications, CLI, and cancellation all implemented.
- **Approval UI:** Web UI with real-time updates, desktop notifications with inline actions, and CLI are all working.

See `docs/REQUIREMENTS.md` for the full roadmap.
