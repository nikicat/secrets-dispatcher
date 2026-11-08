---
status: todo
tags: [ssh, config, service]
---
# Warn when the SSH proxy has no upstream agent

## Goal
With an `ssh:` section, an empty `ssh.upstream` and no `$SSH_AUTH_SOCK`, serve
logs an error and runs without the SSH proxy, while `config validate` passes.
That's common under systemd, whose user manager often has no `SSH_AUTH_SOCK`.

## Decisions
- Serve keeps running without the SSH proxy in that case (user's choice,
  2026-11-07), rather than refusing to start.

## Next
`config validate` warns about it, and `service install` writes `ssh.upstream`
from the installing shell's `SSH_AUTH_SOCK`.

Found by the 2026-11-07 unsmell run.
