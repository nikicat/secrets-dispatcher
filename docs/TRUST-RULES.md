# Trust rules

Trust rules auto-approve (or deny / ignore) known-safe patterns so
secrets-dispatcher only prompts you for the unexpected. After a first pass of
adding rules for the tools you trust, the dispatcher goes quiet — that's the
intended steady state.

Config rules live under `serve.rules` in
`~/.config/secrets-dispatcher/config.yaml` and take effect on restart. The web
UI can also create narrowly scoped saved approvals without editing the config.

## The easy way: the `secrets-rule` agent skill

This repo ships a [Claude Code](https://claude.com/claude-code) skill —
[`.claude/skills/secrets-rule/`](../.claude/skills/secrets-rule/SKILL.md) — that
writes and edits rules for you.

**The intended way is to point it at a specific request.** When something shows
up in the web UI, a desktop notification, or `secrets-dispatcher list` /
`history`, hand its ID prefix to the skill:

> `/secrets-rule b260def`

Because it pulls that exact request from history, the agent has the whole
context — the full process chain, `exe` paths, the secret attributes accessed —
and composes an accurate rule (or adjusts the existing rule that matched) with no
guessing. This is both the easiest and the most reliable path.

You can also just describe what you want in plain language:

> - *"block evolution from reading my secrets"*
> - *"always allow Firefox"*
> - *"ignore Chrome's dummy write probe"*

Either way, the skill:

1. inspects your request history (`secrets-dispatcher history -json`) and the
   journal to pin down the exact process,
2. walks **up** the process chain to the real application (skipping shells,
   terminals, and generic tools like `secret-tool`),
3. composes a rule keyed on the kernel-resolved `exe` (not the spoofable `name`),
4. shows it to you, writes it to `config.yaml`, validates, and offers to restart.

**To use it**, run Claude Code from a clone of this repo, or copy the skill into
your own Claude config once:

```bash
cp -r .claude/skills/secrets-rule ~/.claude/skills/
```

## Writing rules by hand

### Saved approvals from the web UI

After manually approving an eligible secret read, expand **Recent Activity**
and select **Always approve exact access**. The confirmation shows the complete
scope before anything is saved. Saved approvals require all of the following to
match exactly:

- a `get_secret` request for one item,
- the direct requester's kernel-resolved absolute executable path,
- the secret collection and every recorded secret attribute,
- for an interpreter, the script argument and, when the script is relative, the
  absolute working directory.

Interpreter argv matching is advisory because a process can rewrite its argv.
The direct executable match remains authoritative. Requests without enough
information to build this scope do not offer the action. Search, write, delete,
unlock, signing, denied, expired, cancelled, ignored, and automatically approved
requests cannot be used to create a saved approval.

Saved approvals are listed under **Saved Approvals** in the web UI and can be
removed there. They cannot be edited, broadened, disabled, or created from
arbitrary values. They are stored separately from config rules in
`<state_dir>/approval-rules.json`; the directory and file are owner-only
(`0700` and `0600`) and the file is validated when the daemon starts. Changes
take effect immediately and persist across restarts.

Config `deny` and `ignore` rules are hard policy and are evaluated before all
approval caches and saved approvals. After those, the order is the recent
approval cache, temporary auto-approve rules, saved approvals, then config
`approve` rules. A config `deny` or `ignore` rule therefore cannot be bypassed
by a saved approval. Activity approved by a saved approval identifies the
managed rule that made the decision.

```yaml
serve:
  rules:
    # Auto-approve Firefox accessing any secret
    - name: firefox
      action: approve
      process:
        exe: "/usr/lib/firefox/firefox"

    # Auto-approve tools running from your project directory
    - name: my-project
      action: approve
      process:
        cwd: "/home/me/src/my-project/*"

    # Auto-approve a shell-script wrapper (exe is the interpreter, so
    # identify the script via its argv)
    - name: logcli-wrapper
      action: approve
      process:
        exe: "/usr/bin/bash"
        args: "/home/me/.local/bin/logcli"

    # Ignore Chrome's dummy secret probe
    - name: chrome-probe
      action: ignore
      request_types: [write]
      process:
        exe: "*chrome*"

    # Auto-approve a deploy script accessing only deploy secrets
    - name: deploy
      action: approve
      process:
        exe: "/usr/bin/ansible-playbook"
      secret:
        collection: "deploy"

  # Auto-approve GPG signing from specific editors/tools
  trusted_signers:
    - exe_path: /usr/bin/nvim
```

Rules match on process attributes (`exe`, `name`, `args`, `cwd`, systemd `unit`)
and secret attributes (`collection`, `label`, custom `attributes`). All patterns
are globs (`path.Match`, **not** regex), and all non-empty matchers are AND-ed.
Process matching checks the **full process chain**, not just the immediate D-Bus
caller — `args` is matched against each individual cmdline argument of each
process in the chain, which is how you identify interpreter-run scripts (whose
`exe` is the interpreter, `/usr/bin/bash`, with the script path only in argv).
Set `direct: true` to apply all process fields only to the immediate requester
(`process_chain[0]`) instead. Saved approvals always use `direct: true`, so a
matching executable elsewhere in the ancestry is not sufficient.

### Match on what can't be spoofed

For security-relevant rules — especially `deny` — match on **`exe`**: it compares
the kernel-resolved `/proc/PID/exe` and cannot be forged. **`name`** matches the
process `comm`, which any process can set freely (`prctl(PR_SET_NAME)`) and which
Linux truncates to 15 chars — treat it as advisory only and never rely on it to
block an application. **`args`** is likewise self-reported (a process can rewrite
its argv after exec) — advisory only. **`unit`** matches the caller's real
systemd unit (resolved via `GetUnitByPID`), which is authoritative for
systemd-managed services.

## Matcher reference

| Field | Notes |
|---|---|
| `action` | `approve` · `deny` · `ignore` (`ignore` is valid only for `write`) |
| `request_types` | `get_secret` · `search` · `delete` · `write` · `unlock` — omit to match all |
| `process.exe` | glob; kernel-resolved `/proc/PID/exe` — **preferred** |
| `process.name` | glob; `comm`, 15-char, spoofable — advisory |
| `process.args` | glob; matched per cmdline arg — for interpreter-run scripts |
| `process.cwd` | glob; working directory of any process in the chain |
| `process.unit` | glob; systemd unit name |
| `process.direct` | if true, process fields must match the immediate requester instead of any process in the chain |
| `secret.collection` / `secret.label` | glob (for `get_secret` / `delete` / `write`) |
| `secret.attributes` | subset match; values are globs |
| `search_attributes` | glob map, for `search` requests |
