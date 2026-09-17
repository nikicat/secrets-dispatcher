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
//   - Callback (PromptReady/PromptDone): gnome-shell calls these back on the
//     callback object path. Because the bridge issued the Begin call,
//     gnome-shell addresses the bridge's front connection, so the bridge
//     exports a callback proxy there and forwards them to gnome-keyring's real
//     callback object on the backend bus.
//
// # Owner pinning
//
// Ownership of org.gnome.keyring.SystemPrompter on the front (session) bus is
// NOT stable during login: gnome-shell registers around startup, and a
// D-Bus-activated gcr-prompter can take the well-known name over mid-
// conversation. Forwarding by well-known name can then split one conversation
// across two prompters — observed in the wild as: shell draws the dialog and
// the unlock succeeds, gcr-prompter (which never saw BeginPrompting) receives
// StopPrompting, answers "couldn't find the callback", and never delivers
// PromptDone — leaving an orphaned dialog on a dimmed desktop. The bridge
// therefore resolves the name to a UNIQUE bus name at BeginPrompting time and
// pins the whole conversation to it; a later name takeover cannot redirect
// Perform/Stop to a prompter that knows nothing of the prompt.
//
// Forwarded calls carry FlagNoAutoStart: forwarding must never D-Bus-activate
// a prompter as a side effect (the activation race above is exactly how a
// second prompter appeared mid-login in the first place).
type prompterBridge struct {
	frontConn   *dbus.Conn    // session bus: gnome-shell owns the real prompter
	backendConn *dbus.Conn    // private bus: backend gnome-keyring lives here
	toShell     callForwarder // fallback forward: by well-known name, no auto-start
	logger      *logging.Logger

	mu        sync.Mutex
	callbacks map[dbus.ObjectPath]*promptSession // live conversations by callback path
	owned     bool                               // claimed the name on the backend bus
	active    bool                               // claimed + exported (idempotency guard)
	closed    bool                               // close() called; block late activation

	sigStop chan struct{} // closed to stop the owner-watch goroutine
	sigDone chan struct{} // closed by the owner-watch goroutine on exit
	sigCh   chan *dbus.Signal
}

// promptSession is one prompter conversation. shellOwner is the unique name
// the conversation is pinned to ("" until resolved).
type promptSession struct {
	shellOwner string     // unique front-bus name of the prompter that accepted Begin
	keyring    senderName // unique backend-bus name of gnome-keyring (callback owner)
}

// newPrompterBridge sets up the bridge when the topology needs one: the backend
// must be a distinct bus without its own prompter (the local-takeover case). It
// claims org.gnome.keyring.SystemPrompter on the backend bus immediately and
// unconditionally — it does NOT wait for gnome-shell's front prompter to appear
// first. That ownership is what pre-empts the gcr-prompter fallback: the backend
// bus can D-Bus-activate that fallback the instant gnome-keyring asks to unlock,
// and in the local-takeover topology the backend runs inside the graphical
// session, so the fallback actually draws its own (GTK) dialog instead of just
// hanging. Claiming the name from birth denies it that window. Forwarding to the
// real prompter resolves gnome-shell by name at prompt time (by which point the
// interactive session — and thus gnome-shell — is up), so it also survives a
// relogin/gnome-shell restart without re-arming anything.
//
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
		toShell:     callForwarder{dst: frontConn, dstName: systemPrompterName, noAutoStart: true},
		logger:      logger,
		callbacks:   make(map[dbus.ObjectPath]*promptSession),
		sigStop:     make(chan struct{}),
		sigDone:     make(chan struct{}),
	}

	if err := b.activate(); err != nil {
		return nil, err
	}
	b.watchPrompterOwner()
	return b, nil
}

// activate claims org.gnome.keyring.SystemPrompter on the backend bus BEFORE any
// client triggers an unlock, so gnome-keyring's prompter calls land on us and
// the gcr-prompter fallback is never activated, then exports the bridge.
// Idempotent, and a no-op after close(). DoNotQueue: if something already owns
// the name (a gcr-prompter that an unlock somehow activated first), we can't
// take over — leave it be.
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

// watchPrompterOwner tracks org.gnome.keyring.SystemPrompter ownership on the
// front bus. When the name loses its owner entirely (gnome-shell crashed or the
// session ended mid-prompt), the pinned prompter can never finish the
// conversation, and no replacement prompter knows the callback — gcr's
// StopPrompting handler even refuses unknown callbacks. The bridge therefore
// synthesizes PromptDone to the backend for every pinned conversation, which is
// exactly what a prompter tearing down its prompts sends, so gnome-keyring
// stops waiting instead of hanging. Name handovers to a NEW owner are ignored:
// pinning already keeps the conversation with the old owner, which is still
// connected and can still finish it.
func (b *prompterBridge) watchPrompterOwner() {
	b.sigCh = make(chan *dbus.Signal, 16)
	if err := b.frontConn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchSender("org.freedesktop.DBus"),
	); err != nil {
		b.logger.Warn("prompter bridge cannot watch prompter owner", "error", err.Error())
		close(b.sigDone)
		return
	}
	b.frontConn.Signal(b.sigCh)
	go func() {
		defer close(b.sigDone)
		for {
			select {
			case <-b.sigStop:
				return
			case sig, ok := <-b.sigCh:
				if !ok {
					return
				}
				if len(sig.Body) != 3 {
					continue
				}
				name, _ := sig.Body[0].(string)
				newOwner, _ := sig.Body[2].(string)
				if name != systemPrompterName || newOwner != "" {
					continue
				}
				b.prompterVanished()
			}
		}
	}()
}

// prompterVanished fails every conversation pinned to an owner that no longer
// exists, with a synthesized PromptDone to the backend.
func (b *prompterBridge) prompterVanished() {
	type stale struct {
		path    dbus.ObjectPath
		keyring senderName
	}
	var staleSessions []stale

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	for path, s := range b.callbacks {
		if s.shellOwner == "" || nameHasOwner(b.frontConn, s.shellOwner) {
			continue
		}
		staleSessions = append(staleSessions, stale{path: path, keyring: s.keyring})
		_ = b.frontConn.Export(nil, path, prompterCallbackInterface) // best-effort unexport
		delete(b.callbacks, path)
	}
	b.mu.Unlock()

	for _, s := range staleSessions {
		b.logger.Info("session prompter vanished mid-prompt; failing the prompt", "callback", string(s.path))
		b.sendPromptDone(s.path, s.keyring)
	}
}

// sendPromptDone delivers a synthesized PromptDone to the backend gnome-keyring
// callback at path. Best-effort: if the backend is gone nobody is waiting.
func (b *prompterBridge) sendPromptDone(path dbus.ObjectPath, keyring senderName) {
	if keyring == "" {
		return
	}
	obj := b.backendConn.Object(string(keyring), path)
	if err := obj.Call(prompterCallbackInterface+".PromptDone", dbus.FlagNoAutoStart).Err; err != nil {
		b.logger.Debug("synthesized PromptDone failed", "callback", string(path), "error", err.Error())
	}
}

// --- Prompter interface (received from gnome-keyring on the backend bus) ---

// BeginPrompting starts a prompt. The callback object lives on gnome-keyring's
// backend connection; before forwarding to gnome-shell we export a proxy for it
// on the front connection so gnome-shell's PromptReady/PromptDone callbacks
// reach us. The forward is addressed to the prompter's UNIQUE name, resolved
// here and pinned: whoever accepts the Begin is who the rest of the
// conversation stays with, immune to later flips of the well-known name. With
// no prompter on the front bus the call fails outright (never auto-starts one)
// and gnome-keyring treats the prompt as dismissed.
func (b *prompterBridge) BeginPrompting(msg dbus.Message, callback dbus.ObjectPath) *dbus.Error {
	owner, err := getNameOwner(b.frontConn, systemPrompterName)
	if err != nil {
		return dbustypes.ErrFailed(fmt.Errorf("no session prompter available: %w", err))
	}
	if err := b.exportCallback(callback, senderOf(msg)); err != nil {
		return dbustypes.ErrFailed(err)
	}
	f := callForwarder{dst: b.frontConn, dstName: owner, noAutoStart: true}
	if err := f.forwardVoid(msg); err != nil {
		b.unexportCallback(callback)
		return err
	}
	b.mu.Lock()
	if s, ok := b.callbacks[callback]; ok && !b.closed {
		s.shellOwner = owner
	}
	b.mu.Unlock()
	b.logger.Info("forwarding keyring unlock prompt to the session prompter", "callback", string(callback), "prompter", owner)
	return nil
}

// PerformPrompt drives one round of the prompt (shows the dialog, collects the
// reply). Pass-through apart from owner pinning; the exchange payload is
// opaque to us.
func (b *prompterBridge) PerformPrompt(msg dbus.Message, callback dbus.ObjectPath, promptType string, properties map[string]dbus.Variant, exchange string) *dbus.Error {
	return b.forwardPinned(msg, callback)
}

// StopPrompting ends the prompt; tear down the callback proxy and pin after.
func (b *prompterBridge) StopPrompting(msg dbus.Message, callback dbus.ObjectPath) *dbus.Error {
	err := b.forwardPinned(msg, callback)
	b.unexportCallback(callback)
	return err
}

// forwardPinned re-issues msg on the front bus addressed to the pinned owner
// of its conversation. Unpinned conversations (never begun, or already torn
// down) fall back to the well-known name — never auto-starting a prompter.
func (b *prompterBridge) forwardPinned(msg dbus.Message, callback dbus.ObjectPath) *dbus.Error {
	b.mu.Lock()
	s, ok := b.callbacks[callback]
	owner := ""
	if ok {
		owner = s.shellOwner
	}
	b.mu.Unlock()
	if owner == "" {
		return b.toShell.forwardVoid(msg)
	}
	f := callForwarder{dst: b.frontConn, dstName: owner, noAutoStart: true}
	return f.forwardVoid(msg)
}

// Owner pinning happens inline in BeginPrompting, where the destination
// unique name is known exactly (resolved before the send).

// exportCallback exports the front-bus proxy for a backend callback object so
// the prompter's PromptReady/PromptDone calls reach the bridge.
func (b *prompterBridge) exportCallback(path dbus.ObjectPath, keyring senderName) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("prompter bridge is closed")
	}
	if _, ok := b.callbacks[path]; ok {
		return nil
	}
	cb := &prompterCallback{
		toKeyring: callForwarder{dst: b.backendConn, dstName: string(keyring)},
		logger:    b.logger,
	}
	if err := b.frontConn.Export(cb, path, prompterCallbackInterface); err != nil {
		return err
	}
	b.callbacks[path] = &promptSession{keyring: keyring}
	return nil
}

func (b *prompterBridge) unexportCallback(path dbus.ObjectPath) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.callbacks[path]; !ok {
		return
	}
	// Unexport by clearing the object; ignore the error (best-effort teardown).
	_ = b.frontConn.Export(nil, path, prompterCallbackInterface)
	delete(b.callbacks, path)
}

func (b *prompterBridge) close() {
	b.mu.Lock()
	b.closed = true
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
	if b.sigCh != nil {
		close(b.sigStop)
		b.frontConn.RemoveSignal(b.sigCh)
		<-b.sigDone
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
