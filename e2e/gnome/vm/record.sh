#!/usr/bin/env bash
# Host-side screen recording for the Tier-2 GNOME VM. QEMU exposes the guest
# framebuffer over VNC (run.sh boots with `-vnc 127.0.0.1:590$VNC_DISP` when
# VNC_DISP is set); gstreamer's rfbsrc reads it back on the host and encodes
# VP8/WebM — the same format the in-guest GNOME screencast used to produce, so
# demo.sh's finish() (webm -> mp4/webp) is unchanged.
#
# Why host-side and not the in-guest screencast: the screencast service is part
# of the GNOME session, so it dies the moment gnome-shell restarts. Capturing
# the framebuffer *below* the session (at QEMU/VNC) lets one continuous clip
# span a relogin/reboot — the whole point of the permanent-install demo. It also
# moves vp8 encoding off the guest (no more encoder starving the demo it records)
# and needs no in-guest holder process.
#
# Two caveats the demo path must satisfy (see host-side-capture spike):
#   - Resolution: run.sh pins the guest to 1280x800 via EDID (the capture size
#     below is fixed and must match).
#   - Cursor: VNC sends the pointer out-of-band (RFB cursor pseudo-encoding),
#     which rfbsrc drops. demo.sh forces a *software* cursor
#     (MUTTER_DEBUG_DISABLE_HW_CURSORS=1) so the pointer composites into the
#     framebuffer and is captured.
#
# Usage:
#   record.sh start <host-webm-path>   begin capturing to that host file
#   record.sh stop                     end the current capture (file is final)
#
# Env: VNC_DISP (required — the display number run.sh booted with), plus the
# usual UBUNTU_SERIES for per-series state isolation.
set -euo pipefail

VNC_DISP=${VNC_DISP:?record.sh needs VNC_DISP (the display run.sh booted -vnc with)}
VNC_PORT=$((5900 + VNC_DISP))
UBUNTU_SERIES=${UBUNTU_SERIES:-noble}
# Per-display state (pid + log) so concurrent series don't collide.
STATE_DIR=${XDG_RUNTIME_DIR:-/tmp}/sd-record-$UBUNTU_SERIES-$VNC_DISP
PIDFILE=$STATE_DIR/gst.pid
LOGFILE=$STATE_DIR/gst.log

check_tools() {
    command -v gst-launch-1.0 >/dev/null || {
        echo "error: gst-launch-1.0 not found (install gstreamer1.0-tools)" >&2
        exit 1
    }
    local el
    for el in rfbsrc vp8enc webmmux; do
        gst-inspect-1.0 "$el" >/dev/null 2>&1 || {
            echo "error: gstreamer element '$el' missing" >&2
            echo "hint: apt install gstreamer1.0-plugins-{base,good,bad}" >&2
            exit 1
        }
    done
}

cmd_start() {
    local out=$1
    check_tools
    mkdir -p "$STATE_DIR" "$(dirname "$out")"
    echo "$out" >"$STATE_DIR/out"

    # Wait for QEMU's VNC server to accept connections before launching gst.
    local i
    for ((i = 0; i < 40; i++)); do
        (exec 3<>"/dev/tcp/127.0.0.1/$VNC_PORT") 2>/dev/null && { exec 3>&- 3<&-; break; }
        sleep 0.25
    done

    # incremental=false: request full framebuffers, so frames keep flowing even
    # while the screen is static (during a typed command's pauses or the relogin
    # blank) — videorate then normalizes to a steady 15 fps and vp8 dedupes the
    # unchanged frames to almost nothing. -e => a clean EOS on SIGINT so webmmux
    # finalizes the file. nohup so it outlives this short `start` invocation.
    nohup gst-launch-1.0 -e \
        rfbsrc host=127.0.0.1 port="$VNC_PORT" incremental=false \
        ! videoconvert ! videorate ! video/x-raw,framerate=15/1 \
        ! vp8enc deadline=1 cpu-used=4 target-bitrate=1500000 \
        ! webmmux ! filesink location="$out" \
        >"$LOGFILE" 2>&1 &
    echo $! >"$PIDFILE"

    # Confirm it connected and is still running. Don't gate on file size: a
    # static screen may not have flushed a frame to disk yet, and webmmux only
    # writes its header once frames flow.
    sleep 2
    if ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "error: gstreamer capture exited immediately (VNC 127.0.0.1:$VNC_PORT reachable?)" >&2
        sed 's/^/  gst: /' "$LOGFILE" >&2 || true
        return 1
    fi
    echo "recording -> $out"
}

cmd_stop() {
    [[ -f "$PIDFILE" ]] || { echo "error: no active recording (no $PIDFILE)" >&2; return 1; }
    local pid out
    pid=$(cat "$PIDFILE")
    out=$(cat "$STATE_DIR/out" 2>/dev/null || true)
    kill -INT "$pid" 2>/dev/null || true
    local i
    for ((i = 0; i < 40; i++)); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.25
    done
    if kill -0 "$pid" 2>/dev/null; then
        echo "warning: gstreamer did not stop on SIGINT, killing (webm may be truncated)" >&2
        kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$PIDFILE"
    if [[ -n "$out" && ! -s "$out" ]]; then
        echo "warning: recording $out is empty (no frames captured)" >&2
    fi
    echo "recording stopped"
}

case "${1:-}" in
start) cmd_start "${2:?usage: record.sh start <host-webm-path>}" ;;
stop) cmd_stop ;;
*)
    echo "usage: $0 start <host-webm-path> | stop" >&2
    exit 2
    ;;
esac
