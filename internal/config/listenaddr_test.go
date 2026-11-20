package config

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// web/src/App.svelte reads the "token" and "request" parameters of these links.
func TestListenAddrWebUILinks(t *testing.T) {
	addr := ListenAddr("127.0.0.1:8484")
	for _, tc := range []struct {
		link, param, value string
	}{
		{addr.LoginURL("a.b-c_d"), "token", "a.b-c_d"},
		{addr.RequestURL("0123abcd"), "request", "0123abcd"},
	} {
		u, err := url.Parse(tc.link)
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1:8484", u.Host)
		assert.Equal(t, "/", u.Path)
		assert.Equal(t, tc.value, u.Query().Get(tc.param))
	}
}

func TestListenAddrSet(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{"127.0.0.1:8484", true},
		{"localhost:0", true},
		{"[::1]:8484", true},
		{"8484", false},
		{"http://127.0.0.1:8484", false},
		{"", false},
	} {
		var a ListenAddr
		err := a.Set(tc.value)
		if !tc.ok {
			assert.Error(t, err, tc.value)
			continue
		}
		require.NoError(t, err, tc.value)
		assert.Equal(t, ListenAddr(tc.value), a)
	}
}

// An empty listen: key is how a marshalled Config spells an unset address.
func TestListenAddrUnmarshalEmptyIsUnset(t *testing.T) {
	var a ListenAddr
	require.NoError(t, a.UnmarshalText(nil))
	assert.Equal(t, ListenAddr(""), a)
}
