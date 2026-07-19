// Secrets Dispatcher Demo Locator — a test-only gnome-shell extension for the
// Tier-2 demo harness.
//
// gnome-shell's UI is St-on-Clutter, whose accessibility layer (Cally) exposes
// the object tree and labels but NOT element geometry (AT-SPI get_extents comes
// back as INT_MIN), and there is no supported external API to query a shell
// widget's position. The GNOME-recommended answer for automation is to run
// inside the shell and export a D-Bus interface — which is all this does: walk
// the stage for a button whose visible label matches, and return its
// transformed (screen, logical-pixel) rectangle. demo.sh feeds that rectangle
// to RemoteDesktop so the cursor glides to a button located by *text*, not by
// a hardcoded coordinate.

import Gio from 'gi://Gio';
import Meta from 'gi://Meta';
import St from 'gi://St';

import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

const IFACE = `
<node>
  <interface name="org.gnome.Shell.SecretsDemoLocator">
    <method name="Locate">
      <arg type="s" name="label" direction="in"/>
      <arg type="b" name="found" direction="out"/>
      <arg type="d" name="x" direction="out"/>
      <arg type="d" name="y" direction="out"/>
      <arg type="d" name="w" direction="out"/>
      <arg type="d" name="h" direction="out"/>
    </method>
    <method name="PlaceActive">
      <arg type="i" name="x" direction="in"/>
      <arg type="i" name="y" direction="in"/>
      <arg type="i" name="w" direction="in"/>
      <arg type="i" name="h" direction="in"/>
      <arg type="b" name="ok" direction="out"/>
    </method>
    <method name="Dump">
      <arg type="s" name="labels" direction="out"/>
    </method>
  </interface>
</node>`;

export default class LocatorExtension extends Extension {
    enable() {
        this._dbus = Gio.DBusExportedObject.wrapJSObject(IFACE, this);
        this._dbus.export(Gio.DBus.session, '/org/gnome/Shell/SecretsDemoLocator');
    }

    disable() {
        this._dbus?.unexport();
        this._dbus = null;
    }

    // Locate returns the on-screen rectangle of the clickable element whose
    // visible label equals `label`. It matches the label text anywhere (an
    // St.Label, or an St.Button's label property) and then walks up to the
    // nearest clickable ancestor — notification action buttons and dialog
    // buttons are built differently, so matching by text is more robust than
    // matching a specific widget class. Coordinates are stage (screen) logical
    // pixels, matching RemoteDesktop's absolute-motion coordinate space.
    Locate(label) {
        const target = this._matchByText(global.stage, label);
        if (!target)
            return [false, 0, 0, 0, 0];
        const clickable = this._clickableAncestor(target);
        const [x, y] = clickable.get_transformed_position();
        const [w, h] = clickable.get_transformed_size();
        return [true, x, y, w, h];
    }

    // PlaceActive unmaximizes and moves/resizes the currently-focused window to
    // an absolute rectangle (logical px). The demo uses it to arrange its two
    // terminal windows: a Wayland client cannot position itself, but the shell
    // can. Call it right after opening a window, while it still holds focus.
    PlaceActive(x, y, w, h) {
        const win = global.display.get_focus_window();
        if (!win)
            return false;
        // GNOME 48 (Ubuntu 26.04) removed Meta.Window.get_maximized(); feature-
        // detect it. The demo's freshly-opened terminals are never maximized, so
        // skipping the unmaximize where the API is gone is harmless.
        if (typeof win.get_maximized === 'function' && win.get_maximized())
            win.unmaximize(Meta.MaximizeFlags.BOTH);
        win.move_resize_frame(false, x, y, w, h);
        win.raise?.(); // also version-guarded (raise() has churned across Meta)
        return true;
    }

    // Dump lists every visible labelled actor with its rectangle — a debugging
    // aid for finding how a given button is structured.
    Dump() {
        const lines = [];
        const walk = actor => {
            if (!actor.visible)
                return;
            const text = this._textOf(actor);
            if (text) {
                const [x, y] = actor.get_transformed_position();
                const [w, h] = actor.get_transformed_size();
                lines.push(`${JSON.stringify(text)} @ ${Math.round(x)},${Math.round(y)} ` +
                    `${Math.round(w)}x${Math.round(h)} mapped=${actor.mapped} ` +
                    `reactive=${actor.reactive} ${actor.constructor.name}`);
            }
            for (const child of actor.get_children())
                walk(child);
        };
        walk(global.stage);
        return lines.join('\n');
    }

    _matchByText(actor, label) {
        if (!actor.visible)
            return null;
        if (this._textOf(actor) === label)
            return actor;
        for (const child of actor.get_children()) {
            const found = this._matchByText(child, label);
            if (found)
                return found;
        }
        return null;
    }

    _textOf(actor) {
        if (actor instanceof St.Label)
            return actor.text;
        if (actor instanceof St.Button && typeof actor.label === 'string' && actor.label)
            return actor.label;
        return null;
    }

    _clickableAncestor(actor) {
        let a = actor;
        while (a) {
            if (a instanceof St.Button || a.reactive)
                return a;
            a = a.get_parent();
        }
        return actor;
    }
}
