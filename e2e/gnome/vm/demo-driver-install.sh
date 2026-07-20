#!/usr/bin/env bash
# Runs INSIDE the Tier-2 VM (scp'd in by demo.sh). Drives the *permanent*
# install arc (US-10), the deliberate counterpart to demo-driver.sh's reversible
# `try`. It runs in two phases either side of a relogin that demo.sh triggers
# between them (`systemctl restart gdm`) — the whole reason the recording is
# host-side (VNC), so one continuous clip spans the session restart:
#
# demo_install (part1 + relogin + part2):
#   part1 (before relogin):
#     go install -> service install --mode local --start (permanent takeover)
#     -> service status shows it enabled + in front.
#   << demo.sh relogs in here; the recording keeps rolling >>
#   part2 (after relogin):
#     service status STILL shows it in front (survived the restart — the payoff)
#     -> a client asks for a secret -> the (re-locked) keyring unlock dialog.
#     Ends with the lookup typed; demo.sh then drives the unlock + APPROVE click
#     host-side over QMP, and part2b narrates the payoff. Leaves the service
#     installed for demo_uninstall.
#   part2b: the "it survived, and still served the secret" comment.
# demo_uninstall (uninstall):
#     service uninstall restores the stock provider, then a final secret-tool
#     lookup proves the stock keyring still serves the secret — with NO approval
#     step (that was the dispatcher's). demo.sh soft-unlocks the keyring host-side
#     if it happens to be locked.
#
# Two windows are used the way a user would: an upper "admin" window for the
# service commands, a lower "client" window for the secret request. Both are
# placed by the locator extension (a Wayland client can't position itself).
#
# GUI input (leaving the overview, typing the keyring password, clicking APPROVE)
# is NOT done here: demo.sh drives it host-side over QMP between these phases, so
# no RemoteDesktop/ScreenCast session — and thus no share indicator — is created.
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
# rename), and that late write would clobber a single cp — leaving the service to
# run (and, critically, re-run after the relogin) the unfixed @latest, whose
# unlock dialog is the display-less gcr fallback. Re-cp until a copy survives.
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

part1() {
    sleep 0.5
    # Upper "admin" window: install from the README, then make it permanent. Tall
    # (~18 lines) so `service status`'s output doesn't scroll the typed command
    # off the top.
    open_window admin
    sleep 1
    place 110 84 1100 440
    # Remove any binary a prior demo left here, so wait_for below blocks on THIS
    # go install finishing rather than passing instantly on a stale binary (which
    # would let go install overwrite the swap and the service run unfixed @latest).
    rm -f "$HOME/go/bin/secrets-dispatcher"
    type_cmd admin "go install github.com/nikicat/secrets-dispatcher@latest"
    wait_for 180 test -x "$HOME/go/bin/secrets-dispatcher"
    swap_fixed
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
    # A fresh login re-locks the keyring; the overview was already left by demo.sh
    # (host-side Esc over QMP). Re-assert no-blank so the unlock dialog renders.
    gsettings set org.gnome.desktop.session idle-delay 0 2>/dev/null || true
    busctl --user call org.gnome.ScreenSaver /org/gnome/ScreenSaver \
        org.gnome.ScreenSaver SetActive b false 2>/dev/null || true
    sleep 1

    # Upper "admin" window: prove it survived the relogin.
    open_window admin
    sleep 1
    place 110 84 1100 360
    type_cmd admin "secrets-dispatcher service status   # STILL in front after relogin"
    sleep 4

    # Lower "client" window: a real secret request. Ends with the lookup typed;
    # the (re-locked) keyring's unlock dialog is now up. The prompter bridge
    # forwards it to gnome-shell's modal (re-established after the relogin), so
    # it's in the shell actor tree and the locator can aim at it by text —
    # demo.sh then types the password + Enter and clicks APPROVE, host-side.
    open_window client
    sleep 1
    place 140 470 1100 260
    sleep 1
    type_cmd client "secret-tool lookup service demo   # locked: unlock, then APPROVE"
}

part2b() {
    sleep 2 # the secret value prints (it lands right after the host's Approve click)
    # Call out the payoff (plain glyphs only — the mono terminal font has no
    # colour-emoji, they render as tofu).
    type_cmd client "# ✓ survived the relogin, still served the secret — it works!"
    sleep 5
}

# The logout between part1 and part2 is a real, mouse-visible GNOME logout driven
# by demo.sh host-side over QMP (so it reads as a deliberate logout, not a crash);
# GDM's TimedLogin then logs back in on its own. See demo.sh.
#
# uninstall is its own demo (demo_uninstall): the deliberate reversal, back to
# stock. Runs after demo_install left the service in front (demo.sh reinstalls
# off-camera first when this demo is run standalone). A single, focused window.
# It ends by typing a lookup that proves the stock keyring still serves the
# secret with no approval step; demo.sh soft-unlocks host-side if the keyring is
# locked, then the secret prints.
uninstall() {
    gsettings set org.gnome.desktop.session idle-delay 0 2>/dev/null || true
    busctl --user call org.gnome.ScreenSaver /org/gnome/ScreenSaver \
        org.gnome.ScreenSaver SetActive b false 2>/dev/null || true
    sleep 1
    open_window admin
    sleep 1
    place 110 130 1100 460
    type_cmd admin "secrets-dispatcher service status   # secrets-dispatcher in front"
    sleep 4
    type_cmd admin "secrets-dispatcher service uninstall   # reverse it — back to stock"
    wait_for 30 owner_is gnome-keyring-daemon
    sleep 2
    type_cmd admin "secrets-dispatcher service status   # stock gnome-keyring restored"
    sleep 4
    # The payoff: the stock keyring still serves the secret, and there's no
    # approval prompt any more — that was the dispatcher's. (demo.sh drives the
    # keyring unlock host-side if a dialog appears; then the secret prints.)
    type_cmd admin "secret-tool lookup service demo   # stock keyring serves it — no approval"
}

case "${1:?usage: demo-driver-install.sh part1|part2|part2b|uninstall}" in
part1) part1 ;;
part2) part2 ;;
part2b) part2b ;;
uninstall) uninstall ;;
*) echo "unknown phase: $1" >&2; exit 2 ;;
esac
