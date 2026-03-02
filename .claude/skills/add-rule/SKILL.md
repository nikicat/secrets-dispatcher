---
name: add-rule
description: Add an auto-approve or ignore trust rule to config from recent approval history
argument-hint: "[request-id-prefix]"
disable-model-invocation: true
---

# Add Trust Rule from History

Add a persistent trust rule to the secrets-dispatcher config based on a recently approved/ignored request.

## Steps

1. **Get the request.** If `$ARGUMENTS` is provided, use it as a request ID prefix to look up:
   ```
   ./secrets-dispatcher history -json | jq '.[] | select(.request.id | startswith("ID_PREFIX"))'
   ```
   Otherwise, show the 10 most recent approved requests (excluding gpg_sign) as a summary table and ask the user to pick one:
   ```
   ./secrets-dispatcher history -json | jq '[.[] | select(.resolution == "approved") | select(.request.type != "gpg_sign")] | sort_by(.created_at) | reverse | .[0:10]'
   ```

2. **Derive a rule** from the request. Use the request fields to build a `TrustRule`:
   - `name`: descriptive kebab-case name based on process + action (e.g., `kubelogin-get-secret`, `chrome-ignore-write`)
   - `action`: `approve` (default) or `ignore` — ask if unclear
   - `request_types`: from `request.type` (valid: `get_secret`, `search`, `delete`, `write`)
   - `process.exe`: walk UP the process chain to find the **application** that initiated the request. Skip these categories:
     - Shells: fish, bash, zsh, sh
     - Terminals: terminator, alacritty, kitty, gnome-terminal, konsole
     - Sandboxes: firejail, bwrap, flatpak
     - Generic D-Bus/secret CLI tools: secret-tool, dbus-send, busctl
     The first process that doesn't fall into these categories is the exe to use. Use the exact exe path.
   - `secret.collection`: extracted from item path (`/org/freedesktop/secrets/collection/NAME/...` -> `NAME`)
   - `secret.attributes`: from item attributes. Use glob patterns (`*`) for values that look like hashes, tokens, or session-specific IDs
   - `search_attributes`: from `request.search_attributes` if type is `search`

   Do NOT include in the rule:
   - `process.name` (redundant with exe)
   - `secret.label` (usually too specific / session-dependent)
   - Attributes that are clearly session-specific with no useful pattern

3. **Show the proposed rule** as YAML and ask the user to confirm or adjust before writing.

4. **Write the rule** to the config file. The config is at `~/.config/secrets-dispatcher/config.yaml`. Add the rule to `serve.rules` list, creating the `rules:` key if it doesn't exist. Place it before `trusted_signers:`.

5. **Validate** by running `./secrets-dispatcher config show` and checking for errors.

6. **Offer to restart** the daemon so it picks up the new rule:
   ```
   systemctl --user restart secrets-dispatcher.service
   ```

## Config format reference

```yaml
serve:
    rules:
        - name: rule-name
          action: approve          # or "ignore"
          request_types: [get_secret]  # get_secret, search, delete, write
          process:
              exe: /usr/bin/kubectl     # glob, matches any process in chain
              name: kubectl             # glob, matches any process in chain
              unit: "kubectl-*"         # glob, matches systemd unit name
          secret:
              collection: default       # glob
              label: "GitHub*"          # glob
              attributes:               # glob match per value
                  service: kubelogin
                  username: "kubelogin/tokencache/*"
          search_attributes:            # glob match per value (for search type)
              xdg:schema: "org.gnome.keyring.Note"
```
