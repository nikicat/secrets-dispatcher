---
status: todo
tags: [refactor, service]
---
# Status mixes reading, rules and printing

## Goal
`service.Status` (`internal/service/status.go`) reads unit and file state,
applies the health rules and prints the report in one function. Done when it
reads into a status value, a pure function turns that into problems, and a
third prints them, so the rules can be tested without stdout.

Found by the 2026-11-19 unsmell run. (b2-6)
