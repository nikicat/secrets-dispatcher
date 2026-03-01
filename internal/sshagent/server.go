package sshagent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/sys/unix"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// Server listens on a Unix socket and proxies SSH agent connections,
// gating sign requests through the approval manager.
type Server struct {
	listenPath        string
	upstreamPath      string
	approval          *approval.Manager
	trimProcessChain  bool
	logger            *slog.Logger
}

// NewServer creates a new SSH agent proxy server.
func NewServer(listenPath, upstreamPath string, approvalMgr *approval.Manager, trimProcessChain bool, logger *slog.Logger) *Server {
	return &Server{
		listenPath:       listenPath,
		upstreamPath:     upstreamPath,
		approval:         approvalMgr,
		trimProcessChain: trimProcessChain,
		logger:           logger,
	}
}

// Run starts the proxy server. It blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("unix", s.listenPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.listenPath, err)
	}

	s.logger.Info("SSH agent proxy listening", "socket", s.listenPath, "upstream", s.upstreamPath)

	var wg sync.WaitGroup
	defer wg.Wait()

	// Close listener when context is done to unblock Accept
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.logger.Error("accept error", "error", err)
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

func (s *Server) handleConnection(ctx context.Context, clientConn net.Conn) {
	defer clientConn.Close()

	senderInfo := s.extractSenderInfo(clientConn)

	// Resolve SSH destination from the connecting process
	destination := procutil.ResolveSSHDestination(int32(senderInfo.PID))

	s.logger.Debug("new SSH agent connection",
		"pid", senderInfo.PID,
		"invoker", senderInfo.UnitName,
		"destination", destination)

	// Dial upstream agent
	upstreamConn, err := net.Dial("unix", s.upstreamPath)
	if err != nil {
		s.logger.Error("failed to connect to upstream agent", "path", s.upstreamPath, "error", err)
		return
	}
	defer upstreamConn.Close()

	upstream := agent.NewClient(upstreamConn)
	proxy := newProxyAgent(upstream, s.approval, senderInfo, destination, s.logger)

	// agent.ServeAgent handles the SSH agent protocol framing.
	// It returns when the client connection is closed.
	if err := agent.ServeAgent(proxy, clientConn); err != nil {
		// Connection closed by client is normal
		if ctx.Err() == nil {
			s.logger.Debug("agent connection ended", "error", err)
		}
	}
}

// extractSenderInfo builds a SenderInfo from the Unix socket peer credentials.
func (s *Server) extractSenderInfo(conn net.Conn) approval.SenderInfo {
	uc, ok := conn.(*net.UnixConn)
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

	chain := procutil.ReadProcessChain(cred.Pid, s.trimProcessChain)
	processChain := make([]approval.ProcessInfo, len(chain))
	for i, entry := range chain {
		processChain[i] = approval.ProcessInfo{
			Name: entry.Comm,
			PID:  uint32(entry.PID),
		}
	}

	// Resolve invoker (skip shells)
	comm, invokerPID := procutil.ResolveInvoker(uint32(cred.Pid))

	return approval.SenderInfo{
		PID:          invokerPID,
		UID:          uint32(cred.Uid),
		UnitName:     comm,
		ProcessChain: processChain,
	}
}
