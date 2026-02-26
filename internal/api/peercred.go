package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

type connContextKey struct{}

// connContext returns a ConnContext function for http.Server that stores
// the net.Conn in the request context. This allows handlers to retrieve
// the underlying connection (e.g., for Unix socket peer credentials).
func connContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connContextKey{}, c)
}

type procInfo struct {
	pid  int32
	comm string
}

func (p procInfo) String() string {
	return fmt.Sprintf("%s[%d]", p.comm, p.pid)
}

// resolvePeerInfo extracts peer credentials from a Unix socket connection
// in the request context and resolves the user-facing process that invoked
// git commit.
//
// Process chain example: claude → zsh → git → secrets-dispatcher → (HTTP)
// We walk up from the peer PID, skip the thin client and git, then skip
// any intermediate shells to find the real invoker (e.g., "claude").
func resolvePeerInfo(ctx context.Context) approval.SenderInfo {
	c, ok := ctx.Value(connContextKey{}).(net.Conn)
	if !ok || c == nil {
		return approval.SenderInfo{}
	}

	uc, ok := c.(*net.UnixConn)
	if !ok {
		return approval.SenderInfo{}
	}

	raw, err := uc.SyscallConn()
	if err != nil {
		return approval.SenderInfo{}
	}

	var cred *unix.Ucred
	var credErr error
	raw.Control(func(fd uintptr) { //nolint:errcheck
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if credErr != nil || cred == nil {
		return approval.SenderInfo{}
	}

	// Build the process chain from peer up to init.
	var chain []procInfo
	for pid := cred.Pid; pid > 1; pid = procutil.ReadPPID(pid) {
		chain = append(chain, procInfo{pid: pid, comm: procutil.ReadComm(pid)})
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		labels := make([]string, len(chain))
		for i, p := range chain {
			labels[i] = p.String()
		}
		slog.Debug("process chain", "chain", strings.Join(labels, " → "))
	}

	// chain[0] = thin client, chain[1] = git, chain[2+] = ancestors.
	// Start from git's parent and skip shells.
	invoker := chain[0] // fallback to peer
	for i := 2; i < len(chain); i++ {
		invoker = chain[i]
		if !procutil.IsShell(chain[i].comm) {
			break
		}
	}

	return approval.SenderInfo{
		PID:      uint32(invoker.pid),
		UID:      uint32(cred.Uid),
		UnitName: invoker.comm,
	}
}
