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
}

// NewPromptHandler creates a new PromptHandler.
func NewPromptHandler(localConn *dbus.Conn, logger *logging.Logger) *PromptHandler {
	return &PromptHandler{
		toBackend: callForwarder{dst: localConn, dstName: dbustypes.BusName},
		logger:    logger,
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
	if !isPromptPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	h.logger.Info("forwarding prompt", "path", path, "sender", senderOf(msg))

	return h.toBackend.forwardVoid(msg)
}

// Dismiss dismisses the prompt.
func (h *PromptHandler) Dismiss(msg dbus.Message) *dbus.Error {
	path := pathOf(msg)
	if !isPromptPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	h.logger.Info("dismissing prompt", "path", path, "sender", senderOf(msg))

	return h.toBackend.forwardVoid(msg)
}
