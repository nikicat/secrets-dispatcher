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

// Stores c in the request context, for resolvePeerInfo.
func connContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connContextKey{}, c)
}

// Returns the sender behind the unix socket connection in ctx, as ClientLocal.
// Without a unix peer, the sender holds only ClientUnknown. The invoker is the
// nearest non-shell process above git, since git runs the thin client (ADR 0008).
func resolvePeerInfo(ctx context.Context, chains sender.ChainReader) approval.SenderInfo {
	c, ok := ctx.Value(connContextKey{}).(net.Conn)
	if !ok || c == nil {
		return approval.SenderInfo{Client: approval.ClientUnknown}
	}
	peer, ok := unixsock.PeerOf(c)
	if !ok {
		return approval.SenderInfo{Client: approval.ClientUnknown}
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
		Client:       approval.ClientLocal,
		PID:          invokerPID,
		UID:          new(peer.UID),
		InvokerName:  invokerName,
		ProcessChain: chain,
		// A peer that runs our own binary is the gpg-sign thin client (ADR 0008).
		PeerTrusted: peerExeKnown && chains.IsSelf(peerExe),
	}
}
