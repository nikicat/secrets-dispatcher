# ADR 0011: Remote git signing through a dedicated socket per remote

Date: 2026-11-05 · Status: proposed

## Context

Commits made on another machine (a build server, a dev container) still need
the user's signature, and the key stays on the laptop. Two ways to get there
exist today, and both are wrong:

- **Forwarding gpg-agent**, which VS Code Dev Containers does by default, gives
  the remote side ungated use of the signing key: any process there can sign
  anything, and the dispatcher never sees it ([ADR 0002](0002-visibility-not-isolation.md)).
- **Forwarding the API socket together with the master token** would route
  signing through the dispatcher, but the token grants the whole API
  ([ADR 0006](0006-api-auth-cookie-and-login-token.md)): the remote side could
  approve its own requests and read every pending request's metadata.

The remote-proxy precedent ([ADR 0003](0003-remote-clients-identified-by-their-tunnel.md))
already identifies a remote by the tunnel it arrives on.

## Decision

- **One listener per configured remote.** `serve.remotes: [{name: <name>}]`
  makes the daemon listen on
  `$XDG_RUNTIME_DIR/secrets-dispatcher/remote/<name>.sock` (mode 0600). The user
  forwards that socket to the remote host with `ssh -R`. The socket a request
  arrives on is the remote's identity; there is no token.
- **The remote socket serves signing only:** one endpoint that submits a
  gpg-sign request and blocks until it is resolved, returning the signature and
  gpg status, or the denial. No approve, list, history or WebSocket endpoint is
  reachable from it.
- **Requests are labelled with the remote,** as client `remote:<name>`.
  Peer credentials there only name the local `ssh` process, so the dispatcher
  records no process chain, never applies `trusted_signers`, and marks the
  repository and changed files the remote reports as unverified. The commit
  author, committer and message stay bound to the signed bytes, as for local
  requests.
- **The thin client is unchanged in role:** on the remote host git's
  `gpg.program` is `secrets-dispatcher gpg-sign`, and
  `SECRETS_DISPATCHER_REMOTE_SOCKET=<forwarded path>` makes it use the remote
  endpoint instead of the local socket and cookie.
- The daemon still runs gpg locally ([ADR 0008](0008-gpg-signing-through-gpg-program.md)),
  so the remote host needs neither gpg-agent nor the key.

## Consequences

- A remote host gets exactly one capability, asking for a signature, and every
  request needs an approval on the laptop, unless an "approve and auto-approve"
  rule for that remote is active.
- Trust in a remote is the trust in whoever set up its tunnel, as in ADR 0003;
  a compromised remote can submit requests but cannot approve them, see other
  requests, or sign without the user seeing the commit.
- Remotes must stop forwarding gpg-agent for the gate to mean anything; that
  is configuration on the remote side (for Dev Containers: keep the agent
  unforwarded), not something the dispatcher can enforce.
