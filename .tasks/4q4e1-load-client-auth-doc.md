---
status: todo
tags: [docs, cli]
---
# loadClientAuth's doc calls serve "the daemon"

## Goal
The doc of `loadClientAuth` (`main.go`) says its error "says the daemon is not
running", meaning `serve`, while the binary also has a `daemon` subcommand for
the companion daemon. Done when the doc names `serve`.

Found by the 2026-11-19 unsmell run. (b1-9)
