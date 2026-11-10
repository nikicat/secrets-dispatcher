// Stands between applications and the user's secrets and asks the user to
// approve each access. It guards three ways in: the Secret Service, the
// ssh-agent and gpg commit signing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
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
	"github.com/nikicat/secrets-dispatcher/internal/sshagent"
	"gopkg.in/yaml.v3"
)

const (
	defaultListenAddr   = "127.0.0.1:8484"
	defaultTimeout      = 5 * time.Minute
	defaultHistoryLimit = 100
)

var (
	progName = filepath.Base(os.Args[0])
	version  = "dev" // injected via -ldflags "-X main.version=..."
)

func init() {
	// `go install module@version` builds without our Makefile ldflags; the
	// module version from build info is the next best truth.
	if version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
	}
}

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
	case "try":
		runTry(os.Args[2:])
	case "service":
		runService(os.Args[2:])
	case "gpg-sign":
		runGPGSign(os.Args[2:])
	case "provision":
		runProvision(os.Args[2:])
	case "daemon":
		runDaemon(os.Args[2:])
	case "version":
		fmt.Println(version)
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
  show          Show details of a request (pending or resolved)
  approve       Approve a pending request
  deny          Deny a pending request
  history       Show resolved requests
  config        Show or manage configuration
  try           Reversible trial: take over the Secret Service, Ctrl-C restores everything
  service       Manage the systemd user service
  gpg-sign      GPG signing proxy (called by git as gpg.program)
  gpg-sign setup  Configure git to use secrets-dispatcher for GPG signing
  provision     Provision companion user and deployment artifacts (requires root)
  daemon        Run companion daemon (registers on system D-Bus)
  version       Print the version and exit

Run '%s <command> -h' for command-specific help.
`, progName, progName)
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	listenAddr := fs.String("listen", defaultListenAddr, "HTTP API listen address")
	var stateDir config.StateDir
	fs.Var(&stateDir, "state-dir", "State `directory` (default: $XDG_STATE_HOME/secrets-dispatcher)")
	fs.Parse(args)

	// Load config and apply values for flags not explicitly set
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	set := setFlags(fs)
	if !set["state-dir"] && cfg.StateDir != "" {
		stateDir = cfg.StateDir
	}
	if !set["listen"] && cfg.Listen != "" {
		*listenAddr = cfg.Listen
	}

	auth, err := loadClientAuth(stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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
	fmt.Printf("(Single-use link; expires in %d minutes)\n", int(api.LoginLinkLifetime.Minutes()))

	// Try to open the URL in the default browser
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		// Silently ignore errors - user can still copy the URL manually
	}
}

func runCLI(cmd string, args []string) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	var stateDir config.StateDir
	fs.Var(&stateDir, "state-dir", "State `directory` (default: $XDG_STATE_HOME/secrets-dispatcher)")
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
		stateDir = cfg.StateDir
	}
	if !set["server"] && cfg.Listen != "" {
		*serverAddr = cfg.Listen
	}

	auth, err := loadClientAuth(stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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
		result, err := client.Show(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		formatter.FormatShowResult(result)

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
	var stateDir config.StateDir
	fs.Var(&stateDir, "state-dir", "State `directory` (default: $XDG_STATE_HOME/secrets-dispatcher)")
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
		stateDir = cfg.StateDir
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
	cfg, err = cfg.Resolve()
	if err != nil {
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
		handler = tint.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))

	// Create approval manager
	var trustedSigners []approval.TrustedSigner
	for _, ts := range cfg.Serve.TrustedSigners {
		trustedSigners = append(trustedSigners, approval.TrustedSigner{
			ExePath:    ts.ExePath,
			RepoPath:   ts.RepoPath,
			FilePrefix: ts.FilePrefix,
		})
	}
	var trustRules []approval.TrustRule
	for _, r := range cfg.Serve.Rules {
		tr := approval.TrustRule{
			Name:             r.Name,
			Action:           r.Action,
			RequestTypes:     r.RequestTypes,
			Client:           r.Client,
			SearchAttributes: r.SearchAttributes,
		}
		if r.Process != nil {
			tr.Process = &approval.ProcessMatcher{
				Exe:  r.Process.Exe,
				Name: r.Process.Name,
				Args: r.Process.Args,
				CWD:  r.Process.CWD,
				Unit: r.Process.Unit,
			}
		}
		if r.Secret != nil {
			tr.Secret = &approval.SecretMatcher{
				Collection: r.Secret.Collection,
				Label:      r.Secret.Label,
				Attributes: r.Secret.Attributes,
			}
		}
		trustRules = append(trustRules, tr)
	}
	approvalMgr := approval.NewManager(approval.ManagerConfig{
		Timeout:             *timeout,
		HistoryMax:          *historyLimit,
		ApprovalWindow:      time.Duration(cfg.Serve.ApprovalWindow),
		AutoApproveDuration: time.Duration(cfg.Serve.AutoApproveDuration),
		TrustedSigners:      trustedSigners,
		IgnoreChromeDummy:   *cfg.Serve.IgnoreChromeDummySecret,
		TrustRules:          trustRules,
	})

	// Set up desktop notifications
	var desktopNotifier *notification.DBusNotifier
	var notifHandler *notification.Handler
	var slowUpstreamNotifier proxy.UpstreamNotifier
	if *notifications {
		notifier, err := notification.NewDBusNotifier()
		if err != nil {
			slog.Warn("failed to create desktop notifier, notifications disabled", "error", err)
		} else {
			desktopNotifier = notifier
			slowUpstreamNotifier = notification.NewSlowUpstreamNotifier(notifier)
			slog.Debug("desktop notifications enabled")
		}
	}
	upstreamSlowThreshold := time.Duration(*cfg.Serve.UpstreamSlowThreshold)
	notificationDelay := time.Duration(cfg.Serve.NotificationDelay)
	if desktopNotifier != nil {
		notifHandler = notification.NewHandler(desktopNotifier, api.NewResolver(approvalMgr, slowUpstreamNotifier, upstreamSlowThreshold), "http://"+*listenAddr, *cfg.Serve.ShowPIDs, approvalMgr.AutoApproveDuration(), notificationDelay)
		approvalMgr.Subscribe(notifHandler)
	}

	stateDir, err = stateDir.OrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
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
	if cfg.Serve.Upstream.Type == config.BusSocket {
		upstreamAddr = "unix:path=" + cfg.Serve.Upstream.Path
	}

	// Build topology from config: create providers and runners for each downstream
	var providers []api.ClientProvider
	type downstreamRunner func(context.Context) error

	var runners []downstreamRunner

	// Each bus holds its client name while it runs.
	for _, ds := range cfg.Serve.Downstream {
		switch ds.Type {
		case config.BusSockets:
			mgr, mgrErr := proxy.NewManager(ds.Path, upstreamAddr, approvalMgr, level, slowUpstreamNotifier, upstreamSlowThreshold)
			if mgrErr != nil {
				fmt.Fprintf(os.Stderr, "error creating proxy manager: %v\n", mgrErr)
				os.Exit(1)
			}
			providers = append(providers, mgr)
			runners = append(runners, func(ctx context.Context) error {
				return mgr.Run(ctx)
			})

		case config.BusSessionBus:
			if !approvalMgr.ClaimClient(approval.ClientLocal) {
				fmt.Fprintf(os.Stderr, "error: client %q claimed twice (should be caught by validation)\n", approval.ClientLocal)
				os.Exit(1)
			}
			sp := &staticProvider{info: proxy.ClientInfo{Name: approval.ClientLocal, SocketPath: "session_bus"}}
			providers = append(providers, sp)
			runners = append(runners, func(ctx context.Context) error {
				if upstreamAddr == "" {
					return fmt.Errorf("upstream and downstream are both session_bus (should be caught by validation)")
				}
				const maxBackoff = 30 * time.Second
				backoff := time.Second
				for {
					p := proxy.New(proxy.Config{
						ClientName:            approval.ClientLocal,
						LocalProcesses:        true,
						LogLevel:              level,
						Approval:              approvalMgr,
						TrimProcessChain:      *cfg.Serve.TrimProcessChain,
						UpstreamNotifier:      slowUpstreamNotifier,
						UpstreamSlowThreshold: upstreamSlowThreshold,
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

		case config.BusSocket:
			clientName, err := approval.ClientFromSocket(ds.Path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			if !approvalMgr.ClaimClient(clientName) {
				fmt.Fprintf(os.Stderr, "error: client %q claimed twice (should be caught by validation)\n", clientName)
				os.Exit(1)
			}
			p := proxy.New(proxy.Config{
				ClientName:            clientName,
				LogLevel:              level,
				Approval:              approvalMgr,
				UpstreamNotifier:      slowUpstreamNotifier,
				UpstreamSlowThreshold: upstreamSlowThreshold,
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

	// Set up SSH agent proxy if configured
	if ssh := cfg.SSH; ssh != nil {
		if ssh.Upstream == "" {
			slog.Error("SSH agent proxy enabled but no upstream socket (set ssh.upstream in config or SSH_AUTH_SOCK)")
		} else {
			sshServer := sshagent.NewServer(ssh.Listen, ssh.Upstream, approvalMgr, *cfg.Serve.TrimProcessChain, slog.Default())
			runners = append(runners, func(ctx context.Context) error {
				return sshServer.Run(ctx)
			})
			slog.Info("SSH agent proxy configured", "listen", ssh.Listen, "upstream", ssh.Upstream)
		}
	}

	// Create API server
	apiServer, err := api.NewServerWithProvider(*listenAddr, approvalMgr, &multiProvider{providers: providers}, auth, *cfg.Serve.TrimProcessChain, slowUpstreamNotifier, upstreamSlowThreshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating API server: %v\n", err)
		os.Exit(1)
	}
	apiServer.WSHandler().SetNotificationDelay(notificationDelay)

	// Enable test mode for API-only mode
	if *apiOnly {
		apiServer.SetTestMode(true)
	}

	// Serve the API socket in the runtime directory, except with --api-only.
	if !*apiOnly {
		runtimeDir, err := config.LookupRuntimeDir()
		if err != nil {
			slog.Warn("the API socket will not be created", "error", err)
		} else if err := apiServer.ServeAPISocket(runtimeDir); err != nil {
			fmt.Fprintf(os.Stderr, "error opening API socket: %v\n", err)
			os.Exit(1)
		} else {
			slog.Info("API socket ready", "socket", runtimeDir.APISocket())
		}
	}

	// Start API server
	if err := apiServer.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error starting API server: %v\n", err)
		os.Exit(1)
	}
	slog.Info("API server started",
		"url", "http://"+apiServer.Addr(),
		"cookie_file", stateDir.Cookie())

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

	if err := serveRemotes(apiServer, cfg.Serve.Remotes); err != nil {
		fmt.Fprintf(os.Stderr, "error opening remote socket: %v\n", err)
		os.Exit(1)
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

// Opens the signing socket of every configured remote.
func serveRemotes(apiServer *api.Server, remotes []config.RemoteConfig) error {
	for _, remote := range remotes {
		rs, err := apiServer.ServeRemote(remote.Name)
		if err != nil {
			return err
		}
		slog.Info("remote signing socket ready", "remote", remote.Name, "socket", rs.SocketPath)
	}
	return nil
}

// staticProvider wraps a single client info for the API ClientProvider interface.
type staticProvider struct {
	info proxy.ClientInfo
}

func (s *staticProvider) Clients() []proxy.ClientInfo {
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
	case "edit":
		runConfigEdit(args[1:])
	case "validate":
		runConfigValidate(args[1:])
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
	path := configPathOrDefault(*configPath)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *defaults {
		cfg, err = cfg.WithDefaults()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Fprintf(os.Stderr, "# %s\n", path)

	out, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

func runConfigEdit(args []string) {
	fs := flag.NewFlagSet("config edit", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	fs.Parse(args)

	path := configPathOrDefault(*configPath)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config directory: %v\n", err)
		os.Exit(1)
	}

	// Read file content before editing (may not exist yet)
	before, _ := os.ReadFile(path)

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		fmt.Fprintln(os.Stderr, "error: $EDITOR is not set")
		os.Exit(1)
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "editor exited with error: %v\n", err)
		os.Exit(1)
	}

	// Validate the new config
	after, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading config after edit: %v\n", err)
		os.Exit(1)
	}

	if string(before) == string(after) {
		fmt.Fprintln(os.Stderr, "config unchanged")
		return
	}

	// Parse and validate
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: config has errors: %v\n", err)
		fmt.Fprintln(os.Stderr, "the file was saved but may not load correctly")
		return
	}
	_, err = cfg.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: config validation error: %v\n", err)
		fmt.Fprintln(os.Stderr, "the file was saved but may not load correctly")
		return
	}

	fmt.Fprintln(os.Stderr, "config updated")

	// Prompt for service restart
	fmt.Fprint(os.Stderr, "restart secrets-dispatcher service? [y/N] ")
	var answer string
	fmt.Scanln(&answer)
	if answer == "y" || answer == "Y" {
		restart := exec.Command("systemctl", "--user", "restart", "secrets-dispatcher.service")
		restart.Stdout = os.Stdout
		restart.Stderr = os.Stderr
		if err := restart.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error restarting service: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "service restarted")
	}
}

func runConfigValidate(args []string) {
	fs := flag.NewFlagSet("config validate", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	_, err = cfg.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "config ok")
}

func printConfigUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s config <command> [options]

Commands:
  show          Show the current configuration
  edit          Edit config in $EDITOR and optionally restart service
  validate      Validate config syntax and values

Options:
  --config      Path to config file (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)

Show-only options:
  --defaults    Show all fields with program defaults filled in
`, progName)
}

// runService handles the "service" subcommand group (install/uninstall/status).
// runTry handles the `try` command: a reversible trial of the takeover
// (US-9). Ctrl-C (or SIGTERM) restores the original Secret Service.
func runTry(args []string) {
	fs := flag.NewFlagSet("try", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print what the trial would change, then exit without changing anything")
	configPath := fs.String("config", "", "Base config the trial copies settings from, never modified (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	backend := fs.String("backend", "", "Backend for the trial: path or keyword (gopass, gnome-keyring); default auto-detects the current provider")
	narration := narrationFlag(fs)
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := service.Try(ctx, service.TryOptions{
		ConfigPath:  *configPath,
		BackendPath: *backend,
		DryRun:      *dryRun,
		Narration:   *narration,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runService(args []string) {
	if len(args) == 0 {
		printServiceUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "install":
		runServiceInstall(args[1:])
	case "uninstall":
		fs := flag.NewFlagSet("service uninstall", flag.ExitOnError)
		narration := narrationFlag(fs)
		fs.Parse(args[1:])
		if err := service.Uninstall(*narration); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Uninstalled — stock Secret Service restored")
	case "status":
		if err := service.Status(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
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
	start := fs.Bool("start", true, "Start the service immediately after installing (default; use --no-start to skip)")
	noStart := fs.Bool("no-start", false, "Install and enable only — don't start now (starts on next login)")
	configPath := fs.String("config", "", "Config file path (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)")
	mode := fs.String("mode", "local", "Topology mode: local (gate this desktop), remote (serve secrets to remote hosts), or full")
	backend := fs.String("backend", "", "Backend for local/full modes: path or keyword (gopass, gnome-keyring); default auto-detects the current provider")
	dryRun := fs.Bool("dry-run", false, "Print what the install would change, then exit without changing anything")
	narration := narrationFlag(fs)
	fs.Parse(args)

	opts := service.Options{
		ConfigPath:  *configPath,
		Start:       *start && !*noStart,
		Mode:        *mode,
		BackendPath: *backend,
		Narration:   *narration,
	}

	if *dryRun {
		if err := service.PrintPlan(opts); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := service.Install(opts); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// Registers the --verbose flag on fs and returns the narration it sets.
func narrationFlag(fs *flag.FlagSet) *service.Narration {
	n := service.NarrateMilestones
	fs.Var(&n, "verbose", "Narrate every file and unit change (default: milestones only)")
	return &n
}

func printServiceUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s service <command> [options]

Commands:
  install       Install and enable the systemd user service
  uninstall     Stop, disable, and remove the systemd user service
  status        Doctor-style health report: name owner, unit states, mask/demotion consistency

Install options:
  --mode        Topology mode: local (default — gate this desktop), remote, or full
  --no-start    Install and enable only — don't start now (starts on next login)
  --start       Start immediately after installing (default)
  --config      Config file path (default: $XDG_CONFIG_HOME/secrets-dispatcher/config.yaml)
  --backend     Backend for local/full modes: path or keyword (gopass, gnome-keyring); default auto-detects
  --dry-run     Print what the install would change, then exit without changing anything
  --verbose     Narrate every file and unit change (default: milestones only)
`, progName)
}

// Runs the gpg-sign subcommand. It either configures git to use this binary as
// its gpg program or acts as that program.
func runGPGSign(args []string) {
	if len(args) > 0 && args[0] == "setup" {
		runGPGSignSetup(args[1:])
		return
	}
	os.Exit(int(gpgsign.Run(args, os.Stdin)))
}

// runGPGSignSetup handles the "gpg-sign setup" subcommand.
func runGPGSignSetup(args []string) {
	fs := flag.NewFlagSet("gpg-sign setup", flag.ExitOnError)
	local := fs.Bool("local", false, "Configure per-repo (--local) instead of --global")
	remoteSocket := fs.String("remote-socket", "", "Sign through this forwarded remote socket (docs/REMOTE-PROXY.md)")
	fs.Parse(args) //nolint:errcheck

	scope := gpgsign.ScopeGlobal
	if *local {
		scope = gpgsign.ScopeLocal
	}

	if err := gpgsign.SetupGitConfig(scope, *remoteSocket); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// Runs the provision subcommand. It sets up the companion user's deployment or
// checks an existing one, and exits with status 1 when either fails.
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
		for _, r := range results {
			fmt.Println(r)
		}
		if !companion.AllPass(results) {
			os.Exit(1)
		}
		return
	}

	if err := companion.Provision(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// Runs the companion daemon, which serves net.mowaka.SecretsDispatcher1 on D-Bus
// until SIGTERM or SIGINT. Exits with status 1 when the daemon fails.
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
		handler = tint.NewTextHandler(os.Stderr, &tint.Options{
			Level:   level,
			NoColor: false,
		})
	}
	slog.SetDefault(slog.New(handler))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := daemon.Config{
		BusAddress: *busAddress,
		Version:    version,
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

// Loads the API auth token from dir, or from the default state directory when
// dir is empty. Errors when the token file is missing. That error says the
// daemon is not running.
func loadClientAuth(dir config.StateDir) (*api.Auth, error) {
	dir, err := dir.OrDefault()
	if err != nil {
		return nil, err
	}
	auth, err := api.LoadAuth(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s is not running (no cookie file found)\nStart the service first with: %s serve", progName, progName)
	}
	if err != nil {
		return nil, fmt.Errorf("load auth: %w", err)
	}
	return auth, nil
}

// Loads the config file at explicitPath, or the default one when explicitPath
// is empty.
func loadConfig(explicitPath string) (*config.Config, error) {
	if explicitPath != "" {
		return config.Load(explicitPath)
	}
	return config.LoadDefault()
}

// Returns path, or the default config file when path is empty. Exits when
// there is no default.
func configPathOrDefault(path string) string {
	path, err := config.PathOrDefault(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v (set XDG_CONFIG_HOME or use --config)\n", err)
		os.Exit(1)
	}
	return path
}

// setFlags returns the set of flag names that were explicitly provided on the command line.
func setFlags(fs *flag.FlagSet) map[string]bool {
	m := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { m[f.Name] = true })
	return m
}
