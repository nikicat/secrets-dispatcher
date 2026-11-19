---
status: todo
tags: [docs, approval]
---
# ADR 0004 does not say which requests can earn a rule

## Goal
Two decisions are visible in the code but recorded nowhere. Done when ADR 0004
records the reason for each, and the docs of `api.Resolver.AutoApprove` and
`api.Resolver.ApproveAndAutoApprove` cite it.

- An auto-approve rule is made from a cancelled request only (the "timed out,
  auto-approve similar requests?" follow-up), never from one still pending or
  denied.
- A gpg_sign request whose signature fails is still approved, but earns no rule.

## Next
Ask the user for both reasons; neither can be inferred from the code.

Found by the 2026-11-19 unsmell run (design-link gaps).
