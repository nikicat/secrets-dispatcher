package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// Secrets-only demotion of gnome-keyring (spike-validated on Ubuntu 24.04
// GNOME, see docs/plans/onboarding-and-e2e.md).
//
// Masking gnome-keyring's systemd units outright does NOT work: at the next
// login pam_gnome_keyring spawns an unmanaged daemon (--daemonize --login)
// that re-grabs org.freedesktop.secrets anyway, and pkcs11 dies with the
// daemon. Closing that hole would require root-level PAM edits — far too
// invasive. Instead we demote only the *secrets component*, keeping the
// daemon, its control socket (PAM keeps feeding it the login password), and
// pkcs11 intact:
//
//  1. a systemd user drop-in removes `secrets` from --components;
//  2. a user-level autostart shadow hides the gnome-keyring-secrets.desktop
//     kicker (systemd's xdg-autostart-generator honors Hidden=true);
//  3. the D-Bus activation mask (maskDBusActivation) covers on-demand starts.
//
// All three are plain user-level files carrying a managed-by marker; reversal
// removes exactly what we wrote and restores any backed-up user file. This is
// the state model: nothing to record elsewhere, the files ARE the state.

const (
	gkServiceUnit    = "gnome-keyring-daemon.service"
	gkDropInDirName  = gkServiceUnit + ".d"
	gkDropInName     = "50-secrets-dispatcher.conf"
	gkAutostartName  = "gnome-keyring-secrets.desktop"
	gkManagedComment = "# Managed by secrets-dispatcher; removed by `secrets-dispatcher service uninstall`.\n"
)

const gkDropInTemplate = gkManagedComment +
	`# Demotes gnome-keyring to non-secrets components so secrets-dispatcher can
# own org.freedesktop.secrets while pkcs11 and PAM unlock keep working.
[Service]
ExecStart=
ExecStart=%s --foreground --components=pkcs11 --control-directory=%%t/keyring
`

const gkAutostartShadow = gkManagedComment +
	`# Hides the stock /etc/xdg/autostart gnome-keyring secrets kicker, which
# would otherwise respawn a secrets daemon into a fresh session.
[Desktop Entry]
Type=Application
Name=Secret Storage Service
Hidden=true
`

// autostartDir returns the user XDG autostart directory.
func autostartDir() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "autostart"), nil
}

// demoteGnomeKeyring applies the secrets-only demotion. The currently running
// (still full-component) daemon is handled by the caller: try-restart below
// picks up the drop-in for a unit-managed daemon; stopDBusActivatedService
// catches an unmanaged (PAM-spawned) owner.
func demoteGnomeKeyring() error {
	gkDaemon, err := lookPathFunc("gnome-keyring-daemon")
	if err != nil {
		return fmt.Errorf("find gnome-keyring-daemon: %w", err)
	}

	unitsDir, err := unitDir()
	if err != nil {
		return err
	}
	dropInDir := filepath.Join(unitsDir, gkDropInDirName)
	if err := os.MkdirAll(dropInDir, 0755); err != nil {
		return fmt.Errorf("create drop-in dir: %w", err)
	}
	dropIn := filepath.Join(dropInDir, gkDropInName)
	if err := os.WriteFile(dropIn, fmt.Appendf(nil, gkDropInTemplate, gkDaemon), 0644); err != nil {
		return fmt.Errorf("write drop-in: %w", err)
	}
	verbosef("Wrote gnome-keyring drop-in: %s\n", dropIn)

	asDir, err := autostartDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(asDir, 0755); err != nil {
		return fmt.Errorf("create autostart dir: %w", err)
	}
	shadow := filepath.Join(asDir, gkAutostartName)
	// A pre-existing user autostart override is the user's own — back it up
	// so uninstall restores it exactly (same pattern as maskDBusActivation).
	existing, err := os.ReadFile(shadow)
	if err == nil && string(existing) != gkAutostartShadow {
		if err := os.Rename(shadow, shadow+dbusBackupSuffix); err != nil {
			return fmt.Errorf("backup %s: %w", gkAutostartName, err)
		}
		verbosef("Backed up %s\n", shadow)
	}
	if err := os.WriteFile(shadow, []byte(gkAutostartShadow), 0644); err != nil {
		return fmt.Errorf("write autostart shadow: %w", err)
	}
	verbosef("Hid gnome-keyring secrets autostart: %s\n", shadow)

	if err := systemctlFunc("daemon-reload"); err != nil {
		return err
	}
	// Only touches a unit-managed daemon that is actually running; releases
	// org.freedesktop.secrets by restarting it without the secrets component.
	if err := systemctlFunc("try-restart", gkServiceUnit); err != nil {
		// Non-fatal: the unit may not exist on non-systemd-keyring distros.
		fmt.Printf("note: try-restart %s: %v\n", gkServiceUnit, err)
	}
	return nil
}

// restoreGnomeKeyring reverses demoteGnomeKeyring. Safe to call when the
// demotion was never applied: it removes only files we wrote (checked by the
// managed marker/content) and restores any backup.
func restoreGnomeKeyring() error {
	unitsDir, err := unitDir()
	if err != nil {
		return err
	}
	dropIn := filepath.Join(unitsDir, gkDropInDirName, gkDropInName)
	changed := false
	if _, err := os.Stat(dropIn); err == nil {
		if err := os.Remove(dropIn); err != nil {
			return fmt.Errorf("remove drop-in: %w", err)
		}
		_ = os.Remove(filepath.Dir(dropIn)) // rmdir if now empty
		verbosef("Removed gnome-keyring drop-in: %s\n", dropIn)
		changed = true
	}

	asDir, err := autostartDir()
	if err != nil {
		return err
	}
	shadow := filepath.Join(asDir, gkAutostartName)
	if content, err := os.ReadFile(shadow); err == nil && string(content) == gkAutostartShadow {
		if err := os.Remove(shadow); err != nil {
			return fmt.Errorf("remove autostart shadow: %w", err)
		}
		verbosef("Removed gnome-keyring autostart shadow: %s\n", shadow)
		changed = true
		if _, err := os.Stat(shadow + dbusBackupSuffix); err == nil {
			if err := os.Rename(shadow+dbusBackupSuffix, shadow); err != nil {
				return fmt.Errorf("restore %s: %w", gkAutostartName, err)
			}
			verbosef("Restored %s from backup\n", shadow)
		}
	}

	if !changed {
		return nil
	}
	if err := systemctlFunc("daemon-reload"); err != nil {
		return err
	}
	if err := systemctlFunc("try-restart", gkServiceUnit); err != nil {
		fmt.Printf("note: try-restart %s: %v\n", gkServiceUnit, err)
	}
	return nil
}
