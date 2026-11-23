package config

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuthToken(t *testing.T) {
	token, err := NewAuthToken()
	require.NoError(t, err)
	raw, err := hex.DecodeString(string(token))
	require.NoError(t, err, "NewAuthToken() = %q, not hex", string(token))
	assert.Len(t, raw, 32)
}

func TestAuthorizationRoundTrip(t *testing.T) {
	token, err := NewAuthToken()
	require.NoError(t, err)
	header := http.Header{}
	token.Authorize(header)
	assert.Equal(t, "Bearer "+string(token), header.Get("Authorization"))
	got, err := ParseAuthorization(header)
	require.NoError(t, err)
	assert.Equal(t, token, got)
}

func TestParseAuthorization(t *testing.T) {
	tests := []struct {
		value   string
		want    AuthToken
		wantErr error
	}{
		{"", "", ErrNoAuthorization},
		{"abc", "", ErrMalformedAuthorization},
		{"Basic abc", "", ErrMalformedAuthorization},
		{"Bearerabc", "", ErrMalformedAuthorization},
		{"Bearer ", "", nil},
		{"Bearer abc", "abc", nil},
	}
	for _, tc := range tests {
		header := http.Header{}
		if tc.value != "" {
			header.Set("Authorization", tc.value)
		}
		got, err := ParseAuthorization(header)
		assert.ErrorIs(t, err, tc.wantErr, tc.value)
		assert.Equal(t, tc.want, got, tc.value)
	}
}

func TestAuthTokenEqual(t *testing.T) {
	assert.True(t, AuthToken("abc").Equal("abc"))
	for _, other := range []AuthToken{"abd", "ab", ""} {
		assert.False(t, AuthToken("abc").Equal(other), "AuthToken(%q).Equal(%q)", "abc", string(other))
	}
}

func TestAuthTokenRedacts(t *testing.T) {
	assert.Equal(t, "[redacted] [redacted]", fmt.Sprintf("%v %s", AuthToken("secret"), AuthToken("secret")))
}
