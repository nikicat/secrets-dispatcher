#!/usr/bin/env python3
"""RemoteDesktop input agent for the Tier-2 demo harness.

Runs INSIDE the VM. Opens one org.gnome.Mutter.RemoteDesktop session (gnome-
shell's own input API — no portal, no consent) paired with a ScreenCast monitor
stream that defines the coordinate space, then executes commands read from
stdin. It is the demo's hands: it types the keyring password into the unlock
dialog and glides the real cursor onto notification buttons and clicks them.

Buttons are found by *text*, not pixels: `clicktext`/`waittext` call the
secrets-demo-locator gnome-shell extension (org.gnome.Shell.SecretsDemoLocator)
which returns a shell element's on-screen rectangle by label — the only way to
get shell-widget geometry, since Cally exposes no coordinates. The rectangle is
in stage logical pixels, which is exactly RemoteDesktop's absolute-motion
coordinate space, so no scaling is needed.

Commands (one per line on stdin):
  type <text>        type the text (keeps it simple: lower/upper/digits/space)
  key <name>         press a named key: enter esc tab backspace space
  waittext <label>   block until a button with that label is on screen
  clicktext <label>  wait for the button, glide the cursor to it, left-click
  move <x> <y>       glide the cursor to an absolute point
  sleep <seconds>    pause
  quit               end the session and exit
"""
import math
import os
import sys
import time

import gi
gi.require_version('Gio', '2.0')
from gi.repository import Gio, GLib

RD = 'org.gnome.Mutter.RemoteDesktop'
SC = 'org.gnome.Mutter.ScreenCast'
SHELL = 'org.gnome.Shell'
LOCATOR_PATH = '/org/gnome/Shell/SecretsDemoLocator'
LOCATOR_IFACE = 'org.gnome.Shell.SecretsDemoLocator'

# Where the cursor position is remembered between invocations. Each rd() batch
# is a fresh process, so without this the cursor would teleport to the parked
# start point before every glide; persisting it makes movement continuous.
# demo.sh removes the file after the reboot so the first batch starts parked.
CURSOR_STATE = os.environ.get(
    'RD_CURSOR_STATE', os.path.expanduser('~/.cache/rd_agent_cursor'))
CURSOR_PARKED = (1150.0, 730.0)  # bottom-right, where a fresh session leaves it

BTN_LEFT = 0x110
LEFTSHIFT = 42
# evdev keycodes for the characters the demo actually types.
EVDEV = {
    'a': 30, 'b': 48, 'c': 46, 'd': 32, 'e': 18, 'f': 33, 'g': 34, 'h': 35,
    'i': 23, 'j': 36, 'k': 37, 'l': 38, 'm': 50, 'n': 49, 'o': 24, 'p': 25,
    'q': 16, 'r': 19, 's': 31, 't': 20, 'u': 22, 'v': 47, 'w': 17, 'x': 45,
    'y': 21, 'z': 44,
    '1': 2, '2': 3, '3': 4, '4': 5, '5': 6, '6': 7, '7': 8, '8': 9, '9': 10,
    '0': 11, ' ': 57, '-': 12, '_': 12, '.': 52,
}
NAMED = {'enter': 28, 'esc': 1, 'tab': 15, 'backspace': 14, 'space': 57}


class Agent:
    def __init__(self):
        self.bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
        # RemoteDesktop session + linked ScreenCast monitor (coordinate space).
        self.rd = self._call(RD, '/org/gnome/Mutter/RemoteDesktop', RD,
                             'CreateSession').unpack()[0]
        sid = self._call(RD, self.rd, 'org.freedesktop.DBus.Properties', 'Get',
                        GLib.Variant('(ss)', (RD + '.Session', 'SessionId'))).unpack()[0]
        sc = self._call(SC, '/org/gnome/Mutter/ScreenCast', SC, 'CreateSession',
                       GLib.Variant('(a{sv})', ({'remote-desktop-session-id':
                                                 GLib.Variant('s', sid)},))).unpack()[0]
        self.stream = self._call(SC, sc, SC + '.Session', 'RecordMonitor',
                                GLib.Variant('(sa{sv})', ('', {'cursor-mode':
                                                               GLib.Variant('u', 1)}))).unpack()[0]
        self._call(RD, self.rd, RD + '.Session', 'Start')
        time.sleep(1.0)
        # Resume the cursor from where the previous batch left it (falls back to
        # the parked corner on a fresh session).
        self.cx, self.cy = self._load_cursor()

    @staticmethod
    def _load_cursor():
        try:
            with open(CURSOR_STATE) as f:
                x, y = f.read().split()
                return float(x), float(y)
        except (OSError, ValueError):
            return CURSOR_PARKED

    def _save_cursor(self):
        try:
            os.makedirs(os.path.dirname(CURSOR_STATE), exist_ok=True)
            with open(CURSOR_STATE, 'w') as f:
                f.write(f'{self.cx} {self.cy}')
        except OSError:
            pass

    def _call(self, dest, path, iface, method, variant=None):
        return self.bus.call_sync(dest, path, iface, method, variant, None,
                                  Gio.DBusCallFlags.NONE, -1, None)

    def _rd(self, method, variant):
        self._call(RD, self.rd, RD + '.Session', method, variant)

    # --- keyboard ---
    def _tap(self, code):
        self._rd('NotifyKeyboardKeycode', GLib.Variant('(ub)', (code, True)))
        time.sleep(0.03)
        self._rd('NotifyKeyboardKeycode', GLib.Variant('(ub)', (code, False)))
        time.sleep(0.05)

    def type_text(self, text):
        for ch in text:
            if ch.isupper():
                self._rd('NotifyKeyboardKeycode', GLib.Variant('(ub)', (LEFTSHIFT, True)))
                self._tap(EVDEV[ch.lower()])
                self._rd('NotifyKeyboardKeycode', GLib.Variant('(ub)', (LEFTSHIFT, False)))
            else:
                self._tap(EVDEV[ch])

    def key(self, name):
        self._tap(NAMED[name])

    # --- pointer ---
    def _moveto(self, x, y):
        self._rd('NotifyPointerMotionAbsolute', GLib.Variant('(sdd)', (self.stream, float(x), float(y))))

    @staticmethod
    def _ease(t):
        return 3 * t * t - 2 * t * t * t  # smoothstep

    def glide(self, x, y, steps=40, dur=0.8):
        x0, y0 = self.cx, self.cy
        mx, my = (x0 + x) / 2, (y0 + y) / 2
        dx, dy = x - x0, y - y0
        L = math.hypot(dx, dy) or 1
        # bow the path perpendicular to the straight line for a human-like arc
        bow = min(L * 0.12, 60)
        px, py = mx - dy / L * bow, my + dx / L * bow
        for i in range(1, steps + 1):
            t = self._ease(i / steps)
            u = 1 - t
            self._moveto(u * u * x0 + 2 * u * t * px + t * t * x,
                         u * u * y0 + 2 * u * t * py + t * t * y)
            time.sleep(dur / steps)
        self.cx, self.cy = float(x), float(y)

    def click(self):
        self._rd('NotifyPointerButton', GLib.Variant('(ib)', (BTN_LEFT, True)))
        time.sleep(0.08)
        self._rd('NotifyPointerButton', GLib.Variant('(ib)', (BTN_LEFT, False)))

    # --- locate (via the gnome-shell extension) ---
    def locate(self, label):
        r = self._call(SHELL, LOCATOR_PATH, LOCATOR_IFACE, 'Locate',
                       GLib.Variant('(s)', (label,))).unpack()
        found, x, y, w, h = r
        return (x + w / 2, y + h / 2) if found else None

    def waittext(self, label, timeout=20.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            c = self.locate(label)
            if c:
                return c
            time.sleep(0.3)
        raise RuntimeError(f'timed out waiting for button {label!r}')

    def clicktext(self, label):
        cx, cy = self.waittext(label)
        self.glide(cx, cy)
        time.sleep(0.2)
        self.click()

    def run(self):
        print('rd_agent ready', flush=True)
        try:
            for line in sys.stdin:
                line = line.strip()
                if not line:
                    continue
                cmd, _, arg = line.partition(' ')
                try:
                    if cmd == 'type':
                        self.type_text(arg)
                    elif cmd == 'key':
                        self.key(arg)
                    elif cmd == 'waittext':
                        self.waittext(arg)
                    elif cmd == 'clicktext':
                        self.clicktext(arg)
                    elif cmd == 'move':
                        x, y = arg.split()
                        self.glide(float(x), float(y))
                    elif cmd == 'sleep':
                        time.sleep(float(arg))
                    elif cmd == 'quit':
                        break
                    else:
                        print(f'rd_agent: unknown command {line!r}', file=sys.stderr, flush=True)
                        continue
                    print(f'ok {line}', flush=True)
                except Exception as e:  # noqa: BLE001 - report and keep going
                    print(f'ERR {line}: {e}', file=sys.stderr, flush=True)
        finally:
            # Hand the cursor position to the next batch so glides stay continuous.
            self._save_cursor()


if __name__ == '__main__':
    Agent().run()
