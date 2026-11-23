package proxy

import (
	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Serves the backend's prompts at their own paths, so unlock prompts still
// reach the user (ADR 0001).
type PromptHandler struct {
	toBackend callForwarder
	logger    *logging.Logger
}

func NewPromptHandler(backendConn *dbus.Conn, logger *logging.Logger) *PromptHandler {
	return &PromptHandler{
		toBackend: callForwarder{dst: backendConn, dstName: dbustypes.BusName},
		logger:    logger,
	}
}

// Prompts, which the backend creates under PromptsPath.
type promptObjects struct{}

func (promptObjects) serves(path dbus.ObjectPath) bool {
	return dbustypes.IsPromptPath(path)
}

// Performs the prompt. Its result arrives in the Completed signal.
// Signature: Prompt(window_id String)
func (h *PromptHandler) Prompt(msg dbus.Message, windowID string) *dbus.Error {
	path, dbusErr := calledPathIn(promptObjects{}, msg)
	if dbusErr != nil {
		return dbusErr
	}

	h.logger.Info("forwarding prompt", "path", path, "sender", senderOf(msg))

	return h.toBackend.forwardVoid(msg)
}

// Dismisses the prompt.
func (h *PromptHandler) Dismiss(msg dbus.Message) *dbus.Error {
	path, dbusErr := calledPathIn(promptObjects{}, msg)
	if dbusErr != nil {
		return dbusErr
	}

	h.logger.Info("dismissing prompt", "path", path, "sender", senderOf(msg))

	return h.toBackend.forwardVoid(msg)
}
