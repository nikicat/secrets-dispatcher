package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Server is the HTTP API server.
type Server struct {
	httpServer *http.Server
	auth       *Auth
	handlers   *Handlers
	listener   net.Listener
}

// NewServer creates a new API server for single-socket mode.
func NewServer(addr string, manager *approval.Manager, remoteSocket, clientName string, auth *Auth) (*Server, error) {
	return newServerWithHandlers(addr, NewHandlers(manager, remoteSocket, clientName, auth), auth)
}

// NewServerWithProvider creates a new API server for multi-socket mode.
func NewServerWithProvider(addr string, manager *approval.Manager, provider ClientProvider, auth *Auth) (*Server, error) {
	return newServerWithHandlers(addr, NewHandlersWithProvider(manager, provider, auth), auth)
}

// newServerWithHandlers creates a new API server with the given handlers.
func newServerWithHandlers(addr string, handlers *Handlers, auth *Auth) (*Server, error) {

	// Create the main router
	rootMux := http.NewServeMux()

	// API routes that require auth
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/v1/status", handlers.HandleStatus)
	apiMux.HandleFunc("/api/v1/pending", handlers.HandlePendingList)
	apiMux.HandleFunc("/api/v1/log", handlers.HandleLog)

	// Routes with path parameters need pattern matching
	apiMux.HandleFunc("/api/v1/pending/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/approve"):
			handlers.HandleApprove(w, r)
		case strings.HasSuffix(path, "/deny"):
			handlers.HandleDeny(w, r)
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
		Handler: rootMux,
	}

	return &Server{
		httpServer: httpServer,
		auth:       auth,
		handlers:   handlers,
		listener:   listener,
	}, nil
}

// Start begins serving HTTP requests. This is non-blocking.
func (s *Server) Start() error {
	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
		}
	}()
	return nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// CookieFilePath returns the path to the authentication cookie file.
func (s *Server) CookieFilePath() string {
	return s.auth.FilePath()
}
