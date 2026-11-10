---
status: todo
tags: [unsmell]
---
# No ADR records that trust rules can key on the client

## Goal
The changeset lets trust rules key on the tunnel (client) and stops resolving forwarded PIDs, a decision that ADR 0003 (identity is the tunnel) and ADR 0005 (tunneled requests have no local chain) do not record; TRUST-RULES.md:120-121 states the trust consequence on its own.

Where: docs/adr/0003-remote-clients-identified-by-their-tunnel.md:15-29, docs/adr/0005-trust-rules-on-the-process-chain.md:34-35.

Found by the /unsmell run of 2026-11-10 (b3-9).

## Next
- A consequence line in ADR 0003 that rules may match the tunnel by client and that such a rule trusts the whole tunnel; TRUST-RULES.md links to it.
