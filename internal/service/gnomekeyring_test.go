package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type gkFixture struct {
	files takeoverFiles
	units *fakeUnits
	host  host
}

func newGKFixture(t *testing.T) gkFixture {
	t.Helper()
	files := tempTakeoverFiles(t)
	units := &fakeUnits{}
	return gkFixture{
		files: files,
		units: units,
		host:  host{units: units, files: files},
	}
}

func TestDemoteGnomeKeyringWritesDropInAndShadow(t *testing.T) {
	f := newGKFixture(t)
	mockLookPath(t, defaultLookPath)

	require.NoError(t, f.host.demoteGnomeKeyring(t.Context()))

	dropInContent, err := f.files.dropIn.file.Read()
	require.NoError(t, err)
	assert.Contains(t, dropInContent, "ExecStart=\nExecStart=/usr/bin/gnome-keyring-daemon --foreground --components=pkcs11 --control-directory=%t/keyring")
	assert.Contains(t, dropInContent, "Managed by secrets-dispatcher")

	shadowContent, err := f.files.shadow.file.Read()
	require.NoError(t, err)
	assert.Contains(t, shadowContent, "Hidden=true")
	assert.Contains(t, shadowContent, "Managed by secrets-dispatcher")

	assert.Equal(t, []unitCall{{opReload, ""}, {opTryRestart, gkServiceUnit}}, f.units.calls)
}

func TestGnomeKeyringManagedComment(t *testing.T) {
	assert.Equal(t, "# Managed by secrets-dispatcher; removed by `secrets-dispatcher service uninstall`.\n", gkManagedComment,
		"an installed autostart shadow is recognised by its content")
}

func TestDemoteGnomeKeyringBacksUpUserAutostart(t *testing.T) {
	f := newGKFixture(t)
	mockLookPath(t, defaultLookPath)

	// The user has their own autostart override — it must survive a
	// demote/restore round trip exactly.
	userContent := "[Desktop Entry]\nType=Application\nExec=my-custom-thing\n"
	shadow := f.files.shadow
	require.NoError(t, shadow.file.Write(userContent))

	require.NoError(t, f.host.demoteGnomeKeyring(t.Context()))

	assert.Equal(t, userContent, readFile(t, shadow.backup()))

	require.NoError(t, f.host.restoreGnomeKeyring(t.Context()))

	assert.Equal(t, userContent, readFile(t, shadow.file))
	assert.False(t, shadow.backup().Exists())
}

func TestRestoreGnomeKeyringRemovesOnlyOurFiles(t *testing.T) {
	f := newGKFixture(t)
	mockLookPath(t, defaultLookPath)

	require.NoError(t, f.host.demoteGnomeKeyring(t.Context()))
	require.NoError(t, f.host.restoreGnomeKeyring(t.Context()))

	assert.False(t, f.files.dropIn.file.Exists())
	assert.False(t, f.files.shadow.file.Exists())
	// The empty drop-in dir is cleaned up too.
	assert.NoDirExists(t, f.files.dropIn.file.Dir().String())
}

func TestRestoreGnomeKeyringNoopWhenNeverDemoted(t *testing.T) {
	f := newGKFixture(t)

	require.NoError(t, f.host.restoreGnomeKeyring(t.Context()))

	// Nothing was ours to remove — no unit manager churn either.
	assert.Empty(t, f.units.calls)
}

func TestRestoreGnomeKeyringLeavesForeignShadowAlone(t *testing.T) {
	f := newGKFixture(t)

	foreign := "[Desktop Entry]\nType=Application\nHidden=true\n"
	require.NoError(t, f.files.shadow.file.Write(foreign))

	require.NoError(t, f.host.restoreGnomeKeyring(t.Context()))

	assert.Equal(t, foreign, readFile(t, f.files.shadow.file), "a user-authored shadow must not be touched")
	assert.Empty(t, f.units.calls)
}
