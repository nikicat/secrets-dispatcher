---
status: todo
tags: [refactor, proxy]
---
# SubtreePropertiesHandler repeats the shared Properties implementation

## Goal
internal/proxy now has one `properties` type (Get, GetAll, Set) that
CollectionHandler and ItemHandler embed with their path predicate.
`SubtreePropertiesHandler` (subtree.go) has the same bodies but does not report
slow upstream calls, so it was not merged: merging it would start sending
slow-upstream notifications for those calls. Decide whether it should, and
merge it or record why not.

Also decide on a quirk the refactor pinned with TestItemObjectsServes
(`internal/proxy/collection_test.go`): `itemObjects.serves`
(`internal/proxy/item.go`) rejects item paths under `/aliases/`, while the
proxy exports ItemHandler at the aliases prefix, so such calls get
NoSuchObject.
