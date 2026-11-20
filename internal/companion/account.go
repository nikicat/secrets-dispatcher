package companion

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"syscall"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/textfile"
)

// A companion user: the account that provisioned files are handed to, and its
// home.
type companion struct {
	account
	home companionHome
}

// The home directory of a companion user.
type companionHome string

func (h companionHome) String() string { return string(h) }

// The home itself, as a directory that provision creates.
func (h companionHome) Dir() companionDir { return companionDir{textfile.DirAt(string(h))} }

// The directory gopass keeps its config in.
func (h companionHome) GopassDir() companionDir { return h.Dir().join(".config").join("gopass") }

// The GnuPG home directory.
func (h companionHome) GnupgDir() companionDir { return h.Dir().join(".gnupg") }

// The directory the companion's systemd user manager loads units from.
func (h companionHome) UnitDir() systemd.UnitDir {
	return systemd.UserUnitDir(filepath.Join(string(h), ".config"))
}

// A Linux user account that provisioned files are handed to.
type account struct {
	name string
	uid  procutil.UID
	gid  procutil.GID
}

// Looks up the account called name. Errors when there is none, or when its UID
// or GID is not a number.
func lookupAccount(name string) (account, error) {
	u, err := userLookupFunc(name)
	if err != nil {
		return account{}, err
	}
	uid, err := procutil.ParseUID(u.Uid)
	if err != nil {
		return account{}, fmt.Errorf("user %s: %w", name, err)
	}
	gid, err := procutil.ParseGID(u.Gid)
	if err != nil {
		return account{}, fmt.Errorf("user %s: %w", name, err)
	}
	return account{name: name, uid: uid, gid: gid}, nil
}

// A directory handed to the companion. Its file operations go through the
// package's swappable functions, so that tests run without root.
type companionDir struct{ dir textfile.Dir }

func (d companionDir) String() string { return d.dir.String() }

func (d companionDir) join(name string) companionDir { return companionDir{d.dir.Dir(name)} }

// Creates d and any missing parents, and makes the given account the owner of
// d and of each directory it creates. Sets the mode of d to perm. Errors when a
// step fails.
func (d companionDir) ensure(owner account, perm fs.FileMode) error {
	if err := d.mkdirs(owner, perm); err != nil {
		return fmt.Errorf("create dir %q: %w", d, err)
	}
	if err := d.chown(owner); err != nil {
		return fmt.Errorf("chown %q: %w", d, err)
	}
	if err := chmodFunc(d.String(), perm); err != nil {
		return fmt.Errorf("chmod %q: %w", d, err)
	}
	slog.Info("directory ready", "path", d)
	return nil
}

// Creates d and any missing parents with mode perm, and makes the given account
// the owner of each directory it creates. Directories that already exist keep
// their owner.
func (d companionDir) mkdirs(owner account, perm fs.FileMode) error {
	err := mkdirFunc(d.String(), perm)
	if parent := (companionDir{d.dir.Parent()}); errors.Is(err, fs.ErrNotExist) && parent != d {
		if err := parent.mkdirs(owner, perm); err != nil {
			return err
		}
		err = mkdirFunc(d.String(), perm)
	}
	switch {
	case errors.Is(err, fs.ErrExist):
		return d.requireDir()
	case err != nil:
		return err
	}
	return d.chown(owner)
}

// Makes the given account the owner of d.
func (d companionDir) chown(owner account) error {
	return chownFunc(d.String(), owner.uid, owner.gid)
}

// Errors with ENOTDIR, as os.MkdirAll does, when the existing path is not a
// directory.
func (d companionDir) requireDir() error {
	info, err := statFunc(d.String())
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "mkdir", Path: d.String(), Err: syscall.ENOTDIR}
	}
	return nil
}

// Reports whether d is a directory on disk.
func (d companionDir) isDir() bool {
	info, err := statFunc(d.String())
	return err == nil && info.IsDir()
}

// Returns d's permission bits. Errors when d cannot be stat'd.
func (d companionDir) perm() (fs.FileMode, error) {
	info, err := statFunc(d.String())
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

// Returns the UID that owns d. Errors when d cannot be stat'd.
func (d companionDir) ownerUID() (procutil.UID, error) {
	info, err := statFunc(d.String())
	if err != nil {
		return 0, err
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat %q: no owner", d)
	}
	return procutil.UID(sys.Uid), nil
}

// A file handed to the companion, written through the package's swappable
// functions, as companionDir is.
type companionFile struct{ file textfile.File }

func (f companionFile) String() string { return f.file.String() }

// Returns the directory that holds f.
func (f companionFile) dir() companionDir { return companionDir{f.file.Dir()} }

// Writes content to f with mode perm, and makes the given account its owner.
// Errors when a step fails.
func (f companionFile) write(owner account, content []byte, perm fs.FileMode) error {
	if err := writeFileFunc(f.String(), content, perm); err != nil {
		return err
	}
	return chownFunc(f.String(), owner.uid, owner.gid)
}
