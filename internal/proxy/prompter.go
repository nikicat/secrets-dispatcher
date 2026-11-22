package proxy

import (
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// The names of the prompter that gnome-keyring calls on its own bus to show an
// unlock dialog (ADR 0009).
const (
	systemPrompterName                            = "org.gnome.keyring.SystemPrompter"
	systemPrompterPath                            = "/org/gnome/keyring/Prompter"
	prompterInterface         dbustypes.Interface = "org.gnome.keyring.internal.Prompter"
	prompterCallbackInterface dbustypes.Interface = "org.gnome.keyring.internal.Prompter.Callback"
)

// Carries the backend gnome-keyring's unlock prompts from its private bus to
// gnome-shell's prompter on the session bus (ADR 0009).
type prompterBridge struct {
	frontConn   *dbus.Conn    // session bus: gnome-shell owns the real prompter
	backendConn *dbus.Conn    // private bus: backend gnome-keyring lives here
	toShell     callForwarder // forwards Prompter calls to gnome-shell
	logger      *logging.Logger

	// True if the bridge holds the prompter's name on the backend bus. False
	// otherwise.
	active bool

	mu        sync.Mutex
	callbacks map[dbus.ObjectPath]bool // callback proxies exported on frontConn
}

// Returns a bridge between the given buses that holds no name yet.
func newPrompterBridge(frontConn, backendConn *dbus.Conn, logger *logging.Logger) *prompterBridge {
	return &prompterBridge{
		frontConn:   frontConn,
		backendConn: backendConn,
		toShell:     callForwarder{dst: frontConn, dstName: systemPrompterName},
		logger:      logger,
		callbacks:   make(map[dbus.ObjectPath]bool),
	}
}

// Claims the system prompter's name on the backend bus and serves it there
// (ADR 0009). Does nothing when another owner holds the name. Errors when the
// name cannot be claimed or the bridge exported.
func (b *prompterBridge) claim() error {
	if (busDaemon{b.backendConn}).nameHasOwner(systemPrompterName) {
		return nil
	}
	reply, err := b.backendConn.RequestName(systemPrompterName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("claim %s on backend bus: %w", systemPrompterName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		b.logger.Info("could not claim system prompter on backend bus; unlock dialogs use the fallback", "reply", reply)
		return nil
	}
	if err := b.backendConn.Export(b, dbus.ObjectPath(systemPrompterPath), string(prompterInterface)); err != nil {
		_, _ = b.backendConn.ReleaseName(systemPrompterName)
		return fmt.Errorf("export prompter bridge: %w", err)
	}
	b.active = true
	b.logger.Info("prompter bridge active: keyring unlock prompts forwarded to the session prompter")
	return nil
}

// --- Prompter interface (received from gnome-keyring on the backend bus) ---

// Starts the prompt on gnome-shell. gnome-shell calls back the connection that
// called it, so a proxy of gnome-keyring's callback object is exported on the
// front connection first.
func (b *prompterBridge) BeginPrompting(msg dbus.Message, callback dbus.ObjectPath) *dbus.Error {
	if err := b.exportCallback(callback, senderOf(msg)); err != nil {
		return dbustypes.ErrFailed(err)
	}
	b.logger.Info("forwarding keyring unlock prompt to the session prompter", "callback", callback)
	return b.toShell.forwardVoid(msg)
}

// PerformPrompt drives one round of the prompt (shows the dialog, collects the
// reply). Pure pass-through; the exchange payload is opaque to us.
func (b *prompterBridge) PerformPrompt(msg dbus.Message, callback dbus.ObjectPath, promptType string, properties map[string]dbus.Variant, exchange string) *dbus.Error {
	return b.toShell.forwardVoid(msg)
}

// StopPrompting ends the prompt; tear down the callback proxy afterward.
func (b *prompterBridge) StopPrompting(msg dbus.Message, callback dbus.ObjectPath) *dbus.Error {
	err := b.toShell.forwardVoid(msg)
	b.unexportCallback(callback)
	return err
}

func (b *prompterBridge) exportCallback(path dbus.ObjectPath, keyring sender.UniqueName) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callbacks[path] {
		return nil
	}
	cb := &prompterCallback{
		toKeyring: callForwarder{dst: b.backendConn, dstName: string(keyring)},
		logger:    b.logger,
	}
	if err := b.frontConn.Export(cb, path, string(prompterCallbackInterface)); err != nil {
		return err
	}
	b.callbacks[path] = true
	return nil
}

func (b *prompterBridge) unexportCallback(path dbus.ObjectPath) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.callbacks[path] {
		return
	}
	// Unexport by clearing the object; ignore the error (best-effort teardown).
	_ = b.frontConn.Export(nil, path, string(prompterCallbackInterface))
	delete(b.callbacks, path)
}

func (b *prompterBridge) close() {
	b.mu.Lock()
	for path := range b.callbacks {
		_ = b.frontConn.Export(nil, path, string(prompterCallbackInterface))
		delete(b.callbacks, path)
	}
	b.mu.Unlock()
	if b.active {
		_, _ = b.backendConn.ReleaseName(systemPrompterName)
	}
}

// prompterCallback is the front-bus proxy for a gnome-keyring callback object.
// gnome-shell calls PromptReady here; we forward it to gnome-keyring's real
// callback on the backend bus.
type prompterCallback struct {
	toKeyring callForwarder
	logger    *logging.Logger
}

// PromptReady relays the prompter's reply (including the exchange payload with
// the encrypted password) back to gnome-keyring, verbatim.
func (c *prompterCallback) PromptReady(msg dbus.Message, reply string, properties map[string]dbus.Variant, exchange string) *dbus.Error {
	return c.toKeyring.forwardVoid(msg)
}

// PromptDone relays the prompter's final "prompt finished" notification back to
// gnome-keyring so it can tear the prompt down.
func (c *prompterCallback) PromptDone(msg dbus.Message) *dbus.Error {
	return c.toKeyring.forwardVoid(msg)
}
