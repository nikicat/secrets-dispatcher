---
status: todo
tags: [bug]
---
# A gopass backend's passphrase prompt opens on the private bus and never shows

## Goal
The backend unit sets DBUS_SESSION_BUS_ADDRESS to the private backend bus for every backend (internal/service/install.go:74). gnome-keyring-daemon needs it, but gopass-secret takes `--bus-address` instead (internal/service/backend.go:36-40). Its gpg child inherits the variable and passes it to gpg-agent, so pinentry-gnome3 looks for GNOME's prompter on the backend bus, finds none, and hangs with no window. gopass-secret kills the decrypt after 60 s ("Decryption failed: signal: killed"), and the next keyring read starts another one.

What the user sees: "Unlocking keyring" with no passphrase window, and keyring reads from remote clients failing until the key is cached some other way. Approve clicks on gpg_sign notifications also seemed to wait until a stuck prompt was killed. On 2026-11-20 a click took effect only at 01:56:16, four seconds after one was killed.

Reproduce: `printf 'GETPIN\nBYE\n' | DBUS_SESSION_BUS_ADDRESS=unix:path=$XDG_RUNTIME_DIR/secrets-dispatcher/backend-bus.sock WAYLAND_DISPLAY=wayland-0 XDG_SESSION_TYPE=wayland pinentry` hangs. With the session bus it shows the window.

## Next
- The backend unit sets DBUS_SESSION_BUS_ADDRESS only for backends that read it (gnome-keyring). The gopass unit leaves the session bus alone. A test in install_test.go checks both.
- On an installed laptop, `service install` rewrites the unit, then a backend restart.
