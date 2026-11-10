---
status: todo
tags: [unsmell]
---
# Trust-rule globs are plain strings

## Goal
Client is a glob held as a string, as are all other matcher fields, so the doc has to say "A glob", config.go keeps a hand-written globs() list that each new field must join (the changeset added `add("client", r.Client)`), and matchTrustRule repeats the `x != "" && path.Match(x, v)` rule a fifth time at :952-956 (also :1049, :1093, :1098, and the chain loops).

Where: internal/approval/types.go:22, internal/approval/manager.go:952-956.

Found by the /unsmell run of 2026-11-10 (b1-7).

## Next
- A `type Glob string` in approval with UnmarshalText that rejects a bad pattern and `Matches(s string) bool` (empty matches anything); every matcher field becomes Glob, config.globs() and its validation loop are deleted, and the inline checks call Matches.
