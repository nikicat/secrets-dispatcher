package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/home/u/.state")
	dir, err := LookupStateDir()
	if err != nil {
		t.Fatalf("LookupStateDir: %v", err)
	}
	if got, want := dir.Cookie(), "/home/u/.state/secrets-dispatcher/.cookie"; got != want {
		t.Errorf("Cookie() = %q, want %q", got, want)
	}
}

// Returns a state directory whose cookie file holds content.
func stateDirWithCookie(t *testing.T, content string) StateDir {
	t.Helper()
	dir := StateDir(t.TempDir())
	if err := os.WriteFile(dir.Cookie(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadToken(t *testing.T) {
	token, err := stateDirWithCookie(t, "my-secret-token\n").ReadToken()
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if token != "my-secret-token" {
		t.Errorf("ReadToken() = %q, want %q", string(token), "my-secret-token")
	}
}

func TestReadTokenMissing(t *testing.T) {
	_, err := StateDir(t.TempDir()).ReadToken()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadToken() error = %v, want fs.ErrNotExist", err)
	}
}

func TestReadTokenEmpty(t *testing.T) {
	dir := stateDirWithCookie(t, "\n")
	_, err := dir.ReadToken()
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadToken() error = %v, want an empty-file error", err)
	}
	if want := dir.Cookie(); !strings.Contains(err.Error(), want) {
		t.Errorf("ReadToken() error = %v, want it to name %s", err, want)
	}
}

func TestWriteTokenRoundTrip(t *testing.T) {
	dir := StateDir(filepath.Join(t.TempDir(), "nested", "state"))
	if err := dir.WriteToken("my-secret-token"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	token, err := dir.ReadToken()
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if token != "my-secret-token" {
		t.Errorf("ReadToken() = %q, want %q", string(token), "my-secret-token")
	}
	for path, want := range map[string]os.FileMode{string(dir): 0o700, dir.Cookie(): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != want {
			t.Errorf("%s mode = %v, want %v", path, perm, want)
		}
	}
}

func TestStateDirOrDefault(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/home/u/.state")
	for _, tc := range []struct{ dir, want StateDir }{
		{"/srv/state", "/srv/state"},
		{"", "/home/u/.state/secrets-dispatcher"},
	} {
		got, err := tc.dir.OrDefault()
		if err != nil {
			t.Fatalf("StateDir(%q).OrDefault: %v", tc.dir, err)
		}
		if got != tc.want {
			t.Errorf("StateDir(%q).OrDefault() = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestLoadStateDir(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       StateDir
		wantErr    string
	}{
		{name: "absolute", yaml: "state_dir: /var/lib/sd\n", want: "/var/lib/sd"},
		{name: "empty", yaml: "state_dir: \"\"\n", want: ""},
		{name: "relative", yaml: "state_dir: state\n", wantErr: `state directory must be an absolute path: "state"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			os.WriteFile(path, []byte(tc.yaml), 0o644)
			cfg, err := Load(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load() error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.StateDir != tc.want {
				t.Errorf("StateDir = %q, want %q", cfg.StateDir, tc.want)
			}
		})
	}
}

func TestStateDirSet(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for _, tc := range []struct {
		value string
		want  StateDir
	}{
		{value: "/var/lib/sd", want: "/var/lib/sd"},
		{value: "state", want: StateDir(filepath.Join(wd, "state"))},
	} {
		var d StateDir
		if err := d.Set(tc.value); err != nil {
			t.Fatalf("Set(%q): %v", tc.value, err)
		}
		if d != tc.want {
			t.Errorf("Set(%q) = %q, want %q", tc.value, d, tc.want)
		}
	}
}
