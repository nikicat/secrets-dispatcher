---
name: secrets-rule
description: Create, modify, or manage secrets-dispatcher trust rules. TRIGGER when the user wants to "block", "deny", "allow", "approve", or "ignore" an application or process from accessing secrets, mentions suspicious or unwanted Secret Service requests, asks to add/edit/remove rules in secrets-dispatcher config, or says "add rule" / "trust rule". DO NOT TRIGGER for secrets-dispatcher Go source code development, config schema changes, or general coding tasks.
argument-hint: "<description or request-id-prefix>"
---

# secrets-dispatcher Trust Rule Management

Create or modify trust rules based on the user's request. The user may provide:
- A free-text description: "block evolution from accessing secrets"
- A request ID prefix from history: "b260def"
- A vague concern: "something is accessing my keyring"

## Step 1: Understand the Request

Parse `$ARGUMENTS` (or the user's message if auto-triggered). Determine:
- **Intent**: block (deny), allow (approve), ignore, or investigate?
- **Target**: which application/process?
- **Scope**: all request types, or specific ones (search, get_secret, write, delete, unlock)?

If the intent is unclear, ask ONE focused question using AskUserQuestion. Don't ask about things you can resolve from history/logs.

## Step 2: Gather Evidence

Run these in parallel to identify the exact process details:

**In-memory history** (most useful — has full process chain with exe paths):
```
secrets-dispatcher history -json
```

If a request ID prefix was given, filter to it:
```
secrets-dispatcher history -json | jq '.[] | select(.request.id | startswith("PREFIX"))'
```

Otherwise, search for entries matching the user's description — look at process names, exe paths, search_attributes, unit names.

**Journald logs for the current session** (shows request flow, denials, errors):
```
journalctl --user -u secrets-dispatcher.service --since today --no-pager | tail -200
```

From the evidence, extract:
- `sender_info.process_chain[].exe` — **full executable path** (preferred for rules)
- `sender_info.process_chain[].name` — comm name (CAUTION: truncated to 15 chars by Linux)
- `sender_info.unit_name` — systemd unit if applicable
- `request.type` — what request types the process makes
- `request.search_attributes` — search patterns used
- `request.items[].attributes` — secret attributes accessed

## Step 3: Present Findings

Show the user a brief summary of what you found:
- The process exe path and chain
- What request types it made and how often
- What secrets/attributes it accessed
- Any existing rules that already match or partially overlap

## Step 4: Compose the Rule

Build a trust rule. When choosing process identifiers, walk UP the process chain to find the **application** that initiated the request. Skip:
- Shells: fish, bash, zsh, sh
- Terminals: terminator, alacritty, kitty, gnome-terminal, konsole
- Sandboxes: firejail, bwrap, flatpak
- Generic D-Bus/secret CLI tools: secret-tool, dbus-send, busctl

**Prefer `process.exe`** with the full path from history. Avoid `process.name` unless the exe is unavailable (name is truncated at 15 chars by `/proc/PID/comm`).

Do NOT include in the rule:
- `process.name` when exe is available (redundant, and name may be truncated)
- `secret.label` (usually too specific / session-dependent)
- Attributes that are clearly session-specific with no useful pattern

Show the proposed rule as YAML and ask the user to confirm or adjust before writing.

## Step 5: Write the Rule

1. Read `~/.config/secrets-dispatcher/config.yaml`
2. Add the rule under `serve.rules`, before `trusted_signers:`
3. Maintain existing YAML formatting (8-space indentation for rules)

## Step 6: Validate

```
secrets-dispatcher config show
```

Check output for errors. If validation fails, fix the YAML and retry.

## Step 7: Offer Restart

After writing, ask the user:

> Rule written. Restart secrets-dispatcher to apply?
> ```
> systemctl --user restart secrets-dispatcher.service
> ```

Only run the restart if the user confirms.

## Config Format Reference

Valid actions: `approve`, `deny`, `ignore` (ignore only valid for `write` request type)
Valid request_types: `get_secret`, `search`, `delete`, `write`, `unlock`
Matchers use `path.Match` glob syntax (NOT regex). All non-empty matchers are AND-ed.

```yaml
serve:
    rules:
        - name: rule-name           # descriptive kebab-case
          action: approve            # approve | deny | ignore
          request_types: [get_secret]  # omit to match ALL types
          process:
              exe: /usr/bin/app      # glob, matches any process in chain
              name: app              # glob, matches any process in chain (15-char limit!)
              cwd: /home/user/proj   # glob, matches any process in chain
              unit: "app-*"          # glob, matches systemd unit name
          secret:                    # optional, for get_secret/delete/write
              collection: default    # glob
              label: "GitHub*"       # glob
              attributes:            # exact subset match, values are globs
                  service: kubelogin
                  username: "kubelogin/tokencache/*"
          search_attributes:         # optional, for search requests
              xdg:schema: "org.gnome.keyring.Note"
```
