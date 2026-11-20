---
status: todo
tags: [companion, pam]
---
# The companion PAM hook runs for every session and is never removed

## Goal
`provision` appends `session include secrets-dispatcher` to
`/etc/pam.d/common-session` (Debian) or `/etc/pam.d/system-login` (Arch)
(`internal/companion/provision.go`, `includePAMConfig`). Both stacks are
included by every PAM service, so the hook also runs for sudo, cron and ssh
sessions, not only graphical logins; each run is a no-op start of the running
unit. Nothing removes the line: there is no unprovision path.

Done when the hook runs only for the desktop login (e.g. the display manager's
stack, such as `gdm-password`, or a `pam_succeed_if` service filter), and an
undo path removes the include line and `/etc/pam.d/secrets-dispatcher`.

Found by the 2026-11-20 unsmell run (u16).
