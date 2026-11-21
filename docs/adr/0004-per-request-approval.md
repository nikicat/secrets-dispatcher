# ADR 0004: Approve per request, with a short approval cache and time-boxed auto-approve

Date: 2026-02-06 · Status: accepted

## Context

gpg-agent and the keyrings authorize at the key or collection level: one unlock
or one pinentry opens everything behind it, and a second requester can
piggyback on the first approval. Prompting for every single access, on the other
hand, trains the user to click approve without reading.

## Decision

- **Every access is a separate request.** Approving one secret authorizes only
  that request.
- **A recent approval answers the same sender again.** For `approval_window`
  (default 5s), approving an item, or creating it, also approves later requests
  from the same sender for that item. Deletes and writes always prompt. A bulk
  `GetSecrets(items[])` is one request with one prompt, and the cache answers it
  only when every item is cached.
- **Unanswered requests expire** after `timeout` (default 5m) and fail.
- **Auto-approve is opt-in and time-boxed.** "Approve and auto-approve" creates
  a temporary rule for the matching pattern lasting
  `auto_approve_duration` (a Go duration, default 2m). Durable auto-approval is a
  trust rule in `config.yaml` ([ADR 0005](0005-trust-rules-on-the-process-chain.md)).
- **Every attempt is logged**, approved, denied or ruled, on stderr, which
  journald keeps when the dispatcher runs as the user service
  ([ARCHITECTURE.md, Audit logging](../ARCHITECTURE.md#audit-logging)).

## Consequences

- A cached approval cannot be reused by another process for a different secret.
- The audit trail has no file of its own; it lives where the service's stderr
  goes.
