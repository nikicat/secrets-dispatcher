package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/textfile"
)

// Returns the takeover files under a temporary XDG config and data home.
func tempTakeoverFiles(t *testing.T) takeoverFiles {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	files, err := locateTakeoverFiles()
	require.NoError(t, err)
	return files
}

// Places the three takeover files as the dispatcher's own.
func (f takeoverFiles) place(t *testing.T) {
	t.Helper()
	require.NoError(t, f.mask.write(NarrateMilestones))
	require.NoError(t, f.dropIn.write(NarrateMilestones, "x"))
	require.NoError(t, f.shadow.write(NarrateMilestones))
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
		{"backup", f.file.String(), "-> " + f.backup().String()},
		{"write", f.file.String(), "write the test file"},
	}, f.planWrite())
	require.NoError(t, f.write(NarrateMilestones))
	assert.Equal(t, "ours\n", readFile(t, f.file))
	assert.Equal(t, "user's\n", readFile(t, f.backup()))

	// A second write keeps the backup of the user's file.
	assert.Equal(t, []Change{{"write", f.file.String(), "write the test file"}}, f.planWrite())
	require.NoError(t, f.write(NarrateMilestones))
	assert.Equal(t, "user's\n", readFile(t, f.backup()))

	assert.Equal(t, []Change{{"restore", f.backup().String(), "-> " + f.file.String()}}, f.planRestore())
	restored, err := f.restore(NarrateMilestones)
	require.NoError(t, err)
	assert.True(t, restored)
	assert.Equal(t, "user's\n", readFile(t, f.file))
	assert.False(t, f.backup().Exists())
}

func TestManagedFileRoundTripRemovesOurs(t *testing.T) {
	f := newManagedFile(t)

	assert.Equal(t, []Change{{"write", f.file.String(), "write the test file"}}, f.planWrite())
	require.NoError(t, f.write(NarrateMilestones))
	assert.Equal(t, "ours\n", readFile(t, f.file))

	assert.Equal(t, []Change{{"remove", f.file.String(), "test file"}}, f.planRestore())
	restored, err := f.restore(NarrateMilestones)
	require.NoError(t, err)
	assert.True(t, restored)
	assert.False(t, f.file.Exists())
}

func TestManagedFileRestoreLeavesForeignFileAlone(t *testing.T) {
	f := newManagedFile(t)
	require.NoError(t, f.file.Write("user's\n"))

	assert.Empty(t, f.planRestore())
	restored, err := f.restore(NarrateMilestones)
	require.NoError(t, err)
	assert.False(t, restored)
	assert.Equal(t, "user's\n", readFile(t, f.file))
}

func TestManagedDropInRoundTrip(t *testing.T) {
	f := tempTakeoverFiles(t).dropIn

	assert.Empty(t, f.planRestore())
	assert.Equal(t, []Change{{"write", f.file.String(), f.purpose}}, f.planWrite())
	require.NoError(t, f.write(NarrateMilestones, "any content\n"))
	assert.True(t, f.present(), "recognised by its name, whatever its content")

	assert.Equal(t, []Change{{"remove", f.file.String(), "gnome-keyring demotion drop-in"}}, f.planRestore())
	restored, err := f.restore(NarrateMilestones)
	require.NoError(t, err)
	assert.True(t, restored)
	assert.False(t, f.present())
}
