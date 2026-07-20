package service

import (
	"fmt"
	"strings"
)

// resolveBackendExec turns the --backend value into the ExecStart command for
// secrets-dispatcher-backend.service. Accepted forms:
//
//   - "" (unset): detection-driven default — gnome-keyring when it currently
//     owns the name (stock GNOME just works, US-5), gopass otherwise.
//   - a path (contains "/"): used verbatim, advanced use.
//   - a keyword naming a known backend: "gopass", "gnome-keyring".
//
// The gnome-keyring form demotes the user's existing keyring to a private
// backend: same keyring files, secrets component only, and a private control
// directory so it cannot collide with a session gnome-keyring instance
// (%t is expanded by systemd to $XDG_RUNTIME_DIR).
func resolveBackendExec(value string, provider Provider) (string, error) {
	if value == "" {
		if provider.Kind == ProviderGnomeKeyring {
			value = "gnome-keyring"
		} else {
			value = "gopass"
		}
	}

	if strings.Contains(value, "/") {
		return value, nil
	}

	switch value {
	case "gopass":
		// gopass ships the Secret Service daemon as `gopass-secret service`
		// (subcommand). It takes the private bus via --bus-address; it does not
		// read DBUS_SESSION_BUS_ADDRESS (which the backend unit sets for
		// gnome-keyring-daemon), so without --bus-address it crash-loops on a
		// built-in default. (%t is expanded by systemd to $XDG_RUNTIME_DIR.)
		path, err := lookPathFunc("gopass-secret")
		if err != nil {
			return "", fmt.Errorf("find gopass-secret: %w", err)
		}
		return path + " service --bus-address unix:path=%t/secrets-dispatcher/backend-bus.sock", nil
	case "gnome-keyring":
		path, err := lookPathFunc("gnome-keyring-daemon")
		if err != nil {
			return "", fmt.Errorf("find gnome-keyring-daemon: %w", err)
		}
		return path + " --foreground --components=secrets --control-directory=%t/secrets-dispatcher/keyring", nil
	default:
		return "", fmt.Errorf("unknown backend %q (use a path, or one of: gopass, gnome-keyring)", value)
	}
}
