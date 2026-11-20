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

// Returns a context that SIGINT or SIGTERM cancels.
func shutdownContext() (ctx context.Context, stop context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case config.ServeCommand:
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
	fs := newSettingsFlags("login")
	fs.listenFlag("listen", "HTTP API listen `address`")
	cfg := fs.parse(args)

	endpoint, err := loadAPIEndpoint(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	url, err := endpoint.Auth.GenerateLoginURL(endpoint.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating login URL: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Open this URL to access the Web UI:")
	fmt.Println(url)
	fmt.Println()
	fmt.Printf("(Single-use link; expires in %d minutes)\n", int(api.LoginLinkLifetime.Minutes()))

	// The URL is printed above, so a failed open needs no report.
	_ = notification.OpenURL(url)
}

func runCLI(cmd string, args []string) {
	fs := newSettingsFlags(cmd)
	fs.listenFlag("server", "API server `address`")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	cfg := fs.parse(args)

	endpoint, err := loadAPIEndpoint(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	client := cli.NewClient(endpoint.Addr, endpoint.Auth.Token())
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

// The flags of serve.
type serveFlags struct {
	*settingsFlags
	apiOnly *bool
}

func newServeFlags() serveFlags {
	fs := newSettingsFlags(config.ServeCommand)
	fs.listenFlag("listen", "HTTP API listen `address`")
	logFlags(fs.FlagSet, &fs.cfg.Serve.LogLevel, &fs.cfg.Serve.LogFormat)
	fs.Var(&fs.cfg.Serve.Timeout, "timeout", "Approval timeout `duration`")
	showDefault(fs.FlagSet, "timeout", config.DefaultTimeout)
	fs.IntVar(&fs.cfg.Serve.HistoryLimit, "history-limit", 0, "Maximum number of resolved requests to keep in history")
	showDefault(fs.FlagSet, "history-limit", config.DefaultHistoryLimit)
	fs.Var(optionalBool{&fs.cfg.Serve.Notifications}, "notifications", "Enable desktop notifications for approval requests")
	showDefault(fs.FlagSet, "notifications", config.DefaultNotifications)
	apiOnly := fs.Bool("api-only", false, "Run only the API server (for testing)")
	return serveFlags{fs, apiOnly}
}

func runServe(args []string) {
	fs := newServeFlags()

	cfg, err := fs.parse(args).Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	level := cfg.Serve.LogLevel.Level()
	slog.SetDefault(slog.New(newLogHandler(cfg.Serve.LogLevel, cfg.Serve.LogFormat)))

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
		Timeout:             time.Duration(cfg.Serve.Timeout),
		HistoryMax:          cfg.Serve.HistoryLimit,
		ApprovalWindow:      time.Duration(cfg.Serve.ApprovalWindow),
		AutoApproveDuration: time.Duration(cfg.Serve.AutoApproveDuration),
		TrustedSigners:      trustedSigners,
		IgnoreChromeDummy:   *cfg.Serve.IgnoreChromeDummySecret,
		TrustRules:          trustRules,
	})

	// Set up desktop notifications
	var desktopNotifier *notification.DBusNotifier
	var notifHandler *notification.Handler
	slowUpstream := proxy.SlowUpstream{Threshold: time.Duration(*cfg.Serve.UpstreamSlowThreshold)}
	if *cfg.Serve.Notifications {
		notifier, err := notification.NewDBusNotifier()
		if err != nil {
			slog.Warn("failed to create desktop notifier, notifications disabled", "error", err)
		} else {
			desktopNotifier = notifier
			slowUpstream.Notifier = notification.NewSlowUpstreamNotifier(notifier)
			slog.Debug("desktop notifications enabled")
		}
	}
	resolver := api.NewResolver(approvalMgr, slowUpstream)
	notificationDelay := time.Duration(cfg.Serve.NotificationDelay)
	if desktopNotifier != nil {
		notifHandler = notification.NewHandler(desktopNotifier, resolver, cfg.Listen, *cfg.Serve.ShowPIDs, approvalMgr.AutoApproveDuration(), notificationDelay)
		approvalMgr.Subscribe(notifHandler)
	}

	stateDir, err := cfg.StateDir.OrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	auth, err := api.NewAuth(stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating auth: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := shutdownContext()
	defer stop()
	// stop also cancels ctx. Deferring this after stop makes it run first, so a
	// return without a signal logs nothing.
	defer context.AfterFunc(ctx, func() {
		slog.Info("received signal, shutting down", "cause", context.Cause(ctx))
	})()

	// Start notification action listener (needs ctx)
	if desktopNotifier != nil {
		go notifHandler.ListenActions(ctx, desktopNotifier.Actions())
		defer desktopNotifier.Stop()
	}

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
			mgr, mgrErr := proxy.NewManager(ds.Path, upstreamAddr, approvalMgr, level, slowUpstream)
			if mgrErr != nil {
				fmt.Fprintf(os.Stderr, "error creating proxy manager: %v\n", mgrErr)
				os.Exit(1)
			}
			providers = append(providers, mgr)
			runners = append(runners, func(ctx context.Context) error {
				return mgr.Run(ctx)
			})

		case config.BusSessionBus:
			claimClient(approvalMgr, approval.ClientLocal)
			sp := &staticProvider{info: proxy.ClientInfo{Name: approval.ClientLocal, SocketPath: string(config.BusSessionBus)}}
			providers = append(providers, sp)
			runners = append(runners, func(ctx context.Context) error {
				if upstreamAddr == "" {
					return fmt.Errorf("upstream and downstream are both session_bus (should be caught by validation)")
				}
				const maxBackoff = 30 * time.Second
				backoff := time.Second
				for {
					p := proxy.New(proxy.Config{
						ClientName:       approval.ClientLocal,
						LocalProcesses:   true,
						LogLevel:         level,
						Approval:         approvalMgr,
						TrimProcessChain: *cfg.Serve.TrimProcessChain,
						SlowUpstream:     slowUpstream,
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
			claimClient(approvalMgr, ds.Client)
			p := proxy.New(proxy.Config{
				ClientName:   ds.Client,
				LogLevel:     level,
				Approval:     approvalMgr,
				SlowUpstream: slowUpstream,
			})
			sp := &staticProvider{info: proxy.ClientInfo{Name: ds.Client, SocketPath: ds.Path}}
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
	apiServer, err := api.NewServer(api.ServerConfig{
		Addr:              cfg.Listen,
		Resolver:          resolver,
		Auth:              auth,
		TrimProcessChain:  *cfg.Serve.TrimProcessChain,
		NotificationDelay: notificationDelay,
		TestMode:          *fs.apiOnly,
	}, &multiProvider{providers: providers})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating API server: %v\n", err)
		os.Exit(1)
	}

	// Serve the API socket in the runtime directory, except with --api-only.
	if !*fs.apiOnly {
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
		"url", apiServer.Addr().BaseURL(),
		"cookie_file", stateDir.Cookie())

	// Graceful shutdown of API server
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		apiServer.Shutdown(shutdownCtx)
	}()

	// In API-only mode, just wait for shutdown signal
	if *fs.apiOnly {
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
			if err := runFn(ctx); err != nil && !errors.Is(err, context.Canceled) {
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
	fs := newConfigFlags("config show")
	defaults := fs.Bool("defaults", false, "Show all fields with program defaults filled in")
	fs.Parse(args)

	source := configPathOrDefault(fs.path)
	cfg, err := source.Load()
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

	fmt.Fprintf(os.Stderr, "# %s\n", source.File)

	out, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

func runConfigEdit(args []string) {
	fs := newConfigFlags("config edit")
	fs.Parse(args)

	path := configPathOrDefault(fs.path).File

	// Ensure parent directory exists
	if err := path.CreateDir(); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config directory: %v\n", err)
		os.Exit(1)
	}

	// Read file content before editing (may not exist yet)
	before, _ := path.ReadFile()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		fmt.Fprintln(os.Stderr, "error: $EDITOR is not set")
		os.Exit(1)
	}

	cmd := exec.Command(editor, path.String())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "editor exited with error: %v\n", err)
		os.Exit(1)
	}

	// Validate the new config
	after, err := path.ReadFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading config after edit: %v\n", err)
		os.Exit(1)
	}

	if string(before) == string(after) {
		fmt.Fprintln(os.Stderr, "config unchanged")
		return
	}

	// Parse and validate
	cfg, err := path.Load()
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
		if err := service.RestartProxy(); err != nil {
			fmt.Fprintf(os.Stderr, "error restarting service: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "service restarted")
	}
}

func runConfigValidate(args []string) {
	fs := newConfigFlags("config validate")
	fs.Parse(args)

	source, err := fs.path.OrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	cfg, err := source.Load()
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

Run '%s config <command> -h' for its options.
`, progName, progName)
}

// runService handles the "service" subcommand group (install/uninstall/status).
// Handles the `try` command: a reversible trial of the takeover. Ctrl-C (or
// SIGTERM) restores the original Secret Service.
func runTry(args []string) {
	fs := flag.NewFlagSet("try", flag.ExitOnError)
	var opts service.TryOptions
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Print what the trial would change, then exit without changing anything")
	configFlag(fs, &opts.ConfigPath, "Base config `file` the trial copies settings from, never modified")
	backendFlag(fs, &opts.Backend, "Backend for the trial")
	narrationFlag(fs, &opts.Narration)
	fs.Parse(args)

	ctx, stop := shutdownContext()
	defer stop()

	if err := service.Try(ctx, opts); err != nil {
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
		runServiceUninstall(args[1:])
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

func runServiceUninstall(args []string) {
	fs := flag.NewFlagSet("service uninstall", flag.ExitOnError)
	var narration service.Narration
	narrationFlag(fs, &narration)
	fs.Parse(args)
	if err := service.Uninstall(narration); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Uninstalled — stock Secret Service restored")
}

func runServiceInstall(args []string) {
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	opts := service.Options{Mode: service.ModeLocal}
	startFlags(fs, &opts.Start)
	configFlag(fs, &opts.ConfigPath, "Config `file` path")
	fs.Var(&opts.Mode, "mode", "Topology `mode`: local (gate this desktop), remote (serve secrets to remote hosts), or full")
	backendFlag(fs, &opts.Backend, "Backend for local/full modes")
	dryRun := fs.Bool("dry-run", false, "Print what the install would change, then exit without changing anything")
	narrationFlag(fs, &opts.Narration)
	fs.Parse(args)

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

func printServiceUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s service <command> [options]

Commands:
  install       Install and enable the systemd user service
  uninstall     Stop, disable, and remove the systemd user service
  status        Doctor-style health report: name owner, unit states, mask/demotion consistency

Run '%s service <command> -h' for its options.
`, progName, progName)
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
	var logLevel config.LogLevel
	var logFormat config.LogFormat
	logFlags(fs, &logLevel, &logFormat)
	fs.Parse(args)

	slog.SetDefault(slog.New(newLogHandler(logLevel, logFormat)))

	ctx, stop := shutdownContext()
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

// Returns a handler that writes log records at the given level and above to
// stderr, in the given format. Under systemd, text has no colors and no
// times, since the journal adds its own times.
func newLogHandler(level config.LogLevel, format config.LogFormat) slog.Handler {
	if format.OrDefault() == config.LogJSON {
		return slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level.Level()})
	}
	underSystemd := os.Getenv("INVOCATION_ID") != ""
	opts := &tint.Options{
		Level:      level.Level(),
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
	return tint.NewTextHandler(os.Stderr, opts)
}

// Claims name for one downstream client of serve. Exits when name is already
// claimed.
func claimClient(mgr *approval.Manager, name string) {
	if !mgr.ClaimClient(name) {
		fmt.Fprintf(os.Stderr, "error: client %q claimed twice (should be caught by validation)\n", name)
		os.Exit(1)
	}
}

type apiEndpoint struct {
	Addr config.ListenAddr
	Auth *api.Auth
}

// Returns the address of serve's API and the master token that clients send
// to it (ADR 0006), both found from the given settings. Errors when serve has
// not yet written a token in the state directory.
func loadAPIEndpoint(cfg *config.Config) (apiEndpoint, error) {
	dir, err := cfg.StateDir.OrDefault()
	if err != nil {
		return apiEndpoint{}, err
	}
	auth, err := api.LoadAuth(dir)
	if errors.Is(err, os.ErrNotExist) {
		return apiEndpoint{}, fmt.Errorf("no cookie file at %s\nStart the service first with: %s %s", dir.Cookie(), progName, config.ServeCommand)
	}
	if err != nil {
		return apiEndpoint{}, fmt.Errorf("load auth: %w", err)
	}
	return apiEndpoint{Addr: cfg.Listen.OrDefault(), Auth: auth}, nil
}

// Returns p.OrDefault(). Exits when p is empty and there is no default config
// file.
func configPathOrDefault(p config.Path) config.Source {
	file, err := p.OrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v (set XDG_CONFIG_HOME or use --%s)\n", err, config.ConfigFlag)
		os.Exit(1)
	}
	return file
}
