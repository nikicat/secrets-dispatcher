#!/usr/bin/env bash
# Tier-2 acceptance scenario for the GNOME takeover (PR B / issue #1-A) and
# the reversible trial (PR C). Maps to docs/plans/onboarding-and-e2e.md
# scenario steps 2, 3 and 6, plus the try legs:
#   try. `try --dry-run` changes nothing; `try` takes over, SIGINT restores
#      every user-level file byte-exact (US-1 + US-9).
#   2. install --mode local --start on stock GNOME -> secrets-dispatcher owns
#      org.freedesktop.secrets and the pre-takeover secret is visible through
#      it (US-4 "take over the bus, keep my keyring").
#   3. reboot (full gdm autologin session restart) -> gnome-keyring does NOT
#      re-grab; the dispatcher is back in front (US-5).
#   6. uninstall -> gnome-keyring owns again, secret still there, no leftover
#      masks (US-9).
#
# Autologin realism: PAM gets no password, so the login collection is LOCKED —
# exactly like a stock autologin desktop. Non-interactive unlock of a running
# unit-managed gnome-keyring is not possible (--unlock only works when it
# becomes the daemon itself), so the data-continuity gate uses SearchItems:
# item attributes live unencrypted in login.keyring, and SearchItems reports
# matches in its locked[] array straight through dispatcher -> private backend
# -> keyring files. The interactive unlock UX itself is covered by the PR A
# prompt-forwarding gates (Tier-1) and Prompt.Dismiss here.
#
# Usage: scenario.sh <secrets-dispatcher-binary> [leg...]
#   With no legs, the full scenario runs in order. Naming legs (the leg_*
#   function names below) runs just those — handy when iterating — but each
#   leg assumes the end state of the ones before it: legs up to leg_trial
#   start AND end with stock gnome-keyring in front; leg_takeover leaves the
#   dispatcher in front for leg_prompt/leg_reboot, and leg_uninstall returns
#   to stock.
#
# shellcheck disable=SC2016,SC2088  # single-quoted $… and ~ are deliberate:
# they are remote scripts, expanded by the VM's shell, not this one.
set -euo pipefail

BIN=$(readlink -f "${1:?usage: scenario.sh <secrets-dispatcher-binary> [leg...]}")
RUN=$(dirname "$0")/run.sh
KEYRING_PW=tier2-keyring-password
ITEM_ATTRS="service tier2 user demo"

# --- helpers ---

log() { printf '\n=== %s\n' "$*"; }

vmssh() {
    "$RUN" ssh "
        set -euo pipefail
        export XDG_RUNTIME_DIR=/run/user/\$(id -u)
        export DBUS_SESSION_BUS_ADDRESS=unix:path=\$XDG_RUNTIME_DIR/bus
        $*"
}

owner_exe() {
    vmssh '
        pid=$(busctl --user call org.freedesktop.DBus /org/freedesktop/DBus \
            org.freedesktop.DBus GetConnectionUnixProcessID s org.freedesktop.secrets 2>/dev/null \
            | sed "s/^u //") || { echo none; exit 0; }
        readlink "/proc/$pid/exe" || echo unreadable'
}

expect_owner() {
    local want=$1 got
    got=$(owner_exe)
    if [[ "$got" != *"$want"* ]]; then
        echo "error: org.freedesktop.secrets owner is '$got', expected '$want'" >&2
        exit 1
    fi
    echo "   owner: $got"
}

wait_owner() {
    local want=$1 i
    for ((i = 0; i < 30; i++)); do
        [[ "$(owner_exe)" == *"$want"* ]] && return 0
        sleep 1
    done
    echo "error: org.freedesktop.secrets owner never became '$want' (is: $(owner_exe))" >&2
    return 1
}

# Byte-exact fingerprint of every user-level file the takeover touches (plus
# the user's own config, which must never change). The trial (US-9) must leave
# this identical. find fails on dirs that don't exist yet on a stock desktop
# (e.g. ~/.config/autostart) — that's fine, absent stays absent.
state_snapshot() {
    vmssh '(find ~/.config/systemd/user ~/.config/autostart ~/.config/secrets-dispatcher \
        ~/.local/share/dbus-1 -type f 2>/dev/null || true) | sort | xargs -r md5sum'
}

# The pre-takeover item must be visible via SearchItems (unlocked or locked
# array — the collection is locked on an autologin desktop).
expect_item_visible() {
    vmssh "timeout 10 busctl --user call org.freedesktop.secrets /org/freedesktop/secrets \
        org.freedesktop.Secret.Service SearchItems 'a{ss}' 2 $ITEM_ATTRS \
        | grep -q '/org/freedesktop/secrets/collection/login/'"
    echo "   pre-takeover item visible via SearchItems"
}

reboot_and_wait() {
    vmssh 'sudo reboot' 2>/dev/null || true
    sleep 15
    local i
    for ((i = 0; i < 60; i++)); do
        "$RUN" ssh true 2>/dev/null && break
        sleep 3
    done
    "$RUN" wait-desktop
    sleep 5
}

dump_try_log() { vmssh 'cat /tmp/try.log' >&2; }

# --- legs ---

leg_push_binary() {
    log "pushing binary into the VM"
    "$RUN" ssh 'mkdir -p ~/.local/bin'
    # scp to a temp name + mv: replaces the inode, so it works even when a
    # previous scenario run left the service running from this path (ETXTBSY).
    scp -P "${SSH_PORT:-2222}" -i "${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}/id_ed25519" \
        -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
        "$BIN" e2e@127.0.0.1:.local/bin/secrets-dispatcher.new
    "$RUN" ssh 'mv ~/.local/bin/secrets-dispatcher.new ~/.local/bin/secrets-dispatcher'
}

leg_baseline() {
    log "baseline: stock unit-managed gnome-keyring owns the name"
    expect_owner gnome-keyring-daemon

    log "baseline: seed a secret into the login keyring"
    # A running unit-managed daemon cannot be unlocked non-interactively, so
    # pause the units and let a single-shot --unlock daemon create the
    # (unlocked) login keyring, store the secret, then hand back to the stock
    # units. The secret lands in ~/.local/share/keyrings/login.keyring, which
    # every later daemon (stock or demoted private backend) reads.
    vmssh "
        systemctl --user stop gnome-keyring-daemon.service gnome-keyring-daemon.socket
        printf '%s\n' $KEYRING_PW | gnome-keyring-daemon --unlock --components=secrets >/dev/null
        sleep 1
        printf 'takeover-survivor' | timeout 10 secret-tool store --label='Tier2' $ITEM_ATTRS
        [[ \$(timeout 10 secret-tool lookup $ITEM_ATTRS) == takeover-survivor ]]
        pkill -f '^gnome-keyring-daemon' || true
        sleep 1
        systemctl --user start gnome-keyring-daemon.socket gnome-keyring-daemon.service
        sleep 2"
    expect_owner gnome-keyring-daemon
    expect_item_visible
    echo "   baseline secret persisted to login.keyring"

    log "pre-seeding config with an auto-approve rule for test tools"
    vmssh '
        mkdir -p ~/.config/secrets-dispatcher
        cat > ~/.config/secrets-dispatcher/config.yaml <<EOF
serve:
  notifications: false
  rules:
    - name: allow test tools
      action: approve
      process:
        exe: /usr/bin/busctl
EOF'
}

leg_try_dry_run() {
    log "TRY (US-1): --dry-run prints the plan and changes nothing"
    local before dryrun_out
    before=$(state_snapshot)
    dryrun_out=$(vmssh '~/.local/bin/secrets-dispatcher try --dry-run')
    grep -q 'secrets-dispatcher.service' <<<"$dryrun_out"
    [[ "$(state_snapshot)" == "$before" ]] || {
        echo "error: try --dry-run changed user files" >&2
        exit 1
    }
    echo "   dry-run listed the unit changes and left the system untouched"
}

leg_trial() {
    log "TRY (US-9): reversible trial — takeover, then Ctrl-C restores byte-exact"
    local before after
    before=$(state_snapshot)
    # Plain `nohup cmd &` (no compound list): $! must be the dispatcher
    # itself, not a wrapping subshell, or the SIGINT below never reaches it.
    vmssh 'rm -f /tmp/try.log
        nohup ~/.local/bin/secrets-dispatcher try >/tmp/try.log 2>&1 &
        echo $! > /tmp/try.pid'
    wait_owner secrets-dispatcher || { dump_try_log; exit 1; }
    expect_owner secrets-dispatcher
    expect_item_visible

    vmssh 'kill -INT $(cat /tmp/try.pid)'
    vmssh 'for i in $(seq 40); do kill -0 $(cat /tmp/try.pid) 2>/dev/null || exit 0; sleep 0.5; done
        echo "try did not exit after SIGINT" >&2; exit 1' \
        || { dump_try_log; exit 1; }
    wait_owner gnome-keyring-daemon || { dump_try_log; exit 1; }
    expect_item_visible
    after=$(state_snapshot)
    if [[ "$after" != "$before" ]]; then
        echo "error: trial did not restore user files byte-exact" >&2
        diff <(echo "$before") <(echo "$after") >&2 || true
        dump_try_log
        exit 1
    fi
    vmssh '! test -e "$XDG_RUNTIME_DIR/secrets-dispatcher/try-config.yaml"'
    echo "   trial reversed byte-exact, trial config gone (US-9)"
}

leg_takeover() {
    log "STEP 2 (US-4): service install --mode local --start"
    vmssh '~/.local/bin/secrets-dispatcher service install --mode local --start'
    sleep 3
    expect_owner secrets-dispatcher

    log "status doctor reports a healthy takeover (US-11)"
    vmssh '~/.local/bin/secrets-dispatcher service status'

    log "session gnome-keyring demoted to pkcs11-only, private backend running"
    vmssh '
        pgrep -f "components=pkcs11 --control-directory=/run/user/[0-9]*/keyring" >/dev/null
        pgrep -f "components=secrets --control-directory=/run/user/[0-9]*/secrets-dispatcher/keyring" >/dev/null'

    log "pre-takeover secret visible THROUGH the dispatcher (US-4)"
    expect_item_visible
}

leg_prompt() {
    log "prompt plumbing works in the real topology (US-6): Unlock -> Dismiss"
    vmssh '
        prompt=$(timeout 10 busctl --user call org.freedesktop.secrets /org/freedesktop/secrets \
            org.freedesktop.Secret.Service Unlock ao 1 /org/freedesktop/secrets/collection/login \
            | grep -o "/org/freedesktop/secrets/prompt/[[:alnum:]_/]*")
        [[ -n "$prompt" ]]
        timeout 10 busctl --user call org.freedesktop.secrets "$prompt" org.freedesktop.Secret.Prompt Dismiss'
    echo "   Unlock returned a prompt path and Dismiss was forwarded"
}

leg_reboot() {
    log "STEP 3 (US-5): reboot — gnome-keyring must NOT re-grab"
    reboot_and_wait
    sleep 5
    expect_owner secrets-dispatcher
    expect_item_visible
    echo "   still in front after a full session restart (US-5)"
}

leg_uninstall() {
    log "STEP 6 (US-9): uninstall — full reversal"
    vmssh '~/.local/bin/secrets-dispatcher service uninstall'
    sleep 3
    # Poke the name so D-Bus activation (now unmasked) can restart
    # gnome-keyring if try-restart didn't already.
    vmssh 'timeout 10 busctl --user call org.freedesktop.secrets /org/freedesktop/secrets \
        org.freedesktop.DBus.Properties Get ss org.freedesktop.Secret.Service Collections >/dev/null'
    expect_owner gnome-keyring-daemon
    expect_item_visible
    echo "   gnome-keyring restored as owner, secret intact (US-9)"

    log "no leftover dispatcher state"
    vmssh '
        ! test -e ~/.config/systemd/user/gnome-keyring-daemon.service.d/50-secrets-dispatcher.conf
        ! test -e ~/.config/autostart/gnome-keyring-secrets.desktop
        ! test -e ~/.local/share/dbus-1/services/org.freedesktop.secrets.service'

    log "status doctor reports clean after uninstall (US-11)"
    vmssh '~/.local/bin/secrets-dispatcher service status'
}

# --- driver ---

main() {
    if (($#)); then
        local leg
        for leg in "$@"; do "$leg"; done
        log "PASS: $*"
        return
    fi
    leg_push_binary
    leg_baseline
    leg_try_dry_run
    leg_trial
    leg_takeover
    leg_prompt
    leg_reboot
    leg_uninstall
    log "PASS: takeover, trial reversibility, re-grab resistance, and full reversal all verified"
}

main "${@:2}"
