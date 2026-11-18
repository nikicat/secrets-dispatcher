---
status: todo
deps: [Tjk1e0]
tags: [proxy, adr-0014]
---
# The daemon runs on a server with rules only

## Goal
ADR 0014's proxy serves a fleet host's services, with no desktop and no one to approve. Done means the daemon starts without a session bus or desktop notifications, denies every request no rule approves, and reads its credentials from a backend that a server has.

## Next
Find what the daemon needs from a desktop today, and which Secret Service backend a server runs (gnome-keyring headless, KeePassXC, or a file store of the proxy's own).
