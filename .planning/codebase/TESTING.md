# Testing Patterns

**Analysis Date:** 2026-02-23

## Test Framework

**Runner:**
- Go's built-in `testing` package (no external test framework)
- Test command: `go test -race ./...`
- Makefile target: `test-go` runs all Go tests with race detector enabled

**Assertion Library:**
- No external assertion library
- Manual assertions using `if`/`else` with `t.Errorf()`, `t.Fatalf()`

**Run Commands:**
```bash
make test-go              # Run all tests with race detector
make test                 # Run all tests (alias for test-go)
go test ./...            # Manual test run
go test -race ./...      # With race detector (recommended)
go test -v ./...         # Verbose output
go test -run TestName ./...  # Run specific test
```

## Test File Organization

**Location:**
- Co-located: tests are in the same package directory as implementation
- Pattern: `module_test.go` paired with `module.go`
- Example: `internal/proxy/proxy.go` paired with `internal/proxy/proxy_test.go`

**Naming:**
- Test files: `*_test.go`
- Test functions: `Test<FunctionName>` or `Test<Type>_<Method>_<Scenario>`
- Example: `TestHandleStatus`, `TestManager_RequireApproval_Approved`

**Structure:**
```
internal/
  approval/
    manager.go
    manager_test.go      # Tests for Manager type
  proxy/
    proxy.go
    proxy_test.go        # Main integration tests
    manager.go
    manager_test.go      # Tests for Manager type
  api/
    handlers_test.go     # Tests for API handlers
```

## Test Structure

**Suite Organization:**

Tests use table-driven testing pattern with descriptive names:

```go
func TestClientNameFromSocket(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/myserver.sock", "myserver"},
		{"/path/to/server1.sock", "server1"},
		{"relative/path.sock", "path"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := clientNameFromSocket(tc.path)
			if result != tc.expected {
				t.Errorf("clientNameFromSocket(%q) = %q, want %q", tc.path, result, tc.expected)
			}
		})
	}
}
```

**Patterns:**

- **Setup:** Test helpers use `t.Helper()` and `t.TempDir()` for isolation
  ```go
  func testHandlers(t *testing.T, mgr *approval.Manager) *Handlers {
  	t.Helper()
  	tmpDir := t.TempDir()
  	auth, err := NewAuth(tmpDir)
  	if err != nil {
  		t.Fatalf("failed to create auth: %v", err)
  	}
  	return NewHandlers(mgr, "/path/to/socket", "test-client", auth)
  }
  ```

- **Teardown:** Deferred cleanup (handled implicitly by `t.TempDir()`) or explicit cleanup
  ```go
  defer mgr.watcher.Close()
  defer os.RemoveAll(tmpDir)
  ```

- **Assertion:** Direct `if` comparison with `t.Errorf()` for non-fatal failures, `t.Fatalf()` for fatal
  ```go
  if rr.Code != http.StatusOK {
  	t.Errorf("expected status 200, got %d", rr.Code)
  }
  if err != nil {
  	t.Fatalf("failed to create auth: %v", err)
  }
  ```

## Mocking

**Framework:** None - manual mocking with interfaces

**Patterns:**

Example mock interface implementation from `internal/notification/desktop_test.go`:
```go
type mockNotifier struct{}

func (m *mockNotifier) Send(body string) (uint32, error) {
	return 1, nil
}

func (m *mockNotifier) Close(id uint32) error {
	return nil
}
```

Example D-Bus test helper with mock service:
```go
type testEnv struct {
	t          *testing.T
	tmpDir     string
	localAddr  string
	remoteAddr string
	localCmd   *exec.Cmd
	remoteCmd  *exec.Cmd
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "secrets-dispatcher-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	env := &testEnv{t: t, tmpDir: tmpDir}
	localSocket := filepath.Join(tmpDir, "local.sock")
	env.localCmd, env.localAddr = startDBusDaemon(t, localSocket)
	remoteSocket := filepath.Join(tmpDir, "remote.sock")
	env.remoteCmd, env.remoteAddr = startDBusDaemon(t, remoteSocket)
	return env
}

func (e *testEnv) cleanup() {
	if e.localCmd != nil && e.localCmd.Process != nil {
		e.localCmd.Process.Kill()
		e.localCmd.Wait()
	}
	if e.remoteCmd != nil && e.remoteCmd.Process != nil {
		e.remoteCmd.Process.Kill()
		e.remoteCmd.Wait()
	}
	if e.tmpDir != "" {
		os.RemoveAll(e.tmpDir)
	}
}
```

**What to Mock:**
- External services: D-Bus connections, HTTP clients
- Interface implementations: custom notifier, mock services
- Interfaces are preferred over concrete types for testability

**What NOT to Mock:**
- Standard library functions (use real `http.Test*` from net/http/httptest)
- Temporary directories (use `t.TempDir()`)
- Time-based operations in critical paths (test with real time or use `time.Sleep`)

## Fixtures and Factories

**Test Data:**

Helper functions create test objects:

```go
func testHandlers(t *testing.T, mgr *approval.Manager) *Handlers {
	t.Helper()
	tmpDir := t.TempDir()
	auth, err := NewAuth(tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}
	return NewHandlers(mgr, "/path/to/socket", "test-client", auth)
}

// Usage in test:
mgr := approval.NewManager(5*time.Minute, 100)
handlers := testHandlers(t, mgr)
```

Config fixtures created inline:
```go
os.WriteFile(path, []byte(`
state_dir: /tmp/state
listen: 0.0.0.0:9090
serve:
  sockets_dir: /run/socks
  log_level: debug
`), 0o644)
```

**Location:**
- Fixtures defined within test files or `testutil/` package
- Helper functions (with `t.Helper()`) at top of test files
- Shared test utilities in `internal/testutil/` for reuse across packages

## Coverage

**Requirements:** Not enforced - no coverage reports found

**View Coverage:**
```bash
go test -cover ./...           # Quick coverage percentage
go test -coverprofile=cover.out ./...
go tool cover -html=cover.out  # HTML report
```

## Test Types

**Unit Tests:**
- Scope: Individual functions/methods
- Approach: Table-driven tests with multiple input scenarios
- Location: `*_test.go` files in same package
- Example: `TestClientNameFromSocket`, `TestDecodeUnitPath`

**Integration Tests:**
- Scope: Multiple components working together
- Approach: Real D-Bus daemon instances via `startDBusDaemon()`, real HTTP servers via `httptest`
- Location: Main integration test in `/home/nb/src/secrets-dispatcher/proxy_test.go`
- D-Bus integration uses isolated temporary sockets to avoid system state pollution

**E2E Tests:**
- Framework: Deno (TypeScript/JavaScript for web UI tests)
- Command: `make test-e2e`
- Location: `web/` directory (frontend)
- Requires: Built binary (`secrets-dispatcher`)

## Common Patterns

**Async Testing:**

Waiting for goroutine results with channels and `sync.WaitGroup`:

```go
func TestManager_RequireApproval_Approved(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)

	var wg sync.WaitGroup
	var approvalErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(context.Background(), "test-client",
			[]ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear in pending list")
	}

	// Approve the request
	if err := mgr.Approve(reqID); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	wg.Wait()

	if approvalErr != nil {
		t.Errorf("RequireApproval returned error: %v", approvalErr)
	}
}
```

**Error Testing:**

Testing specific error conditions:

```go
func TestManager_RequireApproval_Timeout(t *testing.T) {
	mgr := NewManager(100*time.Millisecond, 100)

	err := mgr.RequireApproval(context.Background(), "test-client",
		[]ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})

	if err != ErrTimeout {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

func TestJWTInvalidSignature(t *testing.T) {
	tmpDir := t.TempDir()
	auth, err := NewAuth(tmpDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

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
```

**HTTP Handler Testing:**

Using `net/http/httptest` for handler testing:

```go
func TestHandleStatus(t *testing.T) {
	mgr := approval.NewManager(5*time.Minute, 100)
	handlers := testHandlers(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rr := httptest.NewRecorder()

	handlers.HandleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Running {
		t.Error("expected running=true")
	}
}
```

---

*Testing analysis: 2026-02-23*
