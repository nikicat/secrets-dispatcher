---
status: todo
tags: [unsmell]
---
# config.TrustRule mirrors approval.TrustRule and is copied by hand

## Goal
config.TrustRule/ProcessMatcher/SecretMatcher mirror approval.TrustRule and its matchers field for field, and main.go copies one into the other by hand; the changeset had to add Client in three places (config.go:375, types.go:23, main.go:350). A missed copy line would make a configured matcher silently match everything.

Where: main.go:344-370, internal/config/config.go:366-413.

Found by the /unsmell run of 2026-11-10 (b3-6).

## Next
- One rule type: config decodes straight into approval.TrustRule (yaml tags on the approval types, validation as methods there), or a config.TrustRule.Approval() conversion next to the type if the packages must stay apart.
