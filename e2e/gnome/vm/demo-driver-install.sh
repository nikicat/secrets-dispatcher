#!/usr/bin/env bash
# Runs INSIDE the Tier-2 VM (scp'd in by demo.sh). Drives the *permanent*
# install arc (US-10), the deliberate counterpart to demo-driver.sh's reversible
# `try`. It runs in two phases either side of a relogin that demo.sh triggers
# between them (`systemctl restart gdm`) — the whole reason the recording is
# host-side (VNC), so one continuous clip spans the session restart:
#
#   part1 (before relogin):
#     go install -> service install --mode local --start (permanent takeover)
#     -> service status shows it enabled + in front.
#   << demo.sh relogs in here; the recording keeps rolling >>
#   part2 (after relogin):
#     service status STILL shows it in front (survived the restart — the payoff)
#     -> a client asks for a secret -> the (re-locked) keyring unlock dialog ->
#     type the password -> the approval notification -> click APPROVE (prints)
#     -> service uninstall restores the stock provider.
#
# Two windows are used the way a user would: an upper "admin" window for the
# service commands, a lower "client" window for the secret request. Both are
# placed by the locator extension (a Wayland client can't position itself).
#
# Env in:
#   KEYRING_PW   password typed into the "Unlock Login Keyring" dialog (part2)
#
# shellcheck disable=SC2016  # single-quoted $… is deliberate: text typed into
# the terminal for the VM's interactive shell to expand, not by this script.
set -euo pipefail
export PATH="$HOME/go/bin:$PATH"

type_cmd() { # type_cmd <tmux-session> <command line> — type at human pace, Enter
    local s=$1 i
    shift
    local line=$*
    for ((i = 0; i < ${#line}; i++)); do
        tmux send-keys -t "$s:0" -l -- "${line:i:1}"
        sleep 0.045
    done
    sleep 0.5
    tmux send-keys -t "$s:0" Enter
}

wait_for() { # wait_for <max-seconds> <cmd...> — poll until cmd succeeds
    local max=$1 i
    shift
    for ((i = 0; i < max; i++)); do
        "$@" &>/dev/null && return 0
        sleep 1
    done
    return 1
}

owner_is() { # owner_is <exe-substr> — is that binary the org.freedesktop.secrets owner?
    local pid
    pid=$(busctl --user call org.freedesktop.DBus /org/freedesktop/DBus \
        org.freedesktop.DBus GetConnectionUnixProcessID s org.freedesktop.secrets | sed 's/^u //') || return 1
    [[ $(readlink "/proc/$pid/exe") == *"$1"* ]]
}

# rd_agent runs one command batch synchronously (fresh RemoteDesktop session),
# persisting the cursor position so each glide continues from the last.
rd() { printf '%s\nquit\n' "$1" | python3 ~/rd_agent.py; }

# place moves/resizes the just-opened (focused) window to an absolute rectangle
# via the locator extension — the compositor-side way to position a Wayland win.
place() { # place <x> <y> <w> <h>
    busctl --user call org.gnome.Shell /org/gnome/Shell/SecretsDemoLocator \
        org.gnome.Shell.SecretsDemoLocator PlaceActive iiii "$1" "$2" "$3" "$4" >/dev/null
}

open_window() { # open_window <tmux-session> — open a gnome-terminal on that session
    local s=$1 i
    tmux kill-session -t "$s" 2>/dev/null || true
    tmux new-session -d -s "$s" -e PATH="$HOME/go/bin:$PATH"
    tmux set-option -t "$s" status off
    setsid gnome-terminal -- tmux attach -t "$s" >/dev/null 2>&1 &
    for i in $(seq 15); do
        tmux list-clients -t "$s" 2>/dev/null | grep -q . && return 0
        sleep 1
    done
    echo "gnome-terminal never attached to tmux session $s" >&2
    return 1
}

# --- phases ---

part1() {
    sleep 2
    # Upper "admin" window: install from the README, then make it permanent.
    open_window admin
    sleep 1
    place 110 84 1100 300
    type_cmd admin "go install github.com/nikicat/secrets-dispatcher@latest"
    wait_for 180 test -x "$HOME/go/bin/secrets-dispatcher"
    # Off-camera: swap in the fixed build until the prompter-bridge fix ships in
    # a release (see demo.sh prep_common). No-op once @latest carries the fix.
    if [ -f "$HOME/secrets-dispatcher-fixed" ]; then
        cp "$HOME/secrets-dispatcher-fixed" "$HOME/go/bin/secrets-dispatcher"
    fi
    sleep 1
    type_cmd admin 'export PATH="$HOME/go/bin:$PATH"'
    sleep 1
    # The permanent install: enable + start the user service (US-10).
    type_cmd admin "secrets-dispatcher service install --mode local --start"
    wait_for 30 owner_is secrets-dispatcher
    sleep 2
    type_cmd admin "secrets-dispatcher service status   # enabled + in front"
    sleep 4
}

part2() {
    local pw=${KEYRING_PW:?part2 needs KEYRING_PW}
    # A fresh login parks in the overview and re-locks the keyring; leave the
    # overview and re-assert no-blank so the unlock dialog renders.
    rd 'key esc'
    gsettings set org.gnome.desktop.session idle-delay 0 2>/dev/null || true
    busctl --user call org.gnome.ScreenSaver /org/gnome/ScreenSaver \
        org.gnome.ScreenSaver SetActive b false 2>/dev/null || true
    sleep 1

    # Upper "admin" window: prove it survived the relogin.
    open_window admin
    sleep 1
    place 110 84 1100 260
    type_cmd admin "secrets-dispatcher service status   # STILL in front after relogin"
    sleep 4

    # Lower "client" window: a real secret request, approved on camera.
    open_window client
    sleep 1
    place 140 420 1100 300
    sleep 1
    type_cmd client "secret-tool lookup service demo   # locked: unlock, then APPROVE"
    # Post-relogin the unlock dialog is a gcr-prompter window, not part of the
    # gnome-shell actor tree the locator walks — so waittext can't find its
    # button. Drive it by keyboard focus instead: wait for it to grab focus,
    # then type the password + Enter. The approval that follows IS a shell
    # notification, so the locator can aim the Approve click by text.
    sleep 4
    rd "$(printf 'type %s\nkey enter' "$pw")"
    rd $'waittext Approve\nclicktext Approve'
    sleep 4 # the secret value prints

    # Undo it: uninstall restores the stock Secret Service.
    type_cmd admin "secrets-dispatcher service uninstall   # stock provider restored"
    wait_for 30 owner_is gnome-keyring-daemon
    sleep 2
    type_cmd admin "secrets-dispatcher service status"
    sleep 5
}

case "${1:?usage: demo-driver-install.sh part1|part2}" in
part1) part1 ;;
part2) part2 ;;
*) echo "unknown phase: $1" >&2; exit 2 ;;
esac
