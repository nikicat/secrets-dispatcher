---
status: todo
tags: [refactor, service]
---
# Drive systemd over D-Bus with go-systemd instead of exec'ing systemctl

## Goal
`internal/service` runs `systemctl --user …` as a subprocess (`host.systemctl`,
`systemctlFunc`, `systemctlUnit` with the `systemctlVerb` constants,
`stateOf` parsing `is-active` output), and the tests record argv through
`systemctlFunc`. [go-systemd](https://github.com/coreos/go-systemd)'s `dbus`
package does the same over the user manager's D-Bus API
(`NewUserConnectionContext`, `StartUnitContext`, `StopUnitContext`,
`TryRestartUnitContext`, `EnableUnitFilesContext`, `DisableUnitFilesContext`,
`ReloadContext`, `GetUnitPropertyContext("ActiveState")`), on the
`godbus/dbus/v5` the repo already uses.

Done when Install, Uninstall, Status and the gnome-keyring demotion call a
small interface backed by go-systemd, the tests fake that interface instead of
argv, and `systemctlVerb` is gone.

## Open points
- `--verbose` (NarrateChanges) prints systemctl's own output today; decide what
  it prints instead (the job results).
- `Plan` prints the equivalent `systemctl --user …` lines for people to read;
  that text can stay.
- The companion PAM hook stays a `systemctl --user --machine=…` command line,
  because pam_exec runs a program.

Found by the 2026-11-20 unsmell run (the user's question after R8).
