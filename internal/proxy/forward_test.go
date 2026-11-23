package proxy

import (
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"

	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

func TestHeaderAccessors(t *testing.T) {
	msg := dbus.Message{Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldSender:    dbus.MakeVariant(":1.42"),
		dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath(dbustypes.ServicePath)),
		dbus.FieldInterface: dbus.MakeVariant(string(dbustypes.ServiceInterface)),
		dbus.FieldMember:    dbus.MakeVariant("OpenSession"),
	}}
	assert.Equal(t, sender.UniqueName(":1.42"), senderOf(msg))
	assert.Equal(t, dbus.ObjectPath(dbustypes.ServicePath), dbustypes.PathOf(msg))
	assert.Equal(t, dbustypes.ServiceInterface, interfaceOf(msg))
	assert.Equal(t, "OpenSession", memberOf(msg))

	var bare dbus.Message
	assert.Empty(t, senderOf(bare))
	assert.Empty(t, dbustypes.PathOf(bare))
	assert.Empty(t, interfaceOf(bare))
	assert.Empty(t, memberOf(bare))
}
