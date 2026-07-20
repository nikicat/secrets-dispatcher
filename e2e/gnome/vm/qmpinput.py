#!/usr/bin/env python3
"""Host-side input control for the Tier-2 demo VM via QEMU's QMP input-send-event
(the guest's virtio-tablet + virtio-keyboard).

Runs on the HOST. This is the demo's ONLY input path: it drives every pointer
move/click AND every keystroke (leaving the overview, typing the keyring
password, clicking notification buttons). It deliberately replaces the old
in-guest RemoteDesktop agent so that:
  - RemoteDesktop can't drive the top-panel/quick-settings menus (injected clicks
    there don't open them), and each RemoteDesktop batch was its own session, so
    an opened menu dismissed before the next click — the host-side tablet has
    neither problem.
  - RemoteDesktop needs a linked ScreenCast stream for absolute motion, which
    lights the "screen is being shared" indicator on camera. QMP input needs no
    such session, so nothing ever appears.

Coordinates come from the locator gnome-shell extension (a pure D-Bus query, no
ScreenCast), resolved in-guest by demo.sh and passed here as guest pixels.

The guest must run with MUTTER_DEBUG_DISABLE_HW_CURSORS=1 (demo.sh sets it) so the
software cursor composites into the framebuffer VNC captures — otherwise the
moving pointer wouldn't be recorded (keystrokes need no such thing).

Reads a command script on stdin, one per line:
  glide X Y   ease the pointer to guest pixel (X, Y) with a visible, moving cursor
  move  X Y   jump the pointer to (X, Y)
  click       left press + release at the current position
  type  TEXT  type TEXT into the focused window (rest of the line, spaces kept)
  key   NAME  press a named key: enter esc tab backspace space
  sleep S     wait S seconds (let a menu open/animate before the next click)
Pointer coordinates are guest pixels in a WIDTHxHEIGHT screen (default 1280x800),
mapped to QMP's 0..32767 absolute axis range. The pointer position is persisted
between invocations so glides continue from where they stopped.

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
PARKED = (640, 775)  # bottom-centre, clear of the demo's terminal windows

# Character -> (QKeyCode, shift) for the text the demo types (the keyring
# password + short shell-ish tokens). Only what's needed; unknown chars warn.
CHAR2KEY = {}
for _c in "abcdefghijklmnopqrstuvwxyz":
    CHAR2KEY[_c] = (_c, False)
    CHAR2KEY[_c.upper()] = (_c, True)
for _c in "0123456789":
    CHAR2KEY[_c] = (_c, False)
CHAR2KEY[" "] = ("spc", False)
CHAR2KEY["-"] = ("minus", False)
CHAR2KEY["_"] = ("minus", True)
CHAR2KEY["."] = ("dot", False)
CHAR2KEY["/"] = ("slash", False)

# `key NAME` -> QKeyCode for the named keys the demo presses.
NAMED = {"enter": "ret", "esc": "esc", "tab": "tab",
         "backspace": "backspace", "space": "spc"}


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

    def _key(self, qcode, down):
        self.send([{"type": "key", "data": {"down": down,
                                            "key": {"type": "qcode", "data": qcode}}}])

    def tap(self, qcode, shift=False):
        if shift:
            self._key("shift", True)
        self._key(qcode, True)
        time.sleep(0.02)
        self._key(qcode, False)
        if shift:
            self._key("shift", False)
        time.sleep(0.045)  # human pace

    def type_text(self, text):
        for ch in text:
            qk = CHAR2KEY.get(ch)
            if qk is None:
                print(f"qmpinput: no key mapping for {ch!r}, skipping", file=sys.stderr)
                continue
            self.tap(qk[0], qk[1])

    def key_named(self, name):
        qcode = NAMED.get(name)
        if qcode is None:
            print(f"qmpinput: unknown key name {name!r}", file=sys.stderr)
            return
        self.tap(qcode)


def ease(t):  # ease-in-out
    return 3 * t * t - 2 * t * t * t


def main():
    q = QMP(SOCK)
    cx, cy = load_pos()
    q.abs_to(cx, cy)
    for line in sys.stdin:
        line = line.rstrip("\n")
        if not line.strip():
            continue
        cmd, _, arg = line.partition(" ")
        if cmd == "click":
            q.click()
        elif cmd == "type":
            q.type_text(arg)
        elif cmd == "key":
            q.key_named(arg.strip())
        elif cmd == "sleep":
            time.sleep(float(arg))
        elif cmd in ("glide", "move"):
            parts = arg.split()
            tx, ty = float(parts[0]), float(parts[1])
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
