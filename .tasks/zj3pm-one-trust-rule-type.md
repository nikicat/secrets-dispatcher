---
status: todo
tags: [refactor, config, approval]
---
# One type for trust rules and trusted signers

## Goal
`main.go` copies `config.TrustRule`, `ProcessMatcher`, `SecretMatcher` and
`TrustedSigner` field by field into their `approval` twins. Done when there is
one set of types in `approval`, with both `yaml` and `json` tags, and `config`
uses them directly.

## Next
`TrustedSigner.RepoPath` holds a repository basename, not a path. Renaming it
changes the config file format, so decide first: rename with a migration, or
keep the key and only fix the doc.

Found by the 2026-11-05 and 2026-11-07 unsmell runs.
