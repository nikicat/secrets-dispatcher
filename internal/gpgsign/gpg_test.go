package gpgsign

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRealGPG(t *testing.T) {
	// Save and restore PATH around each subtest.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) }) //nolint:errcheck

	t.Run("gpg exists and is not self", func(t *testing.T) {
		dir := t.TempDir()
		gpgPath := filepath.Join(dir, "gpg")
		if err := os.WriteFile(gpgPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		os.Setenv("PATH", dir+":"+origPath) //nolint:errcheck

		got, err := FindRealGPG()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != gpgPath {
			t.Errorf("FindRealGPG() = %q, want %q", got, gpgPath)
		}
	})

	t.Run("gpg not found at all", func(t *testing.T) {
		emptyDir := t.TempDir()
		os.Setenv("PATH", emptyDir) //nolint:errcheck

		_, err := FindRealGPG()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("only self in PATH under gpg name", func(t *testing.T) {
		// Copy the test executable to a temp dir as "gpg" so SameFile matches.
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}

		selfData, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		fakeSelf := filepath.Join(dir, "gpg")
		if err := os.WriteFile(fakeSelf, selfData, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create a hard link so os.SameFile returns true.
		linkPath := filepath.Join(dir, "gpg")
		// Remove what we wrote above; use a hard link to get inode equality.
		if err := os.Remove(linkPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(self, linkPath); err != nil {
			// Hard link may fail across filesystems; skip rather than fail.
			t.Skip("hard link not supported across filesystems:", err)
		}

		os.Setenv("PATH", dir) //nolint:errcheck

		_, err = FindRealGPG()
		if err == nil {
			t.Fatal("expected error when only self is found, got nil")
		}
	})

	t.Run("multiple gpg binaries first is self second is real", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}

		dirSelf := t.TempDir()
		linkPath := filepath.Join(dirSelf, "gpg")
		if err := os.Link(self, linkPath); err != nil {
			t.Skip("hard link not supported across filesystems:", err)
		}

		dirReal := t.TempDir()
		realGPG := filepath.Join(dirReal, "gpg")
		if err := os.WriteFile(realGPG, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		// Self is first in PATH; real gpg is second.
		os.Setenv("PATH", dirSelf+":"+dirReal) //nolint:errcheck

		got, err := FindRealGPG()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != realGPG {
			t.Errorf("FindRealGPG() = %q, want %q", got, realGPG)
		}
	})
}

func TestExtractKeyID(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "combined -bsau flag",
			args: []string{"--status-fd=2", "-bsau", "846FFFEFC1039264"},
			want: "846FFFEFC1039264",
		},
		{
			name: "separate -u flag",
			args: []string{"--status-fd=2", "-bsa", "-u", "846FFFEFC1039264"},
			want: "846FFFEFC1039264",
		},
		{
			name: "only -u flag",
			args: []string{"-u", "ABCD1234"},
			want: "ABCD1234",
		},
		{
			name: "no key ID",
			args: []string{"--status-fd=2", "-bsa"},
			want: "",
		},
		{
			name: "empty args",
			args: []string{},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractKeyID(tc.args)
			if got != tc.want {
				t.Errorf("extractKeyID(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
