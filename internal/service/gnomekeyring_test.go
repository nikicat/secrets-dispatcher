package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gkPaths(t *testing.T) (dropIn, shadow string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	dropIn = filepath.Join(tmp, "systemd", "user", gkDropInDirName, gkDropInName)
	shadow = filepath.Join(tmp, "autostart", gkAutostartName)
	return dropIn, shadow
}

func TestDemoteGnomeKeyringWritesDropInAndShadow(t *testing.T) {
	dropIn, shadow := gkPaths(t)
	mockLookPath(t, defaultLookPath)
	calls := mockSystemctl(t)

	require.NoError(t, demoteGnomeKeyring())

	dropInContent, err := os.ReadFile(dropIn)
	require.NoError(t, err)
	assert.Contains(t, string(dropInContent), "ExecStart=\nExecStart=/usr/bin/gnome-keyring-daemon --foreground --components=pkcs11 --control-directory=%t/keyring")
	assert.Contains(t, string(dropInContent), "Managed by secrets-dispatcher")

	shadowContent, err := os.ReadFile(shadow)
	require.NoError(t, err)
	assert.Contains(t, string(shadowContent), "Hidden=true")
	assert.Contains(t, string(shadowContent), "Managed by secrets-dispatcher")

	assert.Equal(t, []string{"daemon-reload", "try-restart " + gkServiceUnit}, *calls)
}

func TestDemoteGnomeKeyringBacksUpUserAutostart(t *testing.T) {
	_, shadow := gkPaths(t)
	mockLookPath(t, defaultLookPath)
	mockSystemctl(t)

	// The user has their own autostart override — it must survive a
	// demote/restore round trip exactly.
	userContent := "[Desktop Entry]\nType=Application\nExec=my-custom-thing\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(shadow), 0755))
	require.NoError(t, os.WriteFile(shadow, []byte(userContent), 0644))

	require.NoError(t, demoteGnomeKeyring())

	backup, err := os.ReadFile(shadow + dbusBackupSuffix)
	require.NoError(t, err)
	assert.Equal(t, userContent, string(backup))

	require.NoError(t, restoreGnomeKeyring())

	restored, err := os.ReadFile(shadow)
	require.NoError(t, err)
	assert.Equal(t, userContent, string(restored))
	assert.NoFileExists(t, shadow+dbusBackupSuffix)
}

func TestRestoreGnomeKeyringRemovesOnlyOurFiles(t *testing.T) {
	dropIn, shadow := gkPaths(t)
	mockLookPath(t, defaultLookPath)
	mockSystemctl(t)

	require.NoError(t, demoteGnomeKeyring())
	require.NoError(t, restoreGnomeKeyring())

	assert.NoFileExists(t, dropIn)
	assert.NoFileExists(t, shadow)
	// The empty drop-in dir is cleaned up too.
	assert.NoDirExists(t, filepath.Dir(dropIn))
}

func TestRestoreGnomeKeyringNoopWhenNeverDemoted(t *testing.T) {
	gkPaths(t)
	calls := mockSystemctl(t)

	require.NoError(t, restoreGnomeKeyring())

	// Nothing was ours to remove — no systemctl churn either.
	assert.Empty(t, *calls)
}

func TestRestoreGnomeKeyringLeavesForeignShadowAlone(t *testing.T) {
	_, shadow := gkPaths(t)
	calls := mockSystemctl(t)

	foreign := "[Desktop Entry]\nType=Application\nHidden=true\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(shadow), 0755))
	require.NoError(t, os.WriteFile(shadow, []byte(foreign), 0644))

	require.NoError(t, restoreGnomeKeyring())

	content, err := os.ReadFile(shadow)
	require.NoError(t, err)
	assert.Equal(t, foreign, string(content), "a user-authored shadow must not be touched")
	assert.Empty(t, *calls)
}
