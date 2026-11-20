package textfile

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileLifecycle(t *testing.T) {
	d := DirAt(t.TempDir(), "sub")
	f := d.File("f.conf")
	assert.Equal(t, d, f.Dir())
	assert.False(t, f.Exists())

	require.NoError(t, f.Write("one\n"), "creates the missing directory")
	content, err := f.Read()
	require.NoError(t, err)
	assert.Equal(t, "one\n", content)

	backup := f.WithSuffix(".bak")
	require.NoError(t, f.RenameTo(backup))
	assert.False(t, f.Exists())
	assert.True(t, backup.Exists())

	assert.Error(t, d.Remove(), "a directory that is not empty stays")
	require.NoError(t, backup.Remove())
	assert.ErrorIs(t, backup.Remove(), fs.ErrNotExist)
	require.NoError(t, d.Remove())
	assert.NoDirExists(t, d.String())
}
