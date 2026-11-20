// Stands between applications and the user's secrets and asks the user to
// approve each access. It guards three ways in: the Secret Service, the
// ssh-agent and gpg commit signing.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/lmittmann/tint"
	"github.com/nikicat/secrets-dispatcher/internal/api"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/cli"
	"github.com/nikicat/secrets-dispatcher/internal/companion"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/daemon"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/notification"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/service"
	"github.com/nikicat/secrets-dispatcher/internal/sshagent"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
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
	if args, ok := gpgProgramArgs(os.Args[1:]); ok {
		os.Exit(int(gpgsign.Run(args, os.Stdin)))
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// Runs the command that args select.
func run(args []string) error {
	var c commandLine
	parser, err := newParser(&c)
	if err != nil {
		return err
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	stop := func() {}
	defer func() { stop() }()
	if err := kctx.BindSingletonProvider(func() context.Context {
		var ctx context.Context
		ctx, stop = shutdownContext()
		return ctx
	}); err != nil {
		return err
	}
	return kctx.Run()
}

// Returns the arguments that git passes to gpg-sign as its gpg.program, and
// whether args are such a call, which is any gpg-sign call but setup. They
// carry gpg's own flags, so they bypass the command-line parser (ADR 0008).
func gpgProgramArgs(args []string) ([]string, bool) {
	if len(args) == 0 || args[0] != gpgsign.Command {
		return nil, false
	}
	if len(args) > 1 && args[1] == gpgSignSetupCommand {
		return nil, false
	}
	return args[1:], true
}

// The one gpg-sign subcommand that the command-line parser handles.
const gpgSignSetupCommand = "setup"

// Returns the parser of the command line into c.
func newParser(c *commandLine) (*kong.Kong, error) {
	return kong.New(c,
		kong.Name(progName),
		kong.Description("Stands between applications and the user's secrets and asks the user to approve each access."),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
		helpVars(),
	)
}

// The values that the help texts in commandLine interpolate.
func helpVars() kong.Vars {
	return kong.Vars{
		"default_config":        config.DefaultPathHelp,
		"default_state_dir":     config.DefaultStateDirHelp,
		"default_listen":        config.DefaultListenAddr.String(),
		"log_levels":            config.LogLevels.String(),
		"default_log_level":     config.DefaultLogLevel.String(),
		"log_formats":           config.LogFormats.String(),
		"default_log_format":    config.DefaultLogFormat.String(),
		"default_timeout":       config.DefaultTimeout.String(),
		"default_history_limit": strconv.Itoa(config.DefaultHistoryLimit),
		"default_notifications": strconv.FormatBool(config.DefaultNotifications),
		"modes":                 service.Modes.String(),
		"default_mode":          service.ModeLocal.String(),
		"default_home_base":     companion.DefaultHomeBase,
		"backends":              service.BackendKeywords(),
	}
}

type commandLine struct {
	Serve     serveCmd     `cmd:"" help:"Start the proxy server and API."`
	Login     loginCmd     `cmd:"" help:"Generate a login URL for the web UI."`
	List      listCmd      `cmd:"" help:"List pending approval requests."`
	Show      showCmd      `cmd:"" help:"Show details of a request (pending or resolved)."`
	Approve   approveCmd   `cmd:"" help:"Approve a pending request."`
	Deny      denyCmd      `cmd:"" help:"Deny a pending request."`
	History   historyCmd   `cmd:"" help:"Show resolved requests."`
	Config    configCmd    `cmd:"" help:"Show or manage configuration."`
	Try       tryCmd       `cmd:"" help:"Reversible trial: take over the Secret Service, Ctrl-C restores everything."`
	Service   serviceCmd   `cmd:"" help:"Manage the systemd user service."`
	GPGSign   gpgSignCmd   `cmd:"" name:"gpg-sign" help:"GPG signing proxy, which git runs as gpg.program."`
	Provision provisionCmd `cmd:"" help:"Provision companion user and deployment artifacts (requires root)."`
	Daemon    daemonCmd    `cmd:"" help:"Run companion daemon (registers on system D-Bus)."`
	Version   versionCmd   `cmd:"" help:"Print the version and exit."`
}

type loginCmd struct {
	settingsFlags
	listenFlag
}

func (c loginCmd) Run() error {
	endpoint, err := c.endpoint(c.Listen)
	if err != nil {
		return err
	}
	url, err := endpoint.Auth.GenerateLoginURL(endpoint.Addr)
	if err != nil {
		return fmt.Errorf("generate login URL: %w", err)
	}

	fmt.Println("Open this URL to access the Web UI:")
	fmt.Println(url)
	fmt.Println()
	fmt.Printf("(Single-use link; expires in %d minutes)\n", int(api.LoginLinkLifetime.Minutes()))

	// The URL is printed above, so a failed open needs no report.
	_ = notification.OpenURL(url)
	return nil
}

// The flags of a command that talks to serve's API.
type clientFlags struct {
	settingsFlags
	Server config.ListenAddr `placeholder:"ADDRESS" help:"API server address (default: ${default_listen})."`
	JSON   bool              `help:"Output as JSON."`
}

// Returns a client of serve's API. Errors as loadAPIEndpoint does.
func (f clientFlags) client() (*cli.Client, error) {
	endpoint, err := f.endpoint(f.Server)
	if err != nil {
		return nil, err
	}
	return cli.NewClient(endpoint.Addr, endpoint.Auth.Token()), nil
}

// Returns the formatter of the API's replies on stdout.
func (f clientFlags) formatter() *cli.Formatter { return cli.NewFormatter(os.Stdout, f.JSON) }

// The positional argument of a command that acts on one request.
type requestArg struct {
	ID string `arg:"" name:"request-id" help:"The request's ID."`
}

type listCmd struct{ clientFlags }

func (c listCmd) Run() error {
	client, err := c.client()
	if err != nil {
		return err
	}
	requests, err := client.List()
	if err != nil {
		return err
	}
	c.formatter().FormatRequests(requests)
	return nil
}

type showCmd struct {
	clientFlags
	requestArg
}

func (c showCmd) Run() error {
	client, err := c.client()
	if err != nil {
		return err
	}
	result, err := client.Show(c.ID)
	if err != nil {
		return err
	}
	c.formatter().FormatShowResult(result)
	return nil
}

type approveCmd struct {
	clientFlags
	requestArg
}

func (c approveCmd) Run() error {
	client, err := c.client()
	if err != nil {
		return err
	}
	if err := client.Approve(c.ID); err != nil {
		return err
	}
	c.formatter().FormatAction("approved", c.ID)
	return nil
}

type denyCmd struct {
	clientFlags
	requestArg
}

func (c denyCmd) Run() error {
	client, err := c.client()
	if err != nil {
		return err
	}
	if err := client.Deny(c.ID); err != nil {
		return err
	}
	c.formatter().FormatAction("denied", c.ID)
	return nil
}

type historyCmd struct{ clientFlags }

func (c historyCmd) Run() error {
	client, err := c.client()
	if err != nil {
		return err
	}
	entries, err := client.History()
	if err != nil {
		return err
	}
	c.formatter().FormatHistory(entries)
	return nil
}

type configCmd struct {
	Show     configShowCmd     `cmd:"" help:"Show the current configuration."`
	Edit     configEditCmd     `cmd:"" help:"Edit config in $EDITOR and optionally restart service."`
	Validate configValidateCmd `cmd:"" help:"Validate config syntax and values."`
}

type configShowCmd struct {
	configFlag
	Defaults bool `help:"Show all fields with program defaults filled in."`
}

func (c configShowCmd) Run() error {
	source, err := c.source()
	if err != nil {
		return err
	}
	cfg, err := source.Load()
	if err != nil {
		return err
	}
	if c.Defaults {
		if cfg, err = cfg.WithDefaults(); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "# %s\n", source.File)

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

type configEditCmd struct{ configFlag }

func (c configEditCmd) Run() error {
	source, err := c.source()
	if err != nil {
		return err
	}
	path := source.File

	if err := path.CreateDir(); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// The file may not exist yet.
	before, _ := path.ReadFile()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		return errors.New("$EDITOR is not set")
	}

	cmd := exec.Command(editor, path.String())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor exited with error: %w", err)
	}

	after, err := path.ReadFile()
	if err != nil {
		return fmt.Errorf("read config after edit: %w", err)
	}

	if string(before) == string(after) {
		fmt.Fprintln(os.Stderr, "config unchanged")
		return nil
	}

	if err := c.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: config has errors: %v\n", err)
		fmt.Fprintln(os.Stderr, "the file was saved but may not load correctly")
		return nil
	}

	fmt.Fprintln(os.Stderr, "config updated")

	fmt.Fprint(os.Stderr, "restart secrets-dispatcher service? [y/N] ")
	var answer string
	fmt.Scanln(&answer)
	if answer == "y" || answer == "Y" {
		ctx, stop := shutdownContext()
		defer stop()
		if err := service.RestartProxy(ctx); err != nil {
			return fmt.Errorf("restart service: %w", err)
		}
		fmt.Fprintln(os.Stderr, "service restarted")
	}
	return nil
}

type configValidateCmd struct{ configFlag }

func (c configValidateCmd) Run() error {
	if err := c.validate(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "config ok")
	return nil
}

type tryCmd struct {
	DryRun bool        `help:"Print what the trial would change, then exit without changing anything."`
	Config config.Path `placeholder:"FILE" help:"Base config file the trial copies settings from, never modified (default: ${default_config})."`
	backendFlag
	narrationFlag
}

func (c tryCmd) Run(ctx context.Context) error {
	return service.Try(ctx, service.TryOptions{
		ConfigPath: c.Config,
		Backend:    c.Backend,
		DryRun:     c.DryRun,
		Narration:  c.narration(),
	})
}

type serviceCmd struct {
	Install   serviceInstallCmd   `cmd:"" help:"Install and enable the systemd user service."`
	Uninstall serviceUninstallCmd `cmd:"" help:"Stop, disable, and remove the systemd user service."`
	Status    serviceStatusCmd    `cmd:"" help:"Doctor-style health report: name owner, unit states, mask/demotion consistency."`
}

type serviceInstallCmd struct {
	Start bool `default:"true" negatable:"" help:"Start the service now. Otherwise it starts at the next login."`
	configFlag
	Mode service.Mode `default:"${default_mode}" placeholder:"MODE" help:"Topology mode: ${modes} (default: ${default}). local gates this desktop, remote serves secrets to remote hosts, and full does both."`
	backendFlag
	DryRun bool `help:"Print what the install would change, then exit without changing anything."`
	narrationFlag
}

func (c serviceInstallCmd) Run(ctx context.Context) error {
	opts := service.Options{
		ConfigPath: c.Config,
		Start:      c.Start,
		Mode:       c.Mode,
		Backend:    c.Backend,
		Narration:  c.narration(),
	}
	if c.DryRun {
		return service.PrintPlan(ctx, opts)
	}
	return service.Install(ctx, opts)
}

type serviceUninstallCmd struct{ narrationFlag }

func (c serviceUninstallCmd) Run(ctx context.Context) error {
	if err := service.Uninstall(ctx, c.narration()); err != nil {
		return err
	}
	fmt.Println("✓ Uninstalled — stock Secret Service restored")
	return nil
}

type serviceStatusCmd struct{}

func (serviceStatusCmd) Run(ctx context.Context) error { return service.Status(ctx) }

type gpgSignCmd struct {
	Setup gpgSignSetupCmd `cmd:"" help:"Configure git to use secrets-dispatcher for GPG signing."`
}

type gpgSignSetupCmd struct {
	Local        bool          `help:"Configure per-repo (--local) instead of --global."`
	RemoteSocket unixsock.Path `type:"path" default:"" placeholder:"SOCKET" help:"Sign through this forwarded remote socket (docs/REMOTE-PROXY.md)."`
}

func (c gpgSignSetupCmd) Run() error {
	scope := gpgsign.ScopeGlobal
	if c.Local {
		scope = gpgsign.ScopeLocal
	}
	return gpgsign.SetupGitConfig(scope, c.RemoteSocket)
}

// Sets up the companion user's deployment, or checks an existing one.
type provisionCmd struct {
	User          string `placeholder:"USER" help:"Desktop username to provision companion for (default: $SUDO_USER)."`
	CompanionName string `placeholder:"NAME" help:"Override companion username (default: secrets-{user})."`
	HomeBase      string `placeholder:"DIR" help:"Parent directory for companion homes (default: ${default_home_base})."`
	Check         bool   `help:"Validate deployment instead of provisioning."`
}

func (c provisionCmd) Run() error {
	cfg := companion.Config{
		DesktopUser:   c.User,
		CompanionName: c.CompanionName,
		HomeBase:      c.HomeBase,
	}
	if !c.Check {
		return companion.Provision(cfg)
	}
	results := companion.Check(cfg)
	for _, r := range results {
		fmt.Println(r)
	}
	if !companion.AllPass(results) {
		return errors.New("the deployment check failed")
	}
	return nil
}

// Serves net.mowaka.SecretsDispatcher1 on D-Bus until SIGTERM or SIGINT.
type daemonCmd struct {
	BusAddress string `placeholder:"ADDRESS" help:"D-Bus address to connect to (default: the system bus)."`
	logFlags
}

func (c daemonCmd) Run(ctx context.Context) error {
	slog.SetDefault(slog.New(newLogHandler(c.LogLevel, c.LogFormat)))
	return daemon.Run(ctx, daemon.Config{
		BusAddress: c.BusAddress,
		Version:    version,
	})
}

type versionCmd struct{}

func (versionCmd) Run() error {
	fmt.Println(version)
	return nil
}

// Runs the dispatcher until SIGINT or SIGTERM.
type serveCmd struct {
	serveFlags
	APIOnly bool `help:"Run only the API server (for testing)."`
}

func (c serveCmd) Run(ctx context.Context) error {
	settings, err := c.settings()
	if err != nil {
		return err
	}
	cfg, err := settings.Resolve()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

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
		return err
	}
	auth, err := api.NewAuth(stateDir)
	if err != nil {
		return fmt.Errorf("create auth: %w", err)
	}

	// The function run cancels ctx only after Run returns. Stopping this
	// AfterFunc first means a return without a signal logs nothing.
	defer context.AfterFunc(ctx, func() {
		slog.Info("received signal, shutting down", "cause", context.Cause(ctx))
	})()

	// Start notification action listener (needs ctx)
	if desktopNotifier != nil {
		go notifHandler.ListenActions(ctx, desktopNotifier.Actions())
		defer desktopNotifier.Stop()
	}

	chainEnd := procutil.ChainToInit
	if *cfg.Serve.TrimProcessChain {
		chainEnd = procutil.ChainToSessionStarter
	}
	chains, err := sender.NewChainReader(chainEnd)
	if err != nil {
		return err
	}
	factory := proxy.NewFactory(cfg.Serve.Upstream.Bus.Connect, approvalMgr, slowUpstream, chains)
	down, err := newDownstreams(cfg.Serve.Downstream, factory)
	if err != nil {
		return err
	}
	runners := down.Runners()

	// Set up SSH agent proxy if configured
	if ssh := cfg.SSH; ssh != nil {
		if ssh.Upstream == "" {
			slog.Error("SSH agent proxy enabled but no upstream socket (set ssh.upstream in config or SSH_AUTH_SOCK)")
		} else {
			sshServer := sshagent.NewServer(*ssh, approvalMgr, chains, slog.Default())
			runners = append(runners, sshServer.Run)
			slog.Info("SSH agent proxy configured", "listen", ssh.Listen, "upstream", ssh.Upstream)
		}
	}

	// Create API server
	apiServer, err := api.NewServer(api.ServerConfig{
		Addr:              cfg.Listen,
		Resolver:          resolver,
		Auth:              auth,
		Chains:            chains,
		NotificationDelay: notificationDelay,
		TestMode:          c.APIOnly,
	}, down)
	if err != nil {
		return fmt.Errorf("create API server: %w", err)
	}

	// Serve the API socket in the runtime directory, except with --api-only.
	if !c.APIOnly {
		runtimeDir, err := config.LookupRuntimeDir()
		if err != nil {
			slog.Warn("the API socket will not be created", "error", err)
		} else if err := apiServer.ServeAPISocket(runtimeDir); err != nil {
			return fmt.Errorf("open API socket: %w", err)
		} else {
			slog.Info("API socket ready", "socket", runtimeDir.APISocket())
		}
	}

	// Start API server
	if err := apiServer.Start(); err != nil {
		return fmt.Errorf("start API server: %w", err)
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
	if c.APIOnly {
		slog.Info("running in API-only mode (no D-Bus proxy)")
		<-ctx.Done()
		return nil
	}

	if err := serveRemotes(apiServer, cfg.Serve.Remotes); err != nil {
		return fmt.Errorf("open remote socket: %w", err)
	}

	down.Subscribe(apiServer.WSHandler())

	slog.Info("starting proxy topology",
		"upstream", cfg.Serve.Upstream.Bus.Type(),
		"downstreams", len(cfg.Serve.Downstream))

	var wg sync.WaitGroup
	for i, run := range runners {
		wg.Add(1)
		go func(idx int, runFn runner) {
			defer wg.Done()
			if err := runFn(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("runner error", "index", idx, "error", err)
			}
		}(i, run)
	}
	wg.Wait()
	return nil
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
