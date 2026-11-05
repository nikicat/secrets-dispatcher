package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
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
	// The remote sockets ServeRemote opened. Shutdown closes them under its deadline.
	remotes []*RemoteServer
}

// NewServer creates a new API server for single-socket mode.
// If unixSocketPath is non-empty, the server also listens on that Unix socket
// to serve the thin client (gpg-sign subcommand).
func NewServer(addr string, manager *approval.Manager, remoteSocket, clientName string, auth *Auth, unixSocketPath string, trimProcessChain bool, upstreamNotifier proxy.UpstreamNotifier, slowThreshold time.Duration) (*Server, error) {
	handlers := NewHandlers(manager, remoteSocket, clientName, auth, trimProcessChain, upstreamNotifier, slowThreshold)
	wsHandler := NewWSHandler(manager, nil, auth, remoteSocket, clientName)
	return newServerWithHandlers(addr, handlers, wsHandler, auth, unixSocketPath)
}

// NewServerWithProvider creates a new API server for multi-socket mode.
// If unixSocketPath is non-empty, the server also listens on that Unix socket
// to serve the thin client (gpg-sign subcommand).
func NewServerWithProvider(addr string, manager *approval.Manager, provider ClientProvider, auth *Auth, unixSocketPath string, trimProcessChain bool, upstreamNotifier proxy.UpstreamNotifier, slowThreshold time.Duration) (*Server, error) {
	handlers := NewHandlersWithProvider(manager, provider, auth, trimProcessChain, upstreamNotifier, slowThreshold)
	wsHandler := NewWSHandler(manager, provider, auth, "", "")
	return newServerWithHandlers(addr, handlers, wsHandler, auth, unixSocketPath)
}

// Creates an API server listening on addr, and also on unixSocketPath when it is
// non-empty.
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
	apiMux.HandleFunc("/api/v1/auto-approve", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlers.HandleAutoApproveList(w, r)
		case http.MethodPost:
			handlers.HandleAutoApproveCreate(w, r)
		default:
			writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	apiMux.HandleFunc("/api/v1/auto-approve/", handlers.HandleAutoApproveDelete)

	// Routes with path parameters need pattern matching
	apiMux.HandleFunc("/api/v1/pending/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/approve-and-auto-approve"):
			handlers.HandleApproveAndAutoApprove(w, r)
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

	if unixSocketPath != "" {
		unixListener, err := listenUnix(unixSocketPath)
		if err != nil {
			listener.Close()
			return nil, err
		}
		s.unixListener = unixListener
		s.UnixSocketPath = unixSocketPath
	}

	return s, nil
}

// Listens on a new Unix socket at path that only the owner can connect to.
// Closing the listener removes the socket file.
func listenUnix(path string) (net.Listener, error) {
	os.Remove(path) //nolint:errcheck // stale socket from a previous run
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// Serves srv on listener in the background and logs any failure other than shutdown.
func serveInBackground(srv *http.Server, listener net.Listener, failure string, logArgs ...any) {
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error(failure, append(logArgs, "error", err)...)
		}
	}()
}

// Begins serving HTTP requests in the background.
func (s *Server) Start() error {
	serveInBackground(s.httpServer, s.listener, "HTTP server error")
	if s.unixListener != nil {
		serveInBackground(s.httpServer, s.unixListener, "Unix socket server error")
	}
	return nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Gracefully shuts down the server and its remote sockets. Errors when ctx ends
// before the requests in flight finish.
func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error
	for _, rs := range s.remotes {
		errs = append(errs, rs.httpServer.Shutdown(ctx))
	}
	if s.unixListener != nil {
		s.unixListener.Close()
	}
	return errors.Join(append(errs, s.httpServer.Shutdown(ctx))...)
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
