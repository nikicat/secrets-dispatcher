package procutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUID(t *testing.T) {
	uid, err := ParseUID("1001")
	require.NoError(t, err)
	assert.Equal(t, UID(1001), uid)

	for _, bad := range []string{"", "-1", "alice", "4294967296"} {
		_, err := ParseUID(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseGID(t *testing.T) {
	gid, err := ParseGID("1001")
	require.NoError(t, err)
	assert.Equal(t, GID(1001), gid)

	_, err = ParseGID("staff")
	assert.Error(t, err)
}
