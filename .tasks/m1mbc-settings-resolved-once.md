---
status: todo
tags: [refactor, cli, config]
---
# Resolve each subcommand's settings once, at the edge

## Goal
Flag-over-config precedence and the state directory are merged field by field
in `runLogin`, `runCLI` and `runServe` (nine settings for serve). Around it, in
`main.go`:
- the flag defaults `127.0.0.1:8484` and `info` duplicate
  `config.DefaultListenAddr` and `config.DefaultLogLevel`;
- the `--config`, `--state-dir` and `--verbose` help texts are spelled once per
  subcommand;
- `--start` and `--no-start` are two bools for one setting.

Done when each subcommand has one settings type, filled flags over config over
defaults, with shared flag definitions.

## Next
Decide whether to use a library such as ff or koanf, or a small hand-written
layer.

Found by the 2026-11-05 and 2026-11-07 unsmell runs.
