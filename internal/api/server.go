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

// NewServer creates a new API server.
func NewServer(addr string, manager *approval.Manager, remoteSocket string, auth *Auth) (*Server, error) {
	handlers := NewHandlers(manager, remoteSocket)

	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("/api/v1/status", handlers.HandleStatus)
	mux.HandleFunc("/api/v1/pending", handlers.HandlePendingList)
	mux.HandleFunc("/api/v1/log", handlers.HandleLog)

	// Routes with path parameters need pattern matching
	mux.HandleFunc("/api/v1/pending/", func(w http.ResponseWriter, r *http.Request) {
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

	// Wrap all handlers with auth middleware
	handler := auth.Middleware(mux)

	// Create listener first to catch address-in-use errors early
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	httpServer := &http.Server{
		Handler: handler,
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
