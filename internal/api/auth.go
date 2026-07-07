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
	"sync"
	"time"
)

const (
	cookieFileName    = ".cookie"
	cookieSize        = 32 // 32 bytes = 256 bits
	sessionCookieName = "session"
	sessionIDSize     = 32 // 32 bytes = 256 bits
)

// sessionID is an opaque, independent browser session identifier. It is a random
// value minted server-side — never the master token — so a leaked session cookie
// discloses nothing reusable as the Bearer credential.
type sessionID string

// jti is a login-token nonce (the JWT "jti" claim) used to enforce single use.
type jti string

// Auth handles cookie-based authentication for the API.
//
// The master token (loaded from / persisted to the 0600 .cookie file) is the
// Bearer credential used by same-user thin clients (gpg-sign, CLI). Browser
// sessions are deliberately NOT the master token: SetSessionCookie mints an
// independent random session ID stored server-side, so a leaked session cookie
// never discloses the master token and can be revoked without rotating it.
type Auth struct {
	token    string
	filePath string

	mu sync.Mutex
	// sessions holds the set of live browser session IDs. Membership is the sole
	// session check.
	sessions map[sessionID]struct{}
	// usedJTIs records login-token nonces already redeemed, mapped to their
	// expiry (unix seconds), so a single-use JWT cannot be replayed within its
	// validity window. Pruned lazily on each redemption.
	usedJTIs map[jti]int64
}

// newAuth constructs an Auth with its in-memory session/nonce maps initialized.
func newAuth(token, filePath string) *Auth {
	return &Auth{
		token:    token,
		filePath: filePath,
		sessions: make(map[sessionID]struct{}),
		usedJTIs: make(map[jti]int64),
	}
}

// NewAuth creates or loads an Auth from the config directory.
// If a cookie file exists, it is loaded. Otherwise, a new random cookie is generated.
// The cookie file is created with mode 0600.
func NewAuth(configDir string) (*Auth, error) {
	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, err
	}

	filePath := filepath.Join(configDir, cookieFileName)

	// Try to load existing cookie file
	if data, err := os.ReadFile(filePath); err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return newAuth(token, filePath), nil
		}
	}

	// Generate new random token
	tokenBytes := make([]byte, cookieSize)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)

	// Write cookie file
	if err := os.WriteFile(filePath, []byte(token), 0600); err != nil {
		return nil, err
	}

	return newAuth(token, filePath), nil
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

	return newAuth(token, filePath), nil
}

// GenerateLoginURL creates a login URL with an embedded JWT.
func (a *Auth) GenerateLoginURL(addr string) (string, error) {
	jwt, err := a.GenerateJWT()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s/?token=%s", addr, jwt), nil
}

// ValidateSession checks if the session cookie names a live server-side session.
func (a *Auth) ValidateSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.sessions[sessionID(cookie.Value)]
	return ok
}

// ValidateRequest checks session cookie first, then falls back to Bearer token
// in the Authorization header. Used by WebSocket handler to accept both browser
// sessions and thin client Bearer tokens.
func (a *Auth) ValidateRequest(r *http.Request) bool {
	if a.ValidateSession(r) {
		return true
	}
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) == 2 && parts[0] == "Bearer" {
		return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(a.token)) == 1
	}
	return false
}

// SetSessionCookie mints a fresh, independent server-side session and sets it as
// the session cookie. The cookie value is a random session ID — never the master
// token — so reading it (e.g. from a curl client) grants only a revocable browser
// session, not the Bearer credential.
func (a *Auth) SetSessionCookie(w http.ResponseWriter) error {
	idBytes := make([]byte, sessionIDSize)
	if _, err := rand.Read(idBytes); err != nil {
		return fmt.Errorf("generate session id: %w", err)
	}
	id := sessionID(hex.EncodeToString(idBytes))

	a.mu.Lock()
	a.sessions[id] = struct{}{}
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    string(id),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// consumeJTI records a login-token nonce as redeemed and reports whether this is
// the first time it has been seen (i.e. whether redemption may proceed). Replays
// of the same token — including a token captured from the launcher/browser argv —
// return false. exp is the token's expiry (unix seconds); entries are pruned once
// past expiry since an expired JWT is already rejected by ValidateJWT.
func (a *Auth) consumeJTI(id jti, exp int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now().Unix()
	for used, usedExp := range a.usedJTIs {
		if usedExp <= now {
			delete(a.usedJTIs, used)
		}
	}

	if _, seen := a.usedJTIs[id]; seen {
		return false
	}
	a.usedJTIs[id] = exp
	return true
}
