package api

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// One remote's signing socket. The socket identifies the remote (ADR 0011), so
// it takes no token and serves only gpgsign.RemoteSignPath.
type RemoteServer struct {
	name       config.RemoteName
	SocketPath unixsock.Path
	manager    *approval.Manager
	httpServer *http.Server
}

// Opens the named remote's signing socket in the server's runtime directory.
// Errors when the server has no API socket.
func (s *Server) ServeRemote(name config.RemoteName) (*RemoteServer, error) {
	if s.runtimeDir == "" {
		return nil, fmt.Errorf("remote %s: the API server has no API socket", name)
	}
	rs := &RemoteServer{name: name, SocketPath: s.runtimeDir.RemoteSocket(name), manager: s.handlers.resolver.Manager}
	listener, err := rs.SocketPath.ListenPrivate()
	if err != nil {
		return nil, fmt.Errorf("remote %s: %w", name, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(gpgsign.RemoteSignPath, rs.handleSign)
	rs.httpServer = &http.Server{Handler: mux}
	s.serveInBackground(rs.httpServer, listener, "remote socket server error", "remote", name)
	s.remotes = append(s.remotes, rs)
	return rs, nil
}

// Answers the remote's gpg-sign request with its resolution. A dropped
// connection cancels the request.
func (rs *RemoteServer) handleSign(w http.ResponseWriter, r *http.Request) {
	sign, ok := decodeSignRequest(w, r)
	if !ok {
		return
	}

	sender := approval.SenderInfo{Client: rs.name.Client()}
	resolved := rs.manager.Await(r.Context(), sign.RequestInfo(sender))
	slog.Info("remote gpg sign request resolved",
		"request_id", resolved.Request.ID, "remote", rs.name, "resolution", resolved.Resolution, signLogAttrs(sign, sender))
	writeJSON(w, gpgsign.OutcomeOf(resolved))
}
