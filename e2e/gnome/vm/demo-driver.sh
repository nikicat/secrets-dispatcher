#!/usr/bin/env bash
# Runs INSIDE the Tier-2 VM (scp'd in by demo.sh). Drives the reversible `try`
# arc in phases so the host (demo.sh) can drive the GNOME unlock dialog +
# approval notifications between phases over QMP — host-side pointer/keyboard,
# no RemoteDesktop, so no "screen is being shared" indicator ever appears.
#
# The split: terminal typing (tmux send-keys) and window placement (the locator
# extension's PlaceActive, a compositor-side D-Bus call) stay in-guest here; only
# GUI *input injection* (typing into the unlock dialog, clicking notification
# buttons) is host-side. demo.sh interleaves those host beats between the phases:
#
#   open   : go install -> try (live takeover) -> a client lookup that hits the
#            locked keyring. Ends with the lookup typed; the unlock dialog is now
#            up for the host to drive (unlock, then DENY the access).
#   << host: unlock + click DENY >>
#   second : a second lookup, keyring now unlocked (host clicks APPROVE next).
#   << host: click APPROVE — the secret prints >>
#   restore: Ctrl-C restores the stock provider; show status.
#
# Two windows (not a tmux split) so the client requests visibly run in a separate
# terminal, the way a user would actually try it.
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

# swap_fixed replaces the just-installed @latest binary with the caller-provided
# fixed build (it carries the unreleased prompter-bridge fix; see demo.sh
# prep_common). No-op once @latest carries the fix. It cp's in a loop until the
# swap sticks: `go install` may still be flushing its binary when wait_for's
# test -x first passes (a cross-filesystem install is a non-atomic copy, not a
# rename), and that late write would clobber a single cp — leaving the demo to
# run the unfixed @latest. Re-cp until a copy survives, then go install is done.
swap_fixed() {
    [ -f "$HOME/secrets-dispatcher-fixed" ] || return 0
    local i
    for ((i = 0; i < 30; i++)); do
        cp "$HOME/secrets-dispatcher-fixed" "$HOME/go/bin/secrets-dispatcher"
        sleep 0.5
        cmp -s "$HOME/secrets-dispatcher-fixed" "$HOME/go/bin/secrets-dispatcher" && return 0
    done
    echo "warning: fixed-binary swap did not stick (go install kept overwriting it)" >&2
}

# --- phases ---

open() {
    sleep 0.3 # minimal dead time before the first window opens (was ~2s)
    # Window 1 (upper): the trial itself — install from the README, then take over.
    open_window trial
    sleep 1
    # Wide enough for ~84 cols at Monospace 16 (else `try`'s output wraps), but
    # short so the wallpaper still shows above/below — windowed, not maximized.
    place 110 84 1100 356
    # Remove any binary a prior demo left here, so wait_for below blocks on THIS
    # go install finishing rather than passing instantly on a stale binary.
    rm -f "$HOME/go/bin/secrets-dispatcher"
    type_cmd trial "go install github.com/nikicat/secrets-dispatcher@latest"
    wait_for 180 test -x "$HOME/go/bin/secrets-dispatcher"
    swap_fixed
    sleep 1
    type_cmd trial 'export PATH="$HOME/go/bin:$PATH"'
    sleep 1
    type_cmd trial "secrets-dispatcher try"
    wait_for 30 owner_is secrets-dispatcher
    sleep 3

    # Window 2 (lower): a client asking for a secret, in its own terminal.
    open_window client
    sleep 1
    place 140 452 1100 300
    sleep 1
    # Beat 1: the login keyring is locked — the host unlocks it, then DENIES the
    # access. The trailing "# …" is a comment bash ignores; it narrates the step.
    # Keep these comments short so the line doesn't wrap in the window.
    type_cmd client "secret-tool lookup service demo   # locked: unlock, then DENY"
}

second() {
    sleep 4 # secret-tool reports it was denied
    # Beat 2: keyring already unlocked — try again; the host APPROVEs and the
    # secret prints.
    type_cmd client "secret-tool lookup service demo   # unlocked: APPROVE, prints"
}

restore() {
    sleep 5 # the secret value prints
    # Restore: Ctrl-C in the trial window puts the stock Secret Service back.
    tmux send-keys -t trial:0 C-c
    wait_for 30 owner_is gnome-keyring-daemon
    sleep 2
    type_cmd client "secrets-dispatcher service status   # stock provider restored"
    sleep 6
}

case "${1:?usage: demo-driver.sh open|second|restore}" in
open) open ;;
second) second ;;
restore) restore ;;
*) echo "unknown phase: $1" >&2; exit 2 ;;
esac
