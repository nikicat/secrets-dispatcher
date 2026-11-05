# ADR 0009: Take over from gnome-keyring by demoting only its secrets component

Date: 2026-07-18 · Status: accepted

## Context

On GNOME, installing the dispatcher means taking `org.freedesktop.secrets` from
gnome-keyring and keeping it, while gnome-keyring keeps serving the actual
secrets privately. gnome-keyring is started three ways: systemd user units,
D-Bus activation, and `pam_gnome_keyring` at login, plus XDG autostart. Users
will only try this near their real keyring if it is fully reversible.

## Decision

- **Demote, don't mask.** A spike (PR #14) found that masking gnome-keyring's
  units does not hold: at the next login `pam_gnome_keyring` respawns an
  unmanaged `--daemonize --login` daemon that re-grabs the name, and the pkcs11
  component dies with the mask. Instead the dispatcher installs a user drop-in
  limiting the session daemon to `--components=pkcs11`, a `Hidden=true` shadow
  of its autostart entry, and a D-Bus activation mask, and runs gnome-keyring's
  secrets component as its private backend.
- **Every change is a user-level file the dispatcher records,** so
  `service uninstall` and the `try` trial restore the previous state exactly.
- **The provider is detected, not configured:** the current name owner's exe
  and `XDG_CURRENT_DESKTOP` pick the topology, and `--backend gnome-keyring` is
  a keyword, not a path.

## Consequences

- `try` can take over and restore on Ctrl-C without touching anything outside
  the user's config.
- Re-grab vectors change between Ubuntu releases, so this is verified on a real
  GNOME desktop on every PR ([ADR 0010](0010-e2e-on-a-real-desktop-vm.md)).
