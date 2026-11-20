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
const DefaultPathHelp = "$XDG_CONFIG_HOME/" + appName + "/" + configFileName

// Returns the default config file, under the XDG config home. It may be
// missing. Errors when there is no XDG config home.
func DefaultSource() (Source, error) {
	base, err := ConfigHome()
	if err != nil {
		return Source{}, fmt.Errorf("find the config file: %w", err)
	}
	return Source{File: File{filepath.Join(base, appName, configFileName)}, optional: true}, nil
}

func (p Path) String() string { return string(p) }

// Sets the path from a command-line flag value. Resolves a relative path
// against the current directory. An empty value stays empty.
func (p *Path) Set(value string) error {
	abs, err := absOrEmpty(value)
	if err != nil {
		return err
	}
	*p = Path(abs)
	return nil
}

// Returns value resolved against the current directory, or empty when value
// is empty.
func absOrEmpty(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return filepath.Abs(value)
}

// Returns the file p names, or DefaultSource() when p is empty. Errors when p
// is empty and there is no default.
func (p Path) OrDefault() (Source, error) {
	if p != "" {
		return Source{File: File{string(p)}}, nil
	}
	return DefaultSource()
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
type File struct{ path string }

func (f File) String() string { return f.path }

// Returns f as the value of a --config flag.
func (f File) Path() Path { return Path(f.path) }

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
	return os.WriteFile(f.path, data, 0644)
}

// Returns the contents of f.
func (f File) ReadFile() ([]byte, error) {
	return os.ReadFile(f.path)
}

// Creates the directory that holds f.
func (f File) CreateDir() error { return f.createDir(0755) }

func (f File) createDir(perm os.FileMode) error {
	return os.MkdirAll(filepath.Dir(f.path), perm)
}

// Copies f to dst, creating dst's directory. Only the owner can read the copy.
// Errors with os.ErrNotExist when f does not exist.
func (f File) CopyTo(dst File) error {
	data, err := f.ReadFile()
	if err != nil {
		return fmt.Errorf("read config %s: %w", f, err)
	}
	if err := dst.createDir(0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(dst.path, data, 0600); err != nil {
		return fmt.Errorf("write config %s: %w", dst, err)
	}
	return nil
}

// Deletes f, if it exists.
func (f File) Remove() error {
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
