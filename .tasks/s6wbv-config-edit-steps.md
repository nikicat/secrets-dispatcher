---
status: todo
tags: [refactor, cli]
---
# configEditCmd.Run picks its editor and prompts inline

## Goal
`configEditCmd.Run` (`main.go`) orders named steps (load, resolve, restart) but
picks the editor from `$EDITOR`/`$VISUAL` and runs the y/N restart prompt
inline between them. Done when the editor choice is `editorFromEnv()` and the
prompt is a small `confirm(prompt)` helper, so the function reads as its steps.

Found by the 2026-11-19 unsmell run. (b1-10)
