#!/usr/bin/env bash
# Tier-2 acceptance scenario for the GNOME takeover (PR B / issue #1-A).
# Maps to docs/plans/onboarding-and-e2e.md scenario steps 2, 3 and 6:
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
# Usage: scenario.sh <secrets-dispatcher-binary>  (CGO_ENABLED=0 build)
#
# shellcheck disable=SC2016,SC2088  # single-quoted $… and ~ are deliberate:
# they are remote scripts, expanded by the VM's shell, not this one.
set -euo pipefail

BIN=$(readlink -f "${1:?usage: scenario.sh <secrets-dispatcher-binary>}")
RUN=$(dirname "$0")/run.sh
KEYRING_PW=tier2-keyring-password
ITEM_ATTRS="service tier2 user demo"

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

log "pushing binary into the VM"
"$RUN" ssh 'mkdir -p ~/.local/bin'
# scp to a temp name + mv: replaces the inode, so it works even when a
# previous scenario run left the service running from this path (ETXTBSY).
scp -P "${SSH_PORT:-2222}" -i "${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}/id_ed25519" \
    -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
    "$BIN" e2e@127.0.0.1:.local/bin/secrets-dispatcher.new
"$RUN" ssh 'mv ~/.local/bin/secrets-dispatcher.new ~/.local/bin/secrets-dispatcher'

log "baseline: stock unit-managed gnome-keyring owns the name"
expect_owner gnome-keyring-daemon

log "baseline: seed a secret into the login keyring"
# A running unit-managed daemon cannot be unlocked non-interactively, so pause
# the units and let a single-shot --unlock daemon create the (unlocked) login
# keyring, store the secret, then hand back to the stock units. The secret
# lands in ~/.local/share/keyrings/login.keyring, which every later daemon
# (stock or demoted private backend) reads.
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

log "STEP 2 (US-4): service install --mode local --start"
vmssh '~/.local/bin/secrets-dispatcher service install --mode local --start'
sleep 3
expect_owner secrets-dispatcher

log "session gnome-keyring demoted to pkcs11-only, private backend running"
vmssh '
    pgrep -f "components=pkcs11 --control-directory=/run/user/[0-9]*/keyring" >/dev/null
    pgrep -f "components=secrets --control-directory=/run/user/[0-9]*/secrets-dispatcher/keyring" >/dev/null'

log "pre-takeover secret visible THROUGH the dispatcher (US-4)"
expect_item_visible

log "prompt plumbing works in the real topology (US-6): Unlock -> Dismiss"
vmssh '
    prompt=$(timeout 10 busctl --user call org.freedesktop.secrets /org/freedesktop/secrets \
        org.freedesktop.Secret.Service Unlock ao 1 /org/freedesktop/secrets/collection/login \
        | grep -o "/org/freedesktop/secrets/prompt/[[:alnum:]_/]*")
    [[ -n "$prompt" ]]
    timeout 10 busctl --user call org.freedesktop.secrets "$prompt" org.freedesktop.Secret.Prompt Dismiss'
echo "   Unlock returned a prompt path and Dismiss was forwarded"

log "STEP 3 (US-5): reboot — gnome-keyring must NOT re-grab"
reboot_and_wait
sleep 5
expect_owner secrets-dispatcher
expect_item_visible
echo "   still in front after a full session restart (US-5)"

log "STEP 6 (US-9): uninstall — full reversal"
vmssh '~/.local/bin/secrets-dispatcher service uninstall'
sleep 3
# Poke the name so D-Bus activation (now unmasked) can restart gnome-keyring
# if try-restart didn't already.
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

log "PASS: takeover, re-grab resistance, and full reversal all verified"
