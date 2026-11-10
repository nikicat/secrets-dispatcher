---
status: todo
tags: [unsmell]
---
# main.go redefines config's defaults and bus-type names

## Goal
main.go redefines defaultListenAddr/defaultTimeout/defaultHistoryLimit that config exports as DefaultListenAddr/DefaultTimeout/DefaultHistoryLimit (config.go:21-23), and spells "session_bus" beside config.BusSessionBus.

Where: main.go:37-41, main.go:457.

Found by the /unsmell run of 2026-11-10 (b3-11).

## Next
- Use the config constants.
