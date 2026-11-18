# ADR 0014: an HTTP proxy that attaches credentials after approval

Date: 2026-11-18 · Status: proposed

## Context

A remote client that reads a token through the dispatcher is asked about the
read, not about what it does with the token. Once approved, the token is on the
remote host, and every later request it signs is invisible to the dispatcher
([ADR 0002](0002-visibility-not-isolation.md)). The approval cannot say "this
pushes three commits to main" or "this posts a message to #general", because the
dispatcher never sees those requests.

A credential injection proxy sees them. The client holds a placeholder, and the
proxy replaces it with the real credential on the way out, after its rules or
the user let the request through.

## Decision

**The daemon runs an HTTP proxy that attaches credentials from the backend
Secret Service to the requests it lets through.**

- **Per-remote sockets.** The proxy listens on one socket per configured remote,
  like signing ([ADR 0011](0011-remote-signing-through-per-remote-sockets.md)).
  The socket a request arrives on names the remote.
- **TLS is intercepted only for hosts that a credential is pinned to.** The
  proxy mints a certificate for such a host from its own CA, whose key lives
  in the backend Secret Service. Every other host gets a plain `CONNECT`
  tunnel.
- **A credential is declared, not coded:** the Secret Service item that holds
  it, the hosts it is pinned to, the placeholder, and where it goes (a header
  template, a query parameter or a path segment). The proxy swaps it only on a
  request to a pinned host, drops it from any redirect to another host, and
  denies the endpoints the config marks as minting credentials.
- **Each request is a `http_request` approval request.** Trust rules match it on
  client, method, host and path, and approve, deny or leave it to the user, as
  for other request types.
- **The approval shows the request decoded.** The generic view is method, URL
  and the body as JSON or form fields. Decoders exist per protocol, not per
  service: GraphQL (operation and variables) and git smart HTTP (the refs a
  push updates).
- **The proxy can run without a desktop.** A daemon on a server then runs on
  rules alone, and a request no rule approves is denied.

## Consequences

- A remote client never holds the credential, so it can neither keep it nor
  use it outside the proxy.
- Each client must trust the proxy's CA and send its traffic through the proxy.
  Clients that pin certificates cannot be proxied.
- A request with a large body, such as a git push, is buffered until it is
  approved.
- Schemes that sign each request (AWS SigV4) or trade a credential for another
  (a GCP service account for an access token) need code of their own, one per
  scheme.
- The proxy hides credentials but does not confine the client. A client can
  still send data to any host it can reach.
