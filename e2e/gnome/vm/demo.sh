#!/usr/bin/env bash
# Screen-recorded product demos in the Tier-2 GNOME VM — same harness, same
# cached desktop base as scenario.sh, but instead of asserting acceptance
# gates it stages the VM off-camera and then drives a visible
# gnome-terminal/tmux session at human typing pace while record.sh captures
# the screen (GNOME Shell screencast, VP8/WebM at the monitor's 1280x800).
# Mouse clicks (notification Approve/Deny buttons) and the odd keypress are
# injected at the virtual-hardware level over QEMU's QMP socket — the only
# input path into a headless Wayland session.
#
# Each demo_* function is one video. demo_trial is the install/try arc:
#   go install -> try --dry-run -> try (live takeover) -> secret-tool lookup
#   DENIED via a click on the desktop notification -> second lookup APPROVED
#   the same way (secret prints) -> Ctrl-C restores stock -> status.
#
# Videos are throwaway build artifacts, never committed: locally they land in
# the output dir (Makefile: .build/demos), in CI demos.yml uploads them as
# workflow artifacts.
#
# Usage: demo.sh <output-dir> [demo...]     (default: every demo_* function)
# The VM must be booted with the desktop up (run.sh boot && run.sh wait-desktop).
#
# GO_REF picks the module version the on-camera `go install` fetches.
# Default is master until a release that includes `try` is cut — then flip
# to latest.
#
# shellcheck disable=SC2016  # single-quoted $… is deliberate: remote scripts,
# expanded by the VM's shell, not this one.
set -euo pipefail

OUT=$(realpath "${1:?usage: demo.sh <output-dir> [demo...]}")
shift
GO_REF=${GO_REF:-master}
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
RUN=$SCRIPT_DIR/run.sh
RECORD=$SCRIPT_DIR/record.sh

# The virtual monitor gnome-shell renders to; QMP abs pointer coordinates
# are scaled against this.
SCREEN_W=1280
SCREEN_H=800

log() { printf '\n=== %s\n' "$*"; }

# Host-side input injection via QEMU's QMP socket (headless Wayland has no
# other input path). Subcommands:
#   key <qcode>          press+release one key (e.g. esc)
#   click <x> <y>        glide the pointer there, then left-click
#   move <x> <y>         glide the pointer there
qmp_input() {
    local vm_dir=${VM_DIR:-${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}/instance-${UBUNTU_SERIES:-noble}}
    python3 - "$vm_dir/qmp.sock" "$SCREEN_W" "$SCREEN_H" "$@" <<'EOF'
import json, socket, sys, time

sock_path, w, h, cmd = sys.argv[1:5]
w, h = int(w), int(h)

s = socket.socket(socket.AF_UNIX)
s.connect(sock_path)
f = s.makefile("rw")
f.readline()  # QMP greeting

def qmp(c):
    f.write(json.dumps(c) + "\n")
    f.flush()
    while True:
        r = json.loads(f.readline())
        if "error" in r:
            sys.exit(f"QMP error: {r}")
        if "return" in r:
            return r

def send(events):
    qmp({"execute": "input-send-event", "arguments": {"events": events}})

def abs_xy(x, y):
    return [
        {"type": "abs", "data": {"axis": "x", "value": x * 32767 // w}},
        {"type": "abs", "data": {"axis": "y", "value": y * 32767 // h}},
    ]

def glide(x, y, steps=12):
    # The tablet reports absolute positions, so a straight jump would
    # teleport the cursor; interpolating reads as human mouse motion.
    # Start from the bottom-right resting spot — good enough visually
    # and keeps the helper stateless.
    x0, y0 = w - 120, h - 70
    for i in range(1, steps + 1):
        send(abs_xy(x0 + (x - x0) * i // steps, y0 + (y - y0) * i // steps))
        time.sleep(0.03)

qmp({"execute": "qmp_capabilities"})
if cmd == "key":
    qmp({"execute": "send-key",
         "arguments": {"keys": [{"type": "qcode", "data": sys.argv[5]}]}})
elif cmd == "move":
    glide(int(sys.argv[5]), int(sys.argv[6]))
elif cmd == "click":
    glide(int(sys.argv[5]), int(sys.argv[6]))
    time.sleep(0.15)
    send([{"type": "btn", "data": {"down": True, "button": "left"}}])
    time.sleep(0.08)
    send([{"type": "btn", "data": {"down": False, "button": "left"}}])
else:
    sys.exit(f"unknown qmp_input command: {cmd}")
EOF
}

vmssh() {
    "$RUN" ssh "
        set -euo pipefail
        export XDG_RUNTIME_DIR=/run/user/\$(id -u)
        export DBUS_SESSION_BUS_ADDRESS=unix:path=\$XDG_RUNTIME_DIR/bus
        $*"
}

# --- off-camera staging ---

prep_common() {
    log "staging the VM off-camera"

    # The stage look: dark theme, Ubuntu wallpaper, Yaru-ish dark terminal
    # colors, no screen blanking, no sudo-hint MOTD in fresh shells. Pushed
    # as a file — the terminal palette is quoting soup inline. The [r] keeps
    # pkill -f from matching (and killing) the ssh session whose own command
    # line contains the pattern.
    vmssh "cat > /tmp/demo-stage.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
gsettings set org.gnome.desktop.session idle-delay 0
gsettings set org.gnome.desktop.interface color-scheme prefer-dark
gsettings set org.gnome.desktop.interface monospace-font-name 'Monospace 12'
wp=$(ls /usr/share/backgrounds/warty-final-ubuntu.png /usr/share/backgrounds/*.png \
    /usr/share/backgrounds/*.jpg 2>/dev/null | head -1)
if [[ -n "$wp" ]]; then
    gsettings set org.gnome.desktop.background picture-uri "file://$wp"
    gsettings set org.gnome.desktop.background picture-uri-dark "file://$wp"
fi
# Yaru dark terminal colors (the minimal GNOME stack has no Yaru, and the
# stock profile follows the light GTK theme).
p=$(gsettings get org.gnome.Terminal.ProfilesList default | tr -d "'")
base="org.gnome.Terminal.Legacy.Profile:/org/gnome/terminal/legacy/profiles:/:$p/"
gsettings set "$base" use-theme-colors false
gsettings set "$base" background-color '#171421'
gsettings set "$base" foreground-color '#D0CFCC'
gsettings set "$base" palette "['#171421', '#C01C28', '#26A269', '#A2734C', '#12488B', '#A347BA', '#2AA1B3', '#D0CFCC', '#5E5C64', '#F66151', '#33DA7A', '#E9AD0C', '#2A7BDE', '#C061CB', '#33C7DE', '#FFFFFF']"
touch ~/.sudo_as_admin_successful
tmux kill-server 2>/dev/null || true
pkill -f 'gnome-terminal-serve[r]' 2>/dev/null || true
EOF
    vmssh 'bash /tmp/demo-stage.sh'

    # Notifications ON and no auto-approve rules: requests must visibly wait
    # for a click on the notification — that is the product moment the demo
    # exists to show.
    vmssh '
        mkdir -p ~/.config/secrets-dispatcher
        cat > ~/.config/secrets-dispatcher/config.yaml <<EOF
serve:
  notifications: true
EOF'

    # Seed a secret into a BLANK-password login keyring (the standard
    # autologin setup: gnome-keyring auto-unlocks it on every daemon start,
    # including the dispatcher's private backend — so the on-camera
    # `secret-tool lookup` needs no unlock prompt). Same single-shot
    # --unlock dance as scenario.sh leg_baseline, with an empty password.
    if vmssh 'timeout 10 secret-tool lookup service demo 2>/dev/null | grep -q .'; then
        echo "   demo secret already seeded"
    else
        vmssh "
            systemctl --user stop gnome-keyring-daemon.service gnome-keyring-daemon.socket
            printf '\n' | gnome-keyring-daemon --unlock --components=secrets >/dev/null
            sleep 1
            printf 'hunter2-classic' | timeout 10 secret-tool store --label='Demo secret' service demo
            pkill -f '^gnome-keyring-daemon' || true
            sleep 1
            systemctl --user start gnome-keyring-daemon.socket gnome-keyring-daemon.service
            sleep 2
            [[ \$(timeout 10 secret-tool lookup service demo) == hunter2-classic ]]"
    fi

    # Warm the Go side so the on-camera `go install` takes seconds, not
    # minutes: module graph and build cache happen here; only the binary is
    # removed again. Go itself is /usr/local/go (user-data) — login shells
    # get it from /etc/profile.d, this non-login ssh needs the explicit path.
    log "warming go module/build caches (first time: several minutes)"
    vmssh "export PATH=\$PATH:/usr/local/go/bin
        timeout 900 go install github.com/nikicat/secrets-dispatcher@$GO_REF
        rm -f ~/go/bin/secrets-dispatcher"
}

# The vp8 encoder runs inside the VM and shares its CPUs with everything
# else. Right after a fresh boot (login burst, apt timers, indexing) or the
# go warm-up build, it gets starved and the capture degrades into
# macroblock garbage — silently: the file stays decodable. Let the load
# settle before rolling camera.
wait_load_settled() {
    log "waiting for VM load to settle before recording"
    vmssh 'for i in $(seq 120); do
        awk "{exit (\$1 < 1.0) ? 0 : 1}" /proc/loadavg && exit 0
        sleep 5
    done
    echo "error: VM load never settled below 1.0" >&2
    cat /proc/loadavg >&2
    exit 1'
}

# --- demos ---

demo_trial() {
    log "demo_trial: install -> try -> deny/approve via notification -> Ctrl-C restore"
    wait_load_settled

    vmssh "cat > /tmp/demo-driver.sh" <<'EOF'
#!/usr/bin/env bash
# Runs INSIDE the VM, one act per invocation (the host orchestrates acts and
# interleaves QMP mouse clicks, which only exist host-side). Types into the
# visible tmux session at human pace.
set -euo pipefail
S=demo
GO_REF=${GO_REF:-master}
export PATH="$HOME/go/bin:$PATH"

type_cmd() { # type_cmd <pane> <command line>
    local pane=$1 i
    shift
    local s=$*
    for ((i = 0; i < ${#s}; i++)); do
        tmux send-keys -t "$S:0.$pane" -l -- "${s:i:1}"
        sleep 0.045
    done
    sleep 0.5
    tmux send-keys -t "$S:0.$pane" Enter
}

wait_for() { # wait_for <max-seconds> <command...>
    local max=$1 i
    shift
    for ((i = 0; i < max; i++)); do
        "$@" &>/dev/null && return 0
        sleep 1
    done
    echo "demo-driver: timed out waiting for: $*" >&2
    return 1
}

owner_is() {
    local pid
    pid=$(busctl --user call org.freedesktop.DBus /org/freedesktop/DBus \
        org.freedesktop.DBus GetConnectionUnixProcessID s org.freedesktop.secrets \
        | sed 's/^u //') || return 1
    [[ $(readlink "/proc/$pid/exe") == *"$1"* ]]
}

act_install() {
    sleep 2
    type_cmd 0 "go install github.com/nikicat/secrets-dispatcher@$GO_REF"
    wait_for 180 test -x "$HOME/go/bin/secrets-dispatcher"
    sleep 1
    type_cmd 0 'export PATH="$HOME/go/bin:$PATH"'
    sleep 1
}

act_try() {
    type_cmd 0 "secrets-dispatcher try --dry-run"
    sleep 5
    type_cmd 0 "secrets-dispatcher try"
    wait_for 30 owner_is secrets-dispatcher
    sleep 3
    tmux split-window -v -t "$S:0" -l 40%
    sleep 1.5
}

act_lookup() {
    type_cmd 1 "secret-tool lookup service demo"
    sleep 1 # the approval notification pops; the host clicks it
}

act_interrupt() {
    tmux select-pane -t "$S:0.0"
    sleep 1.5
    tmux send-keys -t "$S:0.0" C-c
    wait_for 30 owner_is gnome-keyring-daemon
    sleep 2
    type_cmd 0 "secrets-dispatcher service status"
    sleep 5
}

"$@"
EOF

    # Recording covers the terminal window opening (Esc first: GNOME parks
    # fresh logins in the Activities overview, and a window launched over
    # SSH carries no activation token to leave it).
    qmp_input key esc
    sleep 1
    "$RECORD" start /home/e2e/demo-trial.webm
    sleep 2

    # The stage: a large-but-windowed terminal (the desktop staying visible
    # around it is part of the show), attached to a pre-created tmux session
    # so the driver can address panes. -e puts ~/go/bin on PATH in every
    # pane; pane 0 still types the export on camera because that IS the
    # documented install step.
    vmssh 'tmux kill-session -t demo 2>/dev/null || true
        tmux new-session -d -s demo -x 108 -y 27 -e PATH="$HOME/go/bin:$PATH"
        tmux set-option -t demo status off
        setsid gnome-terminal --geometry=108x27 -- tmux attach -t demo >/dev/null 2>&1 &
        sleep 1'
    vmssh 'for i in $(seq 15); do tmux list-clients -t demo 2>/dev/null | grep -q . && exit 0; sleep 1; done
        echo "gnome-terminal never attached to tmux" >&2; exit 1'
    sleep 1

    driver() { vmssh "GO_REF='$GO_REF' bash /tmp/demo-driver.sh $*"; }
    fail() {
        "$RECORD" stop /home/e2e/demo-trial.webm "$OUT/trial-failed.webm" || true
        echo "error: demo failed at: $* (partial recording kept for post-mortem)" >&2
        return 1
    }

    driver act_install || { fail act_install; return; }
    driver act_try || { fail act_try; return; }

    # Beat 1: DENIED. The notification banner is top-center; button row
    # coordinates measured from a 1280x800 capture (stable per GNOME
    # version): Approve ~x=473, Deny ~x=818, row y~155.
    driver act_lookup || { fail act_lookup; return; }
    sleep 2.5
    qmp_input click 818 155 || { fail "click deny"; return; }
    sleep 3

    # Beat 2: APPROVED — the secret prints this time.
    driver act_lookup || { fail act_lookup; return; }
    sleep 2.5
    qmp_input click 473 155 || { fail "click approve"; return; }
    sleep 3

    driver act_interrupt || { fail act_interrupt; return; }

    "$RECORD" stop /home/e2e/demo-trial.webm "$OUT/trial.webm"
    vmssh 'tmux kill-server 2>/dev/null || true; pkill -f "gnome-terminal-serve[r]" 2>/dev/null || true'
}

# --- driver ---

finish() {
    local f
    for f in "$OUT"/*.webm; do
        [[ -e "$f" ]] || continue
        if command -v ffprobe >/dev/null; then
            local dur
            dur=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$f")
            LC_ALL=C printf '%s: %.0fs\n' "$f" "$dur"
        else
            ls -la "$f"
        fi
        if command -v ffmpeg >/dev/null; then
            ffmpeg -y -v error -i "$f" -c:v libx264 -pix_fmt yuv420p \
                -movflags +faststart "${f%.webm}.mp4"
        fi
    done
}

main() {
    mkdir -p "$OUT"
    local demos=("$@")
    if ((${#demos[@]} == 0)); then
        mapfile -t demos < <(declare -F | awk '$3 ~ /^demo_/ {print $3}')
    fi
    prep_common
    local d
    for d in "${demos[@]}"; do "$d"; done
    finish
    log "PASS: ${demos[*]} recorded to $OUT"
}

main "$@"
