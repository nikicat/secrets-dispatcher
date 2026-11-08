package api

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/stretchr/testify/require"
)

func TestJWTGenerateAndValidate(t *testing.T) {
	auth := newTestAuth(t)

	// Generate a JWT
	token, err := auth.GenerateJWT()
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	// Token should have 3 parts
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Errorf("JWT should have 3 parts, got %d", len(parts))
	}

	// Validate the token
	claims, err := auth.ValidateJWT(token)
	if err != nil {
		t.Fatalf("ValidateJWT failed: %v", err)
	}

	// Check claims
	now := time.Now().Unix()
	if claims.Iat > now || claims.Iat < now-1 {
		t.Errorf("iat claim should be approximately now")
	}

	expectedExp := now + int64(jwtExpiry.Seconds())
	if claims.Exp > expectedExp || claims.Exp < expectedExp-1 {
		t.Errorf("exp claim should be approximately %d, got %d", expectedExp, claims.Exp)
	}
}

func TestJWTInvalidSignature(t *testing.T) {
	auth := newTestAuth(t)

	token, err := auth.GenerateJWT()
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	// Tamper with the signature
	parts := strings.Split(token, ".")
	parts[2] = "invalid_signature"
	tamperedToken := strings.Join(parts, ".")

	_, err = auth.ValidateJWT(tamperedToken)
	if err == nil {
		t.Error("ValidateJWT should fail with invalid signature")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("error should mention invalid signature, got: %v", err)
	}
}

func TestJWTExpired(t *testing.T) {
	auth := newTestAuth(t)

	expired := time.Now().Add(-time.Minute)
	token := signJWT(auth, fmt.Sprintf(`{"iat":%d,"exp":%d,"jti":"expired"}`,
		expired.Add(-jwtExpiry).Unix(), expired.Unix()))

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
			if err == nil {
				t.Errorf("ValidateJWT(%q) should fail", tt.token)
			}
		})
	}
}

func TestJWTDifferentSecrets(t *testing.T) {
	// Create two auth instances with different tokens
	auth1 := newTestAuth(t)
	auth2 := newTestAuth(t)

	// Generate token with auth1
	token, err := auth1.GenerateJWT()
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	// Should validate with auth1
	_, err = auth1.ValidateJWT(token)
	if err != nil {
		t.Errorf("Token should validate with same auth: %v", err)
	}

	// Should NOT validate with auth2
	_, err = auth2.ValidateJWT(token)
	if err == nil {
		t.Error("Token should not validate with different auth")
	}
}

func TestGenerateLoginURL(t *testing.T) {
	auth := newTestAuth(t)

	addr := "127.0.0.1:8484"
	url, err := auth.GenerateLoginURL(addr)
	if err != nil {
		t.Fatalf("GenerateLoginURL failed: %v", err)
	}

	// Check URL format
	if !strings.HasPrefix(url, "http://127.0.0.1:8484/?token=") {
		t.Errorf("URL should start with http://127.0.0.1:8484/?token=, got: %s", url)
	}

	// Extract and validate the token
	parts := strings.SplitN(url, "?token=", 2)
	if len(parts) != 2 {
		t.Fatalf("URL should contain ?token=")
	}

	token := parts[1]
	_, err = auth.ValidateJWT(token)
	if err != nil {
		t.Errorf("Token from login URL should be valid: %v", err)
	}
}

func TestLoadAuth(t *testing.T) {
	dir := config.StateDir(t.TempDir())

	// Create initial auth
	auth1, err := NewAuth(dir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	// Load auth from same directory
	auth2, err := LoadAuth(dir)
	if err != nil {
		t.Fatalf("LoadAuth failed: %v", err)
	}

	// Tokens should match
	if auth1.token != auth2.token {
		t.Error("Loaded auth should have same token")
	}

	// Generate token with auth1, validate with auth2
	token, err := auth1.GenerateJWT()
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	_, err = auth2.ValidateJWT(token)
	if err != nil {
		t.Errorf("Token should validate with loaded auth: %v", err)
	}
}

func TestLoadAuthNotExists(t *testing.T) {
	_, err := LoadAuth(config.StateDir(t.TempDir()))
	require.ErrorIs(t, err, fs.ErrNotExist)
}
