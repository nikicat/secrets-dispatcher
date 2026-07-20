#!/usr/bin/env bash
# Host-side screen recording for the Tier-2 GNOME VM. QEMU exposes the guest
# framebuffer over VNC (run.sh boots with `-vnc 127.0.0.1:590$VNC_DISP` when
# VNC_DISP is set); gstreamer's rfbsrc reads it back on the host and encodes it.
#
# Why host-side and not the in-guest screencast: the screencast service is part
# of the GNOME session, so it dies the moment gnome-shell restarts. Capturing
# the framebuffer *below* the session (at QEMU/VNC) lets one continuous clip
# span a relogin/reboot — the whole point of the permanent-install demo. It also
# moves encoding off the guest (no more encoder starving the demo it records)
# and needs no in-guest holder process.
#
# Encoder — two paths, chosen by RECORD_HW + what the host actually has:
#   - Hardware (RECORD_HW=1, a render node, and gstreamer's vah264enc present):
#     VA-API H.264 into MP4 at 30 fps. Used locally (the Makefile sets RECORD_HW)
#     where a GPU makes the encoder cheap enough to keep up with the capture, so
#     the recording is genuinely smooth rather than software-encode-bottlenecked.
#   - Software (default / CI): VP8 into WebM at a lower fps. CI runners have no
#     GPU, so this stays deliberately modest; it's the fallback everywhere the
#     hardware path isn't available.
# The output extension follows the encoder (.mp4 vs .webm); `start` prints the
# final path on stdout so demo.sh can find it. demo.sh's finish() turns whichever
# it gets into the mp4 + webp preview.
#
# Two caveats the demo path must satisfy (see host-side-capture spike):
#   - Resolution: run.sh pins the guest to 1280x800 via EDID.
#   - Cursor: VNC sends the pointer out-of-band (RFB cursor pseudo-encoding),
#     which rfbsrc drops. demo.sh forces a *software* cursor
#     (MUTTER_DEBUG_DISABLE_HW_CURSORS=1) so the pointer composites into the
#     framebuffer and is captured.
#
# Usage:
#   record.sh start <host-path-without-ext>   begin capturing; prints final path
#   record.sh stop                            end the current capture (file final)
#
# Env: VNC_DISP (required — the display number run.sh booted with); RECORD_HW=1
# to request the hardware path; UBUNTU_SERIES for per-series state isolation.
set -euo pipefail

VNC_DISP=${VNC_DISP:?record.sh needs VNC_DISP (the display run.sh booted -vnc with)}
VNC_PORT=$((5900 + VNC_DISP))
UBUNTU_SERIES=${UBUNTU_SERIES:-noble}
RECORD_HW=${RECORD_HW:-}
# Per-display state (pid + log) so concurrent series don't collide.
STATE_DIR=${XDG_RUNTIME_DIR:-/tmp}/sd-record-$UBUNTU_SERIES-$VNC_DISP
PIDFILE=$STATE_DIR/gst.pid
LOGFILE=$STATE_DIR/gst.log

have() { gst-inspect-1.0 "$1" >/dev/null 2>&1; }

# hw_available: RECORD_HW requested AND a DRM render node AND the VA-API H.264
# encoder + the MP4 mux path are all present in gstreamer.
hw_available() {
    [[ -n "$RECORD_HW" ]] || return 1
    [[ -e /dev/dri/renderD128 ]] || return 1
    have vah264enc && have vapostproc && have h264parse && have mp4mux
}

check_tools() {
    command -v gst-launch-1.0 >/dev/null || {
        echo "error: gst-launch-1.0 not found (install gstreamer1.0-tools)" >&2
        exit 1
    }
    have rfbsrc || {
        echo "error: gstreamer element 'rfbsrc' missing (apt install gstreamer1.0-plugins-bad)" >&2
        exit 1
    }
    local el
    for el in vp8enc webmmux; do
        have "$el" || {
            echo "error: gstreamer element '$el' missing" >&2
            echo "hint: apt install gstreamer1.0-plugins-{base,good,bad}" >&2
            exit 1
        }
    done
}

cmd_start() {
    local base=$1 ext out fps
    check_tools
    mkdir -p "$STATE_DIR" "$(dirname "$base")"

    # Source: incremental=false so rfbsrc requests full framebuffers and frames
    # keep flowing even while the screen is static (rfbsrc's incremental mode can
    # stall and produce an empty capture); videorate then normalizes to a steady
    # CFR. The smoothness now comes from the hardware encoder keeping up at 30
    # fps, not from incremental updates. Built as an array so the '!' pad
    # separators stay distinct args.
    local -a src enc
    src=(rfbsrc host=127.0.0.1 port="$VNC_PORT" incremental=false ! videoconvert ! videorate)
    if hw_available; then
        ext=mp4; fps=30
        # VA-API H.264 (Intel iHD here). target-usage 4 balances speed/quality;
        # 6 Mbps is ample for 1280x800 terminal text. Fragmented MP4
        # (fragment-duration) writes self-contained moof+mdat fragments as it
        # goes, so the file is always playable up to the last flushed fragment —
        # even if the stop isn't a perfectly clean EOS. (A plain faststart mux
        # only writes the whole file at EOS, so any imperfect stop leaves an
        # empty stub — which is exactly what starved the first capture.)
        enc=("video/x-raw,framerate=$fps/1" ! vapostproc
            ! vah264enc target-usage=4 bitrate=6000 ! h264parse
            ! mp4mux fragment-duration=1000 ! filesink location="$base.mp4")
        echo "recording (hardware VA-API H.264 @ ${fps}fps) -> $base.$ext" >&2
    else
        ext=webm; fps=${RECORD_FPS:-15}
        enc=("video/x-raw,framerate=$fps/1"
            ! vp8enc deadline=1 cpu-used=8 target-bitrate=2000000
            ! webmmux ! filesink location="$base.webm")
        echo "recording (software VP8 @ ${fps}fps) -> $base.$ext" >&2
    fi
    out=$base.$ext
    echo "$out" >"$STATE_DIR/out"

    # Wait for QEMU's VNC server to accept connections before launching gst.
    local i
    for ((i = 0; i < 40; i++)); do
        (exec 3<>"/dev/tcp/127.0.0.1/$VNC_PORT") 2>/dev/null && { exec 3>&- 3<&-; break; }
        sleep 0.25
    done

    # -e => a clean EOS on SIGINT so the muxer finalizes the file; nohup so it
    # outlives this short `start` invocation.
    nohup gst-launch-1.0 -e "${src[@]}" ! "${enc[@]}" \
        >"$LOGFILE" 2>&1 &
    echo $! >"$PIDFILE"

    # Confirm it connected and is still running. Don't gate on file size: a
    # static screen may not have flushed a frame to disk yet, and the muxer only
    # writes its header once frames flow.
    sleep 2
    if ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "error: gstreamer capture exited immediately (VNC 127.0.0.1:$VNC_PORT reachable?)" >&2
        sed 's/^/  gst: /' "$LOGFILE" >&2 || true
        return 1
    fi
    # The final path on stdout (the only stdout line) for demo.sh to capture.
    echo "$out"
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
        echo "warning: gstreamer did not stop on SIGINT, killing (file may be truncated)" >&2
        kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$PIDFILE"
    if [[ -n "$out" && ! -s "$out" ]]; then
        echo "warning: recording $out is empty (no frames captured)" >&2
    fi
    echo "recording stopped" >&2
}

case "${1:-}" in
start) cmd_start "${2:?usage: record.sh start <host-path-without-ext>}" ;;
stop) cmd_stop ;;
*)
    echo "usage: $0 start <host-path-without-ext> | stop" >&2
    exit 2
    ;;
esac
