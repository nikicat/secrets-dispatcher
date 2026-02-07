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
	// Check for subcommands first
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "login":
			runLogin(os.Args[2:])
			return
		}
	}

	runProxy()
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	listenAddr := fs.String("listen", defaultListenAddr, "HTTP API listen address")
	configDirFlag := fs.String("config-dir", "", "Config directory (default: $XDG_CONFIG_HOME/secrets-dispatcher)")
	fs.Parse(args)

	var configDir string
	var err error
	if *configDirFlag != "" {
		configDir = *configDirFlag
	} else {
		configDir, err = getConfigDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	auth, err := api.LoadAuth(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "error: secrets-dispatcher is not running (no cookie file found)")
			fmt.Fprintln(os.Stderr, "Start the service first with: secrets-dispatcher --remote-socket <path>")
		} else {
			fmt.Fprintf(os.Stderr, "error loading auth: %v\n", err)
		}
		os.Exit(1)
	}

	url, err := auth.GenerateLoginURL(*listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating login URL: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Open this URL to access the Web UI:")
	fmt.Println(url)
	fmt.Println()
	fmt.Println("(Link expires in 5 minutes)")
}

func runProxy() {
	var (
		remoteSocket  = flag.String("remote-socket", "", "Path to the remote D-Bus socket (required unless --api-only)")
		clientName    = flag.String("client", "unknown", "Name of the remote client (for logging)")
		logLevel      = flag.String("log-level", "info", "Log level: debug, info, warn, error")
		listenAddr    = flag.String("listen", defaultListenAddr, "HTTP API listen address")
		timeout       = flag.Duration("timeout", defaultTimeout, "Approval timeout")
		configDirFlag = flag.String("config-dir", "", "Config directory (default: $XDG_CONFIG_HOME/secrets-dispatcher)")
		apiOnly       = flag.Bool("api-only", false, "Run only the API server (for testing)")
	)
	flag.Parse()

	if *remoteSocket == "" && !*apiOnly {
		fmt.Fprintln(os.Stderr, "error: --remote-socket is required (or use --api-only for testing)")
		flag.Usage()
		os.Exit(1)
	}

	level := parseLogLevel(*logLevel)

	// Create approval manager
	approvalMgr := approval.NewManager(*clientName, *timeout)

	// Set up config directory for cookie
	var configDir string
	var err error
	if *configDirFlag != "" {
		configDir = *configDirFlag
	} else {
		configDir, err = getConfigDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
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

	// Graceful shutdown of API server
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		apiServer.Shutdown(shutdownCtx)
	}()

	// In API-only mode, just wait for shutdown signal
	if *apiOnly {
		slog.Info("running in API-only mode (no D-Bus proxy)")
		<-ctx.Done()
		return
	}

	cfg := proxy.Config{
		RemoteSocketPath: *remoteSocket,
		ClientName:       *clientName,
		LogLevel:         level,
		Approval:         approvalMgr,
	}

	p := proxy.New(cfg)

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
