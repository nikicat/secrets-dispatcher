// Package textfile holds the os calls on the small text files that the setup
// code writes into the user's home, so that each file is a value, not a path.
package textfile

import (
	"io/fs"
	"os"
	"path/filepath"
)

// A directory at a fixed path.
type Dir struct{ path string }

// Returns the directory at the path that joins the given elements.
func DirAt(elem ...string) Dir { return Dir{filepath.Join(elem...)} }

func (d Dir) String() string { return d.path }

// Creates d and its missing parents with mode perm. Errors when a step fails.
func (d Dir) Create(perm fs.FileMode) error { return os.MkdirAll(d.path, perm) }

// Returns the directory that holds d. The root is its own parent.
func (d Dir) Parent() Dir { return Dir{filepath.Dir(d.path)} }

// Returns the subdirectory called name in d.
func (d Dir) Dir(name string) Dir { return Dir{filepath.Join(d.path, name)} }

// Returns the file called name in d.
func (d Dir) File(name string) File { return File{filepath.Join(d.path, name)} }

// Removes d when it is empty. Errors when d is not empty or cannot be removed.
func (d Dir) Remove() error { return os.Remove(d.path) }

// A text file at a fixed path.
type File struct{ path string }

func (f File) String() string { return f.path }

// Returns the directory that holds f.
func (f File) Dir() Dir { return Dir{filepath.Dir(f.path)} }

// Returns the file whose path is f's with suffix appended.
func (f File) WithSuffix(suffix string) File { return File{f.path + suffix} }

// Reports whether f is on disk.
func (f File) Exists() bool {
	_, err := os.Stat(f.path)
	return err == nil
}

// Returns f's content. Errors when f cannot be read.
func (f File) Read() (string, error) {
	data, err := os.ReadFile(f.path)
	return string(data), err
}

// Writes content to f, readable by everyone, creating its missing parent
// directories first. Errors when a step fails.
func (f File) Write(content string) error {
	if err := f.Dir().Create(0o755); err != nil {
		return err
	}
	return os.WriteFile(f.path, []byte(content), 0o644)
}

// Removes f. Errors when f is absent (fs.ErrNotExist) or cannot be removed.
func (f File) Remove() error { return os.Remove(f.path) }

// Moves f to the given file, and replaces any file that is already there.
// Errors when the move fails.
func (f File) RenameTo(to File) error { return os.Rename(f.path, to.path) }
