package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModeSet(t *testing.T) {
	var m Mode
	require.NoError(t, m.Set("full"))
	assert.Equal(t, ModeFull, m)
	assert.ErrorContains(t, m.Set("bogus"), `unknown mode "bogus"`)
	assert.Error(t, m.Set(""))
}
