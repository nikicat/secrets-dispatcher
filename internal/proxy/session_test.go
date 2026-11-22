package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// A session that closes while its call waits on the backend must not get its
// secret back in plain text.
func TestSessionManagerRefusesUnknownSession(t *testing.T) {
	m := NewSessionManager()
	const closed = "/org/freedesktop/secrets/session/1"

	_, err := m.ForClient(closed, dbustypes.Secret{Value: []byte("hunter2")})
	require.ErrorIs(t, err, errUnknownSession)

	_, err = m.ForUpstream(dbustypes.Secret{Session: closed, Value: []byte("hunter2")})
	assert.ErrorIs(t, err, errUnknownSession)
}
