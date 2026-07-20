#!/usr/bin/env bash
# Screen recording for the Tier-2 GNOME VM via GNOME Shell's own screencast
# service (org.gnome.Shell.Screencast — a D-Bus-activated gjs process, baked
# into the desktop base by user-data). Mutter composites the headless
# virtio-vga monitor like any real display, so the capture is the true
# GNOME session: VP8/WebM at the monitor's native 1280x800, cursor included.
#
# The screencast service stops recording as soon as the D-Bus connection
# that started it drops, so a one-shot `busctl call` cannot drive it:
# `start` leaves a small python (GLib mainloop) holder process running in
# the VM to keep the recording alive; `stop` SIGTERMs the holder — which
# calls StopScreencast and exits — then copies the finished webm out.
#
# Usage:
#   record.sh start <vm-webm-path>              begin recording in the VM
#   record.sh stop <vm-webm-path> <host-dest>   end recording, fetch the file
#
# Uses the same env knobs as run.sh (UBUNTU_SERIES, SSH_PORT, CACHE_DIR).
set -euo pipefail

SSH_PORT=${SSH_PORT:-2222}
CACHE_DIR=${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
RUN=$SCRIPT_DIR/run.sh

# Session-bus env for the autologin e2e user, like scenario.sh's vmssh.
vmssh() {
    "$RUN" ssh "
        set -euo pipefail
        export XDG_RUNTIME_DIR=/run/user/\$(id -u)
        export DBUS_SESSION_BUS_ADDRESS=unix:path=\$XDG_RUNTIME_DIR/bus
        $*"
}

cmd_start() {
    local out=$1
    vmssh "cat > /tmp/recorder.py" <<'EOF'
import signal
import sys

from gi.repository import Gio, GLib

DEST = "org.gnome.Shell.Screencast"
PATH = "/org/gnome/Shell/Screencast"
IFACE = "org.gnome.Shell.Screencast"

bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
ok, used = bus.call_sync(
    DEST, PATH, IFACE, "Screencast",
    GLib.Variant("(sa{sv})", (sys.argv[1], {
        "draw-cursor": GLib.Variant("b", True),
        # 15 fps is plenty for a terminal demo and halves the vp8enc load —
        # the encoder shares the VM's CPUs with what's being demoed, and
        # starving it corrupts the capture (silently: the bitstream stays
        # decodable, the pixels turn to garbage).
        "framerate": GLib.Variant("i", 15),
    })),
    None, Gio.DBusCallFlags.NONE, -1, None,
).unpack()
print("started", ok, used, flush=True)
if not ok:
    sys.exit(1)

loop = GLib.MainLoop()
for sig in (signal.SIGTERM, signal.SIGINT):
    GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, sig,
                         lambda *_: (loop.quit(), GLib.SOURCE_REMOVE)[1])
loop.run()
ok, = bus.call_sync(DEST, PATH, IFACE, "StopScreencast",
                    None, None, Gio.DBusCallFlags.NONE, -1, None).unpack()
print("stopped", ok, flush=True)
EOF
    vmssh "rm -f /tmp/recorder.log
        nohup python3 /tmp/recorder.py '$out' >/tmp/recorder.log 2>&1 &
        echo \$! >/tmp/recorder.pid"
    local i
    for ((i = 0; i < 15; i++)); do
        if vmssh "grep -q '^started True' /tmp/recorder.log" 2>/dev/null; then
            echo "recording -> $out"
            return 0
        fi
        if vmssh "! kill -0 \$(cat /tmp/recorder.pid)" 2>/dev/null; then
            break
        fi
        sleep 1
    done
    echo "error: screencast did not start" >&2
    vmssh "cat /tmp/recorder.log" >&2 || true
    return 1
}

cmd_stop() {
    local out=$1 dest=$2 i
    vmssh "kill -TERM \$(cat /tmp/recorder.pid)"
    for ((i = 0; i < 15; i++)); do
        vmssh "! kill -0 \$(cat /tmp/recorder.pid)" 2>/dev/null && break
        sleep 1
    done
    if ! vmssh "grep -q '^stopped' /tmp/recorder.log"; then
        echo "error: recorder did not stop cleanly" >&2
        vmssh "cat /tmp/recorder.log" >&2 || true
        return 1
    fi
    mkdir -p "$(dirname "$dest")"
    scp -P "$SSH_PORT" -i "$CACHE_DIR/id_ed25519" \
        -o IdentitiesOnly=yes -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
        "e2e@127.0.0.1:$out" "$dest"
    echo "saved $dest"
}

case "${1:-}" in
start) cmd_start "${2:?usage: record.sh start <vm-webm-path>}" ;;
stop) cmd_stop "${2:?vm-webm-path}" "${3:?usage: record.sh stop <vm-webm-path> <host-dest>}" ;;
*)
    echo "usage: $0 start <vm-webm-path> | stop <vm-webm-path> <host-dest>" >&2
    exit 2
    ;;
esac
