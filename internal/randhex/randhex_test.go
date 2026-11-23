package randhex

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestString(t *testing.T) {
	a, err := String(16)
	require.NoError(t, err)
	assert.Len(t, a, 32)
	_, err = hex.DecodeString(a)
	assert.NoError(t, err, "String(16) = %q, not hex", a)
	b, err := String(16)
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "two calls returned the same value")
}
