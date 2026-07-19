#!/usr/bin/env bash
# Screen-recorded product demos in the Tier-2 GNOME VM. Same cached desktop
# base as scenario.sh, but instead of asserting acceptance gates it stages the
# VM, types the install/try arc into a visible gnome-terminal, and drives the
# GNOME unlock dialog + approval notifications with a real, visibly-moving
# cursor — then records the whole thing (GNOME Shell screencast, VP8/WebM).
#
# How the GUI is driven (the interesting part):
#   - Locator: a tiny gnome-shell extension (locator-extension/, exported as
#     org.gnome.Shell.SecretsDemoLocator) returns a shell element's on-screen
#     rectangle *by its visible label*. gnome-shell's own accessibility (Cally)
#     exposes no element geometry and there is no external API for it, so this
#     is the GNOME-recommended path (an extension exporting D-Bus). It lets us
#     aim at "the Approve button" by text, not by hardcoded pixels.
#   - Mover: rd_agent.py drives org.gnome.Mutter.RemoteDesktop (gnome-shell's
#     own input API — no portal, no consent) to type the keyring password and
#     glide the real cursor onto a located button and click it. Absolute
#     coordinates are stage logical pixels, matching the locator's output 1:1.
#
# demo_trial is the install/try arc with two client-request beats:
#   go install -> try (live takeover) -> secret-tool lookup raises the GNOME
#   unlock dialog (US-6/prompter-bridge) -> type the keyring password -> the
#   GetSecret approval notification pops -> click DENY (blocked) -> a second
#   lookup -> click APPROVE (secret prints) -> Ctrl-C restores.
#
# The in-VM halves live in real, lintable files scp'd into the guest:
# demo-stage.sh (desktop look) and demo-driver.sh (the typed/clicked arc).
#
# Videos are throwaway build artifacts, never committed: locally in the output
# dir (Makefile: .build/demos), in CI demos.yml uploads them.
#
# Usage: demo.sh <output-dir> <secrets-dispatcher-binary> [demo...]
# The VM must be booted with the desktop up (run.sh boot && run.sh wait-desktop).
#
# shellcheck disable=SC2016  # single-quoted $… is deliberate: remote scripts,
# expanded by the VM's shell, not this one.
set -euo pipefail

OUT=$(realpath "${1:?usage: demo.sh <output-dir> <binary> [demo...]}")
BIN=$(readlink -f "${2:?usage: demo.sh <output-dir> <binary> [demo...]}")
shift 2
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
RUN=$SCRIPT_DIR/run.sh
RECORD=$SCRIPT_DIR/record.sh
EXT_UUID=secrets-demo-locator@secrets-dispatcher.nikicat
DOCK_UUID=ubuntu-dock@ubuntu.com
KEYRING_PW=opensesame
SECRET_VALUE=ghp_demo_classic_pat_00
SSH_PORT=${SSH_PORT:-2222}
CACHE_DIR=${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}

log() { printf '\n=== %s\n' "$*"; }

vmssh() {
    "$RUN" ssh "
        set -euo pipefail
        export XDG_RUNTIME_DIR=/run/user/\$(id -u)
        export DBUS_SESSION_BUS_ADDRESS=unix:path=\$XDG_RUNTIME_DIR/bus
        $*"
}

scp_in() {
    scp -P "$SSH_PORT" -i "$CACHE_DIR/id_ed25519" -o IdentitiesOnly=yes \
        -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
        "$@"
}

reboot_and_wait() {
    "$RUN" ssh 'sudo reboot' 2>/dev/null || true
    sleep 15
    local i
    for ((i = 0; i < 60; i++)); do
        "$RUN" ssh true 2>/dev/null && break
        sleep 3
    done
    "$RUN" wait-desktop
    sleep 5
}

# --- off-camera staging ---

prep_common() {
    log "installing the Ubuntu dock + locator extension + RemoteDesktop agent"
    # The desktop base is a plain GNOME session (no dock), which doesn't read as
    # Ubuntu on camera. Add the Ubuntu dock so the demo looks like a real Ubuntu
    # desktop; demo-stage.sh then moves it to the left in the classic layout.
    vmssh 'sudo DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a \
        apt-get install -y -qq --no-install-recommends gnome-shell-extension-ubuntu-dock >/dev/null 2>&1'
    vmssh "mkdir -p ~/.local/share/gnome-shell/extensions/$EXT_UUID ~/go/bin"
    scp_in "$SCRIPT_DIR/locator-extension/metadata.json" "$SCRIPT_DIR/locator-extension/extension.js" \
        "e2e@127.0.0.1:.local/share/gnome-shell/extensions/$EXT_UUID/"
    scp_in "$SCRIPT_DIR/rd_agent.py" "$SCRIPT_DIR/demo-stage.sh" "$SCRIPT_DIR/demo-driver.sh" \
        "e2e@127.0.0.1:"
    vmssh "
        gsettings set org.gnome.shell disable-extension-version-validation true
        gsettings set org.gnome.shell enabled-extensions \"['$DOCK_UUID', '$EXT_UUID']\""

    log "staging the desktop look"
    vmssh 'bash ~/demo-stage.sh'

    # Notifications ON, no auto-approve rule: requests must visibly wait for a
    # click — the product moment the demo exists to show.
    vmssh '
        mkdir -p ~/.config/secrets-dispatcher
        printf "serve:\n  notifications: true\n" > ~/.config/secrets-dispatcher/config.yaml'

    log "seeding a password-protected login keyring (unlock is part of the story)"
    vmssh "
        systemctl --user stop gnome-keyring-daemon.service gnome-keyring-daemon.socket
        pkill -f '^gnome-keyring-daemon' || true
        sleep 1
        rm -f ~/.local/share/keyrings/*
        printf '%s' '$KEYRING_PW' | gnome-keyring-daemon --unlock --components=secrets >/dev/null
        sleep 1
        printf '%s' '$SECRET_VALUE' | timeout 10 secret-tool store --label='GitHub token' service demo
        pkill -f '^gnome-keyring-daemon' || true
        sleep 1"

    log "warming the go module cache so the on-camera install is quick"
    # Warm the cache, then REMOVE the binary: the on-camera `go install` must be
    # the one that re-creates ~/go/bin/secrets-dispatcher, so the driver's
    # wait_for blocks until that real install finishes before swapping the fixed
    # build in. Leaving it in place makes wait_for pass instantly and the
    # on-camera install overwrites the swap a moment later.
    vmssh 'export PATH=$PATH:/usr/local/go/bin
        timeout 900 go install github.com/nikicat/secrets-dispatcher@latest || true
        rm -f ~/go/bin/secrets-dispatcher'

    # The demo depends on the prompter-bridge fix, which is not in a tagged
    # release yet — so `go install @latest` would leave an *unfixed* binary and
    # the unlock would hang. Stash the caller-provided (fixed) build; the driver
    # swaps it in off-camera right after the on-camera install. TODO: once the
    # fix ships in a release, drop the stash/swap and let `go install` stand.
    scp_in "$BIN" "e2e@127.0.0.1:secrets-dispatcher-fixed"

    log "rebooting once: loads the extension and gives a clean notification stack"
    reboot_and_wait
    vmssh 'busctl --user call org.gnome.ScreenSaver /org/gnome/ScreenSaver \
        org.gnome.ScreenSaver SetActive b false 2>/dev/null || true'
    # Forget any cursor position from a previous run so the first glide starts
    # from the parked corner (the reboot genuinely re-parks the cursor).
    vmssh 'rm -f ~/.cache/rd_agent_cursor'
    vmssh "gnome-extensions info $EXT_UUID | grep -q 'State: ACTIVE'" ||
        die "locator extension failed to activate"
}

die() {
    echo "error: $*" >&2
    exit 1
}

# --- demos ---

demo_trial() {
    log "demo_trial: install -> try -> unlock+deny -> approve -> Ctrl-C restore"

    # GNOME parks fresh logins in the Activities overview and an SSH-launched
    # window carries no activation token to leave it; a quick Esc via rd_agent
    # lands us on a normal desktop before the camera rolls.
    vmssh 'printf "key esc\nquit\n" | python3 ~/rd_agent.py >/dev/null 2>&1 || true'
    sleep 1
    "$RECORD" start /home/e2e/demo-trial.webm
    sleep 2

    # The driver opens and places its own terminal windows (via the locator
    # extension), so the terminals appear on camera as part of the story.
    if ! vmssh "KEYRING_PW=$KEYRING_PW bash ~/demo-driver.sh"; then
        "$RECORD" stop /home/e2e/demo-trial.webm "$OUT/trial-failed.webm" || true
        vmssh 'tmux kill-server 2>/dev/null || true; pkill -x sd-local 2>/dev/null || true' || true
        die "demo driver failed (partial recording kept for post-mortem)"
    fi

    "$RECORD" stop /home/e2e/demo-trial.webm "$OUT/trial.webm"
    vmssh 'tmux kill-server 2>/dev/null || true; pkill -f "gnome-terminal-serve[r]" 2>/dev/null || true'
}

# --- driver ---

finish() {
    local f
    for f in "$OUT"/*.webm; do
        [[ -e "$f" ]] || continue
        if command -v ffprobe >/dev/null; then
            local dur; dur=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$f")
            LC_ALL=C printf '%s: %.0fs\n' "$f" "$dur"
        fi
        command -v ffmpeg >/dev/null &&
            ffmpeg -y -v error -i "$f" -c:v libx264 -pix_fmt yuv420p -movflags +faststart "${f%.webm}.mp4"
    done
}

main() {
    mkdir -p "$OUT"
    # Drop artifacts from a previous run: a stale trial-failed.webm would
    # otherwise linger and get re-encoded to .mp4 by finish(), making a good
    # run look failed.
    rm -f "$OUT"/trial*.webm "$OUT"/trial*.mp4
    local demos=("$@")
    ((${#demos[@]})) || demos=(demo_trial)
    prep_common
    local d
    for d in "${demos[@]}"; do "$d"; done
    finish
    log "PASS: ${demos[*]} recorded to $OUT"
}

main "$@"
