#!/usr/bin/env python3
"""Host-side pointer control for the Tier-2 demo VM via QEMU's QMP
input-send-event (the guest's virtio-tablet, absolute positioning).

Runs on the HOST (unlike rd_agent, which runs in the guest over RemoteDesktop).
Two reasons the demo uses this for the logout:
  - RemoteDesktop can't drive the top-panel/quick-settings menus (injected clicks
    there don't open them), and each rd_agent call is its own session, so an
    opened menu dismisses before the next click — the host-side tablet has
    neither problem.
  - No RemoteDesktop/ScreenCast session means no "screen is being shared"
    indicator on camera.

The guest must run with MUTTER_DEBUG_DISABLE_HW_CURSORS=1 (demo.sh sets it) so the
software cursor composites into the framebuffer VNC captures — otherwise the
moving pointer wouldn't be recorded.

Reads a command script on stdin, one per line:
  glide X Y   ease the pointer to guest pixel (X, Y) with a visible, moving cursor
  move  X Y   jump the pointer to (X, Y)
  click       left press + release at the current position
  sleep S     wait S seconds (let a menu open/animate before the next click)
Coordinates are guest pixels in a WIDTHxHEIGHT screen (default 1280x800),
mapped to QMP's 0..32767 absolute axis range. The pointer position is persisted
between invocations (like rd_agent) so glides continue from where they stopped.

Usage: qmpinput.py <qmp.sock> [WIDTH HEIGHT]   # script on stdin
"""
import json
import math
import os
import socket
import sys
import time

SOCK = sys.argv[1]
W = int(sys.argv[2]) if len(sys.argv) > 2 else 1280
H = int(sys.argv[3]) if len(sys.argv) > 3 else 800
STATE = os.path.expanduser("~/.cache/secrets-dispatcher-qmpcursor")
PARKED = (W - 110, H - 60)  # bottom-right, matches rd_agent's resting corner


def load_pos():
    try:
        with open(STATE) as f:
            x, y = f.read().split()
            return float(x), float(y)
    except (OSError, ValueError):
        return float(PARKED[0]), float(PARKED[1])


def save_pos(x, y):
    try:
        with open(STATE, "w") as f:
            f.write(f"{x} {y}")
    except OSError:
        pass


class QMP:
    def __init__(self, path):
        self.s = socket.socket(socket.AF_UNIX)
        self.s.connect(path)
        self.f = self.s.makefile("rw")
        self.f.readline()  # greeting
        self._cmd({"execute": "qmp_capabilities"})

    def _cmd(self, obj):
        self.f.write(json.dumps(obj) + "\n")
        self.f.flush()
        return self.f.readline()

    def send(self, events):
        self._cmd({"execute": "input-send-event", "arguments": {"events": events}})

    def abs_to(self, x, y):
        ax = max(0, min(32767, round(x / W * 32767)))
        ay = max(0, min(32767, round(y / H * 32767)))
        self.send([
            {"type": "abs", "data": {"axis": "x", "value": ax}},
            {"type": "abs", "data": {"axis": "y", "value": ay}},
        ])

    def click(self):
        self.send([{"type": "btn", "data": {"down": True, "button": "left"}}])
        time.sleep(0.08)
        self.send([{"type": "btn", "data": {"down": False, "button": "left"}}])


def ease(t):  # ease-in-out, matching rd_agent's glide feel
    return 3 * t * t - 2 * t * t * t


def main():
    q = QMP(SOCK)
    cx, cy = load_pos()
    q.abs_to(cx, cy)
    for line in sys.stdin:
        parts = line.split()
        if not parts:
            continue
        cmd = parts[0]
        if cmd == "click":
            q.click()
        elif cmd == "sleep":
            time.sleep(float(parts[1]))
        elif cmd in ("glide", "move"):
            tx, ty = float(parts[1]), float(parts[2])
            if cmd == "move":
                cx, cy = tx, ty
                q.abs_to(cx, cy)
            else:
                dist = math.hypot(tx - cx, ty - cy)
                steps = max(8, min(60, int(dist / 12)))
                sx, sy = cx, cy
                for i in range(1, steps + 1):
                    t = ease(i / steps)
                    q.abs_to(sx + (tx - sx) * t, sy + (ty - sy) * t)
                    time.sleep(0.016)
                cx, cy = tx, ty
        else:
            print(f"qmpinput: unknown command: {line!r}", file=sys.stderr)
    save_pos(cx, cy)


if __name__ == "__main__":
    main()
