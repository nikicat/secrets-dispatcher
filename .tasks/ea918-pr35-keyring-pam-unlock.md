---
status: todo
deps: [Twhz82, T75jjt, Tnzb1p, T5ghy8, Tytty7]
tags: [external-pr, service, gnome, bug]
---
# Review PR #35: PAM cannot unlock the backend keyring at login

## Goal
Decide on [#35](https://github.com/nikicat/secrets-dispatcher/pull/35) by
timrichardson, a bug fix. At every GNOME login the user gets an "Authentication
required" dialog for the login keyring:
- `pam_gnome_keyring` only delivers the password to a daemon on the standard
  `$XDG_RUNTIME_DIR/keyring/control` socket.
- The backend unit used `--control-directory=%t/secrets-dispatcher/keyring`, so
  PAM unlocked its own daemon instead. The dispatcher's takeover then killed
  that daemon.

The fix moves the backend to `%t/keyring`, adds `WantedBy=default.target` so
the backend runs from boot under linger, and enables it on install.

## State (2026-11-07)
- The PR is a draft, opened on 2026-09-17 from a fork: +7306/-482 in 75 files,
  20 commits.
- Only the last two commits are the fix. The rest is the whole #29–#34 stack,
  plus five unreviewed commits:
  - binding prompt objects to the requesting sender;
  - rejecting stale secret sessions;
  - an authenticated admin view opened from notifications, via xdg-open;
  - migrating legacy saved rules;
  - restoring approval workflows and rule management.
- It conflicts with `master`.
- It touches ADR 0009 (demoting gnome-keyring to secrets-only): one commit
  moves the demoted session keyring off the standard control socket.

## Next
- The user-facing bug is real, so it is worth fixing first.
- Ask whether the two fix commits can stand alone on `master` as their own PR,
  apart from the stack.
- Check the control-socket change against ADR 0009 and the e2e-gnome VM test.
