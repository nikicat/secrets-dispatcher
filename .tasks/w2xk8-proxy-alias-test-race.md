---
status: todo
tags: [test, proxy]
---
# TestProxyAliasPathOperations races under -race

## Goal
`go test -race .` fails in most runs on
`TestProxyAliasPathOperations/CreateItemViaAlias`: a data race inside godbus's
export map, between an Export made from the mock's CreateItem handler
(`internal/testutil/mockservice.go`, MockSecretService.AddItem) and the
connection's dispatch. It passes without -race and fails the same way on
master before the nk04r refactor. Done when the mock exports outside the
handler (or synchronises with dispatch) and the test passes under -race.
