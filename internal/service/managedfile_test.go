package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/textfile"
)

// Returns the layout under the XDG homes that tempXDG sets.
func tempLayout(t *testing.T) layout {
	t.Helper()
	tempXDG(t)
	l, err := locateLayout()
	require.NoError(t, err)
	return l
}

// Returns the takeover files under the XDG homes that tempXDG sets.
func tempTakeoverFiles(t *testing.T) takeoverFiles {
	t.Helper()
	return tempLayout(t).files
}

// Makes the steps' changes on a host without a unit manager.
func applySteps(t *testing.T, steps []step) {
	t.Helper()
	require.NoError(t, host{}.run(t.Context(), steps))
}

// Places the three takeover files as the dispatcher's own.
func (f takeoverFiles) place(t *testing.T) {
	t.Helper()
	applySteps(t, f.mask.writeSteps())
	applySteps(t, []step{f.dropIn.writeStep("x")})
	applySteps(t, f.shadow.writeSteps())
}

// Asserts that none of the dispatcher's takeover files is in place.
func (f takeoverFiles) assertAbsent(t *testing.T) {
	t.Helper()
	for _, file := range []takeoverFile{f.mask, f.dropIn, f.shadow} {
		assert.False(t, file.present(), file.label())
	}
}

func newManagedFile(t *testing.T) managedFile {
	t.Helper()
	return managedFile{
		file:    textfile.DirAt(t.TempDir(), "sub").File("f.conf"),
		name:    "test file",
		purpose: "write the test file",
		content: "ours\n",
	}
}

func readFile(t *testing.T, f textfile.File) string {
	t.Helper()
	content, err := f.Read()
	require.NoError(t, err)
	return content
}

func TestManagedFileRoundTripKeepsForeignFile(t *testing.T) {
	f := newManagedFile(t)
	require.NoError(t, f.file.Write("user's\n"))

	assert.Equal(t, []Change{
		{changeBackup, f.file.String(), "-> " + f.backup().String()},
		{changeWrite, f.file.String(), "write the test file"},
	}, changesOf(f.writeSteps()))
	applySteps(t, f.writeSteps())
	assert.Equal(t, "ours\n", readFile(t, f.file))
	assert.Equal(t, "user's\n", readFile(t, f.backup()))

	// A second write keeps the backup of the user's file.
	assert.Equal(t, []Change{{changeWrite, f.file.String(), "write the test file"}}, changesOf(f.writeSteps()))
	applySteps(t, f.writeSteps())
	assert.Equal(t, "user's\n", readFile(t, f.backup()))

	assert.Equal(t, []Change{{changeRestore, f.backup().String(), "-> " + f.file.String()}}, changesOf(f.restoreSteps()))
	applySteps(t, f.restoreSteps())
	assert.Equal(t, "user's\n", readFile(t, f.file))
	assert.False(t, f.backup().Exists())
}

func TestManagedFileRoundTripRemovesOurs(t *testing.T) {
	f := newManagedFile(t)

	assert.Equal(t, []Change{{changeWrite, f.file.String(), "write the test file"}}, changesOf(f.writeSteps()))
	applySteps(t, f.writeSteps())
	assert.Equal(t, "ours\n", readFile(t, f.file))

	assert.Equal(t, []Change{{changeRemove, f.file.String(), "test file"}}, changesOf(f.restoreSteps()))
	applySteps(t, f.restoreSteps())
	assert.False(t, f.file.Exists())
}

func TestManagedFileRestoreLeavesForeignFileAlone(t *testing.T) {
	f := newManagedFile(t)
	require.NoError(t, f.file.Write("user's\n"))

	assert.Empty(t, f.restoreSteps())
	assert.Equal(t, "user's\n", readFile(t, f.file))
}

func TestManagedDropInRoundTrip(t *testing.T) {
	f := tempTakeoverFiles(t).dropIn

	assert.Empty(t, f.restoreSteps())
	assert.Equal(t, Change{changeWrite, f.file.String(), f.purpose}, f.writeStep("any content\n").Change)
	applySteps(t, []step{f.writeStep("any content\n")})
	assert.True(t, f.present(), "recognised by its name, whatever its content")

	assert.Equal(t, []Change{{changeRemove, f.file.String(), "gnome-keyring demotion drop-in"}}, changesOf(f.restoreSteps()))
	applySteps(t, f.restoreSteps())
	assert.False(t, f.present())
}
