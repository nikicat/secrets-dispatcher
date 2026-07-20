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
	active    bool                     // claimed + exported (idempotency guard)
	closed    bool                     // close() called; block late activation
	watchCh   chan *dbus.Signal        // NameOwnerChanged while waiting for the prompter
	watchDone chan struct{}            // closed to stop awaitFrontPrompter
}

// newPrompterBridge sets up the bridge when the topology needs one: the backend
// must be a distinct bus without its own prompter (the local-takeover case). If
// the front bus already has gnome-shell's prompter it activates immediately;
// otherwise it watches for that prompter to appear and activates then. That
// deferred activation matters after a relogin: the proxy and gnome-shell both
// start with the graphical session and the proxy often wins the race, so a
// one-shot "is the prompter here yet?" check would give up and let the
// display-less gcr-prompter fallback claim the backend name on the first unlock.
// In topologies where the backend already shares a bus with a real prompter
// (remote mode, or same-bus), there is nothing to bridge and it returns (nil, nil).
func newPrompterBridge(frontConn, backendConn *dbus.Conn, logger *logging.Logger) (*prompterBridge, error) {
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

	if nameHasOwner(frontConn, systemPrompterName) {
		if err := b.activate(); err != nil {
			return nil, err
		}
	} else if err := b.watchFrontPrompter(); err != nil {
		return nil, err
	}
	return b, nil
}

// activate claims org.gnome.keyring.SystemPrompter on the backend bus BEFORE any
// client triggers an unlock, so gnome-keyring's prompter calls land on us and
// the gcr-prompter fallback is never activated, then exports the bridge.
// Idempotent, and a no-op after close(). DoNotQueue: if something already owns
// the name (a gcr-prompter an unlock activated while we were still waiting for
// the front prompter), we can't take over — leave it be.
func (b *prompterBridge) activate() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.active {
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
	b.owned = true
	if err := b.backendConn.Export(b, dbus.ObjectPath(systemPrompterPath), prompterInterface); err != nil {
		_, _ = b.backendConn.ReleaseName(systemPrompterName)
		b.owned = false
		return fmt.Errorf("export prompter bridge: %w", err)
	}
	b.active = true
	b.logger.Info("prompter bridge active: keyring unlock prompts forwarded to the session prompter")
	return nil
}

// watchFrontPrompter subscribes to NameOwnerChanged for the SystemPrompter name
// on the front bus and activates the bridge the moment gnome-shell registers it.
func (b *prompterBridge) watchFrontPrompter() error {
	if err := b.frontConn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchArg(0, systemPrompterName),
	); err != nil {
		return err
	}
	b.watchCh = make(chan *dbus.Signal, 4)
	b.watchDone = make(chan struct{})
	b.frontConn.Signal(b.watchCh)
	b.logger.Info("no session unlock prompter yet; waiting for it to appear before bridging keyring unlocks")
	go b.awaitFrontPrompter(b.watchCh, b.watchDone)
	return nil
}

// awaitFrontPrompter activates the bridge on the first NameOwnerChanged that
// gives the SystemPrompter name an owner, then stops watching (one-shot: once
// active, toShell forwards by name and follows any later gnome-shell restart).
// Stops on close() (via done) or when godbus closes ch on connection loss —
// godbus owns ch's lifecycle, so we never close it ourselves.
func (b *prompterBridge) awaitFrontPrompter(ch chan *dbus.Signal, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case sig, ok := <-ch:
			if !ok {
				return
			}
			if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) != 3 {
				continue
			}
			name, _ := sig.Body[0].(string)
			newOwner, _ := sig.Body[2].(string)
			if name != systemPrompterName || newOwner == "" {
				continue
			}
			if err := b.activate(); err != nil {
				b.logger.Warn("failed to activate prompter bridge after the session prompter appeared", "error", err)
			}
			return
		}
	}
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
	b.closed = true
	if b.watchDone != nil {
		close(b.watchDone) // stops awaitFrontPrompter; godbus owns watchCh, don't close it
		b.watchDone = nil
	}
	if b.watchCh != nil {
		b.frontConn.RemoveSignal(b.watchCh)
		b.watchCh = nil
	}
	for path := range b.callbacks {
		_ = b.frontConn.Export(nil, path, prompterCallbackInterface)
		delete(b.callbacks, path)
	}
	owned := b.owned
	b.owned = false
	b.mu.Unlock()
	if owned {
		_, _ = b.backendConn.ReleaseName(systemPrompterName) // not under mu (network call)
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
