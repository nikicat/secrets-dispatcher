package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

func TestAuth_CreatesConfigDir(t *testing.T) {
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "nested", "config")

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
