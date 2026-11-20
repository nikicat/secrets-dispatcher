package service

import (
	"fmt"

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

// Returns the drop-in that demotes gnome-keyring, in dir.
func gkDropIn(dir systemd.UnitDir) managedDropIn {
	return managedDropIn{narratedFile[systemd.DropIn]{
		file:    dir.DropIn(gkServiceUnit, gkDropInName),
		name:    "gnome-keyring demotion drop-in",
		purpose: "demote gnome-keyring to pkcs11-only (secrets move to the private backend)",
	}}
}

// Returns the shadow in dir that hides gnome-keyring's secrets autostart entry
// (ADR 0009).
func gkAutostart(dir textfile.Dir) managedFile {
	return managedFile{
		file:    dir.File(gkAutostartName),
		name:    "gnome-keyring autostart shadow",
		purpose: "hide the gnome-keyring secrets autostart kicker (Hidden=true)",
		content: gkAutostartShadow,
	}
}

// Returns the steps that limit gnome-keyring to its pkcs11 component, and
// restart it when it runs as a unit (ADR 0009). Errors when
// gnome-keyring-daemon is not installed.
func (f takeoverFiles) demoteSteps() ([]step, error) {
	gkDaemon, err := gnomeKeyringDaemon()
	if err != nil {
		return nil, err
	}
	demoted := systemd.NewCommand(gkDaemon, "--foreground", "--components=pkcs11", "--control-directory=%t/keyring")
	steps := []step{f.dropIn.writeStep(fmt.Sprintf(gkDropInTemplate, demoted.ExecLine()))}
	steps = append(steps, f.shadow.writeSteps()...)
	return append(steps, restartKeyringSteps("restart without the secrets component")...), nil
}

// Returns the steps that undo demoteSteps, and restart gnome-keyring with all
// its components when it runs as a unit (ADR 0009). Returns none when no file
// of the demotion is in place.
func (f takeoverFiles) restoreKeyringSteps() []step {
	steps := append(f.dropIn.restoreSteps(), f.shadow.restoreSteps()...)
	if len(steps) == 0 {
		return nil
	}
	return append(steps, restartKeyringSteps("restart with all components")...)
}

// Returns the steps that reload the unit files and restart gnome-keyring if it
// is running. The dry run shows the given note beside the restart.
func restartKeyringSteps(note string) []step {
	return []step{reloadStep(), unitStep(opTryRestart, gkServiceUnit, note)}
}
