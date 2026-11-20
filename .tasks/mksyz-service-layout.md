---
status: todo
tags: [refactor, service]
---
# The service setup's layout is resolved again by each entry point

## Goal
The takeover files own their paths and checks (`takeoverFiles`), but
`unitDir()` is still resolved separately in `Install`, `Uninstall`, `Plan`,
`Status`, `Try` and `gkDropIn`, `locateTakeoverFiles()` in `openHost`, `Plan`
and `Status`, and the "which of `allUnitNames` have a unit file" loop is
written twice, in `Status` and `Try`. Done when one layout value, built once
from the XDG homes, gives the unit directory, the takeover files and the
installed units, and `Install`, `Uninstall`, `Plan`, `Status` and `Try` take it.

Found by the 2026-11-19 unsmell run (b2-7); narrowed by the 2026-11-20 run (r1-16).
