package sshagent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"golang.org/x/crypto/ssh/agent"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// Listens on a unix socket and proxies SSH agent connections, gating sign
// requests through the approval manager.
type Server struct {
	sockets  config.SSHConfig
	approval *approval.Manager
	chains   sender.ChainReader
	logger   *slog.Logger
}

func NewServer(sockets config.SSHConfig, approvalMgr *approval.Manager, chains sender.ChainReader, logger *slog.Logger) *Server {
	return &Server{
		sockets:  sockets,
		approval: approvalMgr,
		chains:   chains,
		logger:   logger,
	}
}

// Serves the socket until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	listener, err := s.sockets.Listen.Listen()
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.sockets.Listen, err)
	}

	s.logger.Info("SSH agent proxy listening", "socket", s.sockets.Listen, "upstream", s.sockets.Upstream)

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

		wg.Go(func() {
			s.handleConnection(ctx, conn)
		})
	}
}

func (s *Server) handleConnection(ctx context.Context, clientConn net.Conn) {
	defer clientConn.Close()

	senderInfo := s.extractSenderInfo(clientConn)
	senderInfo.Client = approval.ClientSSHAgent

	// Resolve SSH destination from the connecting process
	host, known := senderInfo.PID.SSHDestination()
	var destination *string
	if known {
		destination = &host
	}

	s.logger.Debug("new SSH agent connection",
		"pid", senderInfo.PID,
		"invoker", senderInfo.InvokerName,
		"destination", host)

	// Dial upstream agent
	upstreamConn, err := s.sockets.Upstream.Dial(ctx)
	if err != nil {
		s.logger.Error("failed to connect to upstream agent", "path", s.sockets.Upstream, "error", err)
		return
	}
	defer upstreamConn.Close()

	upstream := agent.NewClient(upstreamConn)
	proxy := newProxyAgent(upstream, s.approval, senderInfo, destination, s.logger)

	// agent.ServeAgent handles the SSH agent protocol framing. It always
	// returns an error, io.EOF when the client closes the connection.
	err = agent.ServeAgent(proxy, clientConn)
	if ctx.Err() == nil {
		s.logger.Debug("agent connection ended", "error", err)
	}
}

// Describes the process at the other end of conn.
func (s *Server) extractSenderInfo(conn net.Conn) approval.SenderInfo {
	peer, ok := unixsock.PeerOf(conn)
	if !ok {
		return approval.SenderInfo{}
	}
	info := approval.SenderInfo{
		PID:          peer.PID,
		UID:          new(peer.UID),
		ProcessChain: s.chains.Chain(peer.PID),
	}
	if invoker, ok := peer.PID.Invoker(); ok {
		info.InvokerName, info.PID = invoker.Name, invoker.PID
	}
	return info
}
