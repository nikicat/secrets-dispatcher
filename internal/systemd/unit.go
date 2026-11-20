// Package systemd holds what the packages that install systemd units share.
package systemd

import (
	"github.com/nikicat/secrets-dispatcher/internal/textfile"
)

// The name of a systemd unit, such as "secrets-dispatcher.service".
type Unit string

// A directory that systemd loads unit files from.
type UnitDir struct{ dir textfile.Dir }

// Returns the user unit directory under configHome, the XDG config home of the
// user whose systemd --user manager loads it.
func UserUnitDir(configHome string) UnitDir {
	return UnitDir{textfile.DirAt(configHome, "systemd", "user")}
}

func (d UnitDir) String() string { return d.dir.String() }

// Returns u's unit file in d.
func (d UnitDir) UnitFile(u Unit) UnitFile { return UnitFile{d.dir.File(string(u))} }

// Returns the drop-in file with the given name for u, in d.
func (d UnitDir) DropIn(u Unit, name string) DropIn {
	return DropIn{d.dir.Dir(string(u) + ".d").File(name)}
}

// The file that defines a unit, in a UnitDir.
type UnitFile struct{ textfile.File }

// A file in a unit's drop-in directory, in a UnitDir.
type DropIn struct{ textfile.File }

// Removes f, and its unit's drop-in directory when that is left empty. Errors
// when f is absent (fs.ErrNotExist) or cannot be removed.
func (f DropIn) Remove() error {
	if err := f.File.Remove(); err != nil {
		return err
	}
	_ = f.Dir().Remove() // fails while other drop-ins remain
	return nil
}
