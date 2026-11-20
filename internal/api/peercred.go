package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

type connContextKey struct{}

// connContext returns a ConnContext function for http.Server that stores
// the net.Conn in the request context. This allows handlers to retrieve
// the underlying connection (e.g., for Unix socket peer credentials).
func connContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connContextKey{}, c)
}

// Describes the process behind the unix socket connection in ctx. The invoker
// is the nearest process above git that is not a shell, since the peer is our
// gpg-sign thin client run by git.
func resolvePeerInfo(ctx context.Context, chains sender.ChainReader) approval.SenderInfo {
	c, ok := ctx.Value(connContextKey{}).(net.Conn)
	if !ok || c == nil {
		return approval.SenderInfo{}
	}
	peer, ok := unixsock.PeerOf(c)
	if !ok {
		return approval.SenderInfo{}
	}

	chain := chains.Chain(peer.PID)
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		labels := make([]string, len(chain))
		for i, p := range chain {
			labels[i] = fmt.Sprintf("%s[%d]", p.Name, p.PID)
		}
		slog.Debug("process chain", "chain", strings.Join(labels, " → "))
	}

	invokerName, invokerPID := peer.PID.Comm(), peer.PID
	if aboveGit := peer.PID.Parent().Parent(); aboveGit > 1 {
		if invoker, ok := aboveGit.Invoker(); ok {
			invokerName, invokerPID = invoker.Name, invoker.PID
		}
	}

	peerExe, peerExeKnown := peer.PID.Exe()
	return approval.SenderInfo{
		PID:          invokerPID,
		UID:          new(peer.UID),
		InvokerName:  invokerName,
		ProcessChain: chain,
		// A peer that runs our own binary is our gpg-sign thin client, so the
		// repo and changed-file fields it reported were computed by our trusted
		// code (Manager.CheckTrustedSigner).
		PeerTrusted: peerExeKnown && chains.IsSelf(peerExe),
	}
}
