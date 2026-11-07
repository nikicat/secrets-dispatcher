---
status: todo
tags: [external-pr, docs, security]
---
# Decide on PR #32: secure-local isolation architecture (RFC)

## Goal
Decide on [#32](https://github.com/nikicat/secrets-dispatcher/pull/32) by
timrichardson. It is a design document, with no code, for a mode where nothing
can bypass the proxy. It covers the threat model, Polkit-backed decisions,
root-owned policy, path validation, migration, VM coverage and milestones. It
also includes a retrospective on the author's earlier secure-local code.

## State (2026-11-07)
- The PR opened on 2026-08-04 and was last updated on 2026-08-07: +376 in one
  file, 3 comments.
- It merges cleanly.
- In the comments, nikicat agreed with the goal but doubts that this
  architecture can reach it, and sketched an alternative: a custom LSM that
  profiles processes by code and communication surface. timrichardson
  replied that a retrofit needs two things: an unavoidable proxy, and robust
  identification of non-sandboxed apps, possibly through opt-in AppArmor or
  SELinux profiles.
- ADR 0002 ("visibility, not isolation") is the current position.

## Next
Decide among three options, and reply on the PR:
- merge it as a proposal;
- turn it into an ADR that records why isolation is out of scope for now;
- close it with thanks.
