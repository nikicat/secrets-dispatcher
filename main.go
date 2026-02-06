// secrets-dispatcher proxies Secret Service requests from a remote D-Bus to the local Secret Service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/api"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

const (
	defaultListenAddr = "127.0.0.1:8484"
	defaultTimeout    = 5 * time.Minute
)

func main() {
	var (
		remoteSocket = flag.String("remote-socket", "", "Path to the remote D-Bus socket (required)")
		clientName   = flag.String("client", "unknown", "Name of the remote client (for logging)")
		logLevel     = flag.String("log-level", "info", "Log level: debug, info, warn, error")
		listenAddr   = flag.String("listen", defaultListenAddr, "HTTP API listen address")
		timeout      = flag.Duration("timeout", defaultTimeout, "Approval timeout")
	)
	flag.Parse()

	if *remoteSocket == "" {
		fmt.Fprintln(os.Stderr, "error: --remote-socket is required")
		flag.Usage()
		os.Exit(1)
	}

	level := parseLogLevel(*logLevel)

	// Create approval manager
	approvalMgr := approval.NewManager(*clientName, *timeout)

	// Set up config directory for cookie
	configDir, err := getConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Create auth with cookie file
	auth, err := api.NewAuth(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating auth: %v\n", err)
		os.Exit(1)
	}

	// Create API server
	apiServer, err := api.NewServer(*listenAddr, approvalMgr, *remoteSocket, auth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating API server: %v\n", err)
		os.Exit(1)
	}

	// Start API server
	if err := apiServer.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error starting API server: %v\n", err)
		os.Exit(1)
	}
	slog.Info("API server started",
		"address", apiServer.Addr(),
		"cookie_file", apiServer.CookieFilePath())

	cfg := proxy.Config{
		RemoteSocketPath: *remoteSocket,
		ClientName:       *clientName,
		LogLevel:         level,
		Approval:         approvalMgr,
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

	// Graceful shutdown of API server
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		apiServer.Shutdown(shutdownCtx)
	}()

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

func getConfigDir() (string, error) {
	// Use XDG_CONFIG_HOME if set, otherwise ~/.config
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "secrets-dispatcher"), nil
}
