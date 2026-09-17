package proxy

import (
	"strings"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// PromptHandler handles Prompt interface calls for prompt objects.
// It is exported as a subtree handler for /org/freedesktop/secrets/prompt/*.
//
// Prompt paths returned by the backend (from Unlock, Lock, CreateCollection,
// CreateItem, Delete) are passed through to clients 1:1, so forwarding only
// needs to relay the method calls; the Completed signal is already forwarded
// by the signal forwarder.
type PromptHandler struct {
	toBackend callForwarder
	logger    *logging.Logger
	prompts   *promptRegistry
}

// NewPromptHandler creates a new PromptHandler.
func NewPromptHandler(localConn *dbus.Conn, logger *logging.Logger, prompts *promptRegistry) *PromptHandler {
	return &PromptHandler{
		toBackend: callForwarder{dst: localConn, dstName: dbustypes.BusName},
		logger:    logger,
		prompts:   prompts,
	}
}

// isPromptPath checks if the path is a prompt object.
// Prompt paths: /org/freedesktop/secrets/prompt/xxx
func isPromptPath(path dbus.ObjectPath) bool {
	p := string(path)
	prefix := "/org/freedesktop/secrets/prompt/"
	return strings.HasPrefix(p, prefix) && len(p) > len(prefix)
}

// Prompt performs the prompt. The result is delivered via the Completed signal.
// Signature: Prompt(window_id String)
func (h *PromptHandler) Prompt(msg dbus.Message, windowID string) *dbus.Error {
	path := pathOf(msg)
	if err := h.authorize(msg, path); err != nil {
		return err
	}

	h.logger.Info("forwarding prompt", "path", path, "sender", senderOf(msg))

	return h.toBackend.forwardVoid(msg)
}

// Dismiss dismisses the prompt.
func (h *PromptHandler) Dismiss(msg dbus.Message) *dbus.Error {
	path := pathOf(msg)
	if err := h.authorize(msg, path); err != nil {
		return err
	}

	h.logger.Info("dismissing prompt", "path", path, "sender", senderOf(msg))

	return h.toBackend.forwardVoid(msg)
}

func (h *PromptHandler) authorize(msg dbus.Message, path dbus.ObjectPath) *dbus.Error {
	if !isPromptPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}
	sender, ok := senderFrom(msg)
	if !ok {
		return dbustypes.ErrAccessDenied("prompt caller has no D-Bus sender")
	}
	lease, ok := h.prompts.lookup(path)
	if !ok {
		return dbustypes.ErrObjectNotFound(string(path))
	}
	if lease.owner != sender {
		return dbustypes.ErrAccessDenied("prompt is owned by a different sender")
	}
	return nil
}
