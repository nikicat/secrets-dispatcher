# secrets-dispatcher

A secure proxy for dispatching secrets from a local [Secret Service](https://specifications.freedesktop.org/secret-service/latest/) to remote servers.

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
- [x] Audit logging
- [x] Standard libsecret compatibility (no client-side changes)
- [ ] Secret Service DH encryption for secrets in transit

## Usage

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

### How It Works

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

Phase 1 (basic proxy with logging) is complete. See `docs/REQUIREMENTS.md` for the full roadmap.
