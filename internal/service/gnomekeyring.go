package service

import (
	"context"
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/textfile"
)

const gkServiceUnit systemd.Unit = "gnome-keyring-daemon.service"

const (
	gkDropInName    = "50-secrets-dispatcher.conf"
	gkAutostartName = "gnome-keyring-secrets.desktop"
)

var gkManagedComment = "# Managed by secrets-dispatcher; removed by " + UninstallCommand.hint() + ".\n"

var gkDropInTemplate = gkManagedComment +
	`# Demotes gnome-keyring to non-secrets components so secrets-dispatcher can
# own org.freedesktop.secrets while pkcs11 and PAM unlock keep working.
[Service]
ExecStart=
ExecStart=%s
`

var gkAutostartShadow = gkManagedComment +
	`# Hides the stock /etc/xdg/autostart gnome-keyring secrets kicker, which
# would otherwise respawn a secrets daemon into a fresh session.
[Desktop Entry]
Type=Application
Name=Secret Storage Service
Hidden=true
`

// Returns the drop-in that demotes gnome-keyring, in the systemd user unit
// directory.
func gkDropIn() (managedDropIn, error) {
	dir, err := unitDir()
	if err != nil {
		return managedDropIn{}, err
	}
	return managedDropIn{narratedFile[systemd.DropIn]{
		file:    dir.DropIn(gkServiceUnit, gkDropInName),
		name:    "gnome-keyring demotion drop-in",
		purpose: "demote gnome-keyring to pkcs11-only (secrets move to the private backend)",
	}}, nil
}

// Returns the user XDG autostart directory, under the XDG config home.
func autostartDir() (textfile.Dir, error) {
	configHome, err := config.ConfigHome()
	if err != nil {
		return textfile.Dir{}, err
	}
	return textfile.DirAt(configHome, "autostart"), nil
}

// Returns the shadow that hides gnome-keyring's secrets autostart entry, in
// the user XDG autostart directory.
func gkAutostart() (managedFile, error) {
	dir, err := autostartDir()
	if err != nil {
		return managedFile{}, err
	}
	return managedFile{
		file:    dir.File(gkAutostartName),
		name:    "gnome-keyring autostart shadow",
		purpose: "hide the gnome-keyring secrets autostart kicker (Hidden=true)",
		content: gkAutostartShadow,
	}, nil
}

// Applies the secrets-only demotion (ADR 0009) and restarts a running
// unit-managed gnome-keyring onto it. Errors when gnome-keyring-daemon is not
// installed.
func (h host) demoteGnomeKeyring(ctx context.Context) error {
	gkDaemon, err := gnomeKeyringDaemon()
	if err != nil {
		return err
	}
	demoted := systemd.NewCommand(gkDaemon, "--foreground", "--components=pkcs11", "--control-directory=%t/keyring")
	if err := h.files.dropIn.write(h.narration, fmt.Sprintf(gkDropInTemplate, demoted.ExecLine())); err != nil {
		return err
	}
	if err := h.files.shadow.write(h.narration); err != nil {
		return err
	}
	return h.restartKeyring(ctx)
}

// The takeover files that the gnome-keyring demotion writes, in its order.
func (f takeoverFiles) demotion() []takeoverFile { return []takeoverFile{f.dropIn, f.shadow} }

// Restores gnome-keyring's stock secrets component, and restarts it when any
// file changed.
func (h host) restoreGnomeKeyring(ctx context.Context) error {
	changed := false
	for _, f := range h.files.demotion() {
		restored, err := f.restore(h.narration)
		if err != nil {
			return err
		}
		changed = changed || restored
	}
	if !changed {
		return nil
	}
	return h.restartKeyring(ctx)
}

// Reloads unit files and restarts a running unit-managed gnome-keyring. A failed
// restart only prints a note, since the unit may not exist outside
// systemd-managed keyrings. Errors when the reload fails.
func (h host) restartKeyring(ctx context.Context) error {
	if err := h.reload(ctx); err != nil {
		return err
	}
	if err := h.units.Unit(gkServiceUnit).TryRestart(ctx); err != nil {
		fmt.Printf("note: %v\n", err)
		return nil
	}
	h.narration.printf("Restarted %s if it was running\n", gkServiceUnit)
	return nil
}
