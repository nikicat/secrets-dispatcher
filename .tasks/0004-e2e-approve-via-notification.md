---
status: todo
tags: [e2e, gnome, spike]
---
# Drive Approve through the notification in the desktop VM test

## Goal
The Tier-2 VM covers approving from the CLI and the web UI, but not by clicking
Approve on the GNOME notification. Done when the scenario approves a request
through the notification button.

## Ruled out
- AT-SPI `doAction`: GNOME 46 exposes the banner and its Approve/Deny buttons
  through Cally, but reports `nActions=0`, so nothing can be invoked.

## Next
Inject a click with Mutter RemoteDesktop or QMP input, aimed at the button's
AT-SPI extents; the demo harness already injects input over QMP.
