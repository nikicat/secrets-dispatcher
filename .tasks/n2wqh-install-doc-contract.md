---
status: todo
tags: [docs, service]
---
# service.Install's doc lists its steps, not its contract

## Goal
`internal/service/install.go` `Install`'s doc lists steps ("writes unit
files, updates the config topology, reloads systemd, and enables the
service") and leaves out the takeover effects a caller must know: a local or
full install masks D-Bus activation, demotes gnome-keyring and takes
org.freedesktop.secrets over. Done when the doc states that contract.

## Related
xfwr7 (Install runs Plan's steps) may change what the doc says.

Found by the 2026-11-20 unsmell run (r1b-19).
