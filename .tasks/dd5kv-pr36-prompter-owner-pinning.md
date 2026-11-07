---
status: todo
tags: [external-pr, proxy, gnome, bug]
---
# Review PR #36: pin prompter conversations to the accepting prompter

## Goal
Decide on [#36](https://github.com/nikicat/secrets-dispatcher/pull/36) by
timrichardson, a bug fix. During login, `org.gnome.keyring.SystemPrompter`
can change owner in the middle of a prompt: a D-Bus-activated gcr-prompter
takes the name from the shell. The backend's `StopPrompting` then reaches a
prompter that never saw the `BeginPrompting`. The unlock dialog is left
orphaned over a dimmed desktop.

The fix makes three changes:
- `BeginPrompting` resolves the prompter's unique bus name, and the rest of the
  conversation stays pinned to it.
- Forwarded calls carry `FlagNoAutoStart`.
- If the pinned prompter vanishes, the bridge sends `PromptDone` itself.

It comes with two new Go tests.

## State (2026-11-07)
- The PR is a draft, opened on 2026-09-17 from a fork: +7662/-505 in 78 files,
  21 commits.
- It is stacked on #35 (Tea918). Only the last commit is this fix.
- It conflicts with `master`.

## Next
- Like Tea918, this is a real bug with a small fix buried under the stack.
- Ask the author to rebase the single commit onto `master` as its own PR, or
  cherry-pick it with their consent.
- Check it against ADR 0001 (proxying the Secret Service).
