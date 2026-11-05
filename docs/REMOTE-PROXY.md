# Remote secret access over SSH

Use secrets stored on your trusted laptop from an untrusted server **without
forwarding your gpg-agent** — which would hand the server blanket power to
decrypt *any* secret with no per-secret control. Instead, the server's apps talk
to a standard Secret Service over an SSH tunnel, and every request surfaces on
your laptop for approval.

```
SERVER (untrusted)                         LAPTOP (trusted)
┌─────────────────────────┐               ┌─────────────────────────────────┐
│                         │               │                                 │
│  App ──► local D-Bus ───┼── SSH ───────►│ secrets-dispatcher              │
│          (libsecret)    │   tunnel      │        │                        │
│                         │               │        ▼                        │
│  No secrets stored here │               │  Local Secret Service           │
│                         │               │  (gopass / gnome-keyring / …)   │
└─────────────────────────┘               └─────────────────────────────────┘
```

```bash
# 1. SSH with a tunnel (from the laptop)
ssh -L /run/user/1000/secrets-dispatcher/myserver.sock:/run/user/1001/bus user@server

# 2. Run the dispatcher against the tunneled bus (laptop)
secrets-dispatcher serve --downstream socket:/run/user/1000/secrets-dispatcher/myserver.sock

# 3. On the server — no app changes; standard libsecret / D-Bus
secret-tool lookup service myapp
```

Each secret request from the server appears on your laptop for approval, tagged
with the requesting client. Trust rules (see
[TRUST-RULES.md](TRUST-RULES.md)) let you pre-authorize known-good patterns
(e.g. a deploy script → deploy secrets) and prompt for everything else.

See [ADR 0003](adr/0003-remote-clients-identified-by-their-tunnel.md) for why a
remote is identified by its tunnel, and why client pairing is deferred.

## Signing git commits on a remote host

The remote host's git signs through your laptop's dispatcher, so the signing key
and gpg-agent never leave the laptop and every commit is shown to you first
([ADR 0011](adr/0011-remote-signing-through-per-remote-sockets.md)).

```yaml
# 1. ~/.config/secrets-dispatcher/config.yaml on the laptop, then restart
serve:
  remotes:
    - name: myserver   # served at $XDG_RUNTIME_DIR/secrets-dispatcher/remote/myserver.sock
```

```bash
# 2. Forward that socket to the server (from the laptop)
ssh -R /run/user/1001/secrets-dispatcher-remote.sock:/run/user/1000/secrets-dispatcher/remote/myserver.sock user@server
# The server's sshd needs StreamLocalBindUnlink yes, or a reconnect fails while the old socket file exists

# 3. On the server: point git's gpg.program at a wrapper that signs through the forwarded socket
secrets-dispatcher gpg-sign setup --remote-socket /run/user/1001/secrets-dispatcher-remote.sock
```

Requests show up as `remote:myserver`. The socket accepts signing requests and
nothing else, so the server can ask for a signature but cannot approve it or
see your other requests. Don't forward gpg-agent to the same host, or anything
there can sign without asking.
