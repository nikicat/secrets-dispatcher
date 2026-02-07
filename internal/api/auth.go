// Package api provides the REST API for approval management.
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	cookieFileName    = ".cookie"
	cookieSize        = 32 // 32 bytes = 256 bits
	sessionCookieName = "session"
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

// Middleware returns an HTTP middleware that checks for valid auth.
// Supports both Bearer token (Authorization header) and session cookie.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First, try session cookie
		if a.ValidateSession(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Fall back to Bearer token
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
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

// LoadAuth loads an existing Auth from the cookie file.
// Returns an error if the cookie file doesn't exist or is invalid.
func LoadAuth(configDir string) (*Auth, error) {
	filePath := filepath.Join(configDir, cookieFileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(string(data))
	if token == "" {
		return nil, fmt.Errorf("empty cookie file")
	}

	return &Auth{
		token:    token,
		filePath: filePath,
	}, nil
}

// GenerateLoginURL creates a login URL with an embedded JWT.
func (a *Auth) GenerateLoginURL(addr string) (string, error) {
	jwt, err := a.GenerateJWT()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s/?token=%s", addr, jwt), nil
}

// ValidateSession checks if the session cookie is valid.
// Returns true if the session cookie matches the auth token.
func (a *Auth) ValidateSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(a.token)) == 1
}

// SetSessionCookie sets the session cookie on the response.
func (a *Auth) SetSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    a.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}
