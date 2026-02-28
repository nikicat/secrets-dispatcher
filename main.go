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
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/lmittmann/tint"
	"github.com/nikicat/secrets-dispatcher/internal/api"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/cli"
	"github.com/nikicat/secrets-dispatcher/internal/companion"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/daemon"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/notification"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/service"
	"gopkg.in/yaml.v3"
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
	case "config":
		runConfig(os.Args[2:])
	case "service":
		runService(os.Args[2:])
	case "gpg-sign":
		runGPGSign(os.Args[2:])
	case "provision":
		runProvision(os.Args[2:])
	case "daemon":
		runDaemon(os.Args[2:])
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
  serve         Start the proxy server and API
  login         Generate a login URL for the web UI
  list          List pending approval requests
  show          Show details of a pending request
  approve       Approve a pending request
  deny          Deny a pending request
  history       Show resolved requests
  config        Show or manage configuration
  service       Manage the systemd user service
  gpg-sign      GPG signing proxy (called by git as gpg.program)
  gpg-sign setup  Configure git to use secrets-dispatcher for GPG signing
  provision     Provision companion user and deployment artifacts (requires root)
  daemon        Run companion daemon (registers on system D-Bus)

Run '%s <command> -h' for command-specific help.
`, progName, progName)
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	listenAddr := fs.String("listen", defaultListenAddr, "HTTP API listen address")
	stateDirFlag := fs.String("state-dir", "", "State directory (default: $XDG_STATE_HOME/secrets-dispatcher)")
	fs.Parse(args)

	// Load config and apply values for flags not explicitly set
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	set := setFlags(fs)
	if !set["state-dir"] && cfg.StateDir != "" {
		*stateDirFlag = cfg.StateDir
	}
	if !set["listen"] && cfg.Listen != "" {
		*listenAddr = cfg.Listen
	}

	var stateDir string
	if *stateDirFlag != "" {
		stateDir = *stateDirFlag
	} else {
		var sdErr error
		stateDir, sdErr = getStateDir()
		if sdErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", sdErr)
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
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	stateDirFlag := fs.String("state-dir", "", "State directory (default: $XDG_STATE_HOME/secrets-dispatcher)")
	serverAddr := fs.String("server", defaultListenAddr, "API server address")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	// Load config and apply values for flags not explicitly set
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	set := setFlags(fs)
	if !set["state-dir"] && cfg.StateDir != "" {
		*stateDirFlag = cfg.StateDir
	}
	if !set["server"] && cfg.Listen != "" {
		*serverAddr = cfg.Listen
	}

	var stateDir string
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
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	logLevel := fs.String("log-level", "info", "Log level: debug, info, warn, error")
	logFormat := fs.String("log-format", "text", "Log format: text (colored) or json")
	listenAddr := fs.String("listen", defaultListenAddr, "HTTP API listen address")
	timeout := fs.Duration("timeout", defaultTimeout, "Approval timeout")
	historyLimit := fs.Int("history-limit", defaultHistoryLimit, "Maximum number of resolved requests to keep in history")
	stateDirFlag := fs.String("state-dir", "", "State directory (default: $XDG_STATE_HOME/secrets-dispatcher)")
	apiOnly := fs.Bool("api-only", false, "Run only the API server (for testing)")
	notifications := fs.Bool("notifications", true, "Enable desktop notifications for approval requests")
	fs.Parse(args)

	// Load config and apply values for flags not explicitly set
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	set := setFlags(fs)
	if !set["state-dir"] && cfg.StateDir != "" {
		*stateDirFlag = cfg.StateDir
	}
	if !set["listen"] && cfg.Listen != "" {
		*listenAddr = cfg.Listen
	}
	if !set["log-level"] && cfg.Serve.LogLevel != "" {
		*logLevel = cfg.Serve.LogLevel
	}
	if !set["log-format"] && cfg.Serve.LogFormat != "" {
		*logFormat = cfg.Serve.LogFormat
	}
	if !set["timeout"] && cfg.Serve.Timeout != 0 {
		*timeout = time.Duration(cfg.Serve.Timeout)
	}
	if !set["history-limit"] && cfg.Serve.HistoryLimit != 0 {
		*historyLimit = cfg.Serve.HistoryLimit
	}
	if !set["notifications"] && cfg.Serve.Notifications != nil {
		*notifications = *cfg.Serve.Notifications
	}

	// Apply defaults and validate
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	level := parseLogLevel(*logLevel)

	// Set global slog default with configured level and format
	var handler slog.Handler
	switch *logFormat {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	default:
		// When running under systemd, the journal adds its own timestamps.
		underSystemd := os.Getenv("INVOCATION_ID") != ""
		opts := &tint.Options{
			Level:      level,
			TimeFormat: time.TimeOnly,
			NoColor:    underSystemd,
		}
		if underSystemd {
			opts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					return slog.Attr{}
				}
				return a
			}
		}
		handler = tint.NewHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))

	// Create approval manager
	approvalMgr := approval.NewManager(*timeout, *historyLimit, time.Duration(cfg.Serve.ApprovalWindow))

	// Set up desktop notifications
	var desktopNotifier *notification.DBusNotifier
	var notifHandler *notification.Handler
	if *notifications {
		notifier, err := notification.NewDBusNotifier()
		if err != nil {
			slog.Warn("failed to create desktop notifier, notifications disabled", "error", err)
		} else {
			desktopNotifier = notifier
			notifHandler = notification.NewHandler(notifier, api.NewResolver(approvalMgr), "http://"+*listenAddr)
			approvalMgr.Subscribe(notifHandler)
			slog.Debug("desktop notifications enabled")
		}
	}

	// Set up state directory for cookie
	var stateDir string
	if *stateDirFlag != "" {
		stateDir = *stateDirFlag
	} else {
		var sdErr error
		stateDir, sdErr = getStateDir()
		if sdErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", sdErr)
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

	// Start notification action listener (needs ctx)
	if desktopNotifier != nil {
		go notifHandler.ListenActions(ctx, desktopNotifier.Actions())
		defer desktopNotifier.Stop()
	}

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Resolve upstream address
	var upstreamAddr string
	if cfg.Serve.Upstream.Type == "socket" {
		upstreamAddr = "unix:path=" + cfg.Serve.Upstream.Path
	}

	// Build topology from config: create providers and runners for each downstream
	var providers []api.ClientProvider
	type downstreamRunner func(context.Context) error

	var runners []downstreamRunner

	for _, ds := range cfg.Serve.Downstream {
		switch ds.Type {
		case "sockets":
			mgr, mgrErr := proxy.NewManager(ds.Path, upstreamAddr, approvalMgr, level)
			if mgrErr != nil {
				fmt.Fprintf(os.Stderr, "error creating proxy manager: %v\n", mgrErr)
				os.Exit(1)
			}
			providers = append(providers, mgr)
			runners = append(runners, func(ctx context.Context) error {
				return mgr.Run(ctx)
			})

		case "session_bus":
			sp := &staticProvider{info: proxy.ClientInfo{Name: "local", SocketPath: "session_bus"}}
			providers = append(providers, sp)
			runners = append(runners, func(ctx context.Context) error {
				if upstreamAddr == "" {
					return fmt.Errorf("upstream and downstream are both session_bus (should be caught by validation)")
				}
				const maxBackoff = 30 * time.Second
				backoff := time.Second
				for {
					p := proxy.New(proxy.Config{
						ClientName: "local",
						LogLevel:   level,
						Approval:   approvalMgr,
					})
					frontConn, err := dbus.ConnectSessionBus()
					if err == nil {
						var backendConn *dbus.Conn
						backendConn, err = dbus.Connect(upstreamAddr)
						if err != nil {
							frontConn.Close()
						} else {
							err = p.ConnectWith(frontConn, backendConn)
							if err == nil {
								backoff = time.Second
								err = p.Run(ctx)
								p.Close()
							}
						}
					}
					if ctx.Err() != nil {
						return ctx.Err()
					}
					slog.Warn("session bus downstream disconnected, reconnecting",
						"error", err, "after", backoff)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(backoff):
					}
					backoff = min(backoff*2, maxBackoff)
				}
			})

		case "socket":
			clientName := filepath.Base(ds.Path)
			p := proxy.New(proxy.Config{
				ClientName: clientName,
				LogLevel:   level,
				Approval:   approvalMgr,
			})
			sp := &staticProvider{info: proxy.ClientInfo{Name: clientName, SocketPath: ds.Path}}
			providers = append(providers, sp)
			runners = append(runners, func(ctx context.Context) error {
				// Connect front=socket, backend=upstream
				frontConn, connErr := dbus.Connect("unix:path=" + ds.Path)
				if connErr != nil {
					return fmt.Errorf("connect to downstream socket %s: %w", ds.Path, connErr)
				}
				var backendConn *dbus.Conn
				if upstreamAddr == "" {
					backendConn, connErr = dbus.ConnectSessionBus()
				} else {
					backendConn, connErr = dbus.Connect(upstreamAddr)
				}
				if connErr != nil {
					frontConn.Close()
					return fmt.Errorf("connect to upstream: %w", connErr)
				}
				if connErr := p.ConnectWith(frontConn, backendConn); connErr != nil {
					return connErr
				}
				defer p.Close()
				return p.Run(ctx)
			})
		}
	}

	// Build composite ClientProvider
	var provider api.ClientProvider
	switch len(providers) {
	case 0:
		provider = &staticProvider{}
	case 1:
		provider = providers[0]
	default:
		provider = &multiProvider{providers: providers}
	}

	// Compute Unix socket path for thin client (gpg-sign subcommand).
	// Skip in --api-only mode (test/dev use case — no signing pipeline needed).
	var apiUnixSocket string
	if !*apiOnly {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			slog.Warn("XDG_RUNTIME_DIR is not set; Unix socket for gpg-sign will not be created")
		} else {
			apiUnixSocket = filepath.Join(runtimeDir, "secrets-dispatcher", "api.sock")
		}
	}

	// Create API server
	apiServer, err := api.NewServerWithProvider(*listenAddr, approvalMgr, provider, auth, apiUnixSocket)
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
	if apiServer.UnixSocketPath != "" {
		slog.Info("Unix socket ready for gpg-sign", "socket", apiServer.UnixSocketPath)
	}

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

	// Subscribe WebSocket handler to Manager observers
	for _, p := range providers {
		if mgr, ok := p.(*proxy.Manager); ok {
			mgr.Subscribe(apiServer.WSHandler())
		}
	}

	// Run all downstreams
	slog.Info("starting proxy topology",
		"upstream", cfg.Serve.Upstream.Type,
		"downstreams", len(cfg.Serve.Downstream))

	var wg sync.WaitGroup
	for i, run := range runners {
		wg.Add(1)
		go func(idx int, runFn downstreamRunner) {
			defer wg.Done()
			if err := runFn(ctx); err != nil && err != context.Canceled {
				slog.Error("downstream error", "index", idx, "error", err)
			}
		}(i, run)
	}
	wg.Wait()
}

// staticProvider wraps a single client info for the API ClientProvider interface.
type staticProvider struct {
	info proxy.ClientInfo
}

func (s *staticProvider) Clients() []proxy.ClientInfo {
	if s.info.Name == "" {
		return nil
	}
	return []proxy.ClientInfo{s.info}
}

// multiProvider aggregates multiple ClientProviders.
type multiProvider struct {
	providers []api.ClientProvider
}

func (m *multiProvider) Clients() []proxy.ClientInfo {
	var all []proxy.ClientInfo
	for _, p := range m.providers {
		all = append(all, p.Clients()...)
	}
	return all
}

// runConfig handles the "config" subcommand group.
func runConfig(args []string) {
	if len(args) == 0 {
		printConfigUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "show":
		runConfigShow(args[1:])
	case "-h", "--help", "help":
		printConfigUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown config command: %s\n\n", args[0])
		printConfigUsage()
		os.Exit(1)
	}
}

func runConfigShow(args []string) {
	fs := flag.NewFlagSet("config show", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	defaults := fs.Bool("defaults", false, "Show all fields with program defaults filled in")
	fs.Parse(args)

	// Determine which path to display
	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *defaults {
		cfg = cfg.WithDefaults()
	}

	fmt.Fprintf(os.Stderr, "# %s\n", path)

	out, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

func printConfigUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s config <command> [options]

Commands:
  show          Show the current configuration

Show options:
  --config      Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)
  --defaults    Show all fields with program defaults filled in
`, progName)
}

// runService handles the "service" subcommand group (install/uninstall/status).
func runService(args []string) {
	if len(args) == 0 {
		printServiceUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "install":
		runServiceInstall(args[1:])
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "status":
		service.Status()
	case "-h", "--help", "help":
		printServiceUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown service command: %s\n\n", args[0])
		printServiceUsage()
		os.Exit(1)
	}
}

func runServiceInstall(args []string) {
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	start := fs.Bool("start", false, "Start the service immediately after installing")
	configPath := fs.String("config", "", "Config file path (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	mode := fs.String("mode", "remote", "Topology mode: remote, local, or full")
	backend := fs.String("backend", "", "Backend binary path (default: gopass-secret-service from PATH)")
	fs.Parse(args)

	if err := service.Install(service.Options{
		ConfigPath:  *configPath,
		Start:       *start,
		Mode:        *mode,
		BackendPath: *backend,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printServiceUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s service <command> [options]

Commands:
  install       Install and enable the systemd user service
  uninstall     Stop, disable, and remove the systemd user service
  status        Show the service status

Install options:
  --start       Start the service immediately after installing
  --config      Config file path (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)
  --mode        Topology mode: remote, local, or full (default: remote)
  --backend     Backend binary path for local/full modes (default: gopass-secret-service from PATH)
`, progName)
}

// runGPGSign handles the gpg-sign subcommand.
// When called as "gpg-sign setup [--local]", it configures git to use this binary.
// Otherwise, it acts as a gpg proxy: reads commit object from stdin, sends to daemon,
// and blocks until the signing request is resolved.
func runGPGSign(args []string) {
	if len(args) > 0 && args[0] == "setup" {
		runGPGSignSetup(args[1:])
		return
	}
	os.Exit(gpgsign.Run(args, os.Stdin))
}

// runGPGSignSetup handles the "gpg-sign setup" subcommand.
func runGPGSignSetup(args []string) {
	fs := flag.NewFlagSet("gpg-sign setup", flag.ExitOnError)
	local := fs.Bool("local", false, "Configure per-repo (--local) instead of --global")
	fs.Parse(args) //nolint:errcheck

	scope := "global"
	if *local {
		scope = "local"
	}

	if err := gpgsign.SetupGitConfig(scope); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runProvision handles the "provision" subcommand.
// Without --check, it creates the companion user and all deployment artifacts.
// With --check, it validates an existing deployment and prints pass/fail per component.
func runProvision(args []string) {
	fs := flag.NewFlagSet("provision", flag.ExitOnError)
	desktopUser := fs.String("user", "", "Desktop username to provision companion for (default: $SUDO_USER)")
	companionName := fs.String("companion-name", "", "Override companion username (default: secrets-{user})")
	homeBase := fs.String("home-base", "/var/lib/secret-companion", "Parent directory for companion homes")
	check := fs.Bool("check", false, "Validate deployment instead of provisioning")
	fs.Parse(args)

	cfg := companion.Config{
		DesktopUser:   *desktopUser,
		CompanionName: *companionName,
		HomeBase:      *homeBase,
	}

	if *check {
		results := companion.Check(cfg)
		allPass := true
		for _, r := range results {
			status := "[PASS]"
			if !r.Pass {
				status = "[FAIL]"
				allPass = false
			}
			fmt.Printf("%s %s: %s\n", status, r.Name, r.Message)
		}
		if !allPass {
			os.Exit(1)
		}
		return
	}

	if err := companion.Provision(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runDaemon handles the "daemon" subcommand.
// Registers the companion daemon on the system D-Bus as net.mowaka.SecretsDispatcher1.
// Sends READY=1 via sd-notify after registration, then blocks until SIGTERM/SIGINT.
func runDaemon(args []string) {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	busAddress := fs.String("bus-address", "", "D-Bus address to connect to (default: system bus; non-empty used by tests)")
	logLevel := fs.String("log-level", "info", "Log level: debug, info, warn, error")
	fs.Parse(args)

	level := parseLogLevel(*logLevel)

	// Mirror the slog setup from runServe: JSON under systemd, tint for terminals.
	underSystemd := os.Getenv("INVOCATION_ID") != ""
	var handler slog.Handler
	if underSystemd {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		handler = tint.NewHandler(os.Stderr, &tint.Options{
			Level:   level,
			NoColor: false,
		})
	}
	slog.SetDefault(slog.New(handler))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := daemon.Config{
		BusAddress: *busAddress,
		Version:    "dev",
	}

	if err := daemon.Run(ctx, cfg); err != nil {
		slog.Error("daemon error", "err", err)
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

// loadConfig loads a config file. An explicit path that doesn't exist is an error.
// A missing default path is silently ignored (returns empty config).
func loadConfig(explicitPath string) (*config.Config, error) {
	if explicitPath != "" {
		cfg, err := config.Load(explicitPath)
		if err != nil {
			return nil, fmt.Errorf("load config %s: %w", explicitPath, err)
		}
		// If the explicit path didn't exist, Load returns empty config.
		// We need to distinguish: check if the file actually exists.
		if _, statErr := os.Stat(explicitPath); statErr != nil {
			return nil, fmt.Errorf("config file not found: %s", explicitPath)
		}
		return cfg, nil
	}

	defaultPath := config.DefaultPath()
	if defaultPath == "" {
		return &config.Config{}, nil
	}
	cfg, err := config.Load(defaultPath)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", defaultPath, err)
	}
	return cfg, nil
}

// setFlags returns the set of flag names that were explicitly provided on the command line.
func setFlags(fs *flag.FlagSet) map[string]bool {
	m := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { m[f.Name] = true })
	return m
}
