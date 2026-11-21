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
	var body gpgsign.SignRequest
	if !decodePost(w, r, &body) || !requireSignedBytes(w, body.GPGSignInfo) {
		return
	}
	request, ok := boundSignRequest(w, body.GPGSignInfo, approval.SenderInfo{Client: rs.name.Client()})
	if !ok {
		return
	}

	resolved, resolution, err := rs.manager.AwaitGPGSign(r.Context(), request)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("remote gpg sign request resolved", "request_id", resolved.ID, "remote", rs.name, "repo", request.GPGSignInfo.RepoName, "resolution", resolution)
	writeJSON(w, gpgsign.SignOutcome{Result: resolution, GPGResult: resolved.GPGOutput()})
}
