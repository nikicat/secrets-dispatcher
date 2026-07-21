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

See [REQUIREMENTS.md](REQUIREMENTS.md) for the full design — threat model,
access-control model, and the planned client-pairing and transport-encryption
work.
