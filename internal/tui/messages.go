package tui

import (
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procchain"
)

// NewRequestMsg is sent via p.Send() from the D-Bus goroutine when a new
// approval request arrives. The model adds it to the list pane without
// stealing focus from the current selection.
type NewRequestMsg struct {
	Request   *approval.Request
	ProcChain []procchain.ProcInfo
}

// RequestResolvedMsg is sent when a request is approved, denied, expired, or
// cancelled. The model moves the item from the active list to the dimmed
// history section.
type RequestResolvedMsg struct {
	ID         string
	Resolution approval.Resolution
}

// TickMsg fires every second from the tickCmd command to drive countdown
// timer updates in the list pane.
type TickMsg time.Time

// ApproveResultMsg carries the result of an asynchronous Approve or Deny call.
// On error the model may display a status message; the request remains visible
// until a RequestResolvedMsg arrives from the approval.Manager observer.
type ApproveResultMsg struct {
	ID  string
	Err error
}
