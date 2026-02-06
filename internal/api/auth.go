// Package api provides the REST API for approval management.
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	cookieFileName = ".cookie"
	cookieSize     = 32 // 32 bytes = 256 bits
)

// Auth handles cookie-based authentication for the API.
type Auth struct {
	token    string
	filePath string
}

// NewAuth creates a new Auth, generating a random cookie and writing it to the config directory.
// The cookie file is created with mode 0600.
func NewAuth(configDir string) (*Auth, error) {
	// Generate random token
	tokenBytes := make([]byte, cookieSize)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)

	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, err
	}

	// Write cookie file
	filePath := filepath.Join(configDir, cookieFileName)
	if err := os.WriteFile(filePath, []byte(token), 0600); err != nil {
		return nil, err
	}

	return &Auth{
		token:    token,
		filePath: filePath,
	}, nil
}

// Middleware returns an HTTP middleware that checks for valid Bearer token.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error": "missing Authorization header"}`, http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, `{"error": "invalid Authorization header format"}`, http.StatusUnauthorized)
			return
		}

		providedToken := parts[1]
		if subtle.ConstantTimeCompare([]byte(providedToken), []byte(a.token)) != 1 {
			http.Error(w, `{"error": "invalid token"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Token returns the current auth token (for testing).
func (a *Auth) Token() string {
	return a.token
}

// FilePath returns the path to the cookie file.
func (a *Auth) FilePath() string {
	return a.filePath
}
