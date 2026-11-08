package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Builds an Auth on a fresh state directory and fails the test on error.
func newTestAuth(t *testing.T) *Auth {
	t.Helper()
	auth, err := NewAuth(config.StateDir(t.TempDir()))
	require.NoError(t, err)
	return auth
}

func TestNewAuth(t *testing.T) {
	dir := config.StateDir(t.TempDir())

	auth, err := NewAuth(dir)
	require.NoError(t, err)
	stored, err := dir.ReadToken()
	require.NoError(t, err)
	require.Equal(t, auth.Token(), stored, "NewAuth persists its token")

	again, err := NewAuth(dir)
	require.NoError(t, err)
	require.Equal(t, auth.Token(), again.Token(), "a second NewAuth reuses the stored token")
}

// Checks that rr holds the JSON error response with message want.
func assertErrorBody(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body ErrorResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, want, body.Error)
}

func TestAuth_Middleware_ValidToken(t *testing.T) {
	auth := newTestAuth(t)

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := auth.Middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	auth.Token().Authorize(req.Header)

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if !called {
		t.Error("handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestAuth_Middleware_InvalidToken(t *testing.T) {
	auth := newTestAuth(t)

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	wrapped := auth.Middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	config.AuthToken("wrong-token").Authorize(req.Header)

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if called {
		t.Error("handler should not be called with invalid token")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
	assertErrorBody(t, rr, "invalid token")
}

func TestAuth_Middleware_MissingHeader(t *testing.T) {
	auth := newTestAuth(t)

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	wrapped := auth.Middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	// No Authorization header

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if called {
		t.Error("handler should not be called without auth header")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
	assertErrorBody(t, rr, "unauthorized")
}

func TestAuth_Middleware_MalformedHeader(t *testing.T) {
	auth := newTestAuth(t)

	tests := []struct {
		name    string
		header  string
		wantErr string
	}{
		{"no scheme", string(auth.Token()), "invalid Authorization header format"},
		{"wrong scheme", "Basic " + string(auth.Token()), "invalid Authorization header format"},
		{"empty bearer", "Bearer ", "invalid token"},
		{"no space", "Bearer" + string(auth.Token()), "invalid Authorization header format"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})

			wrapped := auth.Middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", tc.header)

			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)

			if called {
				t.Error("handler should not be called with malformed header")
			}
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected status 401, got %d", rr.Code)
			}
			assertErrorBody(t, rr, tc.wantErr)
		})
	}
}

// TestSessionCookieIsNotMasterToken verifies the amplification fix (Vuln 2): the
// session cookie is an independent random value, never the master Bearer token,
// and each login mints a distinct one.
func TestSessionCookieIsNotMasterToken(t *testing.T) {
	auth := newTestAuth(t)

	rr1 := httptest.NewRecorder()
	require.NoError(t, auth.SetSessionCookie(rr1))
	c1 := rr1.Result().Cookies()
	require.Len(t, c1, 1)

	rr2 := httptest.NewRecorder()
	require.NoError(t, auth.SetSessionCookie(rr2))
	c2 := rr2.Result().Cookies()
	require.Len(t, c2, 1)

	assert.NotEqual(t, string(auth.Token()), c1[0].Value, "session cookie must not be the master token")
	assert.NotEqual(t, c1[0].Value, c2[0].Value, "each login must mint a distinct session id")
	assert.True(t, c1[0].HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, c1[0].SameSite)

	// A minted session validates; the master token presented as a session cookie does not.
	reqGood := httptest.NewRequest(http.MethodGet, "/", nil)
	reqGood.AddCookie(&http.Cookie{Name: sessionCookieName, Value: c1[0].Value})
	assert.True(t, auth.ValidateSession(reqGood), "minted session should validate")

	reqBad := httptest.NewRequest(http.MethodGet, "/", nil)
	reqBad.AddCookie(&http.Cookie{Name: sessionCookieName, Value: string(auth.Token())})
	assert.False(t, auth.ValidateSession(reqBad), "master token as session cookie must not validate")
}

// TestLoginJWTIsSingleUse verifies that a login JWT can be redeemed for a session
// exactly once: a replay of the same token (e.g. captured from launcher/browser
// argv) is rejected even while still within its validity window.
func TestLoginJWTIsSingleUse(t *testing.T) {
	auth := newTestAuth(t)

	token, err := auth.GenerateJWT()
	require.NoError(t, err)

	claims, err := auth.ValidateJWT(token)
	require.NoError(t, err)
	require.NotEmpty(t, claims.Jti, "generated JWT must carry a jti nonce")

	// First redemption succeeds.
	assert.True(t, auth.consumeJTI(jti(claims.Jti), claims.Exp))
	// Replay of the same nonce is rejected.
	assert.False(t, auth.consumeJTI(jti(claims.Jti), claims.Exp))

	// The signature/expiry are still valid — only single-use blocks the replay.
	_, err = auth.ValidateJWT(token)
	assert.NoError(t, err, "token remains cryptographically valid; single-use is enforced separately")
}

// TestValidateJWTRejectsMissingJti: a correctly signed token whose claims lack
// a jti (the pre-single-use format) must be rejected outright. If it were
// accepted, "" would act as the shared nonce for every such token and replay
// protection would silently key on nothing.
func TestValidateJWTRejectsMissingJti(t *testing.T) {
	auth := newTestAuth(t)

	now := time.Now()
	token := signJWT(auth, fmt.Sprintf(`{"iat":%d,"exp":%d}`, now.Unix(), now.Add(time.Minute).Unix()))

	_, err := auth.ValidateJWT(token)
	assert.ErrorContains(t, err, "jti", "token without jti must be rejected as malformed")
}

// TestHandleAuthRejectsReplayedToken exercises single use through the HTTP handler:
// the same login token cannot be exchanged for a session cookie twice.
func TestHandleAuthRejectsReplayedToken(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	token, err := handlers.auth.GenerateJWT()
	require.NoError(t, err)
	body := `{"token":"` + token + `"}`

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth", strings.NewReader(body))
	rr1 := httptest.NewRecorder()
	handlers.HandleAuth(rr1, req1)
	require.Equal(t, http.StatusOK, rr1.Code)
	require.Len(t, rr1.Result().Cookies(), 1, "first exchange should set a session cookie")

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth", strings.NewReader(body))
	rr2 := httptest.NewRecorder()
	handlers.HandleAuth(rr2, req2)
	assert.Equal(t, http.StatusUnauthorized, rr2.Code, "replayed token must be rejected")
}
