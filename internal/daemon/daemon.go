package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/tui"
)

// Config holds daemon startup parameters.
type Config struct {
	// BusAddress is the D-Bus address to connect to.
	// Empty means the system bus (production). Non-empty connects to a custom
	// address — used by integration tests to point at a private dbus-daemon.
	BusAddress string

	// Version is the string reported by GetVersion().
	Version string

	// VTPath is the path to the VT device (e.g. "/dev/tty8").
	// Defaults to "/dev/tty8" if empty and VTFile is nil.
	VTPath string

	// LockMode controls VT_PROCESS mode engagement in the TUI.
	LockMode tui.LockMode

	// Timeout is the approval request timeout. Defaults to 5 minutes.
	Timeout time.Duration

	// HistoryMax is the maximum number of history entries to retain. Defaults to 100.
	HistoryMax int

	// CompanionUser is the companion Linux username (e.g. "secrets-nb").
	CompanionUser string

	// Test seams — both nil in production.

	// VTFile is a pre-opened VT file. When non-nil, VTPath is not opened.
	// Set in tests to avoid requiring a real VT.
	VTFile *os.File

	// Resolver overrides the D-Bus sender info resolver. When nil, a real
	// proxy.SenderInfoResolver backed by the D-Bus connection is used.
	Resolver senderResolver

	// Signer overrides the GPG signer. When nil, a real GPG signer is used.
	Signer gpgSigner

	// ApprovalManager overrides the internally-created approval.Manager.
	// When non-nil, Run() uses this manager instead of creating a new one.
	// Used by integration tests to control approval state.
	ApprovalManager *approval.Manager

	// MessageSender injects a MessageSender for the dispatcher in headless mode.
	// When non-nil and VTPath/VTFile are both empty, Run() calls
	// dispatcher.SetProgram(MessageSender) so RequestSecret/RequestSign
	// do not return NotReady. Used by integration tests that test the full
	// approve/deny flow without a real VT.
	MessageSender MessageSender
}

// tuiObserver bridges approval.Manager events to bubbletea messages.
// It is subscribed to the manager after the tea.Program is created.
type tuiObserver struct {
	program *tea.Program
}

// OnEvent implements approval.Observer.
func (o *tuiObserver) OnEvent(e approval.Event) {
	switch e.Type {
	case approval.EventRequestCreated:
		// NewRequestMsg is already sent by the D-Bus handler immediately after
		// creating the request. Skip here to avoid duplicating it.
	case approval.EventRequestApproved:
		o.program.Send(tui.RequestResolvedMsg{ID: e.Request.ID, Resolution: approval.ResolutionApproved})
	case approval.EventRequestDenied:
		o.program.Send(tui.RequestResolvedMsg{ID: e.Request.ID, Resolution: approval.ResolutionDenied})
	case approval.EventRequestExpired:
		o.program.Send(tui.RequestResolvedMsg{ID: e.Request.ID, Resolution: approval.ResolutionExpired})
	case approval.EventRequestCancelled:
		o.program.Send(tui.RequestResolvedMsg{ID: e.Request.ID, Resolution: approval.ResolutionCancelled})
	}
}

// Run starts the daemon, registers on D-Bus, optionally starts the bubbletea
// TUI on the configured VT, sends READY=1 via sd-notify, and blocks until ctx
// is cancelled. Returns nil on clean shutdown.
//
// Headless mode: when both VTPath and VTFile are zero-valued, no TUI is started.
// The dispatcher still works but requests will time out without user interaction.
// This mode is used by integration tests.
func Run(ctx context.Context, cfg Config) error {
	// Apply defaults.
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.HistoryMax == 0 {
		cfg.HistoryMax = 100
	}

	// Create approval manager (or use the injected test seam).
	mgr := cfg.ApprovalManager
	if mgr == nil {
		mgr = approval.NewManager(cfg.Timeout, cfg.HistoryMax)
	}

	// Connect to the appropriate D-Bus bus.
	var conn *dbus.Conn
	var err error
	if cfg.BusAddress == "" {
		conn, err = dbus.ConnectSystemBus()
	} else {
		conn, err = dbus.Connect(cfg.BusAddress)
	}
	if err != nil {
		return fmt.Errorf("connect to D-Bus: %w", err)
	}
	defer conn.Close()

	// Determine the sender resolver.
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = proxy.NewSenderInfoResolver(conn)
	}

	// Create the dispatcher with the manager and resolver.
	dispatcher := NewDispatcher(cfg.Version, mgr, resolver)

	// Inject GPG signer.
	signer := cfg.Signer
	if signer == nil {
		signer = &defaultGPGSigner{}
	}
	dispatcher.SetSigner(signer)

	// Determine whether to start the TUI.
	headless := cfg.VTPath == "" && cfg.VTFile == nil

	if !headless {
		// Open VT file if not provided.
		vtFile := cfg.VTFile
		if vtFile == nil {
			vtPath := cfg.VTPath
			if vtPath == "" {
				vtPath = "/dev/tty8"
			}
			vtFile, err = tui.OpenVT(vtPath)
			if err != nil {
				return fmt.Errorf("open VT %s: %w", vtPath, err)
			}
			defer vtFile.Close()
		}

		// Install VT cleanup handler so VT_AUTO is restored on crash.
		cleanup := tui.CleanupOnSignal(vtFile.Fd())
		defer cleanup()

		// Create TUI model.
		vtPath := cfg.VTPath
		if vtPath == "" {
			vtPath = "/dev/tty8"
		}
		model := tui.NewModel(tui.Config{
			LockMode:      cfg.LockMode,
			VTPath:        vtPath,
			VTFD:          vtFile.Fd(),
			CompanionUser: cfg.CompanionUser,
			StartTime:     time.Now(),
		}, mgr.Approve, mgr.Deny)

		// Start bubbletea program on the VT.
		program := tea.NewProgram(model,
			tea.WithInput(vtFile),
			tea.WithOutput(vtFile),
			tea.WithAltScreen(),
		)

		// Wire TUI to approval manager via observer.
		observer := &tuiObserver{program: program}
		mgr.Subscribe(observer)

		// Make the dispatcher aware of the running program.
		dispatcher.SetProgram(program)

		// Run TUI in a background goroutine; cancel context when it exits.
		tuiDone := make(chan error, 1)
		go func() {
			_, err := program.Run()
			tuiDone <- err
		}()

		// Export dispatcher and set up introspection before notifying systemd.
		if err := exportDispatcher(conn, dispatcher); err != nil {
			program.Quit()
			return err
		}

		slog.Info("daemon ready", "bus_name", BusName, "vt", vtPath)
		SdNotify("READY=1")

		// Block until context cancelled or TUI exits on its own (e.g. user presses q).
		select {
		case <-ctx.Done():
			program.Quit()
			<-tuiDone
		case err := <-tuiDone:
			if err != nil {
				slog.Error("TUI exited with error", "error", err)
			}
		}

		slog.Info("daemon shutting down")
		return nil
	}

	// Headless mode: no TUI — just export dispatcher and block.
	// If MessageSender is injected (integration tests), wire it so that
	// RequestSecret/RequestSign do not return NotReady.
	if cfg.MessageSender != nil {
		dispatcher.program = cfg.MessageSender
	}

	if err := exportDispatcher(conn, dispatcher); err != nil {
		return err
	}

	slog.Info("daemon ready (headless)", "bus_name", BusName)
	SdNotify("READY=1")

	<-ctx.Done()

	slog.Info("daemon shutting down")
	return nil
}

// exportDispatcher exports the dispatcher object, requests the well-known D-Bus
// name, and also exports the Introspectable interface.
func exportDispatcher(conn *dbus.Conn, dispatcher *Dispatcher) error {
	if err := conn.Export(dispatcher, ObjectPath, Interface); err != nil {
		return fmt.Errorf("export dispatcher: %w", err)
	}

	node := &introspect.Node{
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{
				Name:    Interface,
				Methods: introspect.Methods(dispatcher),
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), ObjectPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("export introspectable: %w", err)
	}

	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("request bus name %q: %w", BusName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("not primary owner of %q (reply=%d); policy rejected or name already taken", BusName, reply)
	}
	return nil
}
