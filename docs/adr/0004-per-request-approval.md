# ADR 0004: Approve per request, with batching and time-boxed auto-approve

Date: 2026-02-06 · Status: accepted

## Context

gpg-agent and the keyrings authorize at the key or collection level: one unlock
or one pinentry opens everything behind it, and a second requester can
piggyback on the first approval. Prompting for every single access, on the other
hand, trains the user to click approve without reading.

## Decision

- **Every access is a separate request.** Approving one secret authorizes only
  that request.
- **Concurrent requests are batched.** Requests arriving within
  `approval_window` (default 2s) are shown as one prompt; a bulk
  `GetSecrets(items[])` is split by rule result and its prompted items share one
  approval.
- **Unanswered requests expire** after `timeout` (default 5m) and fail.
- **Auto-approve is opt-in and time-boxed.** "Approve and auto-approve" creates
  a temporary rule for the matching pattern lasting
  `auto_approve_duration_seconds` (default 120s). Durable auto-approval is a
  trust rule in `config.yaml` ([ADR 0005](0005-trust-rules-on-the-process-chain.md)).
- **Every attempt is logged**, approved, denied or ruled, as structured JSON on
  stderr, which journald keeps when the dispatcher runs as the user service.

## Consequences

- A cached approval cannot be reused by another process for a different secret.
- The audit trail has no file of its own; it lives where the service's stderr
  goes.
