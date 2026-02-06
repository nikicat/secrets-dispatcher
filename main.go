// secrets-dispatcher proxies Secret Service requests from a remote D-Bus to the local Secret Service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

func main() {
	var (
		remoteSocket = flag.String("remote-socket", "", "Path to the remote D-Bus socket (required)")
		clientName   = flag.String("client", "unknown", "Name of the remote client (for logging)")
		logLevel     = flag.String("log-level", "info", "Log level: debug, info, warn, error")
	)
	flag.Parse()

	if *remoteSocket == "" {
		fmt.Fprintln(os.Stderr, "error: --remote-socket is required")
		flag.Usage()
		os.Exit(1)
	}

	level := parseLogLevel(*logLevel)

	cfg := proxy.Config{
		RemoteSocketPath: *remoteSocket,
		ClientName:       *clientName,
		LogLevel:         level,
	}

	p := proxy.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	if err := p.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer p.Close()

	if err := p.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
