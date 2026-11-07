---
status: todo
tags: [remote, security]
---
# Pair remote clients cryptographically

## Goal
Remote clients are identified only by the SSH tunnel they arrive on (ADR 0003),
so the transport's MITM resistance is SSH's. Pairing would let a new client
prove its identity independently: the user confirms a matching code on both
ends, and the dispatcher stores the client's public key and applies per-client
rules (allow/deny/prompt by secret path).

## Decisions
- Candidates: SRP (as Enpass does), SPAKE2 (as Magic Wormhole does), or a
  visually confirmed code. Not chosen yet.
- Paired clients would live in `clients.yaml` next to `config.yaml`.

## Next
Decide whether pairing still earns its place now that remotes are identified per
tunnel and per socket (ADR 0003, ADR 0011); if yes, pick the protocol.
