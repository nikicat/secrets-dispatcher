package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Server is the HTTP API server.
type Server struct {
	httpServer     *http.Server
	auth           *Auth
	handlers       *Handlers
	wsHandler      *WSHandler
	listener       net.Listener
	unixListener   net.Listener
	UnixSocketPath string
	testMode       bool
}

// NewServer creates a new API server for single-socket mode.
// If unixSocketPath is non-empty, the server also listens on that Unix socket
// to serve the thin client (gpg-sign subcommand).
func NewServer(addr string, manager *approval.Manager, remoteSocket, clientName string, auth *Auth, unixSocketPath string) (*Server, error) {
	handlers := NewHandlers(manager, remoteSocket, clientName, auth)
	wsHandler := NewWSHandler(manager, nil, auth, remoteSocket, clientName)
	return newServerWithHandlers(addr, handlers, wsHandler, auth, unixSocketPath)
}

// NewServerWithProvider creates a new API server for multi-socket mode.
// If unixSocketPath is non-empty, the server also listens on that Unix socket
// to serve the thin client (gpg-sign subcommand).
func NewServerWithProvider(addr string, manager *approval.Manager, provider ClientProvider, auth *Auth, unixSocketPath string) (*Server, error) {
	handlers := NewHandlersWithProvider(manager, provider, auth)
	wsHandler := NewWSHandler(manager, provider, auth, "", "")
	return newServerWithHandlers(addr, handlers, wsHandler, auth, unixSocketPath)
}

// newServerWithHandlers creates a new API server with the given handlers.
// If unixSocketPath is non-empty, the server also listens on a Unix socket.
func newServerWithHandlers(addr string, handlers *Handlers, wsHandler *WSHandler, auth *Auth, unixSocketPath string) (*Server, error) {

	// Create the main router
	rootMux := http.NewServeMux()

	// API routes that require auth
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/v1/status", handlers.HandleStatus)
	apiMux.HandleFunc("/api/v1/pending", handlers.HandlePendingList)
	apiMux.HandleFunc("/api/v1/log", handlers.HandleLog)
	apiMux.HandleFunc("/api/v1/ws", wsHandler.HandleWS)
	apiMux.HandleFunc("/api/v1/test/history", handlers.HandleTestInjectHistory)
	apiMux.HandleFunc("/api/v1/gpg-sign/request", handlers.HandleGPGSignRequest)

	// Routes with path parameters need pattern matching
	apiMux.HandleFunc("/api/v1/pending/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/approve"):
			handlers.HandleApprove(w, r)
		case strings.HasSuffix(path, "/deny"):
			handlers.HandleDeny(w, r)
		case strings.HasSuffix(path, "/cancel"):
			handlers.HandleCancel(w, r)
		default:
			writeError(w, "not found", http.StatusNotFound)
		}
	})

	// Auth endpoint (no auth required - it's how you get auth)
	rootMux.HandleFunc("/api/v1/auth", handlers.HandleAuth)

	// All other API routes require auth
	rootMux.Handle("/api/", auth.Middleware(apiMux))

	// Static files (no auth required)
	rootMux.Handle("/", NewSPAHandler())

	// Create listener first to catch address-in-use errors early
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	httpServer := &http.Server{
		Handler:     rootMux,
		ConnContext: connContext,
	}

	s := &Server{
		httpServer: httpServer,
		auth:       auth,
		handlers:   handlers,
		wsHandler:  wsHandler,
		listener:   listener,
	}

	// Set up Unix socket listener if path is provided.
	if unixSocketPath != "" {
		// Remove stale socket file from previous runs (Pitfall 5).
		os.Remove(unixSocketPath) //nolint:errcheck

		// Ensure parent directory exists with restrictive permissions.
		if err := os.MkdirAll(filepath.Dir(unixSocketPath), 0700); err != nil {
			listener.Close()
			return nil, err
		}

		unixListener, err := net.Listen("unix", unixSocketPath)
		if err != nil {
			listener.Close()
			return nil, err
		}

		// Owner-only access: thin client runs as same user as daemon.
		os.Chmod(unixSocketPath, 0600) //nolint:errcheck

		s.unixListener = unixListener
		s.UnixSocketPath = unixSocketPath
	}

	return s, nil
}

// Start begins serving HTTP requests. This is non-blocking.
func (s *Server) Start() error {
	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
		}
	}()
	if s.unixListener != nil {
		go func() {
			if err := s.httpServer.Serve(s.unixListener); err != nil && err != http.ErrServerClosed {
				slog.Error("Unix socket server error", "error", err)
			}
		}()
	}
	return nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.unixListener != nil {
		s.unixListener.Close()
		if s.UnixSocketPath != "" {
			os.Remove(s.UnixSocketPath) //nolint:errcheck
		}
	}
	return s.httpServer.Shutdown(ctx)
}

// CookieFilePath returns the path to the authentication cookie file.
func (s *Server) CookieFilePath() string {
	return s.auth.FilePath()
}

// WSHandler returns the WebSocket handler for broadcasting client events.
func (s *Server) WSHandler() *WSHandler {
	return s.wsHandler
}

// SetTestMode enables test-only endpoints.
func (s *Server) SetTestMode(enabled bool) {
	s.testMode = enabled
	s.handlers.SetTestMode(enabled)
}
