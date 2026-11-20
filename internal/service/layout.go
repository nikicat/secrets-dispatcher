package service

import (
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/textfile"
)

// Where the service setup keeps its files under the user's XDG homes.
type layout struct {
	unitDir systemd.UnitDir
	files   takeoverFiles
}

// Returns the layout under the user's XDG config and data homes. Errors when
// either home cannot be found.
func locateLayout() (layout, error) {
	configHome, err := config.ConfigHome()
	if err != nil {
		return layout{}, err
	}
	dataHome, err := config.DataHome()
	if err != nil {
		return layout{}, err
	}
	unitDir := systemd.UserUnitDir(configHome)
	return layout{
		unitDir: unitDir,
		files: takeoverFiles{
			mask:   dbusMask(textfile.DirAt(dataHome, "dbus-1", "services")),
			shadow: gkAutostart(textfile.DirAt(configHome, "autostart")),
			dropIn: gkDropIn(unitDir),
		},
	}, nil
}

// Returns the units whose unit file is in l.unitDir, in allUnitNames order.
func (l layout) installed() []systemd.Unit {
	var installed []systemd.Unit
	for _, u := range allUnitNames {
		if l.unitDir.UnitFile(u).Exists() {
			installed = append(installed, u)
		}
	}
	return installed
}

// Returns the unit file of u in the layout. The narration and errors name the
// file by u.
func (l layout) unitFile(u systemd.Unit, note string) narratedFile[systemd.UnitFile] {
	return narratedFile[systemd.UnitFile]{file: l.unitDir.UnitFile(u), name: string(u), purpose: note}
}
