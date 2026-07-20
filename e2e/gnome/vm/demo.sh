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
#     aim at "the Approve button" by text, not by hardcoded pixels — a plain
#     D-Bus query (wait_locate below), no ScreenCast, so no share indicator.
#   - Input: qmpinput.py drives the guest's virtio tablet + keyboard host-side
#     over QEMU's QMP socket (the gui_* helpers below). It types the keyring
#     password and glides the real cursor onto a located button and clicks it.
#     Guest pixels map 1:1 to the locator's logical-pixel output (the guest is
#     pinned to 1280x800, scale 1). Doing input host-side — instead of the old
#     in-guest RemoteDesktop agent — means no ScreenCast session is ever created,
#     so the "screen is being shared" indicator never flashes on camera, and the
#     same path can open the top-panel menus (the logout) that RemoteDesktop
#     couldn't.
#
# demo_trial is the reversible-`try` arc with two client-request beats:
#   go install -> try (live takeover) -> secret-tool lookup raises the GNOME
#   unlock dialog (US-6/prompter-bridge) -> type the keyring password -> the
#   GetSecret approval notification pops -> click DENY (blocked) -> a second
#   lookup -> click APPROVE (secret prints) -> Ctrl-C restores.
#
# demo_install is the *permanent* counterpart (US-10): go install ->
#   `service install --mode local --start` -> status -> a relogin (the recording
#   spans it) -> still in front -> unlock + APPROVE. The relogin is why recording
#   is host-side (record.sh/VNC): the in-guest screencast would die with the
#   session. It leaves the service installed.
# demo_uninstall is the deliberate reversal, split out as its own clip: service
#   uninstall -> stock gnome-keyring restored. Runs after demo_install (or
#   reinstalls off-camera when run standalone).
#
# The in-VM halves live in real, lintable files scp'd into the guest:
# demo-stage.sh (desktop look), demo-driver.sh (the try arc) and
# demo-driver-install.sh (the install + uninstall arcs).
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
SECRET_VALUE='ghp_super_secret_key_123##@!'
SSH_PORT=${SSH_PORT:-2222}
CACHE_DIR=${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}
UBUNTU_SERIES=${UBUNTU_SERIES:-noble}
# QEMU QMP socket (run.sh's start_qemu creates it per instance) — the demo drives
# the pointer host-side through it for the logout menu; see qmpinput.py.
QMP_SOCK=${VM_DIR:-$CACHE_DIR/instance-$UBUNTU_SERIES}/qmp.sock

log() { printf '\n=== %s\n' "$*"; }

# qmp runs a host-side input script (read on stdin: glide/move/click/type/key/
# sleep) against the guest via QMP — the demo's only pointer + keyboard path.
# Coordinates are guest pixels for the pinned 1280x800 screen.
qmp() { python3 "$SCRIPT_DIR/qmpinput.py" "$QMP_SOCK"; }

# gui_esc leaves the Activities overview (a fresh/SSH-launched session parks
# there); gui_park moves the cursor clear of the terminal windows so it never
# obscures typed text. Both are host-side keystroke/motion, no indicator.
gui_esc() { printf 'key esc\n' | qmp; }
gui_park() { printf 'move 640 775\n' | qmp; }

# wait_locate_snippet emits an in-guest bash snippet that polls the locator for a
# labelled element and prints its centre "X Y" (guest px) once found, or exits 1
# after <timeout> seconds. Run via vmssh; the coordinates come back on stdout.
# busctl prints the Locate reply as "b <found> d <x> d <y> d <w> d <h>"; the awk
# scans past the "true" flag for the four numeric fields (tolerant of the type
# markers) and returns the centre.
wait_locate_snippet() { # <label> <timeout-seconds>
    local label=$1 timeout=${2:-20}
    cat <<SNIPPET
for i in \$(seq $timeout); do
    out=\$(busctl --user call org.gnome.Shell /org/gnome/Shell/SecretsDemoLocator \
        org.gnome.Shell.SecretsDemoLocator Locate s "$label" 2>/dev/null) || out=
    xy=\$(printf '%s\n' "\$out" | awk '{ for (i=1;i<=NF;i++) if (\$i=="true") {
        n=0; for (j=i+1;j<=NF;j++) if (\$j ~ /^-?[0-9.]+\$/) v[++n]=\$j;
        if (n>=4) printf "%d %d", v[1]+v[3]/2, v[2]+v[4]/2 } }')
    if [ -n "\$xy" ]; then printf '%s' "\$xy"; exit 0; fi
    sleep 0.5
done
exit 1
SNIPPET
}

# gui_unlock waits for the keyring unlock dialog, then types the password + Enter
# host-side. Args: [timeout] [soft]; with "soft" a missing dialog is not an error
# (used by the uninstall demo, where the keyring may already be unlocked).
gui_unlock() { # [timeout] [soft]
    local timeout=${1:-20} soft=${2:-}
    if vmssh "$(wait_locate_snippet Unlock "$timeout")" >/dev/null 2>&1; then
        printf 'type %s\nkey enter\n' "$KEYRING_PW" | qmp
    elif [[ -z "$soft" ]]; then
        return 1
    fi
}

# gui_click waits for a labelled notification button (Approve/Deny), glides the
# visible cursor onto it (the on-camera "click"), then activates it in-process via
# the locator extension. A synthesized QMP click on the banner was timing-fragile
# — on a slower host the banner was still animating in (or the just-dismissed
# keyring-unlock modal's grab was still releasing) and never took the hover, so
# the click hit nothing and the request timed out. Emitting the button's action
# in-process sidesteps pointer-event routing entirely; the glide is kept purely
# for the visible cursor. Confirms the label is gone, retrying if not.
gui_click() { # <label>
    local xy i
    for ((i = 0; i < 4; i++)); do
        xy=$(vmssh "$(wait_locate_snippet "$1" 15)") || return 1
        [[ -n "$xy" ]] || return 1
        printf 'glide %s\n' "$xy" | qmp # visible cursor movement onto the button
        vmssh "busctl --user call org.gnome.Shell /org/gnome/Shell/SecretsDemoLocator \
            org.gnome.Shell.SecretsDemoLocator ClickButton s '$1' >/dev/null 2>&1" || true
        sleep 0.6
        # If the label is gone, the action fired; otherwise retry.
        vmssh "$(wait_locate_snippet "$1" 1)" >/dev/null 2>&1 || return 0
    done
    echo "warning: '$1' still present after $i activations" >&2
    return 1
}

# verify_secret confirms the secret value actually printed in a client terminal
# (the whole point of an approve beat) — so a click that silently failed to
# register fails the demo loudly instead of producing a recording where nothing
# was served. Polls the pane for a few seconds; the secret lands right after the
# approval reaches the backend.
verify_secret() { # <tmux-session>
    vmssh "for i in \$(seq 12); do tmux capture-pane -t $1 -p 2>/dev/null | grep -q 'ghp_super_secret_key' && exit 0; sleep 0.5; done; exit 1"
}

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
    log "installing the Ubuntu dock + locator extension"
    # The desktop base is a plain GNOME session (no dock), which doesn't read as
    # Ubuntu on camera. Add the Ubuntu dock so the demo looks like a real Ubuntu
    # desktop; demo-stage.sh then moves it to the left in the classic layout.
    vmssh 'sudo DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a \
        apt-get install -y -qq --no-install-recommends gnome-shell-extension-ubuntu-dock >/dev/null 2>&1'
    vmssh "mkdir -p ~/.local/share/gnome-shell/extensions/$EXT_UUID ~/go/bin"
    scp_in "$SCRIPT_DIR/locator-extension/metadata.json" "$SCRIPT_DIR/locator-extension/extension.js" \
        "e2e@127.0.0.1:.local/share/gnome-shell/extensions/$EXT_UUID/"
    scp_in "$SCRIPT_DIR/demo-stage.sh" "$SCRIPT_DIR/demo-driver.sh" \
        "$SCRIPT_DIR/demo-driver-install.sh" "e2e@127.0.0.1:"
    vmssh "
        gsettings set org.gnome.shell disable-extension-version-validation true
        gsettings set org.gnome.shell enabled-extensions \"['$DOCK_UUID', '$EXT_UUID']\""

    # The relogin flashes the GDM greeter, which shows the account's display name.
    # A bare login of "e2e" reads as a test rig; set the GECOS full name so the
    # greeter (and its TimedLogin line) shows a neutral "User".
    vmssh "sudo usermod -c User e2e"

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

    # Host-side VNC capture (record.sh) only sees the cursor if mutter renders
    # it into the framebuffer instead of a virtio-gpu HW cursor plane (which VNC
    # sends out-of-band and the grabber drops). Force a software cursor; it takes
    # effect at the next login, which the reboot below provides.
    vmssh 'grep -q MUTTER_DEBUG_DISABLE_HW_CURSORS /etc/environment \
        || echo "MUTTER_DEBUG_DISABLE_HW_CURSORS=1" | sudo tee -a /etc/environment >/dev/null'

    # Kernel console -> serial only (drop the default console=tty1), so the
    # graphical framebuffer VT never carries the boot-log text. Otherwise the
    # relogin's gdm restart briefly paints it while no compositor owns the
    # display — a headless-VM artifact. Serial still reaches run.sh's log file.
    # Takes effect on the reboot below.
    vmssh 'sudo tee /etc/default/grub.d/99-demo-console.cfg >/dev/null <<EOF
GRUB_CMDLINE_LINUX_DEFAULT="quiet splash console=ttyS0"
EOF
        sudo update-grub >/dev/null 2>&1'

    # Enable TimedLogin so GDM re-logs-in on its own after the demo's mouse-driven
    # logout: an autologin desktop won't AutomaticLogin again after a manual
    # logout (GDM loop-prevention leaves it at the greeter), but TimedLogin auto-
    # logs-in after a short delay at the greeter. So the demo can show a real
    # logout without also having to drive the greeter login.
    vmssh 'sudo tee /etc/gdm3/custom.conf >/dev/null <<EOF
[daemon]
AutomaticLoginEnable=true
AutomaticLogin=e2e
TimedLoginEnable=true
TimedLogin=e2e
TimedLoginDelay=2
EOF'

    log "rebooting once: loads the extension and gives a clean notification stack"
    reboot_and_wait
    vmssh 'busctl --user call org.gnome.ScreenSaver /org/gnome/ScreenSaver \
        org.gnome.ScreenSaver SetActive b false 2>/dev/null || true'
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

    # GNOME parks fresh logins in the Activities overview; leave it (host-side
    # Esc) and park the cursor clear of the windows before the camera rolls.
    gui_esc
    gui_park
    local video; video=$("$RECORD" start "$OUT/trial")
    sleep 0.5

    # The driver (in-guest) opens and places its terminals and types the arc; the
    # host (gui_*) drives the unlock dialog and the notification buttons between
    # its phases. See demo-driver.sh for the phase boundaries.
    if ! { vmssh "bash ~/demo-driver.sh open" \
        && gui_unlock && gui_click Deny \
        && vmssh "bash ~/demo-driver.sh second" \
        && gui_click Approve && verify_secret client \
        && vmssh "bash ~/demo-driver.sh restore"; }; then
        "$RECORD" stop || true
        mv "$video" "${video%.*}-failed.${video##*.}" 2>/dev/null || true
        vmssh 'tmux kill-server 2>/dev/null || true; pkill -x sd-local 2>/dev/null || true' || true
        die "demo driver failed (partial recording kept for post-mortem)"
    fi

    "$RECORD" stop
    vmssh 'tmux kill-server 2>/dev/null || true; pkill -f "gnome-terminal-serve[r]" 2>/dev/null || true'
}

demo_install() {
    log "demo_install: install --start -> status -> RELOGIN -> still in front -> unlock+approve"

    gui_esc
    gui_park
    local video; video=$("$RECORD" start "$OUT/install")
    sleep 0.5

    # Phase 1: the permanent takeover + status, before the relogin.
    if ! vmssh "bash ~/demo-driver-install.sh part1"; then
        "$RECORD" stop || true
        mv "$video" "${video%.*}-failed.${video##*.}" 2>/dev/null || true
        vmssh 'tmux kill-server 2>/dev/null || true
            ~/go/bin/secrets-dispatcher service uninstall 2>/dev/null || true' || true
        die "demo install part1 failed (partial recording kept for post-mortem)"
    fi

    # The permanence proof: a real, mouse-driven logout on camera (so it reads as
    # a deliberate logout, not a crash), then GDM's TimedLogin logs back in. The
    # cursor is driven host-side over QMP through the GNOME menu — quick settings
    # -> power -> Log Out... -> Log Out — because in-guest RemoteDesktop can't
    # open the panel menus and would flash the screen-share indicator. Host-side
    # capture keeps rolling across the whole session cycle; the kernel console is
    # already on serial (grub drop-in in prep), so the handoff shows clean black,
    # not the boot-log console. An autologin desktop won't AutomaticLogin after a
    # logout, so prep enabled TimedLogin to bring it back on its own.
    log "mouse-driven logout (host-side QMP) — GDM's TimedLogin re-logs-in"
    local oldshell
    oldshell=$(vmssh 'pgrep -u e2e -x gnome-shell | head -1' 2>/dev/null || true)
    qmp <<'EOF' || true
glide 1256 15
click
sleep 0.9
glide 1236 76
click
sleep 0.9
glide 946 322
click
sleep 1.3
glide 749 472
click
EOF
    # Wait for TimedLogin to bring up a NEW gnome-shell (a different pid — the old
    # one lingers briefly while the session tears down).
    local i newshell=
    for ((i = 0; i < 60; i++)); do
        newshell=$(vmssh 'pgrep -u e2e -x gnome-shell | head -1' 2>/dev/null || true)
        [[ -n "$newshell" && "$newshell" != "$oldshell" ]] && break
        sleep 1
    done
    "$RUN" wait-desktop
    # Wait for the locator D-Bus to answer (the extension restarts with the shell)
    # before we drive the GUI, else the first wait_locate races a cold locator.
    vmssh 'for i in $(seq 40); do busctl --user call org.gnome.Shell \
        /org/gnome/Shell/SecretsDemoLocator org.gnome.Shell.SecretsDemoLocator \
        Dump >/dev/null 2>&1 && break; sleep 1; done'
    # The new session parks in the overview again; leave it and re-park the cursor.
    gui_esc
    gui_park
    sleep 1

    # Phase 2: still in front, a live unlock + approve. The driver types up to the
    # lookup; the host drives the unlock + APPROVE; part2b narrates the payoff.
    # Leaves the service installed — demo_uninstall records the deliberate reversal.
    if ! { vmssh "bash ~/demo-driver-install.sh part2" \
        && gui_unlock && gui_click Approve && verify_secret client \
        && vmssh "bash ~/demo-driver-install.sh part2b"; }; then
        "$RECORD" stop || true
        mv "$video" "${video%.*}-failed.${video##*.}" 2>/dev/null || true
        vmssh 'tmux kill-server 2>/dev/null || true
            export XDG_RUNTIME_DIR=/run/user/$(id -u)
            export DBUS_SESSION_BUS_ADDRESS=unix:path=$XDG_RUNTIME_DIR/bus
            ~/go/bin/secrets-dispatcher service uninstall 2>/dev/null || true' || true
        die "demo install part2 failed (partial recording kept for post-mortem)"
    fi

    "$RECORD" stop
    vmssh 'tmux kill-server 2>/dev/null || true; pkill -f "gnome-terminal-serve[r]" 2>/dev/null || true'
}

demo_uninstall() {
    log "demo_uninstall: reverse the permanent install — back to stock"
    # Off-camera: ensure the service is installed. It already is when this runs
    # right after demo_install; install from the stashed fixed binary when the
    # demo is run standalone.
    vmssh 'export XDG_RUNTIME_DIR=/run/user/$(id -u)
        export DBUS_SESSION_BUS_ADDRESS=unix:path=$XDG_RUNTIME_DIR/bus
        owner=$(busctl --user call org.freedesktop.DBus /org/freedesktop/DBus \
            org.freedesktop.DBus GetConnectionUnixProcessID s org.freedesktop.secrets 2>/dev/null | sed "s/^u //")
        case "$(readlink /proc/$owner/exe 2>/dev/null)" in
            *secrets-dispatcher*) : ;;
            *) cp ~/secrets-dispatcher-fixed ~/go/bin/secrets-dispatcher 2>/dev/null || true
               ~/go/bin/secrets-dispatcher service install --mode local --start ;;
        esac'

    gui_esc
    gui_park
    local video; video=$("$RECORD" start "$OUT/uninstall")
    sleep 0.5
    # The driver uninstalls and ends by typing a final lookup; the host soft-
    # unlocks the (possibly locked) keyring, then the secret prints on camera —
    # no approval, because the dispatcher that gated it is gone.
    if ! { vmssh "bash ~/demo-driver-install.sh uninstall" \
        && gui_unlock 5 soft; }; then
        "$RECORD" stop || true
        mv "$video" "${video%.*}-failed.${video##*.}" 2>/dev/null || true
        vmssh 'tmux kill-server 2>/dev/null || true' || true
        die "demo uninstall failed (partial recording kept for post-mortem)"
    fi
    sleep 5 # let the secret value print on camera after the (soft) unlock
    "$RECORD" stop
    vmssh 'tmux kill-server 2>/dev/null || true; pkill -f "gnome-terminal-serve[r]" 2>/dev/null || true'
}

# --- driver ---

# report_duration prints a video's duration (or "n/a" — gstreamer's live muxers
# often write no container duration header, and feeding N/A to printf %f would
# error out under set -e).
report_duration() {
    command -v ffprobe >/dev/null || return 0
    local dur; dur=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$1" 2>/dev/null)
    case "$dur" in
        '' | N/A) printf '%s: (duration n/a)\n' "$1" ;;
        *) LC_ALL=C printf '%s: %.0fs\n' "$1" "$dur" ;;
    esac
}

# make_webp builds the inline run-summary preview from a video. It embeds *inline*
# in the summary (an image, served through GitHub's proxy), where an external
# <video> is CSP-blocked. Full colour AND smaller than a 256-colour GIF because
# `img2webp -min_size` does changed-rectangle inter-frame diffing (only the
# changed part of each frame is stored). ffmpeg's own webp muxer can't do that —
# it re-encodes every full frame (~20 MB) — hence the frames -> img2webp two-step.
# Tuned for a small, fast preview (click through to the mp4 for full quality):
# 10 fps (-d 100), 720px wide, q60, -m 4. Terminal text stays legible at 720px.
make_webp() { # <video-in> <webp-out>
    command -v img2webp >/dev/null || return 0
    local fr; fr=$(mktemp -d)
    ffmpeg -y -v error -i "$1" -vf "fps=10,scale=720:-1:flags=lanczos" "$fr/f-%05d.png"
    img2webp -min_size -lossy -q 60 -m 4 -d 100 "$fr"/f-*.png -o "$2" >/dev/null 2>&1
    rm -rf "$fr"
}

finish() {
    local f
    # WebM originals (software / CI path): transcode to mp4 for the click-through
    # player, then build the webp. -crf 30 + a pinned 30 fps CFR keep the mp4
    # small: the source is variable-rate (undefined avg frame rate), which without
    # -r lets some ffmpeg builds duplicate frames and balloon the bitrate.
    for f in "$OUT"/*.webm; do
        [[ -e "$f" ]] || continue
        report_duration "$f"
        command -v ffmpeg >/dev/null || continue
        ffmpeg -y -v error -i "$f" -c:v libx264 -pix_fmt yuv420p \
            -crf 30 -preset veryfast -r 30 -movflags +faststart "${f%.webm}.mp4"
        make_webp "$f" "${f%.webm}.webp"
    done
    # MP4 originals (hardware / local path): the fragmented capture mux isn't
    # reliably seekable and runs ~6 Mbps. Re-encode it to a properly-indexed
    # faststart mp4 with libx264 -crf — seekable, and a fraction of the size (the
    # same encode the CI path produces from webm, so both paths yield one format).
    # A software re-encode is fine here: it's offline, not the real-time capture.
    # Skip any mp4 finish() itself produced from a webm above (same basename).
    for f in "$OUT"/*.mp4; do
        [[ -e "$f" ]] || continue
        [[ -e "${f%.mp4}.webm" ]] && continue
        report_duration "$f"
        command -v ffmpeg >/dev/null || continue
        if ffmpeg -y -v error -i "$f" -c:v libx264 -pix_fmt yuv420p -crf 30 \
            -preset veryfast -r 30 -movflags +faststart "$f.reenc.mp4"; then
            mv "$f.reenc.mp4" "$f"
        fi
        make_webp "$f" "${f%.mp4}.webp"
    done
}

main() {
    mkdir -p "$OUT"
    # Drop artifacts from a previous run: a stale *-failed.webm would otherwise
    # linger and get re-encoded to .mp4 by finish(), making a good run look
    # failed; and a stale .webp (finish keeps webp, not just webm/mp4) would
    # orphan a demo that didn't record this run.
    rm -f "$OUT"/*.webm "$OUT"/*.mp4 "$OUT"/*.webp
    # Forget any cursor position a previous run left on the host, so the first
    # gui_park starts from a known state (qmpinput persists it host-side).
    rm -f ~/.cache/secrets-dispatcher-qmpcursor
    local demos=("$@")
    # demo_uninstall must follow demo_install (it reverses install's end-state,
    # or reinstalls off-camera if run standalone).
    ((${#demos[@]})) || demos=(demo_trial demo_install demo_uninstall)
    prep_common
    local d
    for d in "${demos[@]}"; do "$d"; done
    finish
    log "PASS: ${demos[*]} recorded to $OUT"
}

main "$@"
