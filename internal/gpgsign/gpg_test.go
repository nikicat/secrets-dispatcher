package gpgsign

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRealGPG(t *testing.T) {
	origPath := os.Getenv("PATH")

	t.Run("gpg exists and is not self", func(t *testing.T) {
		dir := t.TempDir()
		gpgPath := filepath.Join(dir, "gpg")
		if err := os.WriteFile(gpgPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		t.Setenv("PATH", dir+":"+origPath)

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
		t.Setenv("PATH", emptyDir)

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

		t.Setenv("PATH", dir)

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
		t.Setenv("PATH", dirSelf+":"+dirReal)

		got, err := FindRealGPG()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != realGPG {
			t.Errorf("FindRealGPG() = %q, want %q", got, realGPG)
		}
	})
}
