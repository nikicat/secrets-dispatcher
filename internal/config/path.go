package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// The location of a config file, or empty for the default one.
type Path string

const configFileName = "config.yaml"

// The default config file as help texts spell it, with $XDG_CONFIG_HOME
// left unexpanded.
var DefaultPathHelp = defaultConfigFile("$XDG_CONFIG_HOME")

func defaultConfigFile(base string) string {
	return filepath.Join(base, appName, configFileName)
}

// Returns the default config file, under the XDG config home. It may be
// missing. Errors when there is no XDG config home.
func DefaultSource() (Source, error) {
	base, err := ConfigHome()
	if err != nil {
		return Source{}, fmt.Errorf("find the config file: %w", err)
	}
	return Source{File: File{path: defaultConfigFile(base)}, optional: true}, nil
}

// Parses a config file location and resolves a relative one against the
// current directory. An empty text is unset.
func (p *Path) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*p = ""
		return nil
	}
	abs, err := filepath.Abs(string(text))
	if err != nil {
		return fmt.Errorf("resolve config file %q: %w", text, err)
	}
	*p = Path(abs)
	return nil
}

// Returns the file p names, or DefaultSource() when p is empty. Errors when p
// is empty and there is no default.
func (p Path) OrDefault() (Source, error) {
	if p != "" {
		return Source{File: File{path: string(p)}}, nil
	}
	return DefaultSource()
}

// Returns the settings in the file p names, or in the default one, as a draft
// that saves back to that file. A missing file gives empty settings. Errors as
// OrDefault does, and when the file fails to load.
func (p Path) Draft() (Draft, error) {
	source, err := p.OrDefault()
	if err != nil {
		return Draft{}, err
	}
	cfg, err := source.File.LoadOrEmpty()
	if err != nil {
		return Draft{}, fmt.Errorf("load config: %w", err)
	}
	return Draft{cfg, source.File}, nil
}

// A config file to load. A file the user named must exist; the default one
// may be missing.
type Source struct {
	File     File
	optional bool
}

// Returns the config in s.File, or an empty config when that file may be
// missing and is. Errors as File.Load does.
func (s Source) Load() (*Config, error) {
	if s.optional {
		return s.File.LoadOrEmpty()
	}
	return s.File.Load()
}

// A config file location that is never empty.
type File struct {
	path string
	// True if only the owner may read the file and a directory created for it.
	private bool
}

func (f File) String() string { return f.path }

// Returns the config in f. Unknown keys are rejected, and each error names the
// file. Errors with os.ErrNotExist when f does not exist.
func (f File) Load() (*Config, error) {
	data, err := f.ReadFile()
	if err != nil {
		return nil, err
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", f, err)
	}
	return &cfg, nil
}

// Returns the config in f, or an empty config when f does not exist.
func (f File) LoadOrEmpty() (*Config, error) {
	cfg, err := f.Load()
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	return cfg, err
}

// Writes cfg to f, creating its directory.
func (f File) Save(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := f.CreateDir(); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return os.WriteFile(f.path, data, f.modes().file)
}

// Returns the contents of f.
func (f File) ReadFile() ([]byte, error) {
	return os.ReadFile(f.path)
}

// Creates the directory that holds f.
func (f File) CreateDir() error {
	return os.MkdirAll(filepath.Dir(f.path), f.modes().dir)
}

// The modes that a config file and a directory created for it get.
type fileModes struct{ file, dir os.FileMode }

func (f File) modes() fileModes {
	if f.private {
		return fileModes{0600, 0700}
	}
	return fileModes{0644, 0755}
}

type Draft struct {
	*Config
	file File
}

func (d Draft) File() File { return d.file }

// Returns a draft of the same settings that saves to dst.
func (d Draft) To(dst File) Draft { return Draft{d.Config, dst} }

func (d Draft) Save() error { return d.file.Save(d.Config) }

// Deletes f, if it exists.
func (f File) Remove() error {
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
