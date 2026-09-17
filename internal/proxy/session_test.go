package proxy

import (
	"log/slog"
	"testing"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionManagerRemotePathsAreUniqueAcrossInstances(t *testing.T) {
	first := NewSessionManager().nextRemotePath()
	second := NewSessionManager().nextRemotePath()

	require.True(t, first.IsValid())
	require.True(t, second.IsValid())
	assert.NotEqual(t, first, second)
}

func TestSessionManagerRemotePathsAreUniqueWithinInstance(t *testing.T) {
	manager := NewSessionManager()
	first := manager.nextRemotePath()
	second := manager.nextRemotePath()

	assert.NotEqual(t, first, second)
}

func TestCollectionCreateItemRejectsUnknownSessionBeforeApproval(t *testing.T) {
	handler := &CollectionHandler{
		sessions: NewSessionManager(),
		logger:   logging.New(slog.LevelDebug, "test"),
	}
	msg := dbus.Message{Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldPath: dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")),
	}}
	secret := dbustypes.Secret{Session: "/org/freedesktop/secrets/session/stale"}

	_, _, dbusErr := handler.CreateItem(msg, nil, secret, false)

	require.NotNil(t, dbusErr)
	assert.Equal(t, dbustypes.ErrNoSession, dbusErr.Name)
}
