// secrets-dispatcher proxies Secret Service requests from a remote D-Bus to the local Secret Service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/nikicat/secrets-dispatcher/internal/api"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/cli"
	"github.com/nikicat/secrets-dispatcher/internal/notification"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

const (
	defaultListenAddr   = "127.0.0.1:8484"
	defaultTimeout      = 5 * time.Minute
	defaultHistoryLimit = 100
)

var progName = filepath.Base(os.Args[0])

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "login":
		runLogin(os.Args[2:])
	case "list":
		runCLI("list", os.Args[2:])
	case "show":
		runCLI("show", os.Args[2:])
	case "approve":
		runCLI("approve", os.Args[2:])
	case "deny":
		runCLI("deny", os.Args[2:])
	case "history":
		runCLI("history", os.Args[2:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s <command> [options]

Commands:
  serve     Start the proxy server and API
  login     Generate a login URL for the web UI
  list      List pending approval requests
  show      Show details of a pending request
  approve   Approve a pending request
  deny      Deny a pending request
  history   Show resolved requests

Run '%s <command> -h' for command-specific help.
`, progName, progName)
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	listenAddr := fs.String("listen", defaultListenAddr, "HTTP API listen address")
	stateDirFlag := fs.String("state-dir", "", "State directory (default: $XDG_STATE_HOME/secrets-dispatcher)")
	fs.Parse(args)

	var stateDir string
	var err error
	if *stateDirFlag != "" {
		stateDir = *stateDirFlag
	} else {
		stateDir, err = getStateDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	auth, err := api.LoadAuth(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "error: %s is not running (no cookie file found)\n", progName)
			fmt.Fprintf(os.Stderr, "Start the service first with: %s serve\n", progName)
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

	// Try to open the URL in the default browser
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		// Silently ignore errors - user can still copy the URL manually
	}
}

func runCLI(cmd string, args []string) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	stateDirFlag := fs.String("state-dir", "", "State directory (default: $XDG_STATE_HOME/secrets-dispatcher)")
	serverAddr := fs.String("server", defaultListenAddr, "API server address")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	var stateDir string
	var err error
	if *stateDirFlag != "" {
		stateDir = *stateDirFlag
	} else {
		stateDir, err = getStateDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	auth, err := api.LoadAuth(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "error: %s is not running (no cookie file found)\n", progName)
			fmt.Fprintf(os.Stderr, "Start the service first with: %s serve\n", progName)
		} else {
			fmt.Fprintf(os.Stderr, "error loading auth: %v\n", err)
		}
		os.Exit(1)
	}

	client := cli.NewClient(*serverAddr, auth.Token())
	formatter := cli.NewFormatter(os.Stdout, *jsonOutput)

	switch cmd {
	case "list":
		requests, err := client.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		formatter.FormatRequests(requests)

	case "show":
		if fs.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s show <request-id>\n", progName)
			os.Exit(1)
		}
		req, err := client.Show(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		formatter.FormatRequest(req)

	case "approve":
		if fs.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s approve <request-id>\n", progName)
			os.Exit(1)
		}
		id := fs.Arg(0)
		if err := client.Approve(id); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		formatter.FormatAction("approved", id)

	case "deny":
		if fs.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s deny <request-id>\n", progName)
			os.Exit(1)
		}
		id := fs.Arg(0)
		if err := client.Deny(id); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		formatter.FormatAction("denied", id)

	case "history":
		entries, err := client.History()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		formatter.FormatHistory(entries)
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	remoteSocket := fs.String("remote-socket", "", "Path to the remote D-Bus socket (single-socket mode)")
	socketsDir := fs.String("sockets-dir", "", "Directory to watch for socket files (default: $XDG_RUNTIME_DIR/secrets-dispatcher)")
	clientName := fs.String("client", "unknown", "Name of the remote client (for logging, single-socket mode)")
	logLevel := fs.String("log-level", "info", "Log level: debug, info, warn, error")
	logFormat := fs.String("log-format", "text", "Log format: text (colored) or json")
	listenAddr := fs.String("listen", defaultListenAddr, "HTTP API listen address")
	timeout := fs.Duration("timeout", defaultTimeout, "Approval timeout")
	historyLimit := fs.Int("history-limit", defaultHistoryLimit, "Maximum number of resolved requests to keep in history")
	stateDirFlag := fs.String("state-dir", "", "State directory (default: $XDG_STATE_HOME/secrets-dispatcher)")
	apiOnly := fs.Bool("api-only", false, "Run only the API server (for testing)")
	notifications := fs.Bool("notifications", true, "Enable desktop notifications for approval requests")
	fs.Parse(args)

	// Validate mode selection
	if *remoteSocket != "" && *socketsDir != "" {
		fmt.Fprintln(os.Stderr, "error: --remote-socket and --sockets-dir are mutually exclusive")
		fs.Usage()
		os.Exit(1)
	}

	// Use default sockets directory if neither mode is specified
	if *remoteSocket == "" && *socketsDir == "" && !*apiOnly {
		defaultDir, err := getSocketsDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		*socketsDir = defaultDir
	}

	level := parseLogLevel(*logLevel)

	// Set global slog default with configured level and format
	var handler slog.Handler
	switch *logFormat {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	default:
		handler = tint.NewHandler(os.Stderr, &tint.Options{
			Level:      level,
			TimeFormat: time.TimeOnly,
		})
	}
	slog.SetDefault(slog.New(handler))

	// Create approval manager
	approvalMgr := approval.NewManager(*timeout, *historyLimit)

	// Set up desktop notifications
	if *notifications {
		notifier, err := notification.NewDBusNotifier()
		if err != nil {
			slog.Warn("failed to create desktop notifier, notifications disabled", "error", err)
		} else {
			handler := notification.NewHandler(notifier)
			approvalMgr.Subscribe(handler)
			slog.Debug("desktop notifications enabled")
		}
	}

	// Set up state directory for cookie
	var stateDir string
	var err error
	if *stateDirFlag != "" {
		stateDir = *stateDirFlag
	} else {
		stateDir, err = getStateDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// Create auth with cookie file
	auth, err := api.NewAuth(stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating auth: %v\n", err)
		os.Exit(1)
	}

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

	// Create proxy manager for multi-socket mode (needed before API server for ClientProvider)
	var proxyMgr *proxy.Manager
	if *socketsDir != "" {
		var err error
		proxyMgr, err = proxy.NewManager(*socketsDir, approvalMgr, level)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating proxy manager: %v\n", err)
			os.Exit(1)
		}
	}

	// Create API server
	var apiServer *api.Server
	if proxyMgr != nil {
		apiServer, err = api.NewServerWithProvider(*listenAddr, approvalMgr, proxyMgr, auth)
	} else {
		apiServer, err = api.NewServer(*listenAddr, approvalMgr, *remoteSocket, *clientName, auth)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating API server: %v\n", err)
		os.Exit(1)
	}

	// Enable test mode for API-only mode
	if *apiOnly {
		apiServer.SetTestMode(true)
	}

	// Start API server
	if err := apiServer.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error starting API server: %v\n", err)
		os.Exit(1)
	}
	slog.Info("API server started",
		"url", "http://"+apiServer.Addr(),
		"cookie_file", apiServer.CookieFilePath())

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

	// Multi-socket mode
	if proxyMgr != nil {
		slog.Info("running in multi-socket mode", "sockets_dir", *socketsDir)

		// Subscribe WebSocket handler to receive client connect/disconnect events
		proxyMgr.Subscribe(apiServer.WSHandler())

		if err := proxyMgr.Run(ctx); err != nil && err != context.Canceled {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Single-socket mode
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

func getStateDir() (string, error) {
	// Use XDG_STATE_HOME if set, otherwise ~/.local/state
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "secrets-dispatcher"), nil
}

func getSocketsDir() (string, error) {
	// Use XDG_RUNTIME_DIR for runtime files like sockets
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is not set")
	}
	return filepath.Join(runtimeDir, "secrets-dispatcher"), nil
}
