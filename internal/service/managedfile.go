package service

import (
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/textfile"
)

// The suffix of the backup file. A managedFile keeps the user's own file there
// when it writes over it.
const backupSuffix = ".pre-dispatcher"

// A user-level file that a takeover writes and leaving it removes (ADR 0009).
type takeoverFile interface {
	// What the file is, as the dry run, the narration and status name it.
	label() string
	// Reports whether the dispatcher's file is in place.
	present() bool
	// Returns the changes that writing the file makes, as the dry run lists them.
	planWrite() []Change
	// Returns the changes that restore makes, as the dry run lists them.
	planRestore() []Change
	// Removes the dispatcher's file and reports whether it did. Errors when a
	// file operation fails.
	restore(n Narration) (bool, error)
}

// The files that entering takeover writes and leaving it removes.
type takeoverFiles struct {
	mask, shadow managedFile
	dropIn       managedDropIn
}

// Returns the takeover files at their places under the user's XDG homes.
func locateTakeoverFiles() (takeoverFiles, error) {
	mask, err := dbusMask()
	if err != nil {
		return takeoverFiles{}, err
	}
	shadow, err := gkAutostart()
	if err != nil {
		return takeoverFiles{}, err
	}
	dropIn, err := gkDropIn()
	if err != nil {
		return takeoverFiles{}, err
	}
	return takeoverFiles{mask: mask, shadow: shadow, dropIn: dropIn}, nil
}

// The file operations that a takeover file of either kind narrates.
type diskFile interface {
	fmt.Stringer
	Write(content string) error
	Remove() error
}

// A takeover file with what it is called, and what writing it does, as the
// dry run notes it.
type narratedFile[F diskFile] struct {
	file          F
	name, purpose string
}

func (f narratedFile[F]) label() string { return f.name }

// Writes content to f, and prints the change under NarrateChanges. Errors when
// the write fails.
func (f narratedFile[F]) write(n Narration, content string) error {
	if err := f.file.Write(content); err != nil {
		return fmt.Errorf("write %s: %w", f.name, err)
	}
	n.printf("Wrote %s: %s\n", f.name, f.file)
	return nil
}

// Removes f, prints the change under NarrateChanges, and reports whether it
// did. Errors when the removal fails.
func (f narratedFile[F]) remove(n Narration) (bool, error) {
	if err := f.file.Remove(); err != nil {
		return false, fmt.Errorf("remove %s: %w", f.name, err)
	}
	n.printf("Removed %s: %s\n", f.name, f.file)
	return true, nil
}

func (f narratedFile[F]) writeChange() Change {
	return Change{"write", f.file.String(), f.purpose}
}

func (f narratedFile[F]) removeChange() Change {
	return Change{"remove", f.file.String(), f.name}
}

// A takeover file that the dispatcher recognises as its own by its content.
type managedFile struct {
	narratedFile[textfile.File]
	content string
}

func (f managedFile) backup() textfile.File { return f.file.WithSuffix(backupSuffix) }

func (f managedFile) present() bool {
	existing, err := f.file.Read()
	return err == nil && existing == f.content
}

// Reports whether a file other than ours is at f's path.
func (f managedFile) foreign() bool {
	existing, err := f.file.Read()
	return err == nil && existing != f.content
}

// Writes f, moving a foreign file at its path to its backup first. Errors
// when a file operation fails.
func (f managedFile) write(n Narration) error {
	if f.foreign() {
		if err := f.file.RenameTo(f.backup()); err != nil {
			return fmt.Errorf("back up %s: %w", f.name, err)
		}
		n.printf("Backed up %s\n", f.file)
	}
	return f.narratedFile.write(n, f.content)
}

func (f managedFile) planWrite() []Change {
	var changes []Change
	if f.foreign() {
		changes = append(changes, Change{"backup", f.file.String(), "-> " + f.backup().String()})
	}
	return append(changes, f.writeChange())
}

// Puts f's backup back in its place when there is one. A foreign file is left
// alone.
func (f managedFile) restore(n Narration) (bool, error) {
	if !f.present() {
		return false, nil
	}
	if f.backup().Exists() {
		if err := f.backup().RenameTo(f.file); err != nil {
			return false, fmt.Errorf("restore %s: %w", f.file, err)
		}
		n.printf("Restored %s from backup\n", f.file)
		return true, nil
	}
	return f.remove(n)
}

func (f managedFile) planRestore() []Change {
	switch {
	case !f.present():
		return nil
	case f.backup().Exists():
		return []Change{{"restore", f.backup().String(), "-> " + f.file.String()}}
	default:
		return []Change{f.removeChange()}
	}
}

// A takeover drop-in that the dispatcher recognises as its own by its name.
// Its content is given when it is written.
type managedDropIn struct{ narratedFile[systemd.DropIn] }

func (f managedDropIn) present() bool { return f.file.Exists() }

func (f managedDropIn) planWrite() []Change { return []Change{f.writeChange()} }

func (f managedDropIn) restore(n Narration) (bool, error) {
	if !f.present() {
		return false, nil
	}
	return f.remove(n)
}

func (f managedDropIn) planRestore() []Change {
	if !f.present() {
		return nil
	}
	return []Change{f.removeChange()}
}
