package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuth(t *testing.T) {
	tempDir := t.TempDir()

	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	// Check cookie file exists
	cookiePath := filepath.Join(tempDir, cookieFileName)
	info, err := os.Stat(cookiePath)
	if err != nil {
		t.Fatalf("cookie file not created: %v", err)
	}

	// Check file permissions
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected mode 0600, got %o", info.Mode().Perm())
	}

	// Check file contents
	content, err := os.ReadFile(cookiePath)
	if err != nil {
		t.Fatalf("failed to read cookie file: %v", err)
	}

	// Token should be 64 hex chars (32 bytes)
	if len(content) != 64 {
		t.Errorf("expected 64 char token, got %d", len(content))
	}

	// Token should match
	if string(content) != auth.Token() {
		t.Error("token in file doesn't match auth.Token()")
	}
}

func TestAuth_Middleware_ValidToken(t *testing.T) {
	tempDir := t.TempDir()
	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := auth.Middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+auth.Token())

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
	tempDir := t.TempDir()
	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	wrapped := auth.Middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if called {
		t.Error("handler should not be called with invalid token")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestAuth_Middleware_MissingHeader(t *testing.T) {
	tempDir := t.TempDir()
	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

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
}

func TestAuth_Middleware_MalformedHeader(t *testing.T) {
	tempDir := t.TempDir()
	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	tests := []struct {
		name   string
		header string
	}{
		{"no scheme", auth.Token()},
		{"wrong scheme", "Basic " + auth.Token()},
		{"empty bearer", "Bearer "},
		{"no space", "Bearer" + auth.Token()},
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
		})
	}
}

func TestAuth_FilePath(t *testing.T) {
	tempDir := t.TempDir()
	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	expected := filepath.Join(tempDir, cookieFileName)
	if auth.FilePath() != expected {
		t.Errorf("expected path %s, got %s", expected, auth.FilePath())
	}
}

func TestAuth_CreatesStateDir(t *testing.T) {
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "nested", "state")

	_, err := NewAuth(nestedDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	info, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("nested dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestAuth_PreservesExistingCookie(t *testing.T) {
	tempDir := t.TempDir()

	// Create first auth - generates new cookie
	auth1, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth (first) failed: %v", err)
	}
	token1 := auth1.Token()

	// Create second auth - should load existing cookie
	auth2, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth (second) failed: %v", err)
	}
	token2 := auth2.Token()

	// Tokens should be identical
	if token1 != token2 {
		t.Errorf("token changed across NewAuth calls: %q != %q", token1, token2)
	}

	// Verify file wasn't modified (same content)
	content, err := os.ReadFile(filepath.Join(tempDir, cookieFileName))
	if err != nil {
		t.Fatalf("failed to read cookie file: %v", err)
	}
	if string(content) != token1 {
		t.Error("cookie file content doesn't match original token")
	}
}

// TestSessionCookieIsNotMasterToken verifies the amplification fix (Vuln 2): the
// session cookie is an independent random value, never the master Bearer token,
// and each login mints a distinct one.
func TestSessionCookieIsNotMasterToken(t *testing.T) {
	auth, err := NewAuth(t.TempDir())
	require.NoError(t, err)

	rr1 := httptest.NewRecorder()
	require.NoError(t, auth.SetSessionCookie(rr1))
	c1 := rr1.Result().Cookies()
	require.Len(t, c1, 1)

	rr2 := httptest.NewRecorder()
	require.NoError(t, auth.SetSessionCookie(rr2))
	c2 := rr2.Result().Cookies()
	require.Len(t, c2, 1)

	assert.NotEqual(t, auth.Token(), c1[0].Value, "session cookie must not be the master token")
	assert.NotEqual(t, c1[0].Value, c2[0].Value, "each login must mint a distinct session id")
	assert.True(t, c1[0].HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, c1[0].SameSite)

	// A minted session validates; the master token presented as a session cookie does not.
	reqGood := httptest.NewRequest(http.MethodGet, "/", nil)
	reqGood.AddCookie(&http.Cookie{Name: sessionCookieName, Value: c1[0].Value})
	assert.True(t, auth.ValidateSession(reqGood), "minted session should validate")

	reqBad := httptest.NewRequest(http.MethodGet, "/", nil)
	reqBad.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.Token()})
	assert.False(t, auth.ValidateSession(reqBad), "master token as session cookie must not validate")
}

// TestLoginJWTIsSingleUse verifies that a login JWT can be redeemed for a session
// exactly once: a replay of the same token (e.g. captured from launcher/browser
// argv) is rejected even while still within its validity window.
func TestLoginJWTIsSingleUse(t *testing.T) {
	auth, err := NewAuth(t.TempDir())
	require.NoError(t, err)

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
	auth, err := NewAuth(t.TempDir())
	require.NoError(t, err)

	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString(
		fmt.Appendf(nil, `{"iat":%d,"exp":%d}`, now.Unix(), now.Add(time.Minute).Unix()))
	signingInput := header + "." + claims
	token := signingInput + "." + auth.sign(signingInput)

	_, err = auth.ValidateJWT(token)
	assert.ErrorContains(t, err, "jti", "token without jti must be rejected as malformed")
}

// TestHandleAuthRejectsReplayedToken exercises single use through the HTTP handler:
// the same login token cannot be exchanged for a session cookie twice.
func TestHandleAuthRejectsReplayedToken(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: time.Minute, HistoryMax: 10})
	handlers := testHandlers(t, mgr)

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
