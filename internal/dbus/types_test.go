package dbus

import (
	"errors"
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
)

func TestCollectionOf(t *testing.T) {
	type parsed struct {
		ref CollectionRef
		ok  bool
	}
	tests := []struct {
		path dbus.ObjectPath
		want parsed
	}{
		{"/org/freedesktop/secrets/collection/default/i123", parsed{CollectionRef{Name: "default"}, true}},
		{"/org/freedesktop/secrets/collection/login/abc", parsed{CollectionRef{Name: "login"}, true}},
		{"/org/freedesktop/secrets/collection/mykeys", parsed{CollectionRef{Name: "mykeys"}, true}},
		{"/org/freedesktop/secrets/aliases/default", parsed{CollectionRef{Name: "default", Alias: true}, true}},
		{"/org/freedesktop/secrets/session/s1", parsed{}},
		{"", parsed{}},
	}
	for _, tt := range tests {
		ref, ok := CollectionOf(tt.path)
		assert.Equal(t, tt.want, parsed{ref, ok}, tt.path)
	}
}

func TestParseCollectionAndItemPath(t *testing.T) {
	type parsed struct {
		ref CollectionRef
		ok  bool
	}
	tests := []struct {
		path             string
		collection, item parsed
	}{
		{"/org/freedesktop/secrets/collection/login", parsed{CollectionRef{Name: "login"}, true}, parsed{}},
		{"/org/freedesktop/secrets/aliases/default", parsed{CollectionRef{Name: "default", Alias: true}, true}, parsed{}},
		{"/org/freedesktop/secrets/collection/login/i1", parsed{}, parsed{CollectionRef{Name: "login"}, true}},
		{"/org/freedesktop/secrets/aliases/default/i1", parsed{}, parsed{CollectionRef{Name: "default", Alias: true}, true}},
		{"/org/freedesktop/secrets/collection/login/", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/collection//i1", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/collection/login/i1/x", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/collection/", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/aliases/", parsed{}, parsed{}},
		{"/org/freedesktop/secrets", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/session/s1", parsed{}, parsed{}},
	}
	for _, tt := range tests {
		path := dbus.ObjectPath(tt.path)
		ref, ok := ParseCollectionPath(path)
		assert.Equal(t, tt.collection, parsed{ref, ok}, "ParseCollectionPath(%q)", tt.path)
		ref, ok = ParseItemPath(path)
		assert.Equal(t, tt.item, parsed{ref, ok}, "ParseItemPath(%q)", tt.path)
	}
}

func TestIsPromptPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/org/freedesktop/secrets/prompt/p1", true},
		{"/org/freedesktop/secrets/prompt/u42", true},
		{"/org/freedesktop/secrets/prompt/sub/path", true},

		// The subtree root itself is not a prompt object
		{"/org/freedesktop/secrets/prompt", false},
		{"/org/freedesktop/secrets/prompt/", false},

		{"/org/freedesktop/secrets", false},
		{"/org/freedesktop/secrets/collection/default", false},
		{"/org/freedesktop/secrets/promptx/p1", false},
		{"/completely/different/path", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, IsPromptPath(dbus.ObjectPath(tc.path)))
		})
	}
}

func TestSessionAndPromptPaths(t *testing.T) {
	assert.Equal(t, dbus.ObjectPath("/org/freedesktop/secrets/session/7"), SessionPath(7))
	assert.Equal(t, dbus.ObjectPath("/org/freedesktop/secrets/prompt/p1"), PromptPath("p1"))
	assert.True(t, IsPromptPath(PromptPath("p1")))
}

func TestAsDBusError(t *testing.T) {
	byPointer := NoSession.WithMessage("gone")
	byValue := *IsLocked.WithMessage("locked")
	tests := []struct {
		name string
		err  error
		want *dbus.Error
	}{
		{"pointer", byPointer, byPointer},
		{"value", byValue, IsLocked.WithMessage("locked")},
		{"wrapped value", fmt.Errorf("call: %w", byValue), IsLocked.WithMessage("locked")},
		{"other", errors.New("boom"), Failed.WithMessage("boom")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, AsDBusError(tc.err))
		})
	}
}

func TestAttributes_String(t *testing.T) {
	attrs := Attributes{"service": "gh", "account": "me", "a": ""}
	for range 10 {
		assert.Equal(t, "a=, account=me, service=gh", attrs.String())
	}
	assert.Empty(t, Attributes(nil).String())
}

func TestItemPropertiesRoundTrip(t *testing.T) {
	props := ItemProperties{Label: "token", Attributes: Attributes{"service": "gh"}}
	got, err := ParseItemProperties(props.Variants())
	assert.NoError(t, err)
	assert.Equal(t, props, got)
}

func TestParseItemPropertiesReadsMissingAsEmpty(t *testing.T) {
	got, err := ParseItemProperties(map[string]dbus.Variant{"Label": dbus.MakeVariant("unqualified")})
	assert.NoError(t, err)
	assert.Equal(t, ItemProperties{}, got)
}

func TestParseItemPropertiesRejectsMistypedValues(t *testing.T) {
	tests := []struct {
		name  string
		props map[string]dbus.Variant
		want  string
	}{
		{"label", map[string]dbus.Variant{ItemLabel: dbus.MakeVariant(42)}, "property org.freedesktop.Secret.Item.Label has type i, not s"},
		{"attributes", map[string]dbus.Variant{ItemAttributes: dbus.MakeVariant("not a dict")}, "property org.freedesktop.Secret.Item.Attributes has type s, not a{ss}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseItemProperties(tc.props)
			assert.EqualError(t, err, tc.want)
			assert.Equal(t, string(InvalidArgs), AsDBusError(err).Name)
		})
	}
}
