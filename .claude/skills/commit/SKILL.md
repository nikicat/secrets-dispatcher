---
name: commit
description: Run pre-commit checks, commit, push, and ensure CI passes
argument-hint: "[commit message]"
---

# Commit, Push, and Verify CI

## Step 1: Pre-commit loop

Run `make pre-commit` and fix any failures. This target runs `make check` (formatting, linting, static analysis) and `make test` (Go tests + E2E) in parallel.

- If formatting fails: run `make fmt` and re-run
- If Go vet/staticcheck/tests fail: fix the code and re-run
- If frontend lint/check fails: fix the code and re-run
- Repeat until `make pre-commit` exits 0

## Step 2: Commit

1. Run `git status` and `git diff` to review all changes
2. Stage all relevant files (prefer explicit file names over `git add -A`)
3. If `$ARGUMENTS` is provided, use it as the commit message. Otherwise, draft a message from the changes.
4. Commit. Include `Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>` trailer.

## Step 3: Push

Push the current branch to origin:
```
git push
```

## Step 4: Wait for CI

Poll GitHub checks until they complete:
```
gh run list --branch $(git branch --show-current) --limit 1 --json status,conclusion,databaseId
```

- If all checks pass: done. Print the run URL and exit.
- If any check fails: proceed to step 5.

## Step 5: Fix CI failures

1. Fetch the failed run logs:
   ```
   gh run view <run-id> --log-failed
   ```
2. Diagnose and fix the failure
3. Run `make pre-commit` locally to verify the fix
4. Amend the commit:
   ```
   git add <fixed-files>
   git commit --amend --no-edit
   ```
5. Force push:
   ```
   git push --force-with-lease
   ```
6. Go back to step 4. Give up after 3 CI fix attempts and ask the user for help.
