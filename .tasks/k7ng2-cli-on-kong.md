---
status: todo
tags: [refactor, cli, config]
---
# Parse the command line and config layering with a library

## Goal
The CLI and the flag-over-config layering are hand-written: `flags.go`
(`settingsFlags` and its double parse, `configFlags`, `optionalBool`,
`negatedBool`, `showDefault`), `internal/config/settext.go` (`setText`,
`parseEnum`, `Alternatives`), `main.go`'s dispatch switches and usage texts.
Decided on 2026-11-20 (unsmell run 20261120-102406): move to
[kong](https://github.com/alecthomas/kong) — subcommands from a struct, enum
tags for Mode/Backend/LogLevel/LogFormat, `negatable` for `--start`, defaults
and help from tags — and do flags over file over defaults with kong's YAML
resolver, or koanf (YAML + posflag providers) if the resolver cannot map flags
onto nested keys such as `serve.log_level`.

Keep the domain types (`config.LogLevel`, `LogFormat`, `ListenAddr`, `Path`,
`File`, `Source`, `StateDir`, `service.Mode`, `Backend`); they plug in through
`encoding.TextUnmarshaler`.

Done when `flags.go` and the hand-written usage/dispatch are gone, help output
is generated, and the flags-over-config tests pass against the new layer.

The three dispatch switches in `main.go` each spell `"-h", "--help", "help"`
(2026-11-20 run, r1a-17); kong's generated help removes them.

## Related
Absorbs xambm (one table of CLI commands); 4ecvt (runners return errors) fits
kong's `Run() error` commands.
