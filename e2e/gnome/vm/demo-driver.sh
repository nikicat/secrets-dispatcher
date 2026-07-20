#!/usr/bin/env bash
# Runs INSIDE the Tier-2 VM (scp'd in by demo.sh). Drives the whole recorded
# arc: it opens two real gnome-terminal windows, places them with the locator
# extension (a Wayland client can't position itself — the shell does it), types
# the install/try arc into the upper "trial" window and the client requests into
# the lower "client" window, and drives the GNOME unlock dialog + approval
# notifications through rd_agent (RemoteDesktop) with a real, moving cursor.
#
# Two windows (not a tmux split) so the client requests visibly run in a
# separate terminal, the way a user would actually try it.
#
# Env in:
#   KEYRING_PW   password typed into the "Unlock Login Keyring" dialog
#
# shellcheck disable=SC2016  # single-quoted $… is deliberate: text typed into
# the terminal for the VM's interactive shell to expand, not by this script.
set -euo pipefail
KEYRING_PW=${KEYRING_PW:?demo-driver.sh needs KEYRING_PW}
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

# rd_agent runs one command batch synchronously (fresh RemoteDesktop session).
# It persists the cursor position (~/.cache/rd_agent_cursor), so each glide
# continues from where the last one ended instead of teleporting.
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

sleep 2

# Window 1 (upper): the trial itself — install from the README, then take over.
open_window trial
sleep 1
# Wide enough for ~84 cols at Monospace 16 (else `try`'s output wraps), but
# short so the wallpaper still shows above/below — windowed, not maximized.
place 110 84 1100 356
type_cmd trial "go install github.com/nikicat/secrets-dispatcher@latest"
wait_for 180 test -x "$HOME/go/bin/secrets-dispatcher"
# Off-camera: swap in the fixed build until the prompter-bridge fix is released
# (see demo.sh prep_common). No-op once @latest carries the fix.
if [ -f "$HOME/secrets-dispatcher-fixed" ]; then
    cp "$HOME/secrets-dispatcher-fixed" "$HOME/go/bin/secrets-dispatcher"
fi
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

# Beat 1: the login keyring is locked — unlock it, then DENY the access. The
# trailing "# …" is a comment bash ignores; it narrates what each step tests.
# Keep these comments short so the line doesn't wrap in the window.
type_cmd client "secret-tool lookup service demo   # locked: unlock, then DENY"
rd "$(printf 'waittext Unlock\ntype %s\nkey enter\nwaittext Deny\nclicktext Deny' "$KEYRING_PW")"
sleep 4 # secret-tool reports it was denied

# Beat 2: keyring already unlocked — try again and APPROVE; the secret prints.
type_cmd client "secret-tool lookup service demo   # unlocked: APPROVE, prints"
rd $'waittext Approve\nclicktext Approve'
sleep 5 # the secret value prints

# Restore: Ctrl-C in the trial window puts the stock Secret Service back.
tmux send-keys -t trial:0 C-c
wait_for 30 owner_is gnome-keyring-daemon
sleep 2
type_cmd client "secrets-dispatcher service status   # stock provider restored"
sleep 6
