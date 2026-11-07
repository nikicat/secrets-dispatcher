# Tasks

Open tasks for this repo, one Markdown file each. A closed task is a deleted
file, and git history is the archive. There is no tool or database: edit the
files by hand, or let Claude Code do it with the
[tasks plugin](https://github.com/nikicat/claude-tasks).

## File

`xxxxx-short-slug.md`, where `xxxxx` is a random id from `0-9a-z` without
`i l o u`, so tasks made on parallel branches don't clash. Never reuse an id,
including those of deleted tasks. In references the id is written `T7kq2x`
(`other-repo#T7kq2x` for another repo), so GitHub doesn't link it to PR #7.

```markdown
---
status: todo                 # todo | doing | blocked
deps: [Tc9wdr, other-repo#Tm3xaj]   # optional; other-repo = same-owner repo name, or owner/repo
due: 2026-11-07              # optional
tags: [backend]              # optional
---
# Title

## Goal
## Decisions
## Ruled out
## State
## Next
```

Only the title is required; use the sections as the task grows.

## Workflow

- **Start / block:** edit `status:`. Write blockers in `deps` or in the body.
- **Done:** delete the file in the commit that resolves it, with `(task Txxxxx)`
  in the message.
- **Someone else's task:** ask the author before you close or rewrite it.

## History

```sh
git log --diff-filter=D --name-only -- .tasks/   # closed tasks
git show <commit>^:.tasks/xxxxx-slug.md           # read a closed task
git log -- '.tasks/xxxxx-*'                       # everything that touched it
```
