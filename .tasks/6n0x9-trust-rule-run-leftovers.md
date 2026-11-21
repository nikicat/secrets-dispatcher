---
status: todo
tags: [unsmell, refactor, approval, proxy]
---
# Object paths, the gpg client name and the upstream call context are untyped or duplicated

## Goal
Close the findings that the re-checks of unsmell run 20261121-103738 left on
unchanged lines (run directory `.git/unsmell/20261121-103738`, findings
`recheck-1.yaml` and `recheck-2.yaml`).

## Next
- **Item object paths are strings** (r1-2, `internal/approval/manager.go:66-70`):
  `ItemInfo.Path` holds an item's D-Bus object path as a `string`, and so do the
  approval cache key, `CacheItemForSender` and `dbus.ExtractCollection`'s input.
  Make them `dbus.ObjectPath`, as `RequestInfo.Session` already is. The JSON key
  `path` stays a string.
- **The gpg thin client sends the repository name as its client name** (r1-21,
  `internal/gpgsign/run.go:132`): `startSignRequest(ctx, string(info.RepoName), info)`
  puts a `RepoName` where the request's client name goes. Fold this into Tq7r2n,
  which moves gpg_sign's client into `SenderInfo.Client`.
- **The upstream call context repeats the request** (r2-6,
  `internal/proxy/slow_upstream.go:14-21`): every approval-gated proxy method
  builds an `approval.RequestInfo` and then an `UpstreamCallContext` from the
  same items, type and sender, and `api.Resolver.signGPG` fills the same fields
  by hand. `UpstreamCallContext` should hold the `RequestInfo` beside
  `ResolveSender` and `Prompted`. About 25 sites across proxy and api.
