---
status: todo
tags: [refactor, config, logging]
---
# Typed log level and format, and one slog setup

## Goal
- `serve.log_level` and `log_format` are raw strings. `parseLogLevel` turns an
  unknown level into info without a word, and an unknown format means text.
- `runServe` and `runDaemon` build their slog handlers with different systemd
  rules, and the daemon's comment "Mirror the slog setup from runServe"
  (`main.go`) is false.

Done when `config.LogLevel` and `config.LogFormat` reject unknown values in
`UnmarshalText`, the flags parse into the same types, and one
`newLogHandler(level, format)` serves both subcommands.

Found by the 2026-11-05 and 2026-11-07 unsmell runs.
