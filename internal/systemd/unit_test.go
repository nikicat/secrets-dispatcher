package systemd

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserUnitDirPaths(t *testing.T) {
	d := UserUnitDir("/home/u/.config")
	assert.Equal(t, "/home/u/.config/systemd/user", d.String())
	assert.Equal(t, "/home/u/.config/systemd/user/a.service", d.UnitFile("a.service").String())
	assert.Equal(t, "/home/u/.config/systemd/user/a.service.d/50-x.conf", d.DropIn("a.service", "50-x.conf").String())
}

func TestUnitFileLifecycle(t *testing.T) {
	d := UserUnitDir(t.TempDir())
	f := d.UnitFile("a.service")
	assert.False(t, f.Exists())

	require.NoError(t, f.Write("[Unit]\n"))
	assert.True(t, f.Exists())
	content, err := f.Read()
	require.NoError(t, err)
	assert.Equal(t, "[Unit]\n", content)

	require.NoError(t, f.Remove())
	assert.False(t, f.Exists())
	assert.ErrorIs(t, f.Remove(), fs.ErrNotExist)
}

func TestDropInLifecycle(t *testing.T) {
	d := UserUnitDir(t.TempDir())
	f := d.DropIn("a.service", "50-x.conf")
	assert.False(t, f.Exists())

	require.NoError(t, f.Write("[Service]\n"), "creates the drop-in directory")
	content, err := f.Read()
	require.NoError(t, err)
	assert.Equal(t, "[Service]\n", content)

	require.NoError(t, f.Remove())
	assert.False(t, f.Exists())
	assert.NoDirExists(t, f.Dir().String(), "the emptied drop-in directory goes too")
	assert.ErrorIs(t, f.Remove(), fs.ErrNotExist)
}
