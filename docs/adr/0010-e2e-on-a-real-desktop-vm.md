# ADR 0010: End-to-end tests in a container and on a real GNOME desktop VM

Date: 2026-07-18 · Status: accepted

## Context

What breaks the dispatcher in practice is desktop-session behavior: gnome-keyring
re-grabbing the bus name via `gnome-session` and autostart, `gcr-prompter`
unlock prompts, gnome-shell's notification handling, snap-confined browsers
mediated by snapd, and dbus-broker's `NameOwnerChanged` timing. Mocks cover the
proxy logic but none of these.

## Decision

- **Tier 0:** the mock Secret Service and Playwright, for proxy logic and the
  web UI.
- **Tier 1:** a container with `dbus-run-session` and a real gnome-keyring
  (`e2e/gnome/fast.sh`), for prompt forwarding and approval plumbing.
- **Tier 2:** a real Ubuntu GNOME desktop in **raw QEMU, booted from a cloud
  image with cloud-init** (`e2e/gnome/vm/`), on every PR and as a required check
  on master. QEMU gives the same script and image locally and in CI
  (GitHub-hosted runners expose `/dev/kvm`), with no daemon or snap.
- **A server VM is rejected** as a middle tier: it misses exactly the
  desktop-only re-grab and notification behavior, and is neither faster than
  the container nor as faithful as the desktop.
- Rejected VM tools: multipass (snap and daemon in CI), quickemu
  (interactive-desktop oriented), smolvm (a microVM without a full init or
  desktop stack).

## Consequences

- The Tier-2 gate takes about 1.5 minutes with the weekly image cache.
- Approving through a notification button is not driven in tests yet: GNOME 46
  exposes the buttons over AT-SPI but with no actions to invoke (task T0004).
