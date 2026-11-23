package gpgsign

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindRealGPG(t *testing.T) {
	origPath := os.Getenv("PATH")

	t.Run("gpg exists and is not self", func(t *testing.T) {
		dir := t.TempDir()
		gpgPath := fakeGPG(t, dir)

		t.Setenv("PATH", dir+":"+origPath)

		got, err := FindRealGPG()
		require.NoError(t, err)
		assert.Equal(t, gpgPath, got)
	})

	t.Run("gpg not found at all", func(t *testing.T) {
		emptyDir := t.TempDir()
		t.Setenv("PATH", emptyDir)

		_, err := FindRealGPG()
		assert.Error(t, err)
	})

	t.Run("only self in PATH under gpg name", func(t *testing.T) {
		dir := t.TempDir()
		selfAsGPG(t, dir)

		t.Setenv("PATH", dir)

		_, err := FindRealGPG()
		assert.Error(t, err, "only self is found")
	})

	t.Run("multiple gpg binaries first is self second is real", func(t *testing.T) {
		dirSelf := t.TempDir()
		selfAsGPG(t, dirSelf)

		dirReal := t.TempDir()
		realGPG := fakeGPG(t, dirReal)

		// Self is first in PATH; real gpg is second.
		t.Setenv("PATH", dirSelf+":"+dirReal)

		got, err := FindRealGPG()
		require.NoError(t, err)
		assert.Equal(t, realGPG, got)
	})
}

// Returns the path of a new executable script named gpg in the directory.
func fakeGPG(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "gpg")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	return path
}

// Hard-links the test binary as gpg in the directory, so os.SameFile matches
// it, and skips the test where the filesystem refuses the link.
func selfAsGPG(t *testing.T, dir string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	if err := os.Link(self, filepath.Join(dir, "gpg")); err != nil {
		t.Skip("hard link not supported:", err)
	}
}
