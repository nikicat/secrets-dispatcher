package api

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
)

// One remote's signing socket. The socket identifies the remote (ADR 0011), so
// it takes no token and serves only gpgsign.RemoteSignPath.
type RemoteServer struct {
	name       config.RemoteName
	SocketPath string
	manager    *approval.Manager
	httpServer *http.Server
}

// Opens the named remote's signing socket in the server's runtime directory.
// Errors when the server has no API socket.
func (s *Server) ServeRemote(name config.RemoteName) (*RemoteServer, error) {
	if s.runtimeDir == "" {
		return nil, fmt.Errorf("remote %s: the API server has no API socket", name)
	}
	rs := &RemoteServer{name: name, SocketPath: s.runtimeDir.RemoteSocket(name), manager: s.handlers.manager}
	listener, err := listenUnix(rs.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("remote %s: %w", name, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(gpgsign.RemoteSignPath, rs.handleSign)
	rs.httpServer = &http.Server{Handler: mux}
	serveInBackground(rs.httpServer, listener, "remote socket server error", "remote", name)
	s.remotes = append(s.remotes, rs)
	return rs, nil
}

// The approval client every request from this remote is filed under.
func (rs *RemoteServer) client() string {
	return "remote:" + string(rs.name)
}

// Answers the remote's gpg-sign request with its resolution. A dropped
// connection cancels the request.
func (rs *RemoteServer) handleSign(w http.ResponseWriter, r *http.Request) {
	var req gpgsign.RemoteSignRequest
	if !decodePost(w, r, &req) || !requireSignedBytes(w, req.GPGSignInfo) {
		return
	}
	client := rs.client()
	senderInfo := approval.SenderInfo{InvokerName: client}
	bindDisplayToSignedPayload(req.GPGSignInfo, senderInfo)

	resolved, resolution, err := rs.manager.AwaitGPGSign(r.Context(), client, req.GPGSignInfo, senderInfo)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("remote gpg sign request resolved", "request_id", resolved.ID, "remote", rs.name, "repo", req.GPGSignInfo.RepoName, "resolution", resolution)
	writeJSON(w, gpgsign.SignOutcome{Result: resolution, GPGResult: resolved.GPGOutput()})
}
