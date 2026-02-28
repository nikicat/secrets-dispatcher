// Package approval manages pending secret access requests requiring user approval.
package approval

// ProcessInfo represents a single process in the process chain.
type ProcessInfo struct {
	Name string `json:"name"`
	PID  uint32 `json:"pid"`
}

// SenderInfo contains information about the D-Bus sender process.
type SenderInfo struct {
	Sender       string        `json:"sender"`                  // D-Bus unique name (":1.123")
	PID          uint32        `json:"pid"`                     // Process ID
	UID          uint32        `json:"uid"`                     // User ID
	UserName     string        `json:"user_name"`               // Username (may be empty if lookup fails)
	UnitName     string        `json:"unit_name"`               // Systemd unit (may be empty)
	ProcessChain []ProcessInfo `json:"process_chain,omitempty"` // Full process chain from requestor to init
}
