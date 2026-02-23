# Coding Conventions

**Analysis Date:** 2026-02-23

## Naming Patterns

**Files:**
- Package files use lowercase with underscores: `manager.go`, `proxy.go`, `service.go`
- Test files use `_test.go` suffix: `proxy_test.go`, `manager_test.go`
- Type files explicitly named: `types.go` for constant/type definitions
- Test helper file: `testutil/` package for shared test utilities

**Functions:**
- Exported functions start with capital letter: `NewProxy()`, `Connect()`, `Run()`, `RequireApproval()`
- Unexported functions start with lowercase: `newTestEnv()`, `startDBusDaemon()`, `clientNameFromSocket()`
- Constructor functions use `New` prefix: `NewProxy()`, `NewManager()`, `NewAuth()`, `NewFormatter()`
- Handler methods use `Handle` prefix: `HandleStatus()`, `HandlePendingList()`, `HandleApprove()`, `HandleDeny()`
- Getter/checker methods often use verb form: `Approve()`, `Deny()`, `Connect()`, `Close()`, `Run()`
- Test functions use `Test` prefix with descriptive name: `TestHandleStatus()`, `TestManager_RequireApproval_Approved()`

**Variables:**
- Local variables use camelCase: `tmpDir`, `localAddr`, `remoteCmd`, `approvalMgr`, `wsHandler`
- Receiver names are single letters or two letters: `(t *testing.T)`, `(m *Manager)`, `(s *Server)`, `(f *Formatter)`
- Error variables follow common pattern: `err`, `sdErr`, `dbusErr`, `call.Err`
- Package-level error variables: `ErrDenied`, `ErrTimeout`, `ErrNotFound` (exported, all-caps for constants)

**Types:**
- Exported types use PascalCase: `Proxy`, `Manager`, `Server`, `Formatter`, `Auth`, `Request`
- Interface types named clearly: `Observer`, `ClientProvider`, `Handler`
- Struct field names use camelCase: `remoteSocketPath`, `clientName`, `localConn`, `remoteConn`, `approval`

## Code Style

**Formatting:**
- Uses standard Go formatting (gofmt)
- Lines are generally < 100 characters, but not strictly enforced
- Indentation: 1 tab (Go standard)

**Linting:**
- No `.golangci.yml` detected - relies on standard Go conventions
- Code follows idiomatic Go patterns

## Import Organization

**Order:**
1. Standard library imports: `context`, `fmt`, `log/slog`, `os`, `time`, `sync`, etc.
2. External third-party imports: `github.com/godbus/dbus/v5`, `github.com/coder/websocket`, etc.
3. Internal package imports: `github.com/nikicat/secrets-dispatcher/internal/...`

**Path Aliases:**
- Used in one location: `dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"` for disambiguating D-Bus types
- Otherwise imports are direct with full paths

**Example from `internal/proxy/proxy.go`:**
```go
import (
	"context"
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)
```

## Error Handling

**Patterns:**
- Explicit error returns at end of function signature: `func (...) (..., error)`
- Named error sentinels at package level for library functions: `var ErrDenied = errors.New("access denied by user")`
- Error wrapping using `fmt.Errorf()` with `%w` format verb: `return nil, fmt.Errorf("connect to local session bus: %w", err)`
- Error checking with early return: `if err != nil { return err }` or `if err != nil { t.Fatalf(...) }`
- D-Bus errors explicitly type-checked: `if dbusErr, ok := err.(*dbus.Error); ok { ... }`
- Context cancellation checked as `if err != context.Canceled { ... }` to ignore expected cancellation

**Example from `internal/proxy/proxy.go`:**
```go
func (p *Proxy) Connect(ctx context.Context) error {
	var err error

	// Connect to local session bus
	p.localConn, err = dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to local session bus: %w", err)
	}

	// Connect to remote D-Bus via socket
	p.remoteConn, err = dbus.Connect("unix:path=" + p.remoteSocketPath)
	if err != nil {
		p.localConn.Close()
		return fmt.Errorf("connect to remote D-Bus: %w", err)
	}
	return nil
}
```

## Logging

**Framework:** `log/slog` (Go 1.21+ standard library structured logging)

**Patterns:**
- All logging goes through package-level `slog.Default()` or injected logger
- Structured logging with key-value pairs: `slog.Info("message", "key", value)`
- Log levels used: `Debug`, `Info`, `Warn`, `Error`
- Common pattern in main: `slog.Info("server started", "url", "http://"+addr)`
- Error logging: `slog.Error("HTTP server error", "error", err)`
- Debug logging: `slog.Debug("desktop notifications enabled")`

**Example from `main.go`:**
```go
slog.Info("API server started",
	"url", "http://"+apiServer.Addr(),
	"cookie_file", apiServer.CookieFilePath())
```

## Comments

**When to Comment:**
- Comments precede the code they describe
- Package-level comments explain the package purpose: `// Package proxy implements the Secret Service proxy.`
- Interface methods documented: `// OpenSession opens a session for secret transfer.`
- Complex logic receives line-by-line explanation
- Signature notes in comments: `// Signature: OpenSession(algorithm String, input Variant) -> (...)`

**JSDoc/TSDoc:**
- Not used (Go convention)
- Instead uses standard Go doc comments for exported items
- Format: `// NameOfExport describes what it does.` directly above the declaration

**Example from `internal/approval/manager.go`:**
```go
// ErrDenied is returned when a request is denied by the user.
var ErrDenied = errors.New("access denied by user")

// Request represents a secret access request awaiting approval.
type Request struct {
	ID        string     `json:"id"`
	// ... fields
}
```

## Function Design

**Size:** Functions typically 20-50 lines, rarely exceeding 100 lines

**Parameters:**
- Context always first if needed: `func (s *Server) Shutdown(ctx context.Context) error`
- Receiver before parameters: `(p *Proxy) Connect(ctx context.Context) error`
- Multiple return values for error handling: `(output, sessionPath, err)`
- Configuration passed via Config struct: `func New(cfg Config) *Proxy`

**Return Values:**
- Error always last: `func (...) (..., error)`
- Struct pointers for types with methods: `func NewProxy(cfg Config) *Proxy`
- Simple values for scalar types: `func (f *Formatter) NewFormatter(w io.Writer, asJSON bool) *Formatter`

**Example from `internal/proxy/proxy.go`:**
```go
type Config struct {
	RemoteSocketPath string
	ClientName       string
	LogLevel         slog.Level
	Approval         *approval.Manager
}

func New(cfg Config) *Proxy {
	clientName := cfg.ClientName
	if clientName == "" {
		clientName = "unknown"
	}
	return &Proxy{
		remoteSocketPath: cfg.RemoteSocketPath,
		clientName:       clientName,
		sessions:         NewSessionManager(),
		logger:           logging.New(cfg.LogLevel, clientName),
		approval:         approvalMgr,
	}
}
```

## Module Design

**Exports:**
- Types and interfaces exported at package level
- Constructor functions exported as public API
- Handler methods exported for HTTP routing
- Test helper functions unexported (start with lowercase)

**Barrel Files:**
- No barrel files used (no `index.go` pattern)
- Each file contains related functionality
- Main entry point: `/home/nb/src/secrets-dispatcher/main.go` for CLI/command dispatching

**Package Structure:**
- `internal/proxy/` - D-Bus proxy implementation
- `internal/api/` - HTTP API server and handlers
- `internal/approval/` - Approval request management
- `internal/cli/` - CLI client and formatting
- `internal/config/` - Configuration loading
- `internal/notification/` - Desktop notifications via D-Bus
- `internal/testutil/` - Shared test helpers
- `cmd/` - Command-line binaries

---

*Convention analysis: 2026-02-23*
