// Package api provides the REST API for approval management.
package api

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/randhex"
)

const (
	sessionCookieName = "session"
	sessionIDSize     = 32 // 32 bytes = 256 bits
)

// An opaque browser session identifier. It is a random value minted by the
// server and is never the master token. So a leaked session cookie does not
// disclose the Bearer credential.
type sessionID string

// jti is a login-token nonce (the JWT "jti" claim) used to enforce single use.
type jti string

// Authenticates API requests by a browser session cookie or the master token.
type Auth struct {
	token config.AuthToken

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
func newAuth(token config.AuthToken) *Auth {
	return &Auth{
		token:    token,
		sessions: make(map[sessionID]struct{}),
		usedJTIs: make(map[jti]int64),
	}
}

// Returns the Auth for the master token in dir. Reuses the token that dir
// already holds when it is non-empty. Otherwise generates a new token and
// writes it with config.StateDir.WriteToken.
func NewAuth(dir config.StateDir) (*Auth, error) {
	if token, err := dir.ReadToken(); err == nil {
		return newAuth(token), nil
	}
	token, err := config.NewAuthToken()
	if err != nil {
		return nil, err
	}
	if err := dir.WriteToken(token); err != nil {
		return nil, err
	}
	return newAuth(token), nil
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
		switch err := a.checkBearer(r); {
		case err == nil:
			next.ServeHTTP(w, r)
		case errors.Is(err, config.ErrNoAuthorization):
			writeError(w, "unauthorized", http.StatusUnauthorized)
		default:
			writeError(w, err.Error(), http.StatusUnauthorized)
		}
	})
}

// The Bearer credential in a request is not the master token.
var errWrongToken = errors.New("invalid token")

// Checks that r's Authorization header carries the master token. Errors as
// config.ParseAuthorization does, and with errWrongToken when the header
// carries another token.
func (a *Auth) checkBearer(r *http.Request) error {
	token, err := config.ParseAuthorization(r.Header)
	if err != nil {
		return err
	}
	if !token.Equal(a.token) {
		return errWrongToken
	}
	return nil
}

// Returns the master token.
func (a *Auth) Token() config.AuthToken {
	return a.token
}

// Returns the Auth for the master token that dir already holds.
// Errors as config.StateDir.ReadToken does.
func LoadAuth(dir config.StateDir) (*Auth, error) {
	token, err := dir.ReadToken()
	if err != nil {
		return nil, err
	}
	return newAuth(token), nil
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

// Checks whether r carries a live session cookie or the master token as its
// Bearer credential.
func (a *Auth) ValidateRequest(r *http.Request) bool {
	return a.ValidateSession(r) || a.checkBearer(r) == nil
}

// Starts a new browser session and sets its ID as the session cookie. Errors
// when the session ID cannot be generated.
func (a *Auth) SetSessionCookie(w http.ResponseWriter) error {
	s, err := randhex.String(sessionIDSize)
	if err != nil {
		return fmt.Errorf("generate session id: %w", err)
	}
	id := sessionID(s)

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
