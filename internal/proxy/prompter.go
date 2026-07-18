package proxy

import (
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// The gnome-keyring "system prompter" protocol. When a client asks to unlock a
// locked collection, gnome-keyring drives the unlock dialog by calling
// org.gnome.keyring.SystemPrompter on *its own* session bus; on a real GNOME
// desktop that name is owned by gnome-shell, which renders the dialog.
const (
	systemPrompterName        = "org.gnome.keyring.SystemPrompter"
	systemPrompterPath        = "/org/gnome/keyring/Prompter"
	prompterInterface         = "org.gnome.keyring.internal.Prompter"
	prompterCallbackInterface = "org.gnome.keyring.internal.Prompter.Callback"
)

// prompterBridge reconnects the backend gnome-keyring to the user's real unlock
// prompter across the private-bus boundary the takeover creates.
//
// The dispatcher runs the backend gnome-keyring on a private bus so it can
// mediate the Secret Service. But gnome-keyring reaches its unlock prompter
// over that *same* bus, and the only SystemPrompter reachable there is the
// display-less gcr-prompter fallback (D-Bus-activated, no WAYLAND_DISPLAY) —
// so it can never draw a dialog and any unlock hangs forever. The bridge owns
// org.gnome.keyring.SystemPrompter on the backend bus (pre-empting that
// fallback) and forwards the prompter conversation to gnome-shell's real
// prompter on the front (session) bus, so the normal GNOME unlock dialog
// appears. The exchange payload (carrying the DH-encrypted password) is
// forwarded verbatim, end-to-end between gnome-keyring and gnome-shell — the
// dispatcher never sees the plaintext.
//
// The protocol is two-directional, so the bridge is too:
//   - Prompter (BeginPrompting/PerformPrompt/StopPrompting): the bridge
//     receives these from gnome-keyring on the backend bus and forwards them
//     to gnome-shell on the front bus.
//   - Callback (PromptReady): gnome-shell calls this back on the callback
//     object path. Because the bridge issued the call, gnome-shell addresses
//     the bridge's front connection, so the bridge exports a callback proxy
//     there and forwards PromptReady to gnome-keyring's real callback object
//     on the backend bus.
type prompterBridge struct {
	frontConn   *dbus.Conn    // session bus: gnome-shell owns the real prompter
	backendConn *dbus.Conn    // private bus: backend gnome-keyring lives here
	toShell     callForwarder // forwards Prompter calls to gnome-shell
	logger      *logging.Logger

	mu        sync.Mutex
	callbacks map[dbus.ObjectPath]bool // callback proxies exported on frontConn
	owned     bool                     // claimed the name on the backend bus
}

// newPrompterBridge sets up the bridge when the topology needs one: the front
// bus must have a real prompter (gnome-shell) to forward to, and the backend
// must be a distinct bus without one (the local-takeover case). In every other
// topology (remote mode, or a backend that shares the session bus) there is
// nothing to bridge and it returns (nil, nil).
func newPrompterBridge(frontConn, backendConn *dbus.Conn, logger *logging.Logger) (*prompterBridge, error) {
	if !nameHasOwner(frontConn, systemPrompterName) {
		logger.Info("no session unlock prompter present; keyring unlock dialogs will be unavailable")
		return nil, nil
	}
	if nameHasOwner(backendConn, systemPrompterName) {
		// Same-bus / remote topology: the backend already reaches a real
		// prompter, so inserting the bridge would only fight for the name.
		return nil, nil
	}

	b := &prompterBridge{
		frontConn:   frontConn,
		backendConn: backendConn,
		toShell:     callForwarder{dst: frontConn, dstName: systemPrompterName},
		logger:      logger,
		callbacks:   make(map[dbus.ObjectPath]bool),
	}

	// Claim the name on the backend bus BEFORE any client triggers an unlock,
	// so gnome-keyring's prompter calls land on us and the gcr-prompter
	// fallback is never activated. DoNotQueue: if something already owns it,
	// don't bridge (we'd only conflict).
	reply, err := backendConn.RequestName(systemPrompterName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("claim %s on backend bus: %w", systemPrompterName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		logger.Info("could not claim system prompter on backend bus; unlock dialogs unavailable", "reply", reply)
		return nil, nil
	}
	b.owned = true

	if err := backendConn.Export(b, dbus.ObjectPath(systemPrompterPath), prompterInterface); err != nil {
		b.close()
		return nil, fmt.Errorf("export prompter bridge: %w", err)
	}
	logger.Info("prompter bridge active: keyring unlock prompts forwarded to the session prompter")
	return b, nil
}

// --- Prompter interface (received from gnome-keyring on the backend bus) ---

// BeginPrompting starts a prompt. The callback object lives on gnome-keyring's
// backend connection; before forwarding to gnome-shell we export a proxy for it
// on the front connection so gnome-shell's PromptReady callbacks reach us.
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

func (b *prompterBridge) exportCallback(path dbus.ObjectPath, keyring senderName) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callbacks[path] {
		return nil
	}
	cb := &prompterCallback{
		toKeyring: callForwarder{dst: b.backendConn, dstName: string(keyring)},
		logger:    b.logger,
	}
	if err := b.frontConn.Export(cb, path, prompterCallbackInterface); err != nil {
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
	_ = b.frontConn.Export(nil, path, prompterCallbackInterface)
	delete(b.callbacks, path)
}

func (b *prompterBridge) close() {
	b.mu.Lock()
	for path := range b.callbacks {
		_ = b.frontConn.Export(nil, path, prompterCallbackInterface)
		delete(b.callbacks, path)
	}
	b.mu.Unlock()
	if b.owned {
		_, _ = b.backendConn.ReleaseName(systemPrompterName)
		b.owned = false
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
