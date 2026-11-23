package api

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWTGenerateAndValidate(t *testing.T) {
	auth := newTestAuth(t)

	before := time.Now()
	token, err := auth.GenerateJWT()
	require.NoError(t, err, "GenerateJWT")

	assert.Len(t, strings.Split(token, "."), 3)

	claims, err := auth.ValidateJWT(token)
	require.NoError(t, err, "ValidateJWT")

	assert.WithinRange(t, time.Unix(claims.Iat, 0), before.Truncate(time.Second), time.Now(), "iat")
	assert.Equal(t, LoginLinkLifetime, time.Duration(claims.Exp-claims.Iat)*time.Second, "exp - iat")
}

func TestJWTInvalidSignature(t *testing.T) {
	auth := newTestAuth(t)

	token, err := auth.GenerateJWT()
	require.NoError(t, err, "GenerateJWT")

	// Tamper with the signature
	parts := strings.Split(token, ".")
	parts[2] = "invalid_signature"
	tamperedToken := strings.Join(parts, ".")

	_, err = auth.ValidateJWT(tamperedToken)
	assert.ErrorContains(t, err, "invalid signature")
}

func TestJWTExpired(t *testing.T) {
	auth := newTestAuth(t)

	expired := time.Now().Add(-time.Minute)
	token := signJWT(auth, fmt.Sprintf(`{"iat":%d,"exp":%d,"jti":"expired"}`,
		expired.Add(-LoginLinkLifetime).Unix(), expired.Unix()))

	_, err := auth.ValidateJWT(token)
	require.ErrorContains(t, err, "token expired")
}

// Returns a JWT that auth signs over claims, a JSON object, so a test can
// present claims that GenerateJWT never mints.
func signJWT(auth *Auth, claims string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	signingInput := header + "." + base64.RawURLEncoding.EncodeToString([]byte(claims))
	return signingInput + "." + auth.sign(signingInput)
}

func TestJWTInvalidFormat(t *testing.T) {
	auth := newTestAuth(t)

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"one part", "header"},
		{"two parts", "header.claims"},
		{"four parts", "a.b.c.d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := auth.ValidateJWT(tt.token)
			assert.Error(t, err)
		})
	}
}

func TestJWTDifferentSecrets(t *testing.T) {
	// Create two auth instances with different tokens
	auth1 := newTestAuth(t)
	auth2 := newTestAuth(t)

	// Generate token with auth1
	token, err := auth1.GenerateJWT()
	require.NoError(t, err, "GenerateJWT")

	// Should validate with auth1
	_, err = auth1.ValidateJWT(token)
	assert.NoError(t, err, "same auth")

	// Should NOT validate with auth2
	_, err = auth2.ValidateJWT(token)
	assert.Error(t, err, "different auth")
}

func TestGenerateLoginURL(t *testing.T) {
	auth := newTestAuth(t)

	loginURL, err := auth.GenerateLoginURL("127.0.0.1:8484")
	require.NoError(t, err, "GenerateLoginURL")

	u, err := url.Parse(loginURL)
	require.NoError(t, err, "parse login URL")
	_, err = auth.ValidateJWT(u.Query().Get("token"))
	assert.NoError(t, err, "token from login URL")
}

func TestLoadAuth(t *testing.T) {
	dir := config.StateDir(t.TempDir())

	// Create initial auth
	auth1, err := NewAuth(dir)
	require.NoError(t, err, "NewAuth")

	// Load auth from same directory
	auth2, err := LoadAuth(dir)
	require.NoError(t, err, "LoadAuth")

	assert.Equal(t, auth1.token, auth2.token, "loaded token")

	// Generate token with auth1, validate with auth2
	token, err := auth1.GenerateJWT()
	require.NoError(t, err, "GenerateJWT")

	_, err = auth2.ValidateJWT(token)
	assert.NoError(t, err, "loaded auth")
}

func TestLoadAuthNotExists(t *testing.T) {
	_, err := LoadAuth(config.StateDir(t.TempDir()))
	require.ErrorIs(t, err, fs.ErrNotExist)
}
