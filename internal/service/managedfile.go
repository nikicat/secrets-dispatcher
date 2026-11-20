package service

import (
	"context"
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
}

// The files that entering takeover writes and leaving it removes.
type takeoverFiles struct {
	mask, shadow managedFile
	dropIn       managedDropIn
}

// The file operations that a takeover file of either kind narrates.
type diskFile interface {
	fmt.Stringer
	Write(content string) error
	Remove() error
}

type narratedFile[F diskFile] struct {
	file          F
	name, purpose string
}

func (f narratedFile[F]) label() string { return f.name }

func (f narratedFile[F]) writeStep(content string) step {
	return step{Change{changeWrite, f.file.String(), f.purpose}, func(_ context.Context, h host) error {
		if err := f.file.Write(content); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
		h.narration.printf("Wrote %s: %s\n", f.name, f.file)
		return nil
	}}
}

func (f narratedFile[F]) removeStep() step {
	return step{Change{changeRemove, f.file.String(), f.name}, func(_ context.Context, h host) error {
		if err := f.file.Remove(); err != nil {
			return fmt.Errorf("remove %s: %w", f.name, err)
		}
		h.narration.printf("Removed %s: %s\n", f.name, f.file)
		return nil
	}}
}

// A takeover file that the dispatcher recognises as its own by its content.
type managedFile struct {
	narratedFile[textfile.File]
	content string
}

func (f managedFile) backup() textfile.File { return f.file.WithSuffix(backupSuffix) }

// What is at a managedFile's path.
type occupant int

const (
	occupantAbsent occupant = iota
	occupantOurs
	occupantForeign
)

// An unreadable file counts as absent.
func (f managedFile) occupant() occupant {
	existing, err := f.file.Read()
	switch {
	case err != nil:
		return occupantAbsent
	case existing == f.content:
		return occupantOurs
	default:
		return occupantForeign
	}
}

func (f managedFile) present() bool { return f.occupant() == occupantOurs }

func (f managedFile) backupStep() step {
	return step{Change{changeBackup, f.file.String(), "-> " + f.backup().String()}, func(_ context.Context, h host) error {
		if err := f.file.RenameTo(f.backup()); err != nil {
			return fmt.Errorf("back up %s: %w", f.name, err)
		}
		h.narration.printf("Backed up %s\n", f.file)
		return nil
	}}
}

func (f managedFile) restoreBackupStep() step {
	return step{Change{changeRestore, f.backup().String(), "-> " + f.file.String()}, func(_ context.Context, h host) error {
		if err := f.backup().RenameTo(f.file); err != nil {
			return fmt.Errorf("restore %s: %w", f.file, err)
		}
		h.narration.printf("Restored %s from backup\n", f.file)
		return nil
	}}
}

// Returns the steps that write f. When a file that is not ours is at f's path,
// the steps move it to f's backup, and restoreSteps puts it back (ADR 0009).
func (f managedFile) writeSteps() []step {
	var steps []step
	if f.occupant() == occupantForeign {
		steps = append(steps, f.backupStep())
	}
	return append(steps, f.writeStep(f.content))
}

// Returns the steps that replace f with its backup, or remove f when there is
// no backup (ADR 0009). Returns none when the file at f's path is not ours.
func (f managedFile) restoreSteps() []step {
	switch {
	case f.occupant() != occupantOurs:
		return nil
	case f.backup().Exists():
		return []step{f.restoreBackupStep()}
	default:
		return []step{f.removeStep()}
	}
}

// A takeover drop-in that the dispatcher recognises as its own by its name.
// Its content is given when it is written.
type managedDropIn struct{ narratedFile[systemd.DropIn] }

func (f managedDropIn) present() bool { return f.file.Exists() }

func (f managedDropIn) restoreSteps() []step {
	if !f.present() {
		return nil
	}
	return []step{f.removeStep()}
}
