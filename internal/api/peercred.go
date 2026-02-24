package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

type connContextKey struct{}

// connContext returns a ConnContext function for http.Server that stores
// the net.Conn in the request context. This allows handlers to retrieve
// the underlying connection (e.g., for Unix socket peer credentials).
func connContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connContextKey{}, c)
}

// resolvePeerInfo extracts peer credentials from a Unix socket connection
// in the request context and resolves the grandparent process (the process
// that invoked git, which invoked the thin client).
//
// Process chain: terminal → git → secrets-dispatcher gpg-sign → (HTTP)
// We walk up 2 levels from the peer PID to find git's parent.
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

	// Walk up: peer (thin client) → parent (git) → grandparent (invoker)
	peerComm := readComm(cred.Pid)
	gitPID := readPPID(cred.Pid)
	if gitPID <= 1 {
		slog.Debug("process chain", "peer", fmt.Sprintf("%s[%d]", peerComm, cred.Pid))
		return approval.SenderInfo{PID: uint32(cred.Pid), UID: uint32(cred.Uid)}
	}

	gitComm := readComm(gitPID)
	invokerPID := readPPID(gitPID)
	if invokerPID <= 1 {
		slog.Debug("process chain",
			"peer", fmt.Sprintf("%s[%d]", peerComm, cred.Pid),
			"git", fmt.Sprintf("%s[%d]", gitComm, gitPID))
		return approval.SenderInfo{PID: uint32(gitPID), UID: uint32(cred.Uid), UnitName: gitComm}
	}

	invokerComm := readComm(invokerPID)
	slog.Debug("process chain",
		"peer", fmt.Sprintf("%s[%d]", peerComm, cred.Pid),
		"git", fmt.Sprintf("%s[%d]", gitComm, gitPID),
		"invoker", fmt.Sprintf("%s[%d]", invokerComm, invokerPID))

	return approval.SenderInfo{
		PID:      uint32(invokerPID),
		UID:      uint32(cred.Uid),
		UnitName: invokerComm,
	}
}

// readPPID reads the parent PID from /proc/<pid>/stat.
// Returns 0 on any error.
func readPPID(pid int32) int32 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// Format: "pid (comm) state ppid ..."
	// comm can contain spaces and parentheses, so find the last ')' first.
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return 0
	}
	// After ") " we have "state ppid ..."
	fields := strings.Fields(s[i+2:])
	if len(fields) < 2 {
		return 0
	}
	var ppid int32
	fmt.Sscanf(fields[1], "%d", &ppid)
	return ppid
}

// readComm reads the process name from /proc/<pid>/comm.
// Returns empty string on error.
func readComm(pid int32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
